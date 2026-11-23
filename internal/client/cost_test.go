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
	"testing"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

func ptr(f float64) *float64 { return &f }

func TestModelCostMetricWireFormat(t *testing.T) {
	cases := []struct {
		name string
		cost client.ModelCostMetric
		want string
	}{
		{
			name: "public",
			cost: client.ModelCostMetric{Metric: client.PublicCostMetric},
			want: `{"metric":"public_cost"}`,
		},
		{
			name: "token based",
			cost: client.ModelCostMetric{Metric: "per_1000_tokens", Value: &client.CostValue{Input: ptr(0.00044), Output: ptr(0.00176), CacheRead: ptr(0.00011)}},
			want: `{"metric":"per_1000_tokens","value":{"input":0.00044,"output":0.00176,"cache_read":0.00011}}`,
		},
		{
			name: "scalar",
			cost: client.ModelCostMetric{Metric: "per_1000_embedding_tokens", Value: &client.CostValue{Amount: ptr(0.000143)}},
			want: `{"metric":"per_1000_embedding_tokens","value":0.000143}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.cost)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("Marshal = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestModelCostMetricDecode(t *testing.T) {
	for _, in := range []string{
		`{"metric":"public_cost"}`,
		`{"metric":"public_cost","value":null}`,
		`{"metric":"public_cost","value":{"input":0.00022,"output":"1.32","input_tiers":[{"from":0}]}}`,
		`{"metric":"public_cost","value":"resolved"}`,
	} {
		var cost client.ModelCostMetric
		if err := json.Unmarshal([]byte(in), &cost); err != nil {
			t.Fatalf("Unmarshal(%s): %v", in, err)
		}
		if cost.Metric != client.PublicCostMetric || cost.Value != nil {
			t.Errorf("Unmarshal(%s) = %+v, want public_cost without value", in, cost)
		}
	}

	var scalar client.ModelCostMetric
	if err := json.Unmarshal([]byte(`{"metric":"per_1000_embedding_tokens","value":0.000025}`), &scalar); err != nil {
		t.Fatalf("Unmarshal scalar: %v", err)
	}
	if scalar.Value == nil || scalar.Value.Amount == nil || *scalar.Value.Amount != 0.000025 {
		t.Errorf("scalar cost = %+v, want amount 0.000025", scalar.Value)
	}

	var tokens client.ModelCostMetric
	if err := json.Unmarshal([]byte(`{"metric":"per_1000_tokens","value":{"input":0.00044,"output":0.00176,"cache_read":0.00011}}`), &tokens); err != nil {
		t.Fatalf("Unmarshal token based: %v", err)
	}
	if v := tokens.Value; v == nil || v.Amount != nil || v.Input == nil || *v.Input != 0.00044 || v.CacheRead == nil || *v.CacheRead != 0.00011 {
		t.Errorf("token based cost = %+v, want input 0.00044 and cache_read 0.00011", tokens.Value)
	}

	var missing client.ModelCostMetric
	if err := json.Unmarshal([]byte(`{"metric":"per_1000_tokens","value":null}`), &missing); err != nil {
		t.Fatalf("Unmarshal null value: %v", err)
	}
	if missing.Value != nil {
		t.Errorf("null value decoded to %+v, want nil", missing.Value)
	}
}

func costManifest(cost *client.ModelCostMetric) client.ProviderAccountManifest {
	manifest := testManifest("acct-cost", "gpt-4o")
	manifest.Integrations[0].Cost = cost
	return manifest
}

func TestApplyPublicCostRoundTrip(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()
	if _, err := c.Apply(ctx, costManifest(&client.ModelCostMetric{Metric: client.PublicCostMetric}), false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	all, err := c.ListModelIntegrations(ctx, "")
	if err != nil {
		t.Fatalf("ListModelIntegrations: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d integrations, want 1", len(all))
	}
	if cost := all[0].Manifest.Cost; cost == nil || cost.Metric != client.PublicCostMetric || cost.Value != nil {
		t.Errorf("read back cost = %+v, want public_cost without value", cost)
	}
}

func TestApplyRejectsInvalidCost(t *testing.T) {
	cases := map[string]*client.ModelCostMetric{
		"public with value":   {Metric: client.PublicCostMetric, Value: &client.CostValue{Input: ptr(0.001), Output: ptr(0.002)}},
		"public with scalar":  {Metric: client.PublicCostMetric, Value: &client.CostValue{Amount: ptr(0.001)}},
		"private no value":    {Metric: "per_1000_tokens"},
		"token without input": {Metric: "per_1000_tokens", Value: &client.CostValue{Output: ptr(0.002)}},
	}
	for name, cost := range cases {
		t.Run(name, func(t *testing.T) {
			c, mock := newTestClient(t)
			if _, err := c.Apply(context.Background(), costManifest(cost), false); err == nil {
				t.Fatal("Apply succeeded, want a validation error")
			}
			if n := mock.AccountCount(); n != 0 {
				t.Errorf("%d accounts persisted, want 0", n)
			}
		})
	}
}
