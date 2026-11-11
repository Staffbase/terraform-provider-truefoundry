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

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

// collaboratorsDeprecation is the deprecation message of every per-resource
// `collaborators` attribute. Control plane v0.172.0 moved access into
// standalone role-binding manifests; manifests carrying collaborators are
// rejected after 15 Nov 2026.
const collaboratorsDeprecation = "TrueFoundry retires collaborators on 15 Nov 2026 (control plane v0.172.0 moved access " +
	"control into role bindings); applies that still send them fail after that date. Grant access with " +
	"truefoundry_role_binding instead, then remove this attribute — removing it leaves existing grants in place. " +
	"See the \"Migrating to role bindings\" guide."

type collaboratorModel struct {
	Subject types.String `tfsdk:"subject"`
	RoleID  types.String `tfsdk:"role_id"`
}

// collaboratorsAttribute is the deprecated collaborators attribute shared by
// every resource that supports one. what names the resource ("MCP server"),
// roleExample lists roles valid on it.
func collaboratorsAttribute(what, roleExample string) schema.SetNestedAttribute {
	return schema.SetNestedAttribute{
		Optional:           true,
		DeprecationMessage: collaboratorsDeprecation,
		Description: "Subjects with a role on this " + what + "; use `truefoundry_role_binding` instead. " +
			"Unset leaves the grants on the control plane untouched (and they are not tracked); an empty set clears " +
			"them, which is only accepted until 15 Nov 2026.",
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"subject": schema.StringAttribute{
					Required:    true,
					Description: "Subject FQN, e.g. user:jane@example.com or team:everyone.",
				},
				"role_id": schema.StringAttribute{
					Required:    true,
					Description: "Role to assign, e.g. " + roleExample + ".",
				},
			},
		},
	}
}

// collaboratorsToManifest converts the configured collaborators to their wire
// form: nil (attribute unset) omits the field so existing grants stay
// untouched, an empty set sends [] to clear them.
func collaboratorsToManifest(collaborators []collaboratorModel) *[]client.Collaborator {
	if collaborators == nil {
		return nil
	}
	out := make([]client.Collaborator, 0, len(collaborators))
	for _, c := range collaborators {
		out = append(out, client.Collaborator{Subject: c.Subject.ValueString(), RoleID: c.RoleID.ValueString()})
	}
	return &out
}

// refreshCollaborators returns the collaborators to store on read. An unset
// attribute stays unset: grants are left on the control plane when the
// attribute is removed, and must not reappear as drift afterwards. Control
// planes may also stop echoing collaborators in their manifests, so a
// response without any keeps the prior value instead of reporting every
// grant as removed. Right after an import (see markImported) the grants found
// on the control plane are adopted.
func refreshCollaborators(prior []collaboratorModel, remote *[]client.Collaborator, imported bool) []collaboratorModel {
	if imported {
		prior = nil
		if remote != nil && len(*remote) > 0 {
			prior = []collaboratorModel{}
		}
	}
	if prior == nil || remote == nil || len(*remote) == 0 {
		return prior
	}
	out := make([]collaboratorModel, 0, len(*remote))
	for _, c := range *remote {
		out = append(out, collaboratorModel{Subject: types.StringValue(c.Subject), RoleID: types.StringValue(c.RoleID)})
	}
	return out
}

// legacyGrantsImportKey is the private state key flagging an instance that was
// just imported, so its first read adopts the legacy grants (collaborators or
// virtual account permissions) found on the control plane — every later read
// leaves an unset attribute unset.
const legacyGrantsImportKey = "legacy_grants_import"

// markImported flags a just-imported instance for its first read.
func markImported(ctx context.Context, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, legacyGrantsImportKey, []byte("true"))...)
}

// consumeImportMark reports whether this read directly follows an import, and
// clears the flag so it only applies once.
func consumeImportMark(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) bool {
	value, diags := req.Private.GetKey(ctx, legacyGrantsImportKey)
	resp.Diagnostics.Append(diags...)
	if len(value) == 0 {
		return false
	}
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, legacyGrantsImportKey, nil)...)
	return true
}
