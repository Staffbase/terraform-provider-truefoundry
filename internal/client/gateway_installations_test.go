package client_test

import (
	"context"
	"testing"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

// A dry run must not consume an ID: the real create afterwards gets the ID
// the dry run previewed.
func TestGatewayInstallationDryRunDoesNotConsumeID(t *testing.T) {
	c, mock := newTestClient(t)
	ctx := context.Background()
	manifest := client.GatewayInstallationManifest{
		Type: "gateway-installation", Name: "gateway-eu", URL: "https://gateway.example.com", Enabled: true,
	}

	preview, err := c.Apply(ctx, manifest, true)
	if err != nil {
		t.Fatalf("dry-run Apply: %v", err)
	}
	if n := mock.GatewayInstallationCount(); n != 0 {
		t.Fatalf("dry run persisted %d installations, want 0", n)
	}
	created, err := c.Apply(ctx, manifest, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if created.Data.ID != preview.Data.ID {
		t.Errorf("created ID %q, want the dry run's %q", created.Data.ID, preview.Data.ID)
	}

	got, err := c.GetGatewayInstallation(ctx, created.Data.ID)
	if err != nil || got.Name != "gateway-eu" || got.Manifest.URL != "https://gateway.example.com" || got.IsDefault {
		t.Fatalf("GetGatewayInstallation = %+v, %v", got, err)
	}
	token, err := c.GatewayInstallationToken(ctx, "gateway-eu")
	if err != nil || token == "" {
		t.Fatalf("GatewayInstallationToken = %q, %v", token, err)
	}
	if err := c.DeleteGatewayInstallation(ctx, created.Data.ID); err != nil {
		t.Fatalf("DeleteGatewayInstallation: %v", err)
	}
	if _, err := c.GetGatewayInstallation(ctx, created.Data.ID); !client.IsNotFound(err) {
		t.Fatalf("GetGatewayInstallation after delete error = %v, want 404", err)
	}
}
