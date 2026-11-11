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
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

// pagedAccountsServer serves GET /api/svc/v1/provider-accounts ignoring
// ?fqn=, two accounts per page, and records the requested offsets.
func pagedAccountsServer(t *testing.T, fqns []string) (*client.Client, *[]int) {
	t.Helper()
	var offsets []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/svc/v1/provider-accounts" {
			http.NotFound(w, r)
			return
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		offsets = append(offsets, offset)
		end := min(offset+2, len(fqns))
		data := []map[string]any{}
		for i := offset; i < end; i++ {
			data = append(data, map[string]any{"id": fmt.Sprintf("id-%d", i), "fqn": fqns[i]})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":       data,
			"pagination": map[string]int{"total": len(fqns), "offset": offset, "limit": 2},
		})
	}))
	t.Cleanup(srv.Close)
	c, err := client.New(srv.URL, testAPIKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	return c, &offsets
}

func TestGetProviderAccount_matchOnLaterPage(t *testing.T) {
	c, offsets := pagedAccountsServer(t, []string{
		"t:slack:a", "t:azure:b", "t:custom-endpoint:c", "t:custom-endpoint:wanted", "t:hashicorp:e",
	})

	account, err := c.GetProviderAccount(context.Background(), "t:custom-endpoint:wanted")
	if err != nil {
		t.Fatal(err)
	}
	if account == nil || account.ID != "id-3" {
		t.Fatalf("got %+v, want account id-3", account)
	}
	if want := []int{0, 2}; !slices.Equal(*offsets, want) {
		t.Errorf("offsets = %v, want %v", *offsets, want)
	}
}

func TestGetProviderAccount_notFound(t *testing.T) {
	c, offsets := pagedAccountsServer(t, []string{
		"t:slack:a", "t:azure:b", "t:custom-endpoint:c", "t:hashicorp:d", "t:custom-endpoint:e",
	})

	account, err := c.GetProviderAccount(context.Background(), "t:custom-endpoint:missing")
	if err != nil {
		t.Fatal(err)
	}
	if account != nil {
		t.Fatalf("got %+v, want nil", account)
	}
	if want := []int{0, 2, 4}; !slices.Equal(*offsets, want) {
		t.Errorf("offsets = %v, want %v", *offsets, want)
	}
}
