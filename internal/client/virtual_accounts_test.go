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
	"testing"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

func testVirtualAccountManifest(name string) client.VirtualAccountManifest {
	return client.VirtualAccountManifest{
		Name: name,
		Type: "virtual-account",
		Permissions: []client.Permission{
			{ResourceFQN: "mock-tenant:azure-openai:acct", ResourceType: "provider-account", RoleID: "provider-account-access"},
		},
	}
}

func TestVirtualAccountLifecycle(t *testing.T) {
	c, mock := newTestClient(t)
	ctx := context.Background()

	created, token, err := c.ApplyVirtualAccount(ctx, testVirtualAccountManifest("bot-1"), false)
	if err != nil {
		t.Fatalf("ApplyVirtualAccount create: %v", err)
	}
	if created.ID == "" || token == "" {
		t.Fatalf("create returned id=%q token=%q, want both set", created.ID, token)
	}

	// Update: token is not returned again.
	manifest := testVirtualAccountManifest("bot-1")
	manifest.ExpirationDate = "2027-01-31"
	updated, updateToken, err := c.ApplyVirtualAccount(ctx, manifest, false)
	if err != nil {
		t.Fatalf("ApplyVirtualAccount update: %v", err)
	}
	if updated.ID != created.ID {
		t.Errorf("id changed on update: %q -> %q", created.ID, updated.ID)
	}
	if updateToken != "" {
		t.Errorf("update returned token %q, want empty", updateToken)
	}

	got, err := c.GetVirtualAccount(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetVirtualAccount: %v", err)
	}
	if got.Manifest.ExpirationDate != "2027-01-31" {
		t.Errorf("manifest not updated: %+v", got.Manifest)
	}

	fetchedToken, err := c.GetVirtualAccountToken(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetVirtualAccountToken: %v", err)
	}
	if fetchedToken != token {
		t.Errorf("token endpoint returned %q, want %q", fetchedToken, token)
	}

	if err := c.DeleteVirtualAccount(ctx, created.ID); err != nil {
		t.Fatalf("DeleteVirtualAccount: %v", err)
	}
	if n := mock.VirtualAccountCount(); n != 0 {
		t.Errorf("%d virtual accounts remain after delete, want 0", n)
	}
	if _, err := c.GetVirtualAccount(ctx, created.ID); !client.IsNotFound(err) {
		t.Errorf("get after delete: err = %v, want 404", err)
	}
}

func TestVirtualAccountDryRunDoesNotPersist(t *testing.T) {
	c, mock := newTestClient(t)
	if _, _, err := c.ApplyVirtualAccount(context.Background(), testVirtualAccountManifest("bot-dry"), true); err != nil {
		t.Fatalf("dry-run apply: %v", err)
	}
	if n := mock.VirtualAccountCount(); n != 0 {
		t.Errorf("dry-run persisted %d virtual accounts, want 0", n)
	}
}
