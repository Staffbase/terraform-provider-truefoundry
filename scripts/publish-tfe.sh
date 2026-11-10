#!/usr/bin/env bash
# Copyright 2026, Staffbase SE and contributors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
#
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

#
# Publishes the goreleaser artifacts in dist/ as a provider version to a
# Terraform Enterprise private registry. Runs in the release workflow, but
# can also be run locally (see RELEASING.md).
#
# Required environment:
#   TFE_TOKEN        API token with "Manage Private Registry" permission
#   GPG_FINGERPRINT  fingerprint of the GPG key that signed the SHA256SUMS
# Optional (defaults for this repo):
#   TFE_HOST=terraform.staffbase.com  TFE_ORG=staffbase
#   PROVIDER_NAME=truefoundry         DIST_DIR=dist
#   TFE_ADDRESS=https://$TFE_HOST     (full base URL, overrides TFE_HOST)
set -euo pipefail

TFE_HOST="${TFE_HOST:-terraform.staffbase.com}"
TFE_ADDRESS="${TFE_ADDRESS:-https://${TFE_HOST}}"
TFE_ORG="${TFE_ORG:-staffbase}"
PROVIDER_NAME="${PROVIDER_NAME:-truefoundry}"
DIST_DIR="${DIST_DIR:-dist}"

: "${TFE_TOKEN:?TFE_TOKEN is required}"
: "${GPG_FINGERPRINT:?GPG_FINGERPRINT is required}"
command -v jq >/dev/null || { echo "ERROR: jq is required" >&2; exit 1; }

prefix="terraform-provider-${PROVIDER_NAME}"

shasums_file=$(find "$DIST_DIR" -maxdepth 1 -name "${prefix}_*_SHA256SUMS" | head -n1)
[ -n "$shasums_file" ] || { echo "ERROR: no ${prefix}_*_SHA256SUMS in $DIST_DIR/ — run goreleaser first" >&2; exit 1; }
sig_file="${shasums_file}.sig"
[ -f "$sig_file" ] || { echo "ERROR: missing signature $sig_file" >&2; exit 1; }

version=$(basename "$shasums_file" | sed -E "s/^${prefix}_(.+)_SHA256SUMS$/\1/")
# the registry identifies keys by the long key ID: last 16 hex digits of the
# fingerprint, uppercase
key_id=$(printf '%s' "$GPG_FINGERPRINT" | tr '[:lower:]' '[:upper:]')
key_id="${key_id: -16}"

echo "Publishing ${TFE_ORG}/${PROVIDER_NAME} v${version} to ${TFE_ADDRESS} (GPG key ${key_id})"

RESP=$(mktemp)
trap 'rm -f "$RESP"' EXIT

# tfe_api METHOD PATH [JSON_BODY] — response body lands in $RESP, HTTP status
# in $HTTP_STATUS
tfe_api() {
  local method=$1 path=$2 body=${3:-}
  local args=(-sS -X "$method"
    -H "Authorization: Bearer $TFE_TOKEN"
    -H "Content-Type: application/vnd.api+json"
    -o "$RESP" -w "%{http_code}")
  [ -n "$body" ] && args+=(-d "$body")
  HTTP_STATUS=$(curl "${args[@]}" "${TFE_ADDRESS}${path}")
}

# 1. Create the provider entry (idempotent: 422 "taken" means it exists)
tfe_api POST "/api/v2/organizations/${TFE_ORG}/registry-providers" "$(jq -n \
  --arg name "$PROVIDER_NAME" --arg ns "$TFE_ORG" \
  '{data: {type: "registry-providers",
           attributes: {name: $name, namespace: $ns, "registry-name": "private"}}}')"
if [ "$HTTP_STATUS" = "201" ]; then
  echo "Created provider entry ${TFE_ORG}/${PROVIDER_NAME}"
elif [ "$HTTP_STATUS" = "422" ] && jq -e '.errors[]?.detail | select(test("taken"))' "$RESP" >/dev/null; then
  echo "Provider entry ${TFE_ORG}/${PROVIDER_NAME} already exists"
else
  echo "ERROR: creating provider entry failed (HTTP $HTTP_STATUS):" >&2
  cat "$RESP" >&2
  exit 1
fi

# 2. Create the version
versions_path="/api/v2/organizations/${TFE_ORG}/registry-providers/private/${TFE_ORG}/${PROVIDER_NAME}/versions"
tfe_api POST "$versions_path" "$(jq -n --arg v "$version" --arg kid "$key_id" \
  '{data: {type: "registry-provider-versions",
           attributes: {version: $v, "key-id": $kid, protocols: ["6.0"]}}}')"
if [ "$HTTP_STATUS" = "422" ] && jq -e '.errors[]?.detail | select(test("taken"))' "$RESP" >/dev/null; then
  echo "ERROR: version ${version} already exists in the registry." >&2
  echo "Releases are immutable — delete the version in TFE first, or tag a new version." >&2
  exit 1
elif [ "$HTTP_STATUS" != "201" ]; then
  echo "ERROR: creating version failed (HTTP $HTTP_STATUS):" >&2
  cat "$RESP" >&2
  exit 1
fi

shasums_upload=$(jq -r '.data.links."shasums-upload"' "$RESP")
shasums_sig_upload=$(jq -r '.data.links."shasums-sig-upload"' "$RESP")

# 3. Upload checksums + signature (pre-signed URLs, no auth header)
curl -sSf -T "$shasums_file" "$shasums_upload"
curl -sSf -T "$sig_file" "$shasums_sig_upload"
echo "Uploaded SHA256SUMS and signature"

# 4. Create a platform per zip in the SHA256SUMS and upload the binary
while read -r shasum filename; do
  base=${filename%.zip}
  arch=${base##*_}
  rest=${base%_*}
  os=${rest##*_}

  tfe_api POST "${versions_path}/${version}/platforms" "$(jq -n \
    --arg os "$os" --arg arch "$arch" --arg shasum "$shasum" --arg fn "$filename" \
    '{data: {type: "registry-provider-version-platforms",
             attributes: {os: $os, arch: $arch, shasum: $shasum, filename: $fn}}}')"
  if [ "$HTTP_STATUS" != "201" ]; then
    echo "ERROR: creating platform ${os}/${arch} failed (HTTP $HTTP_STATUS):" >&2
    cat "$RESP" >&2
    exit 1
  fi

  binary_upload=$(jq -r '.data.links."provider-binary-upload"' "$RESP")
  curl -sSf -T "${DIST_DIR}/${filename}" "$binary_upload"
  echo "Uploaded ${os}/${arch} (${filename})"
done < <(grep '\.zip$' "$shasums_file")

echo "Done: ${TFE_ADDRESS}/app/${TFE_ORG}/registry/providers/private/${TFE_ORG}/${PROVIDER_NAME}/${version}"
