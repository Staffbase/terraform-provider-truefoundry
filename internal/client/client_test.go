// Copyright 2026, Staffbase SE and contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
//
// You may obtain a copy of the License at
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// TestOversizedResponseBodyFailsClearly asserts that a response past the
// client's read cap surfaces an explicit error instead of a truncated body
// silently corrupting json.Unmarshal.
func TestOversizedResponseBodyFailsClearly(t *testing.T) {
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// One MiB above the cap: enough to prove the limit isn't silently
		// truncating, without writing gigabytes in a unit test.
		const chunk = 1 << 20
		buf := make([]byte, chunk)
		for i := range buf {
			buf[i] = ' '
		}
		w.Write([]byte(`[`))
		for range 65 {
			w.Write(buf)
		}
		w.Write([]byte(`]`))
	}))
	t.Cleanup(oversized.Close)

	c, err := client.New(oversized.URL, testAPIKey, "test")
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	_, err = c.ListModelIntegrations(context.Background(), "")
	if err == nil {
		t.Fatal("ListModelIntegrations: expected error for oversized response, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds") || !strings.Contains(err.Error(), "MiB") {
		t.Errorf("error = %q, want a clear size-limit message", err.Error())
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

// TestListIntegrationsCacheKeyIncludesType reproduces a real collision: two
// different List*Integrations methods share the same integrationType
// ("secret-store", by both Vault and TrueFoundry-DB secret stores) but
// decode into different Go types. Both list the same tenant-wide
// type=secret-store entries (each caller is responsible for filtering by
// Manifest.Type/ProviderAccountFQN itself, same as ListSecretStoreIntegrations'
// doc comment says) - what must not happen is a cached []SecretStoreIntegration
// getting type-asserted as []TrueFoundrySecretStoreIntegration (or vice
// versa) just because both calls share the same (integrationType, fqn).
func TestListIntegrationsCacheKeyIncludesType(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	hashicorpManifest := client.HashicorpAccountManifest{
		Type: "provider-account/hashicorp",
		Name: "vault-acct",
		Integrations: []client.VaultIntegration{
			{
				Type:        "integration/secret-store/hashicorp/vault",
				Name:        "vault-1",
				VaultURL:    "https://vault.example.com",
				KVMountPath: "kv/path",
				AuthData:    &client.APIKeyAuth{Type: "token", Token: "s.token"},
			},
		},
	}
	if _, err := c.Apply(ctx, hashicorpManifest, false); err != nil {
		t.Fatalf("Apply hashicorp account: %v", err)
	}

	truefoundryManifest := client.TrueFoundryAccountManifest{
		Type: "provider-account/truefoundry",
		Name: "system-store-acct",
		Integrations: []client.TrueFoundrySecretStore{
			{Type: "integration/secret-store/truefoundry/db", Name: "store-1"},
		},
	}
	if _, err := c.Apply(ctx, truefoundryManifest, false); err != nil {
		t.Fatalf("Apply truefoundry account: %v", err)
	}

	// Both calls see every type=secret-store entry tenant-wide (2: one
	// vault, one truefoundry store) - decoded as their own T. The concrete
	// slice types differ, but a naive integrationType|fqn cache key would
	// still panic asserting one cached []T as the other on the second call.
	vaults, err := c.ListSecretStoreIntegrations(ctx, "")
	if err != nil {
		t.Fatalf("ListSecretStoreIntegrations: %v", err)
	}
	if len(vaults) != 2 {
		t.Fatalf("ListSecretStoreIntegrations returned %d entries, want 2", len(vaults))
	}
	vault := findByName(t, vaults, "vault-1", func(v client.SecretStoreIntegration) string { return v.Name })
	if vault.Manifest.VaultURL != "https://vault.example.com" || vault.Manifest.KVMountPath != "kv/path" {
		t.Errorf("vault-1 entry = %+v, want its VaultIntegration fields populated", vault)
	}

	// This call must not panic type-asserting a cached []SecretStoreIntegration
	// as []TrueFoundrySecretStoreIntegration.
	stores, err := c.ListTrueFoundrySecretStoreIntegrations(ctx, "")
	if err != nil {
		t.Fatalf("ListTrueFoundrySecretStoreIntegrations: %v", err)
	}
	if len(stores) != 2 {
		t.Fatalf("ListTrueFoundrySecretStoreIntegrations returned %d entries, want 2", len(stores))
	}
	store := findByName(t, stores, "store-1", func(v client.TrueFoundrySecretStoreIntegration) string { return v.Name })
	if store.Manifest.Type != "integration/secret-store/truefoundry/db" {
		t.Errorf("store-1 entry = %+v, want its TrueFoundrySecretStore manifest type", store)
	}

	// And a repeat call must still return the right, distinct, still-cached type.
	vaultsAgain, err := c.ListSecretStoreIntegrations(ctx, "")
	if err != nil {
		t.Fatalf("ListSecretStoreIntegrations (again): %v", err)
	}
	if len(vaultsAgain) != 2 {
		t.Errorf("ListSecretStoreIntegrations (again) returned %d entries, want 2", len(vaultsAgain))
	}
}

func findByName[T any](t *testing.T, items []T, name string, nameOf func(T) string) T {
	t.Helper()
	for _, item := range items {
		if nameOf(item) == name {
			return item
		}
	}
	t.Fatalf("no entry named %q in %+v", name, items)
	var zero T
	return zero
}

// TestListModelIntegrationsPostInvalidationCallerDoesNotJoinStaleFetch
// reproduces the race review found after the epoch fix: a caller after a
// real Apply/Delete must never join (and receive the result of) a fetch
// that a pre-invalidation caller already has in flight - it must start its
// own, current-epoch fetch instead. Without the epoch folded into the
// singleflight key, both callers would share the same key and the
// post-invalidation caller would block on, and return, the pre-mutation
// snapshot.
func TestListModelIntegrationsPostInvalidationCallerDoesNotJoinStaleFetch(t *testing.T) {
	mock := mockcp.New(testAPIKey)
	realHandler := mock.Handler()

	var gateOnce sync.Once
	seen := make(chan struct{})
	release := make(chan struct{})
	gated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/svc/v1/provider-integrations" {
			gatedThisCall := false
			gateOnce.Do(func() { gatedThisCall = true })
			if gatedThisCall {
				// Capture the response now (reflecting account state at this
				// exact moment, before the test's Apply below runs), then
				// hold delivery until release - simulating a slow first
				// fetch that is still in flight when a later caller arrives.
				rec := httptest.NewRecorder()
				realHandler.ServeHTTP(rec, r)
				close(seen)
				<-release
				for k, vv := range rec.Header() {
					for _, v := range vv {
						w.Header().Add(k, v)
					}
				}
				w.WriteHeader(rec.Code)
				_, _ = w.Write(rec.Body.Bytes())
				return
			}
		}
		realHandler.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(gated)
	t.Cleanup(srv.Close)
	c, err := client.New(srv.URL, testAPIKey, "test")
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()

	if _, err := c.Apply(ctx, testManifest("acct-a", "m1"), false); err != nil {
		t.Fatalf("initial Apply: %v", err)
	}

	type result struct {
		all []client.ProviderIntegration
		err error
	}
	staleCall := make(chan result, 1)
	go func() {
		all, err := c.ListModelIntegrations(ctx, "")
		staleCall <- result{all, err}
	}()

	<-seen // the first (pre-invalidation) fetch is now in flight, gated

	if _, err := c.Apply(ctx, testManifest("acct-a", "m1", "m2"), false); err != nil {
		t.Fatalf("invalidating Apply: %v", err)
	}

	freshCall := make(chan result, 1)
	go func() {
		all, err := c.ListModelIntegrations(ctx, "")
		freshCall <- result{all, err}
	}()

	// The post-invalidation caller must resolve on its own, current-epoch
	// fetch - it must not be blocked waiting on the gated pre-invalidation
	// fetch's release. A generous but bounded timeout: this must be fast
	// (a real, ungated request against an in-memory mock), and if it's
	// actually stuck joining the stale fetch, it would otherwise hang until
	// the test binary's own deadline.
	select {
	case r := <-freshCall:
		if r.err != nil {
			t.Fatalf("post-invalidation ListModelIntegrations: %v", r.err)
		}
		if len(r.all) != 2 {
			t.Errorf("post-invalidation ListModelIntegrations returned %d integrations, want 2 (m1, m2)", len(r.all))
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("post-invalidation call did not return independently - it joined the stale pre-invalidation in-flight fetch (regression)")
	}

	close(release)
	stale := <-staleCall
	if stale.err != nil {
		t.Fatalf("pre-invalidation ListModelIntegrations: %v", stale.err)
	}
	if len(stale.all) != 1 {
		t.Errorf("pre-invalidation ListModelIntegrations returned %d integrations, want 1 (its own frozen snapshot)", len(stale.all))
	}
}
