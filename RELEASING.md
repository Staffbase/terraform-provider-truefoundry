# Releasing

Releases are published to the Staffbase Terraform Enterprise private registry
as `terraform.staffbase.com/staffbase/truefoundry`, plus a GitHub release for
visibility. Pushing a `v*` tag triggers `.github/workflows/release.yml`:
goreleaser builds and GPG-signs the artifacts, then `scripts/publish-tfe.sh`
uploads them to the registry via the
[TFE provider registry API](https://developer.hashicorp.com/terraform/enterprise/registry/publish-providers).

## Cutting a release

[Release-drafter](.github/release-drafter.yml) keeps a draft GitHub release up
to date on every push to `main`; the next version is resolved from PR labels
(`breaking` → major, `feature`/`enhancement` → minor, everything else →
patch).

To release: open the [draft release](https://github.com/Staffbase/terraform-provider-truefoundry/releases),
review notes and version, and click **Publish**. Publishing creates the `v*`
tag, which triggers the *Release* workflow (goreleaser attaches the signed
artifacts to the release and `scripts/publish-tfe.sh` uploads them to the
registry).

Pushing a tag manually also works (the workflow then creates the GitHub
release itself, without drafted notes):

```sh
git checkout main && git pull
git tag v0.2.0
git push origin v0.2.0
```

Watch the *Release* workflow. When it finishes, the version is visible at
<https://terraform.staffbase.com/app/staffbase/registry/providers/private/staffbase/truefoundry>
and consumable with:

```hcl
terraform {
  required_providers {
    truefoundry = {
      source  = "terraform.staffbase.com/staffbase/truefoundry"
      version = "~> 0.2"
    }
  }
}
```

Registry versions are immutable: the publish script refuses to overwrite an
existing version. To re-release, delete the version in the TFE registry UI
first, or (better) tag a new patch version.

## One-time bootstrap

Everything below has to happen only once (or when rotating keys/tokens).

### 1. Generate the GPG signing key

TFE requires the `SHA256SUMS` file of every provider version to carry a
detached GPG signature from a registered key. Generate a dedicated key:

```sh
gpg --full-generate-key
# kind: RSA (sign only), size: 4096, expiry: 0 (does not expire)
# real name: Staffbase TrueFoundry Terraform Provider Signing
# email: <team email>
# choose a strong passphrase and keep it for step 3

gpg --list-secret-keys --keyid-format long  # note the fingerprint
gpg --armor --export <FINGERPRINT> > gpg-public.asc
gpg --armor --export-secret-keys <FINGERPRINT> > gpg-private.asc  # handle with care, delete after step 3
```

### 2. Register the public key in the registry

Requires a TFE token with *Manage Private Registry* permission on the
`staffbase` organization (see step 3 for the token):

```sh
curl -sS --fail-with-body \
  -H "Authorization: Bearer $TFE_TOKEN" \
  -H "Content-Type: application/vnd.api+json" \
  -d "$(jq -n --rawfile key gpg-public.asc \
        '{data: {type: "gpg-keys", attributes: {namespace: "staffbase", "ascii-armor": $key}}}')" \
  https://terraform.staffbase.com/api/registry/private/v2/gpg-keys
```

Verify with
`curl -H "Authorization: Bearer $TFE_TOKEN" "https://terraform.staffbase.com/api/registry/private/v2/gpg-keys?filter%5Bnamespace%5D=staffbase"`.

### 3. Create the GitHub Actions secrets

In this repository (Settings → Secrets and variables → Actions):

| Secret            | Value                                                              |
| ----------------- | ------------------------------------------------------------------ |
| `GPG_PRIVATE_KEY` | contents of `gpg-private.asc`                                      |
| `GPG_PASSPHRASE`  | the key's passphrase                                               |
| `TFE_TOKEN`       | TFE token with *Manage Private Registry* on the `staffbase` org    |

For `TFE_TOKEN`, prefer a [team token](https://developer.hashicorp.com/terraform/enterprise/users-teams-organizations/api-tokens#team-api-tokens)
of a dedicated team that has only the *Manage Private Registry*
organization permission — not the organization token, which has far broader
rights.

Afterwards delete the local `gpg-private.asc`.

## Publishing manually (escape hatch)

If CI is unavailable, the same flow works locally:

```sh
GPG_FINGERPRINT=<fingerprint> goreleaser release --clean --skip=publish
TFE_TOKEN=<token> GPG_FINGERPRINT=<fingerprint> ./scripts/publish-tfe.sh
```

The script creates the provider registry entry on first use, so no manual
setup is needed beyond the GPG key and token above.

## Notes

- The TFE private registry does **not** render provider documentation; the
  generated `docs/` directory serves GitHub readers.
- `terraform-registry-manifest.json` (protocol `6.0`) is not consumed by the
  TFE API but is attached to GitHub releases to keep a later public-registry
  publish possible.
