// Package client is a minimal TrueFoundry control plane API client.
//
// It covers the three endpoints needed to manage AI Gateway models:
//
//	PUT  /api/svc/v1/apply                 – create/update a resource from a manifest
//	POST /api/svc/v1/delete                – delete a resource by manifest
//	GET  /api/svc/v1/provider-integrations – list model integrations
//
// All requests authenticate with `Authorization: Bearer <api key>` against the
// control plane base URL, mirroring the official TrueFoundry SDKs.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	applyPath        = "/api/svc/v1/apply"
	deletePath       = "/api/svc/v1/delete"
	integrationsPath = "/api/svc/v1/provider-integrations"
)

// Client talks to a TrueFoundry control plane.
//
// One Client is shared by every resource and data source instance for the
// lifetime of a single provider run (one `terraform plan`/`apply`). A config
// with many resources of the same integration-backed type (provider
// accounts, custom endpoints, the models data source, ...) each independently
// call listIntegrations for the exact same, tenant-wide, non-account-scoped
// list - the control plane has no server-side filter narrower than "one type,
// every account". listCache/listGroup dedupe those identical calls within one
// run: singleflight collapses concurrent callers (Terraform runs Reads in
// parallel) into a single HTTP round trip, and the cache serves repeat
// callers after that without another round trip. Apply/Delete invalidate the
// whole cache on any real (non-dryRun) mutation, since either can change any
// integration list.
type Client struct {
	baseURL   string
	apiKey    string
	userAgent string
	http      *http.Client

	listMu    sync.Mutex
	listCache map[string]any
	listGroup singleflight.Group
}

// New returns a client for the control plane at baseURL authenticating with apiKey.
func New(baseURL, apiKey, userAgent string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid control plane URL %q", baseURL)
	}
	return &Client{
		baseURL:   u.Scheme + "://" + u.Host,
		apiKey:    apiKey,
		userAgent: userAgent,
		http:      &http.Client{Timeout: 60 * time.Second},
		listCache: map[string]any{},
	}, nil
}

// APIError is a non-2xx response from the control plane.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("truefoundry control plane returned HTTP %d: %s", e.StatusCode, e.Body)
}

// IsNotFound reports whether err is an APIError with status 404.
func IsNotFound(err error) bool {
	apiErr, ok := err.(*APIError)
	return ok && apiErr.StatusCode == http.StatusNotFound
}

// APIKeyAuth is the `auth_data` block of a provider account or integration
// manifest. Type "api-key" uses APIKey; type "access-key-based" (AWS Bedrock)
// uses AccessKeyID and SecretAccessKey; type "header" (custom endpoints) uses
// Headers; type "connection-string" (Azure blob storage) uses
// ConnectionString; types "slack-webhook"/"slack-bot" use WebhookURL/BotToken;
// types "token"/"approle" (HashiCorp Vault) use Token / RoleID+SecretID.
// Values may be raw secrets or TrueFoundry secret references.
type APIKeyAuth struct {
	Type             string            `json:"type"`
	APIKey           string            `json:"api_key,omitempty"`
	AccessKeyID      string            `json:"access_key_id,omitempty"`
	SecretAccessKey  string            `json:"secret_access_key,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	ConnectionString string            `json:"connection_string,omitempty"`
	WebhookURL       string            `json:"webhook_url,omitempty"`
	BotToken         string            `json:"bot_token,omitempty"`
	Token            string            `json:"token,omitempty"`
	RoleID           string            `json:"role_id,omitempty"`
	SecretID         string            `json:"secret_id,omitempty"`
}

// CostValue is the pricing of a model cost metric. The wire format is
// polymorphic: token-based metrics (e.g. per_1000_tokens) use an object with
// input/output rates, scalar metrics (e.g. per_1000_embedding_tokens,
// per_minute_of_audio) use a plain number, carried here in Amount. Volume-based
// pricing tiers (input_tiers etc.) are not modeled.
type CostValue struct {
	Amount     *float64
	Input      *float64
	Output     *float64
	CacheRead  *float64
	CacheWrite *float64
}

type costValueObject struct {
	Input      *float64 `json:"input,omitempty"`
	Output     *float64 `json:"output,omitempty"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
}

// MarshalJSON emits a plain number for scalar metrics and an object otherwise.
func (v CostValue) MarshalJSON() ([]byte, error) {
	if v.Amount != nil {
		return json.Marshal(*v.Amount)
	}
	return json.Marshal(costValueObject{Input: v.Input, Output: v.Output, CacheRead: v.CacheRead, CacheWrite: v.CacheWrite})
}

// UnmarshalJSON accepts both the scalar and the object wire form.
func (v *CostValue) UnmarshalJSON(data []byte) error {
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		v.Amount = &n
		return nil
	}
	var obj costValueObject
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	v.Input, v.Output, v.CacheRead, v.CacheWrite = obj.Input, obj.Output, obj.CacheRead, obj.CacheWrite
	return nil
}

