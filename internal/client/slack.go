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

import "context"

// SlackNotificationChannel is one notification channel entry in a Slack
// provider account manifest: type
// "integration/notification-channel/slack-webhook" (AuthData type
// "slack-webhook" with WebhookURL) or
// "integration/notification-channel/slack-bot" (AuthData type "slack-bot"
// with BotToken).
type SlackNotificationChannel struct {
	Type               string      `json:"type"`
	Name               string      `json:"name"`
	AuthData           *APIKeyAuth `json:"auth_data,omitempty"`
	AuthorizedSubjects []string    `json:"authorized_subjects,omitempty"`
}

// SlackAccountManifest is a "provider-account/slack" manifest carrying
// notification channel integrations.
type SlackAccountManifest struct {
	Type         string                     `json:"type"`
	Name         string                     `json:"name"`
	Integrations []SlackNotificationChannel `json:"integrations"`
}

// NotificationChannelIntegration is one entry of
// GET /api/svc/v1/provider-integrations?type=notification-channel.
type NotificationChannelIntegration struct {
	ID                 string                   `json:"id"`
	Name               string                   `json:"name"`
	FQN                string                   `json:"fqn"`
	Type               string                   `json:"type"`
	Manifest           SlackNotificationChannel `json:"manifest"`
	ProviderAccountFQN string                   `json:"providerAccountFqn"`
	ProviderAccount    *EmbeddedProviderAccount `json:"providerAccount,omitempty"`
}

// ListNotificationChannelIntegrations returns all notification channel
// integrations, following pagination. fqn optionally filters to a single
// integration.
func (c *Client) ListNotificationChannelIntegrations(ctx context.Context, fqn string) ([]NotificationChannelIntegration, error) {
	return listIntegrations[NotificationChannelIntegration](ctx, c, "notification-channel", fqn)
}
