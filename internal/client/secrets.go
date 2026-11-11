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
	"fmt"
	"net/http"
	"net/url"
	"sync"
)

// Secrets are the key/value entries inside a secret group. They have no
// manifest: values are written through the secret group update endpoint,
// which replaces the group's whole secret list, and read and deleted one by
// one through the secrets endpoints.
//
//	GET    /api/svc/v1/secret-groups/{id} – the group with its associatedSecrets (no values)
//	PUT    /api/svc/v1/secret-groups/{id} – replace the group's secrets
//	GET    /api/svc/v1/secrets/{id}       – one secret with its value
//	DELETE /api/svc/v1/secrets/{id}       – delete one secret and its versions
const secretsPath = "/api/svc/v1/secrets"

// SecretRef is one entry of a secret group's associatedSecrets. The group
// endpoints never return secret values, so this type carries none.
type SecretRef struct {
	ID            string `json:"id"`
	FQN           string `json:"fqn"`
	Name          string `json:"name"`
	SecretGroupID string `json:"secretGroupId"`
}

// Secret is a secret as returned by GET /api/svc/v1/secrets/{id}. Value is
// nil when the control plane has secret value viewing disabled
// (DISABLE_SECRET_VALUE_VIEW).
type Secret struct {
	SecretRef
	Value *string `json:"value"`
}

type secretGroupWithSecrets struct {
	ID                string      `json:"id"`
	AssociatedSecrets []SecretRef `json:"associatedSecrets"`
}

// secretInput is one entry of the secret group update request. A nil Value
// keeps the stored value of an existing secret.
type secretInput struct {
	Key   string  `json:"key"`
	Value *string `json:"value,omitempty"`
}

// secretGroupLock returns the mutex serializing secret writes to one group.
// The update endpoint replaces the group's whole secret list, so two
// concurrent read-modify-write cycles (Terraform applies resources in
// parallel) would each drop the other's new secret.
func (c *Client) secretGroupLock(groupID string) *sync.Mutex {
	mu, _ := c.secretGroupLocks.LoadOrStore(groupID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// ListSecrets returns the secrets of the secret group with the given ID,
// without their values.
func (c *Client) ListSecrets(ctx context.Context, groupID string) ([]SecretRef, error) {
	var out struct {
		Data secretGroupWithSecrets `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, secretGroupsPath+"/"+url.PathEscape(groupID), nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Data.AssociatedSecrets, nil
}

// GetSecret fetches a secret, including its value unless the control plane
// hides secret values.
func (c *Client) GetSecret(ctx context.Context, id string) (*Secret, error) {
	var out struct {
		Data Secret `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, secretsPath+"/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// PutSecret creates or updates the secret key in the secret group with the
// given ID. Every other secret in the group is sent without a value, which
// keeps it unchanged; the control plane creates a new version only for a
// secret whose value changed.
func (c *Client) PutSecret(ctx context.Context, groupID, key, value string) (*SecretRef, error) {
	mu := c.secretGroupLock(groupID)
	mu.Lock()
	defer mu.Unlock()

	existing, err := c.ListSecrets(ctx, groupID)
	if err != nil {
		return nil, err
	}
	secrets := make([]secretInput, 0, len(existing)+1)
	for _, s := range existing {
		if s.Name != key {
			secrets = append(secrets, secretInput{Key: s.Name})
		}
	}
	secrets = append(secrets, secretInput{Key: key, Value: &value})

	var out struct {
		Data secretGroupWithSecrets `json:"data"`
	}
	body := map[string]any{"secrets": secrets}
	if err := c.do(ctx, http.MethodPut, secretGroupsPath+"/"+url.PathEscape(groupID), nil, body, &out); err != nil {
		return nil, err
	}
	for i, s := range out.Data.AssociatedSecrets {
		if s.Name == key {
			return &out.Data.AssociatedSecrets[i], nil
		}
	}
	return nil, fmt.Errorf("secret %q is missing from secret group %s right after writing it", key, groupID)
}

// DeleteSecret deletes the secret with the given ID from the secret group
// with the given ID. It takes the group's lock so a concurrent PutSecret
// cannot resend the deleted key without a value, which the control plane
// rejects.
func (c *Client) DeleteSecret(ctx context.Context, groupID, id string) error {
	mu := c.secretGroupLock(groupID)
	mu.Lock()
	defer mu.Unlock()
	return c.do(ctx, http.MethodDelete, secretsPath+"/"+url.PathEscape(id), nil, nil, nil)
}