// ModelCostMetric is the optional `cost` block of a model integration,
// e.g. {"metric": "per_1000_tokens", "value": {"input": 0.0022, "output": 0.0088}}.
type ModelCostMetric struct {
	Metric string    `json:"metric"`
	Value  CostValue `json:"value"`
}

// HeadersOverride mutates upstream request headers for a routing target.
type HeadersOverride struct {
	Remove []string          `json:"remove,omitempty"`
	Set    map[string]string `json:"set,omitempty"`
}

// RoutingTarget is one target of a virtual model's routing config. Weight is
// set for weight-based routing, Priority for priority-based routing.
type RoutingTarget struct {
	Target              string            `json:"target"`
	Weight              *float64          `json:"weight,omitempty"`
	Priority            *int64            `json:"priority,omitempty"`
	FallbackStatusCodes []string          `json:"fallback_status_codes,omitempty"`
	FallbackCandidate   *bool             `json:"fallback_candidate,omitempty"`
	MetadataMatch       map[string]string `json:"metadata_match,omitempty"`
	HeadersOverride     *HeadersOverride  `json:"headers_override,omitempty"`
}

// RoutingConfig is the routing_config of a virtual model, e.g.
// {"type": "weight-based-routing", "load_balance_targets": [...]}.
// Retry config, sticky routing, SLA cutoffs, and param overrides are not
// modeled.
type RoutingConfig struct {
	Type               string          `json:"type"`
	LoadBalanceTargets []RoutingTarget `json:"load_balance_targets"`
}

// TLSSettings configures TLS verification for a custom endpoint.
type TLSSettings struct {
	RejectUnauthorized bool   `json:"reject_unauthorized"`
	CACert             string `json:"ca_cert,omitempty"`
}

// EndpointTargetConfig is the per-endpoint load balancing config of a
// custom-endpoint account with routing_type "weight" or "priority".
type EndpointTargetConfig struct {
	Weight              *float64          `json:"weight,omitempty"`
	Priority            *int64            `json:"priority,omitempty"`
	FallbackStatusCodes []string          `json:"fallback_status_codes,omitempty"`
	FallbackCandidate   *bool             `json:"fallback_candidate,omitempty"`
	MetadataMatch       map[string]string `json:"metadata_match,omitempty"`
}

// ModelIntegration is one model entry in a provider account manifest,
// e.g. type "integration/model/openai". The optional fields apply to the
// Azure variants: api_version/foundation_model/deployment_type to
// "integration/model/azure-openai", auth_data/azure_endpoint (plus
// api_version) to "integration/model/azure-foundry".
type ModelIntegration struct {
	Type       string   `json:"type"`
	Name       string   `json:"name"`
	ModelID    string   `json:"model_id,omitempty"`
	ModelTypes []string `json:"model_types,omitempty"`

	Cost               *ModelCostMetric `json:"cost,omitempty"`
	AuthorizedSubjects []string         `json:"authorized_subjects,omitempty"`

	APIVersion      string `json:"api_version,omitempty"`
	FoundationModel string `json:"foundation_model,omitempty"`
	DeploymentType  string `json:"deployment_type,omitempty"`
	Region          string `json:"region,omitempty"`

	AuthData      *APIKeyAuth `json:"auth_data,omitempty"`
	AzureEndpoint string      `json:"azure_endpoint,omitempty"`

	// Virtual models ("integration/model/virtual")
	Slug          string         `json:"slug,omitempty"`
	RoutingConfig *RoutingConfig `json:"routing_config,omitempty"`

	// Custom endpoints ("integration/model/custom-endpoint")
	BaseURL             string                `json:"base_url,omitempty"`
	Headers             map[string]string     `json:"headers,omitempty"`
	TLSSettings         *TLSSettings          `json:"tls_settings,omitempty"`
	LoadbalancingConfig *EndpointTargetConfig `json:"loadbalancing_config,omitempty"`
}

// Collaborator grants a role on the surrounding resource to a subject.
type Collaborator struct {
	Subject string `json:"subject"`
	RoleID  string `json:"role_id"`
}

// ProviderAccountManifest is a gateway provider account, e.g. type
// "provider-account/openai", carrying its model integrations. AzureEndpoint
// applies to "provider-account/azure-openai", Region to
// "provider-account/aws-bedrock", EndpointType/RoutingType/Slug to
// "provider-account/custom-endpoint". SubscriptionID appears on embedded
// "provider-account/azure" manifests (see azure.go).
type ProviderAccountManifest struct {
	Type           string             `json:"type"`
	Name           string             `json:"name"`
	AuthData       *APIKeyAuth        `json:"auth_data,omitempty"`
	BaseURL        string             `json:"base_url,omitempty"`
	AzureEndpoint  string             `json:"azure_endpoint,omitempty"`
	Region         string             `json:"region,omitempty"`
	EndpointType   string             `json:"endpoint_type,omitempty"`
	RoutingType    string             `json:"routing_type,omitempty"`
	Slug           string             `json:"slug,omitempty"`
	SubscriptionID string             `json:"subscription_id,omitempty"`
	Collaborators  []Collaborator     `json:"collaborators,omitempty"`
	Integrations   []ModelIntegration `json:"integrations"`
}

