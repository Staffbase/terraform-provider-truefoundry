default: build

build:
	go build ./...

install:
	go install .

fmt:
	gofmt -w .

lint:
	gofmt -l .
	go vet ./...

test:
	go test ./...

# Acceptance tests run the real terraform CLI against an in-memory mock control plane.
testacc:
	TF_ACC=1 go test ./internal/provider/ -v -timeout 15m

# Regenerates docs/ from the provider schema and examples/.
# tfplugindocs is pinned as a tool dependency in go.mod.
docs:
	go tool tfplugindocs generate --provider-name truefoundry --rendered-provider-name TrueFoundry

docs-check:
	go tool tfplugindocs validate --provider-name truefoundry

.PHONY: default build install fmt lint test testacc docs docs-check
