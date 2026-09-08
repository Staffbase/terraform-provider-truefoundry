package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

const testAPIKey = "test-api-key"

func newTestClient(t *testing.T) (*client.Client, *mockcp.Server) {
	t.Helper()
	mock := mockcp.New(testAPIKey)
	srv := httptest.NewServer(mock.Handler())
	t.Cleanup(srv.Close)
	c, err := client.New(srv.URL, testAPIKey, "test")
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c, mock
}

func testManifest(name string, models ...string) client.ProviderAccountManifest {
	manifest := client.ProviderAccountManifest{
		Type:         "provider-account/openai",
		Name:         name,
		AuthData:     &client.APIKeyAuth{Type: "api-key", APIKey: "sk-upstream"},
		Integrations: []client.ModelIntegration{},
	}
	for _, m := range models {
		manifest.Integrations = append(manifest.Integrations, client.ModelIntegration{
			Type:       "integration/model/openai",
			Name:       m,
			ModelID:    m,
			ModelTypes: []string{"chat"},
		})
	}
	return manifest
}

func TestNewRejectsInvalidURL(t *testing.T) {
	for _, u := range []string{"", "not-a-url", "//missing-scheme"} {
		if _, err := client.New(u, "key", "test"); err == nil {
			t.Errorf("client.New(%q) expected error, got nil", u)
		}
	}
}

func TestApplyCreateThenUpdate(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	created, err := c.Apply(ctx, testManifest("acct-1", "gpt-4o"), false)
	if err != nil {
		t.Fatalf("Apply create: %v", err)
	}
	if created.Action != "CREATE" {
		t.Errorf("first apply action = %q, want CREATE", created.Action)
	}
	if created.Data.ID == "" || created.Data.FQN == "" {
		t.Errorf("apply response missing id/fqn: %+v", created.Data)
	}

	updated, err := c.Apply(ctx, testManifest("acct-1", "gpt-4o", "gpt-4o-mini"), false)
	if err != nil {
		t.Fatalf("Apply update: %v", err)
	}
	if updated.Action != "UPDATE" {
		t.Errorf("second apply action = %q, want UPDATE", updated.Action)
	}
	if updated.Data.ID != created.Data.ID {
		t.Errorf("id changed on update: %q -> %q", created.Data.ID, updated.Data.ID)
	}
}

func TestApplyDryRunDoesNotPersist(t *testing.T) {
	c, mock := newTestClient(t)
	if _, err := c.Apply(context.Background(), testManifest("acct-dry", "gpt-4o"), true); err != nil {
		t.Fatalf("Apply dry-run: %v", err)
	}
	if got := mock.AccountCount(); got != 0 {
		t.Errorf("dry-run persisted %d accounts, want 0", got)
	}
}

