# scaffold-cli

🚀 A CLI tool written in Go to standardize project scaffolding, library creation, and internal
microservices using predefined templates and architectural patterns. Say goodbye to boilerplate!

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

`scaffold-cli` is a universal, dynamically-extensible scaffolding engine. Frameworks, versions,
axes, categories, selector values, and even the CLI flag names themselves are declared by
templates at runtime — none of it is hardcoded in the binary. The engine only knows how to walk an
inheritance chain and render; what gets generated comes entirely from a separate templates
repository (see [Requirements](#requirements) below).

## Requirements

- Go 1.26 or newer (to build from source).
- A templates repository laid out for this engine — see
  [scaffold-templates](https://github.com/yusronMu77/scaffold-templates), the companion repo that
  holds the actual frameworks, versions, categories, and their `jig.yaml`-driven inheritance tree.
  `scaffold-cli` itself ships with none of that content; it only knows how to read and render it.
  By default the CLI looks for a checkout of it in a folder literally named `scaffolding-code` next
  to where you run it — clone it under that name, or point elsewhere with `--scaffolding-code=<path>`
  (see [Configuration](#configuration) for every way to set that path).

## Build

```bash
git clone https://github.com/yusronMu77/scaffold-cli.git
cd scaffold-cli
go build -o scaffold .
```

This produces a `scaffold` (or `scaffold.exe` on Windows) binary in the current directory. Verify
it with:

```bash
./scaffold --version
```

## Configuration

The `--scaffolding-code=<path>` flag can be skipped on every invocation by setting it once,
resolved in this order (first match wins):

1. `--scaffolding-code=<path>` on the command line.
2. The `SCAFFOLD_CODE` environment variable.
3. `scaffolding_code: <path>` in a `.scaffold.yaml` in the current directory.
4. `scaffolding_code: <path>` in a `.scaffold.yaml` in your home directory.
5. A `scaffolding-code` folder next to the `scaffold` executable itself.
6. `./scaffolding-code`, as a last resort.

Example `.scaffold.yaml`:

```yaml
scaffolding_code: ../scaffold-templates
```

## Usage

`scaffold-cli` has three commands: `list` to browse what's available, `create` to generate an
artefact, and `lint` to check that a templates repository is healthy.

### `list` — browse what's available

```bash
scaffold list                          # known frameworks
scaffold list <framework>              # versions, categories, and optional axes
scaffold list <framework> <category>   # full selector tree and variables for that category
```

### `create` — generate an artefact

```bash
scaffold create <framework> <category> <name> [--flag=value ...]
```

All three positional arguments (framework, category, name) can also come from a values file:

```bash
scaffold create -f values.yaml
scaffold create -f base.yaml -f prod.yaml --name=payment-canary   # -f may repeat, later wins
```

A values file is just the flag namespace without the dashes (`--package=x` becomes `package: x`).
Command-line flags always win over values-file entries.

Useful flags for inspecting before you write anything:

| Flag         | What it shows                                            |
|--------------|-----------------------------------------------------------|
| `--dry-run`  | Which files would be produced                             |
| `--print`    | What is actually in them, printed to stdout                |
| `--explain`  | Which level of the inheritance chain contributed each file |

### `lint` — check that the templates repository is healthy

```bash
scaffold lint [<framework>] [--build]
```

Renders every registered combination in memory and reports anything that breaks — a value with no
manifest, a template that fails to parse, a variable with no default, and so on. Add `--build` to
also run each combination's own `verify:` commands against a real scratch build (slower, but the
only way to know the generated project actually builds). Exit code is non-zero on any failure, so
it works as a CI gate.

## License

Distributed under the [MIT License](LICENSE).

## Support

If `scaffold-cli` saves you some boilerplate, consider supporting its development:

- ☕ [Ko-fi](https://ko-fi.com/yusronmu77)
- 💛 [Teer.id](https://teer.id/yusronmu77)
