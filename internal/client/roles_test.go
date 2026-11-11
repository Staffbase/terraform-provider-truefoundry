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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

func TestRolePermissions_UnmarshalJSON(t *testing.T) {
	cases := map[string]client.RolePermissions{
		`["a:B","c:D"]`: {"a:B", "c:D"},
		`[]`:            {},
		`"a:B"`:         {"a:B"},
		`""`:            {},
		`null`:          nil,
	}
	for in, want := range cases {
		var got client.RolePermissions
		if err := json.Unmarshal([]byte(in), &got); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v, want %#v", in, got, want)
		}
	}
	var bad client.RolePermissions
	if err := json.Unmarshal([]byte(`{"x":1}`), &bad); err == nil {
		t.Error("object: expected an error")
	}
}

func TestRolePermissions_MarshalsAsArray(t *testing.T) {
	out, err := json.Marshal(client.RoleManifest{Permissions: client.RolePermissions{"a:B"}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["permissions"].([]any); !ok {
		t.Errorf("permissions not encoded as array: %s", out)
	}
}

// A built-in role with a string permission must not break reading the others
// (seen on the TrueFoundry Cloud control plane).
func TestGetRole_ToleratesStringPermissionsOnOtherRoles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"r1","name":"tenant-admin","resourceType":"tenant","isEditable":false,"isReserved":true,
			 "manifest":{"type":"role","name":"tenant-admin","displayName":"Tenant Admin","resourceType":"tenant","permissions":"*"}},
			{"id":"r2","name":"member","resourceType":"tenant","isEditable":true,"isDefault":true,
			 "manifest":{"type":"role","name":"member","displayName":"Member","resourceType":"tenant","permissions":["user:ListUsers"]}}
		]`))
	}))
	defer srv.Close()

	c, err := client.New(srv.URL, "key", "test")
	if err != nil {
		t.Fatal(err)
	}
	role, err := c.GetRole(context.Background(), "member")
	if err != nil {
		t.Fatal(err)
	}
	if role == nil || !reflect.DeepEqual(role.Manifest.Permissions, client.RolePermissions{"user:ListUsers"}) {
		t.Fatalf("unexpected role: %#v", role)
	}
}
