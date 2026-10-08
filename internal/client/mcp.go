package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

const mcpServersPath = "/api/svc/v1/mcp-servers"

// McpServerAuth is the `auth_data` block of an MCP server manifest. Type
// "header" uses Headers and AuthLevel; type "oauth2" uses the OAuth fields;
// type "passthrough" has no further fields. Header values and the client
// credentials may be raw secrets or TrueFoundry secret references.
type McpServerAuth struct {
	Type string `json:"type"`

	// header
	Headers   map[string]string `json:"headers,omitempty"`
	AuthLevel string            `json:"auth_level,omitempty"` // global or per_user

	// oauth2
	GrantType                     string            `json:"grant_type,omitempty"` // authorization_code or client_credentials
	AuthorizationURL              string            `json:"authorization_url,omitempty"`
	TokenURL                      string            `json:"token_url,omitempty"`
	ClientID                      string            `json:"client_id,omitempty"`
	ClientSecret                  string            `json:"client_secret,omitempty"`
	RegistrationURL               string            `json:"registration_url,omitempty"`
	IntrospectionURL              string            `json:"introspection_url,omitempty"`
	Provider                      string            `json:"provider,omitempty"`   // custom, auth0, or okta
	JWTSource                     string            `json:"jwt_source,omitempty"` // access_token or id_token
	Scopes                        []string          `json:"scopes,omitempty"`
	AdditionalTokenParams         map[string]string `json:"additional_token_params,omitempty"`
	CodeChallengeMethodsSupported []string          `json:"code_challenge_methods_supported,omitempty"`
	UseOrganization               *bool             `json:"use_organization,omitempty"` // auth0 only
	DefaultOrganization           string            `json:"default_organization,omitempty"`
}

// McpToolAnnotations are behavior hints on an MCP tool. The wire format is
// camelCase, unlike the rest of the manifest.
type McpToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
}

// McpToolSetting overrides metadata of one tool exposed by an MCP server.
// Remote servers use Name/Description/Disabled/Annotations; OpenAPI servers
// define tools with Method/Path/Name/Description/Annotations instead.
type McpToolSetting struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Disabled    *bool               `json:"disabled,omitempty"`
	Annotations *McpToolAnnotations `json:"annotations,omitempty"`

	// mcp-server/openapi
	Method string `json:"method,omitempty"` // get, post, put, patch, delete, or head
	Path   string `json:"path,omitempty"`
}

// OpenApiSpecSource points at the OpenAPI spec of an "mcp-server/openapi"
// manifest: type "remote" or "upload" carries a URL, type "inline" carries
// the spec itself (JSON or YAML string).
type OpenApiSpecSource struct {
	Type        string `json:"type"`
	URL         string `json:"url,omitempty"`
	OpenAPISpec string `json:"openapi_spec,omitempty"`
}

// McpServerHeaderOverride overrides request headers for one source server of
// a virtual MCP server. Type is always "header".
type McpServerHeaderOverride struct {
	Type    string            `json:"type"`
	Headers map[string]string `json:"headers"`
}

// McpServerSource is one source server aggregated by a virtual MCP server,
// referenced by the name of an existing MCP server on the same tenant.
type McpServerSource struct {
	Name         string                   `json:"name"`
	EnabledTools []string                 `json:"enabled_tools,omitempty"`
	AuthData     *McpServerHeaderOverride `json:"auth_data,omitempty"`
}

// McpServerManifest is a standalone MCP server manifest, type
// "mcp-server/remote" (URL/TLSSettings/ToolSettings/AuthData/AdditionalHeaders),
// "mcp-server/openapi" (additionally OpenAPISpecSource/EnableAllTools), or
// "mcp-server/virtual" (Servers). The "mcp-server/stdio" and "tfy-managed"
// variants are not modeled.
type McpServerManifest struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`

	// mcp-server/remote and mcp-server/openapi
	URL               string            `json:"url,omitempty"`
	TLSSettings       *TLSSettings      `json:"tls_settings,omitempty"`
	ToolSettings      []McpToolSetting  `json:"tool_settings,omitempty"`
	AuthData          *McpServerAuth    `json:"auth_data,omitempty"`
	AdditionalHeaders map[string]string `json:"additional_headers,omitempty"`

	// mcp-server/openapi
	OpenAPISpecSource *OpenApiSpecSource `json:"openapi_spec_source,omitempty"`
	EnableAllTools    *bool              `json:"enable_all_tools,omitempty"`

	// mcp-server/virtual
	Servers []McpServerSource `json:"servers,omitempty"`

	Collaborators *[]Collaborator   `json:"collaborators,omitempty"`
	Tags          map[string]string `json:"tags,omitempty"`
}

// McpServer is one entry of GET /api/svc/v1/mcp-servers.
type McpServer struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	FQN      string            `json:"fqn"`
	Manifest McpServerManifest `json:"manifest"`
}

// ListMcpServers returns all MCP servers of the tenant, following pagination.
// fqn optionally filters to a single server.
func (c *Client) ListMcpServers(ctx context.Context, fqn string) ([]McpServer, error) {
	var all []McpServer
	offset := 0
	for {
		q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {"100"}}
		if fqn != "" {
			q.Set("fqn", fqn)
		}
		var page listIntegrationsResponse[McpServer]
		if err := c.do(ctx, http.MethodGet, mcpServersPath, q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		offset += len(page.Data)
		if len(page.Data) == 0 || offset >= page.Pagination.Total {
			return all, nil
		}
	}
}
