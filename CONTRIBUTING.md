# Contributing

Contributions are welcome: bug reports, feature requests and pull requests.
Please open an issue first for larger changes, so we can agree on the approach
before you invest time.

## Contributor License Agreement

Staffbase uses CLAs, or Contributor License Agreements, to manage contributions to Free and Open Source Software projects that it hosts.

Staffbase uses two versions of the CLA:

- An **Individual CLA** for individual contributors. This can be signed through CLA Assistant when submitting a commit in GitHub.
- A **Corporate CLA**, for companies that would like to sign one CLA for their contributors. Please contact opensource(at)staffbase.com in this case.

Staffbase’s two CLAs are based on the Apache Software Foundation CLAs that are widely in use for various free and open source software projects. Our changes are generally limited to replacing Staffbase as the licensee, minor formatting changes and headings added for readability, and to comply with our CLA process integrated into GitHub or our processes around signature and management of the software project.

The individual CLA is in [CLA.md](CLA.md). To sign it, comment on your pull
request: "I have read the CLA Document and I hereby sign the CLA".

## Development

```sh
make build     # compile
make lint      # gofmt + go vet
make test      # unit tests
make testacc   # acceptance tests (needs the terraform CLI on PATH)
make docs      # regenerate docs/ after changing schemas, templates or examples
```

Acceptance tests run against an in-memory mock control plane, so no TrueFoundry
credentials are needed. [AGENTS.md](AGENTS.md) describes the architecture and
the quirks of the TrueFoundry API.

## Pull requests

- Follow [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat: ...`, `fix: ...`); CI checks every commit.
- Add tests for behaviour changes and run `make lint test testacc`.
- `docs/` is generated: change the schema descriptions, `templates/` or
  `examples/` and run `make docs`.
- Every Go source file starts with the Apache 2.0 license header.