func TestListModelIntegrations(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()
	if _, err := c.Apply(ctx, testManifest("acct-a", "gpt-4o", "gpt-4o-mini"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(ctx, testManifest("acct-b", "o3-mini"), false); err != nil {
		t.Fatal(err)
	}

	all, err := c.ListModelIntegrations(ctx, "")
	if err != nil {
		t.Fatalf("ListModelIntegrations: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d integrations, want 3", len(all))
	}
	if all[0].Manifest.ModelID == "" || all[0].ProviderAccountFQN == "" {
		t.Errorf("integration not populated: %+v", all[0])
	}
	pa := all[0].ProviderAccount
	if pa == nil || pa.ID == "" || pa.Manifest.Type == "" || pa.Manifest.AuthData == nil {
		t.Errorf("embedded provider account not populated: %+v", pa)
	}

	one, err := c.ListModelIntegrations(ctx, all[0].FQN)
	if err != nil {
		t.Fatalf("ListModelIntegrations(fqn): %v", err)
	}
	if len(one) != 1 || one[0].FQN != all[0].FQN {
		t.Errorf("fqn filter returned %+v, want exactly %q", one, all[0].FQN)
	}
}

func TestDelete(t *testing.T) {
	c, mock := newTestClient(t)
	ctx := context.Background()
	if _, err := c.Apply(ctx, testManifest("acct-del", "gpt-4o"), false); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, testManifest("acct-del", "gpt-4o")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := mock.AccountCount(); got != 0 {
		t.Errorf("after delete %d accounts remain, want 0", got)
	}

	err := c.Delete(ctx, testManifest("acct-del", "gpt-4o"))
	if err == nil || !client.IsNotFound(err) {
		t.Errorf("deleting missing account: err = %v, want 404 APIError", err)
	}
}

func TestUnauthorized(t *testing.T) {
	mock := mockcp.New("right-key")
	srv := httptest.NewServer(mock.Handler())
	t.Cleanup(srv.Close)
	c, err := client.New(srv.URL, "wrong-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Apply(context.Background(), testManifest("acct-x", "gpt-4o"), false)
	apiErr, ok := err.(*client.APIError)
	if !ok || apiErr.StatusCode != 401 {
		t.Errorf("wrong key: err = %v, want APIError 401", err)
	}
}

// countingServer wraps a mockcp server counting every GET to
// /api/svc/v1/provider-integrations, to observe how many times
// ListModelIntegrations actually reaches the network.
func countingServer(t *testing.T) (*client.Client, *mockcp.Server, *atomic.Int64) {
	t.Helper()
	mock := mockcp.New(testAPIKey)
	var listCalls atomic.Int64
	handler := mock.Handler()
	counting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/svc/v1/provider-integrations" {
			listCalls.Add(1)
		}
		handler.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(counting)
	t.Cleanup(srv.Close)
	c, err := client.New(srv.URL, testAPIKey, "test")
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c, mock, &listCalls
}

// TestListModelIntegrationsCachedPerClient reproduces the many-resources
// perf problem this cache fixes: several resources of the same
// integration-backed type each independently calling ListModelIntegrations
// within one provider run (one Client) must not each hit the network - see
// Client's doc comment.
func TestListModelIntegrationsCachedPerClient(t *testing.T) {
	c, _, listCalls := countingServer(t)
	ctx := context.Background()
	if _, err := c.Apply(ctx, testManifest("acct-a", "gpt-4o"), false); err != nil {
		t.Fatal(err)
	}
	listCalls.Store(0) // only count calls from here on

	const readers = 5
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for range readers {
		wg.Go(func() {
			if _, err := c.ListModelIntegrations(ctx, ""); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ListModelIntegrations: %v", err)
	}
	if got := listCalls.Load(); got != 1 {
		t.Errorf("%d concurrent readers caused %d list requests, want 1 (singleflight should collapse them)", readers, got)
	}

	if _, err := c.ListModelIntegrations(ctx, ""); err != nil {
		t.Fatalf("ListModelIntegrations (cached): %v", err)
	}
	if got := listCalls.Load(); got != 1 {
		t.Errorf("repeat call after warm cache caused a request: %d list requests, want still 1", got)
	}

	if _, err := c.Apply(ctx, testManifest("acct-a", "gpt-4o", "gpt-4o-mini"), true /* dryRun */); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListModelIntegrations(ctx, ""); err != nil {
		t.Fatalf("ListModelIntegrations (after dry-run apply): %v", err)
	}
	if got := listCalls.Load(); got != 1 {
		t.Errorf("dry-run apply should not invalidate the cache: %d list requests, want still 1", got)
	}

	if _, err := c.Apply(ctx, testManifest("acct-a", "gpt-4o", "gpt-4o-mini"), false); err != nil {
		t.Fatal(err)
	}
	all, err := c.ListModelIntegrations(ctx, "")
	if err != nil {
		t.Fatalf("ListModelIntegrations (after real apply): %v", err)
	}
	if got := listCalls.Load(); got != 2 {
		t.Errorf("real apply should invalidate the cache: %d list requests, want 2", got)
	}
	if len(all) != 2 {
		t.Errorf("got %d integrations after update, want 2 (cache must reflect the update, not stale data)", len(all))
	}
}
