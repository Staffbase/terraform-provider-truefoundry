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

	// mlRepoNameRegexp mirrors "^[a-zA-Z][a-zA-Z0-9\-]{1,98}[a-zA-Z0-9]$":
	// ML repo names may contain upper case, unlike other manifest names.
	mlRepoNameRegexp = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]+[a-zA-Z0-9]$`)

	// secretGroupNameRegexp mirrors "^(?!.*--)[a-z][a-z0-9-]{3,48}[a-z0-9]$".
	// RE2 has no lookahead, so the "no consecutive hyphens" rule is expressed
	// as an alternation instead.
	secretGroupNameRegexp = regexp.MustCompile(`^[a-z](-?[a-z0-9])*$`)
)

// Verified name length bounds per manifest family. Names shorter than
// minNameLen cannot satisfy the patterns above.
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

	// maxMlRepoNameLen applies to ML repos.
	maxMlRepoNameLen = 100

	// Secret group names are bounded on both ends.
	minSecretGroupNameLen = 5
	maxSecretGroupNameLen = 50
)
