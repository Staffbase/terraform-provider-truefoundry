package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

func TestGuardrailsPolicyApplyID(t *testing.T) {
	for _, tc := range []struct {
		name, applyBody, getBody, want string
		getStatus, wantReads           int
	}{
		{"apply response", `{"data":{"id":"policy-id"}}`, "", "policy-id", 200, 0},
		{"read fallback", `{"data":{}}`, `{"id":"fallback-id","manifest":{"name":"policy-1"}}`, "fallback-id", 200, 1},
		{"missing after apply", `{"data":{}}`, `{}`, "", 404, 1},
		{"missing ID after apply", `{"data":{}}`, `{"manifest":{"name":"policy-1"}}`, "", 200, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					_, _ = w.Write([]byte(tc.applyBody))
					return
				}
				if r.URL.Path != "/api/svc/v1/llm-gateway/config/gateway-guardrails-config/instances/policy-1" {
					t.Errorf("unexpected fallback path: %s", r.URL.Path)
				}
				reads++
				w.WriteHeader(tc.getStatus)
				_, _ = w.Write([]byte(tc.getBody))
			}))
			defer srv.Close()
			c, err := client.New(srv.URL, "key", "test")
			if err != nil {
				t.Fatal(err)
			}
			r := &gatewayGuardrailsPolicyResource{client: c}
			plan := gatewayGuardrailsPolicyModel{Name: types.StringValue("policy-1")}
			var diags diag.Diagnostics
			r.apply(context.Background(), &plan, &diags)
			if diags.HasError() != (tc.want == "") || plan.ID.ValueString() != tc.want {
				t.Fatalf("ID = %q, diagnostics = %v", plan.ID.ValueString(), diags)
			}
			if reads != tc.wantReads {
				t.Errorf("got %d fallback reads, want %d", reads, tc.wantReads)
			}
		})
	}
}

func TestFoldedGuardrailsApplyEmptyReadBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			_, _ = w.Write([]byte(`{"action":"UPDATE","data":{}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, err := client.New(srv.URL, "key", "test")
	if err != nil {
		t.Fatal(err)
	}
	r := &gatewayGuardrailsConfigResource{client: c}
	var diags diag.Diagnostics
	r.apply(context.Background(), &gatewayGuardrailsConfigModel{}, &diags)
	if !diags.HasError() {
		t.Fatal("folded apply accepted empty read-back")
	}
	if detail := diags[0].Detail(); !strings.Contains(detail, "truefoundry_gateway_guardrails_policy") || !strings.Contains(detail, "15 November 2026") {
		t.Fatalf("missing migration advice: %s", detail)
	}
}
