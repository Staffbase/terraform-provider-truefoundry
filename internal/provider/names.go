package provider

import "regexp"

// The rules below mirror the JSON-schema patterns the TrueFoundry control
// plane enforces on manifest names. They were read off a live control plane on
// 2026-09-08, one dryRun apply per manifest family, because neither the
// TypeScript nor the Python SDK carries any length or pattern constraint. Every
// pattern has the shape "^[a-z][a-z0-9\-]{1,N}[a-z0-9]$" — only N differs, and
// the gateway config manifests are not name-validated at all.
var (
	// nameRegexp is the control plane's manifest name shape: a lowercase
	// letter first, a lowercase letter or digit last, at least one
	// alphanumeric-or-hyphen character in between (the "{1,N}" in the shape
	// above), so it never matches fewer than 3 characters on its own.
	nameRegexp = regexp.MustCompile(`^[a-z][a-z0-9-]+[a-z0-9]$`)

	// mlRepoNameRegexp is the shape of "^[a-zA-Z][a-zA-Z0-9\-]{1,98}[a-zA-Z0-9]$"
	// (ML repo names may contain upper case, unlike most other manifest
	// families — tenant/team budget config names allow it too); the
	// regex itself only enforces the minimum (3 characters), not the 98-char
	// upper bound on the middle segment — that is checked separately via
	// maxMlRepoNameLen.
	mlRepoNameRegexp = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]+[a-zA-Z0-9]$`)

	// secretGroupNameRegexp is the shape of
	// "^(?!.*--)[a-z][a-z0-9-]{3,48}[a-z0-9]$". RE2 has no lookahead, so the
	// "no consecutive hyphens" rule is instead expressed by only ever allowing
	// a hyphen directly before an alphanumeric (an optional-hyphen group
	// repeated, not an alternation); the 5-50 length bound is checked
	// separately via minSecretGroupNameLen/maxSecretGroupNameLen.
	secretGroupNameRegexp = regexp.MustCompile(`^[a-z](-?[a-z0-9])*$`)

	// artifactNameRegexp validates truefoundry_prompt and
	// truefoundry_agent_skill names. Deliberately kept permissive: unlike
	// every other family in this file, prompt-versions/agent-skill-versions
	// ignore dryRun and persist on apply, so their real name limit can never
	// be safely probed (see AGENTS.md "Manifest name rules") — tightening
	// this to nameRegexp's control-plane-verified shape would risk rejecting
	// configurations the real API accepts.
	artifactNameRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// Verified name length bounds per manifest family. minNameLen is the lower
// bound for every family that shares nameRegexp (which alone already rejects
// anything shorter than 3 characters); secret groups and prompt/agent-skill
// artifacts use their own regexes and bounds, defined with their own
// constants below and enforced separately from these.
const (
	minNameLen = 3

	// maxAccountNameLen applies to the model provider accounts (openai,
	// azure-openai, azure-foundry, aws-bedrock, custom-endpoint) and to the
	// truefoundry secret store account.
	maxAccountNameLen = 32

	// maxWideAccountNameLen applies to the azure blob storage, hashicorp and
	// slack accounts, to guardrail config groups and to virtual accounts.
	maxWideAccountNameLen = 36

	// maxVirtualModelNameLen applies to virtual-model provider accounts.
	maxVirtualModelNameLen = 64

	// maxAgentNameLen applies to both agent resources.
	maxAgentNameLen = 32

	// maxMcpServerNameLen applies to remote, OpenAPI and virtual MCP servers.
	maxMcpServerNameLen = 73

	// maxRoleNameLen applies to custom roles.
	maxRoleNameLen = 35

	// maxRoleBindingNameLen applies to role bindings. Unlike the other
	// families it comes from the published API reference (pattern
	// "^[a-z][a-z0-9-]{1,64}[a-z0-9]$"), not from a dryRun probe.
	maxRoleBindingNameLen = 66

	// maxMlRepoNameLen applies to ML repos.
	maxMlRepoNameLen = 100

	// Secret group names are bounded on both ends.
	minSecretGroupNameLen = 5
	maxSecretGroupNameLen = 50
)
