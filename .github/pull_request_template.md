<!--
  Label this PR — release-drafter derives the next version and the changelog
  section from labels. See AGENTS.md "Pull requests" for the label table.
-->

## Goal

<!-- What problem does this solve, and why now? One or two sentences. -->

## What changed

<!--
  Describe the change, not the file list (GitHub already shows files).
  For a new/changed resource or data source, name it, e.g.
  `truefoundry_agent_skill`, and say what part of the control-plane API it maps to.
-->

## How to review

<!--
  Where should the reviewer start, and how can they convince themselves it works?
  E.g. "start at internal/provider/resource_x.go, then the acceptance test."
-->

## Terraform impact

- [ ] No practitioner-visible change (internal refactor, tests, CI)
- [ ] New resource / data source / attribute (additive)
- [ ] **Breaking**: renamed or removed attribute, changed default, new required
      field, or a change that forces replacement of existing resources

<!-- If breaking, describe the upgrade path for existing configurations. -->

## Checklist

- [ ] `make lint test` passes
- [ ] `make testacc` passes (required for any `internal/provider/` change)
- [ ] `examples/` updated and `make docs` re-run — `docs/` is generated, never hand-edited
- [ ] Validation kept in sync between the resource's `ValidateConfig` and
      `mockcp.validateManifest`
- [ ] README / AGENTS.md updated if behaviour or API contract knowledge changed
- [ ] Commits follow Conventional Commits (`feat(ABC-123): …`) — CI checks this
- [ ] Labelled for release-drafter
