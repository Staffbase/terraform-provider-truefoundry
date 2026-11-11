# Releasing

Releases are published to the public Terraform Registry as
[`staffbase/truefoundry`](https://registry.terraform.io/providers/staffbase/truefoundry).
Pushing a `v*` tag triggers `.github/workflows/release.yml`: goreleaser builds
and GPG-signs the artifacts, attaches them to the GitHub release and publishes
it. The registry receives a webhook for every published release and ingests
the version from its assets.

## Cutting a release

[Release-drafter](.github/release-drafter.yml) keeps a draft GitHub release up
to date on every push to `main`; the next version is resolved from PR labels
(`breaking` → major, `feature`/`enhancement` → minor, everything else →
patch).

To release:

1. Open the [draft release](https://github.com/Staffbase/terraform-provider-truefoundry/releases)
   and review the notes and the version. **Do not click Publish**: the
   registry would ingest the release before its artifacts are attached and
   fail.
2. Push the tag shown in the draft:

   ```sh
   git checkout main && git pull
   git tag v0.15.0
   git push origin v0.15.0
   ```

3. Watch the *Release* workflow. goreleaser uploads the artifacts to the
   draft (`use_existing_draft`) and publishes it once all are attached.

The version shows up on the registry within a few minutes. If it does not,
open the provider's settings on the registry and click **Resync**, and check
the registry's email to the publishing account for ingestion errors.

Registry versions cannot be overwritten. To fix a broken release, tag a new
patch version. A version can be deleted in the registry settings, but
consumers that already locked it will then fail to install it.

## Signing key

The registry verifies the detached signature of every `SHA256SUMS` file
against the public keys registered for the `staffbase` namespace. The
signing key is RSA (the registry does not accept ECC keys) with fingerprint
`B626E235596F9B5821190F1DF245E42A6E40E02D`. Its private half and passphrase
are stored as the repository secrets `GPG_PRIVATE_KEY` and `GPG_PASSPHRASE`.

To rotate it, generate a new RSA key (`gpg --full-generate-key`, RSA sign
only, 4096 bits), add its ASCII-armored public key to the namespace under
**User Settings → Signing Keys** on the registry, then replace both secrets.
Keep the old public key registered: the registry verifies existing versions
against it.

## One-time setup for the public registry

1. Make the repository public. The registry only reads public repositories.
2. Allow the *Terraform Registry* OAuth app for the `Staffbase` GitHub
   organization (organization owner, *Settings → Third-party access*).
3. Sign in to <https://registry.terraform.io> with a GitHub account that is
   an admin of the repository, and add the public signing key (see above)
   for the `staffbase` namespace under **User Settings → Signing Keys**:

   ```sh
   gpg --armor --export B626E235596F9B5821190F1DF245E42A6E40E02D
   ```

4. **Publish → Provider**, choose `Staffbase/terraform-provider-truefoundry`.
   The registry installs a webhook on the repository. Use **Resync** if
   older releases are missing afterwards.

## Publishing manually (escape hatch)

If CI is unavailable, run goreleaser locally with the signing key imported:

```sh
GPG_FINGERPRINT=<fingerprint> GITHUB_TOKEN=<token> goreleaser release --clean
```

## Notes

- The registry renders the documentation from `docs/` at the released tag.
- `terraform-registry-manifest.json` (protocol `6.0`) is attached to every
  release; the registry rejects releases without it.
