# AGENTS.md

Instructions for AI coding agents (Jules, Claude, etc.) working in this repository.

## What this is

`scaffold-cli` is a Go CLI (module `scaffold-engine-go`) that renders projects from an external
templates repository. **Nothing about scaffolds, versions, templates, or CLI flags is hardcoded
here** — this engine only knows how to walk an inheritance chain (`scaffold → version → dimension →
template → overlay`) and render it. All actual scaffolding content lives in the companion repo
[scaffold-templates](https://github.com/yusronMu77/scaffold-templates), expected as a sibling
checkout named `scaffolding-code` (or pointed to via `--scaffolding-code=<path>` / `SCAFFOLD_CODE` /
`.scaffold.yaml`). That companion repo is **not present** in a fresh clone of this one — anything
depending on it (e.g. `scaffold lint --build` against real template content) cannot be exercised
here without cloning it separately.

## Build & test

```bash
go build -o scaffold .      # build the binary
go test ./...                # run all unit tests
go vet ./...                  # static checks
gofmt -l .                    # check formatting (fix with: gofmt -w .)
```

There is no CI workflow that runs tests on pull requests yet — `.github/workflows/release.yml` only
builds/publishes binaries on tag push. Always run `go build ./...` and `go test ./...` locally
before opening a PR; there is no CI gate to catch failures for you.

Requires Go 1.26+ (see `go.mod`).

## Code layout

- `main.go` — entrypoint, delegates to `internal/cmd`.
- `internal/cmd/` — Cobra commands: `list`, `create`, `lint`, plus flag/arg/config plumbing
  (`args.go`, `config.go`, `values.go`).
- `internal/discovery/` — finds and resolves scaffolds/versions/templates in the templates repo.
- `internal/jig/` — parses and validates `jig.yaml` (the file that declares a template's contract).
- `internal/render/` — the rendering/merge engine: walks the inheritance chain, merges data, writes
  output files, verifies results.

Each package has its own `_test.go` files colocated — follow that pattern for new code rather than
a separate `test/` tree.

## Conventions

- Commit messages follow `task(<scope>): [Issue-#N] <description>` (see `git log`) — GitHub
  auto-links the `#N` to the issue, and the release workflow uses raw `git log` output as changelog
  notes, so keep messages meaningful as standalone lines.
- No linter config is checked in yet; stick to standard `gofmt`/`go vet` cleanliness.
- Keep the "nothing hardcoded" principle: don't add scaffold/template-specific logic into this
  engine — that belongs in `scaffold-templates`, not here.

## PRs

Open PRs against `main`. Include what you ran (`go build`, `go test ./...`) in the PR description.
Don't touch `.github/workflows/release.yml` behavior (tag-triggered release process) unless the task
is specifically about the release pipeline.
