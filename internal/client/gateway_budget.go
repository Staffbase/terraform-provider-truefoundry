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

package client

import (
	"context"
	"net/http"
)

// BudgetWhen scopes a budget rule to requests matching all given subjects,
// models, and metadata.
type BudgetWhen struct {
	Subjects []string          `json:"subjects,omitempty"`
	Models   []string          `json:"models,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// NotificationTarget is one receiver of a budget alert. Type is "email",
// "slack-webhook", "slack-bot", "pagerduty", or "ms-teams-webhook";
// NotificationChannel is the FQN of a notification channel integration.
// ToEmails applies to email targets, Channels to slack-bot targets.
type NotificationTarget struct {
	Type                string   `json:"type"`
	NotificationChannel string   `json:"notification_channel"`
	ToEmails            []string `json:"to_emails,omitempty"`
	Channels            []string `json:"channels,omitempty"`
}

// BudgetAlert notifies targets when usage crosses the given percentage
// thresholds of the budget limit.
type BudgetAlert struct {
	Thresholds         []float64            `json:"thresholds"`
	NotificationTarget []NotificationTarget `json:"notification_target"`
}

// BudgetRule is one rule of a gateway budget config: a spend or token limit
// applied to matching requests.
type BudgetRule struct {
	ID               string       `json:"id"`
	When             BudgetWhen   `json:"when"`
	LimitTo          float64      `json:"limit_to"`
	Unit             string       `json:"unit"`
	BudgetAppliesPer []string     `json:"budget_applies_per,omitempty"`
	Alerts           *BudgetAlert `json:"alerts,omitempty"`
	AuditMode        *bool        `json:"audit_mode,omitempty"`
}

// GatewayBudgetConfigManifest is a "gateway-budget-config" manifest — a
// tenant-wide singleton holding all budget rules of the AI gateway.
type GatewayBudgetConfigManifest struct {
	Type  string       `json:"type"`
	Name  string       `json:"name,omitempty"`
	Rules []BudgetRule `json:"rules"`
}

// GatewayBudgetConfig is the stored gateway budget config as returned by
// GET /api/svc/v1/llm-gateway/config/gateway-budget-config.
type GatewayBudgetConfig struct {
	ID         string                      `json:"id"`
	TenantName string                      `json:"tenantName"`
	Type       string                      `json:"type"`
	Manifest   GatewayBudgetConfigManifest `json:"manifest"`
}

// GetGatewayBudgetConfig returns the tenant's gateway budget config, or nil
// if none is configured.
func (c *Client) GetGatewayBudgetConfig(ctx context.Context) (*GatewayBudgetConfig, error) {
	var out GatewayBudgetConfig
	err := c.do(ctx, http.MethodGet, "/api/svc/v1/llm-gateway/config/gateway-budget-config", nil, nil, &out)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if out.ID == "" && len(out.Manifest.Rules) == 0 {
		return nil, nil
	}
	return &out, nil
}
