package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

func testGuardrailsPolicy(name string) client.GuardrailsPolicyManifest {
	return client.GuardrailsPolicyManifest{
		Type: "gateway-guardrails-config", Name: name,
		When:               client.GuardrailsWhen{Target: &client.GuardrailsTargetGroup{Operator: "or", Conditions: client.GuardrailsTargetClause{Model: &client.InNotInCondition{Condition: "in", Values: []string{"*"}}}}},
		LLMInputGuardrails: []string{"guardrails/shield"}, LLMOutputGuardrails: []string{}, MCPToolPreInvokeGuardrails: []string{}, MCPToolPostInvokeGuardrails: []string{},
	}
}

func seedPolicyGuardrail(t *testing.T, c *client.Client) {
	t.Helper()
	_, err := c.Apply(context.Background(), client.GuardrailGroupManifest{
		Type: "provider-account/guardrail-config-group", Name: "guardrails",
		Integrations: []client.GuardrailConfig{{Type: "integration/guardrail-config/azure-prompt-shield", Name: "shield", AuthData: &client.APIKeyAuth{Type: "api-key", APIKey: "secret"}, Config: client.GuardrailConfigSettings{ResourceName: "safety", APIVersion: "2024-09-01"}}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGuardrailsPoliciesLifecycle(t *testing.T) {
	c, mock := newTestClient(t)
	ctx := context.Background()
	seedPolicyGuardrail(t, c)
	manifest := testGuardrailsPolicy("Policy-1")
	preview, err := c.Apply(ctx, manifest, true)
	if err != nil {
		t.Fatal(err)
	}
	if mock.GuardrailsPolicyCount() != 0 {
		t.Fatal("dry run persisted a policy")
	}
	created, err := c.Apply(ctx, manifest, false)
	if err != nil {
		t.Fatal(err)
	}
	if created.Action != "CREATE" || created.Data.ID == "" || created.Data.ID != preview.Data.ID {
		t.Fatalf("unexpected apply: %+v", created)
	}
	second := testGuardrailsPolicy("Policy-2")
	if _, err := c.Apply(ctx, second, false); err != nil {
		t.Fatal(err)
	}
	policies, err := c.ListGuardrailsPolicies(ctx)
	if err != nil || len(policies) != 2 {
		t.Fatalf("List = %+v, %v", policies, err)
	}
	manifest.CustomErrorMessage = "Blocked"
	updated, err := c.Apply(ctx, manifest, false)
	if err != nil || updated.Action != "UPDATE" || updated.Data.ID != created.Data.ID {
		t.Fatalf("Update = %+v, %v", updated, err)
	}
	got, err := c.GetGuardrailsPolicy(ctx, manifest.Name)
	if err != nil || got == nil || got.Manifest.CustomErrorMessage != "Blocked" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if folded, err := c.GetGatewayGuardrailsConfig(ctx); err != nil || folded != nil {
		t.Fatalf("named policies polluted singleton: %+v, %v", folded, err)
	}
	for _, name := range []string{"Policy-1", "Policy-1", "Policy-2"} {
		if err := c.DeleteGuardrailsPolicy(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := c.GetGuardrailsPolicy(ctx, "Policy-1"); got != nil || err != nil {
		t.Fatalf("missing Get = %+v, %v", got, err)
	}
	if items, err := c.ListGuardrailsPolicies(ctx); err != nil || len(items) != 0 {
		t.Fatalf("empty List = %+v, %v", items, err)
	}
}

func TestGuardrailsPolicyMockValidation(t *testing.T) {
	c, _ := newTestClient(t)
	seedPolicyGuardrail(t, c)
	cases := []struct {
		name   string
		change func(map[string]any)
		status int
		want   string
	}{
		{"missing target", func(m map[string]any) { m["when"] = map[string]any{} }, 422, "target is required"},
		{"empty target", func(m map[string]any) {
			m["when"] = map[string]any{"target": map[string]any{"operator": "or", "conditions": map[string]any{}}}
		}, 422, "target conditions must not be empty"},
		{"missing list", func(m map[string]any) { delete(m, "llm_output_guardrails") }, 422, "llm_output_guardrails is required"},
		{"model without hook", func(m map[string]any) { m["llm_input_guardrails"] = []any{} }, 422, "Model targets require"},
		{"mcp without hook", func(m map[string]any) {
			m["when"].(map[string]any)["target"].(map[string]any)["conditions"] = map[string]any{"mcpServers": map[string]any{"condition": "in", "values": []any{"*"}}}
		}, 422, "MCP targets require"},
		{"empty subjects", func(m map[string]any) {
			m["when"].(map[string]any)["subjects"] = map[string]any{"operator": "or", "conditions": map[string]any{}}
		}, 422, "subjects conditions must not be empty"},
		{"unknown guardrail", func(m map[string]any) { m["llm_input_guardrails"] = []any{"nope/nope"} }, 400, "Guardrail Integration nope/nope referenced in rule Policy-1 does not exist."},
		{"unknown integration in existing group", func(m map[string]any) { m["llm_input_guardrails"] = []any{"guardrails/nope"} }, 400, "does not exist"},
		{"invalid name", func(m map[string]any) { m["name"] = "bad_name" }, 422, "name must contain only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(testGuardrailsPolicy("Policy-1"))
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			tc.change(m)
			_, err := c.Apply(context.Background(), m, true)
			var apiErr *client.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status || !strings.Contains(apiErr.Body, tc.want) {
				t.Fatalf("Apply error = %v, want HTTP %d containing %q", err, tc.status, tc.want)
			}
		})
	}
}

func TestGetGatewayGuardrailsConfigEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	c, err := client.New(srv.URL, testAPIKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.GetGatewayGuardrailsConfig(context.Background()); err != nil || got != nil {
		t.Fatalf("empty 200 = %+v, %v", got, err)
	}
	// Other endpoints must still reject malformed empty successful responses.
	if _, err := c.ListGuardrailsPolicies(context.Background()); err == nil {
		t.Fatal("instances list accepted an empty JSON body")
	}
}

func TestGuardrailsPolicyEscapesNames(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"statusCode":404,"message":"Gateway configuration instance not found"}`))
	}))
	defer srv.Close()
	c, err := client.New(srv.URL, testAPIKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetGuardrailsPolicy(context.Background(), "a/b ?"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteGuardrailsPolicy(context.Background(), "a/b ?"); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, "/a%2Fb%20%3F") {
			t.Errorf("unescaped name: %s", p)
		}
	}
}
