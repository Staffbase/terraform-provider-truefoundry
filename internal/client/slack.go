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
