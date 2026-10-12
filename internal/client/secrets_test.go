package client_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

// newTestSecretGroup creates a system secret store and a secret group on it,
// returning the group's ID.
func newTestSecretGroup(t *testing.T, c *client.Client) string {
	t.Helper()
	ctx := context.Background()
	store := client.TrueFoundryAccountManifest{
		Type: "provider-account/truefoundry",
		Name: "system-secret-store",
		Integrations: []client.TrueFoundrySecretStore{
			{Type: "integration/secret-store/truefoundry/db", Name: "system-secret-store"},
		},
	}
	if _, err := c.Apply(ctx, store, false); err != nil {
		t.Fatalf("Apply secret store: %v", err)
	}
	group := client.SecretGroupManifest{
		Type:           "secret-group",
		Name:           "system",
		IntegrationFQN: "mock-tenant:truefoundry:system-secret-store:secret-store:system-secret-store",
	}
	if _, err := c.Apply(ctx, group, false); err != nil {
		t.Fatalf("Apply secret group: %v", err)
	}
	groups, err := c.ListSecretGroups(ctx, "mock-tenant:system")
	if err != nil || len(groups) != 1 {
		t.Fatalf("ListSecretGroups = %v, %v; want one group", groups, err)
	}
	return groups[0].ID
}

// Every PutSecret replaces the group's whole secret list, so unserialized
// concurrent writes would drop each other's new secrets.
func TestPutSecretConcurrentWritesKeepEverySecret(t *testing.T) {
	c, mock := newTestClient(t)
	ctx := context.Background()
	groupID := newTestSecretGroup(t, c)

	const n = 25
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i)
			if _, err := c.PutSecret(ctx, groupID, key, "value-"+key); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("PutSecret: %v", err)
	}

	if got := mock.SecretCount(); got != n {
		t.Fatalf("SecretCount = %d after %d concurrent writes, want %d", got, n, n)
	}
}

func TestPutSecretUpdateKeepsOtherSecretsAndID(t *testing.T) {
	c, mock := newTestClient(t)
	ctx := context.Background()
	groupID := newTestSecretGroup(t, c)

	first, err := c.PutSecret(ctx, groupID, "a", "1")
	if err != nil {
		t.Fatalf("PutSecret a: %v", err)
	}
	if _, err := c.PutSecret(ctx, groupID, "b", "1"); err != nil {
		t.Fatalf("PutSecret b: %v", err)
	}
	updated, err := c.PutSecret(ctx, groupID, "a", "2")
	if err != nil {
		t.Fatalf("PutSecret a again: %v", err)
	}
	if updated.ID != first.ID || updated.FQN != "mock-tenant:system:a" {
		t.Errorf("updated secret = %+v, want ID %s and FQN mock-tenant:system:a", updated, first.ID)
	}
	if v, version, _ := mock.SecretValue("system", "a"); v != "2" || version != 2 {
		t.Errorf("a = %q (version %d), want \"2\" (version 2)", v, version)
	}
	if v, version, _ := mock.SecretValue("system", "b"); v != "1" || version != 1 {
		t.Errorf("b = %q (version %d), want \"1\" (version 1)", v, version)
	}

	got, err := c.GetSecret(ctx, first.ID)
	if err != nil || got.Value == nil || *got.Value != "2" {
		t.Fatalf("GetSecret = %+v, %v; want value \"2\"", got, err)
	}
	mock.HideSecretValues()
	if got, err := c.GetSecret(ctx, first.ID); err != nil || got.Value != nil {
		t.Fatalf("GetSecret with hidden values = %+v, %v; want no value", got, err)
	}
}

func TestDeleteSecretLastInGroup(t *testing.T) {
	c, mock := newTestClient(t)
	ctx := context.Background()
	groupID := newTestSecretGroup(t, c)

	ref, err := c.PutSecret(ctx, groupID, "only", "1")
	if err != nil {
		t.Fatalf("PutSecret: %v", err)
	}
	// The update endpoint rejects an empty secret list, so the last secret
	// can only go through the secrets endpoint.
	if err := c.DeleteSecret(ctx, groupID, ref.ID); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}
	if n := mock.SecretCount(); n != 0 {
		t.Fatalf("SecretCount = %d after delete, want 0", n)
	}
	if err := c.DeleteSecret(ctx, groupID, ref.ID); !client.IsNotFound(err) {
		t.Fatalf("second DeleteSecret error = %v, want 404", err)
	}
}