// ApplyResponse is the result of PUT /api/svc/v1/apply.
type ApplyResponse struct {
	ExistingManifest json.RawMessage `json:"existingManifest"`
	Action           string          `json:"action"` // CREATE or UPDATE
	Data             ApplyData       `json:"data"`
}

// ApplyData identifies the created or updated resource.
type ApplyData struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	FQN  string `json:"fqn"`
}

// EmbeddedProviderAccount is the provider account object embedded in each
// entry of the provider-integrations list response. Its manifest carries the
// account-level fields (auth_data, azure_endpoint, base_url) but not the
// integrations list.
type EmbeddedProviderAccount struct {
	ID       string                  `json:"id"`
	Name     string                  `json:"name"`
	FQN      string                  `json:"fqn"`
	Provider string                  `json:"provider"`
	Manifest ProviderAccountManifest `json:"manifest"`
}

// ProviderIntegration is one entry of GET /api/svc/v1/provider-integrations.
type ProviderIntegration struct {
	ID                 string                   `json:"id"`
	Name               string                   `json:"name"`
	FQN                string                   `json:"fqn"`
	Type               string                   `json:"type"`
	Manifest           ModelIntegration         `json:"manifest"`
	ProviderAccountFQN string                   `json:"providerAccountFqn"`
	ProviderAccount    *EmbeddedProviderAccount `json:"providerAccount,omitempty"`
	CreatedAt          string                   `json:"createdAt"`
	UpdatedAt          string                   `json:"updatedAt"`
}

type listIntegrationsResponse[T any] struct {
	Data       []T `json:"data"`
	Pagination struct {
		Total  int `json:"total"`
		Offset int `json:"offset"`
		Limit  int `json:"limit"`
	} `json:"pagination"`
}

// Apply creates or updates a resource from its manifest. A real (non-dryRun)
// apply can change any provider-integrations list, so it invalidates the
// whole listIntegrations cache; dryRun does not persist and leaves it alone.
func (c *Client) Apply(ctx context.Context, manifest any, dryRun bool) (*ApplyResponse, error) {
	body := map[string]any{"manifest": manifest, "dryRun": dryRun}
	var out ApplyResponse
	if err := c.do(ctx, http.MethodPut, applyPath, nil, body, &out); err != nil {
		return nil, err
	}
	if !dryRun {
		c.invalidateListCache()
	}
	return &out, nil
}

// Delete deletes the resource identified by manifest, invalidating the
// listIntegrations cache since it can remove entries from any list.
func (c *Client) Delete(ctx context.Context, manifest any) error {
	body := map[string]any{"manifest": manifest}
	if err := c.do(ctx, http.MethodPost, deletePath, nil, body, nil); err != nil {
		return err
	}
	c.invalidateListCache()
	return nil
}

// ListModelIntegrations returns all gateway model integrations, following
// pagination. fqn optionally filters to a single integration.
func (c *Client) ListModelIntegrations(ctx context.Context, fqn string) ([]ProviderIntegration, error) {
	return listIntegrations[ProviderIntegration](ctx, c, "model", fqn)
}

// listIntegrations pages through GET /api/svc/v1/provider-integrations for
// one integration type, decoding entries as T. Results are cached per
// (integrationType, fqn) for the life of c, and concurrent callers for the
// same key share one in-flight fetch - see Client's doc comment for why.
func listIntegrations[T any](ctx context.Context, c *Client, integrationType, fqn string) ([]T, error) {
	key := integrationType + "|" + fqn
	if cached, ok := c.getListCache(key); ok {
		return cached.([]T), nil
	}
	v, err, _ := c.listGroup.Do(key, func() (any, error) {
		if cached, ok := c.getListCache(key); ok {
			return cached, nil
		}
		var all []T
		offset := 0
		for {
			q := url.Values{"type": {integrationType}, "offset": {strconv.Itoa(offset)}, "limit": {"100"}}
			if fqn != "" {
				q.Set("fqn", fqn)
			}
			var page listIntegrationsResponse[T]
			if err := c.do(ctx, http.MethodGet, integrationsPath, q, nil, &page); err != nil {
				return nil, err
			}
			all = append(all, page.Data...)
			offset += len(page.Data)
			if len(page.Data) == 0 || offset >= page.Pagination.Total {
				break
			}
		}
		c.setListCache(key, all)
		return all, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]T), nil
}

func (c *Client) getListCache(key string) (any, bool) {
	c.listMu.Lock()
	defer c.listMu.Unlock()
	v, ok := c.listCache[key]
	return v, ok
}

func (c *Client) setListCache(key string, v any) {
	c.listMu.Lock()
	defer c.listMu.Unlock()
	c.listCache[key] = v
}

func (c *Client) invalidateListCache() {
	c.listMu.Lock()
	defer c.listMu.Unlock()
	c.listCache = map[string]any{}
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("calling %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("reading response of %s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{StatusCode: resp.StatusCode, Body: string(raw)}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decoding response of %s %s: %w", method, path, err)
		}
	}
	return nil
}
