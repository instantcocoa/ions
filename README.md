# ions

A local GitHub Actions workflow runner that achieves high fidelity by using the **official GitHub Actions runner binary** ([actions/runner](https://github.com/actions/runner)) for step execution, with all orchestration implemented in Go.

Unlike [act](https://github.com/nektos/act), which reimplements execution itself, ions delegates step execution to the real runner and implements everything around it: workflow parsing, job scheduling, the HTTP protocol the runner speaks, and local implementations of GitHub's artifact, cache, and action download services.

The result is that actions like `actions/checkout`, `actions/setup-node`, `actions/cache`, and `actions/upload-artifact` work out of the box — they're running against the same runner binary they'd use on GitHub.

## Quick start

```bash
# Build
go build -o ions ./cmd/ions/

# Run a workflow
./ions run .github/workflows/ci.yml

# Run with verbose output
./ions run .github/workflows/ci.yml -v

# Run a specific job
./ions run .github/workflows/ci.yml --job build

# Inject secrets
./ions run .github/workflows/ci.yml --secret GITHUB_TOKEN=ghp_xxx
```

On first run, ions automatically downloads and installs the runner binary to `~/.ions/runner/`.

## Installation

Requires Go 1.25+ and git.

```bash
git clone https://github.com/instantcocoa/ions.git
cd ions
go build -o ions ./cmd/ions/
```

For workflows that use service containers (e.g., postgres), Docker must be installed and running.

## Commands

### `ions run [workflow-file]`

Run a workflow locally. Defaults to `.github/workflows/ci.yml` if no file is specified.

```bash
# Basic run
ions run .github/workflows/ci.yml

# Override event type
ions run .github/workflows/ci.yml --event pull_request

# Inject secrets, variables, and environment
ions run .github/workflows/ci.yml \
  --secret API_KEY=abc123 \
  --var ENVIRONMENT=staging \
  --env DEBUG=true

# Workflow dispatch inputs
ions run .github/workflows/deploy.yml --input version=1.2.3

# Dry run — show execution plan without running
ions run .github/workflows/ci.yml --dry-run

# Override artifact storage location
ions run .github/workflows/ci.yml --artifact-dir ./my-artifacts

# Keep containers running after completion (for debugging)
ions run .github/workflows/ci.yml --reuse-containers
```

| Flag | Description |
|------|-------------|
| `--job <name>` | Run only a specific job |
| `--event <name>` | Override event name (default: `push`) |
| `--secret KEY=VALUE` | Inject a secret (repeatable) |
| `--var KEY=VALUE` | Inject a variable (repeatable) |
| `--env KEY=VALUE` | Inject an environment variable (repeatable) |
| `--input KEY=VALUE` | Inject a workflow_dispatch input (repeatable) |
| `--dry-run` | Print execution plan without running |
| `--artifact-dir PATH` | Override artifact storage location |
| `--reuse-containers` | Don't remove containers after run |
| `--platform <os/arch>` | Override platform detection (e.g., `linux/amd64`) |
| `-v, --verbose` | Show runner output and debug logs |

### `ions list [workflow-file]`

List all jobs in a workflow with their execution stages and dependencies.

```bash
ions list .github/workflows/ci.yml
```

### `ions validate [workflow-file]`

Validate workflow YAML and expressions without running.

```bash
ions validate .github/workflows/ci.yml
```

### `ions runner install`

Explicitly install or update the runner binary.

```bash
# Install latest
ions runner install

# Install specific version
ions runner install --version v2.332.0
```

### `ions clean`

Remove runner caches, work directories, and stopped containers.

```bash
ions clean
```

## What works

ions has been tested with these workflow patterns:

- **Shell steps** — `run:` commands with bash, multi-line scripts, environment variables
- **Marketplace actions** — `actions/checkout@v4`, `actions/setup-node@v4`, `actions/setup-go@v5`, `actions/cache@v4`, `actions/upload-artifact@v4`, `actions/download-artifact@v4`
- **Job dependencies** — `needs:` with output passing between jobs
- **Matrix strategy** — multi-dimensional matrix expansion with concurrent execution (tested with 8 parallel jobs)
- **Conditional execution** — `if:` conditions at job and step level, `continue-on-error`
- **Service containers** — Docker services (e.g., postgres) with port mapping and health checks
- **Composite actions** — local composite actions with inputs, outputs, and multi-step execution
- **Expressions** — `${{ }}` with all standard contexts (`github`, `env`, `steps`, `needs`, `matrix`, `secrets`, `inputs`, `vars`, `runner`, `strategy`, `job`) and functions (`contains`, `startsWith`, `format`, `toJSON`, `fromJSON`, `hashFiles`, `success()`, `failure()`, `always()`, etc.)
- **Artifacts** — upload and download between jobs via v4 (twirp) and v3 (pipelines) APIs
- **Caching** — save and restore with key matching, LRU eviction
- **Secret masking** — secret values are replaced with `***` in all log output
- **Real-world workflows** — production CI pipelines with checkout + setup-go + buf + go build/vet/lint

## Example workflows

### Hello world

```yaml
name: Hello World
on: push
jobs:
  greet:
    runs-on: ubuntu-latest
    steps:
      - run: echo "Hello, World!"
```

```bash
ions run hello-world.yml
```

### Matrix build

```yaml
name: Matrix Build
on: push
jobs:
  test:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest]
        node: [18, 20, 22, 24]
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version: ${{ matrix.node }}
      - run: node --version
```

Expands to 8 concurrent jobs and runs them in parallel.

### Service containers

```yaml
name: Integration Tests
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:15
        env:
          POSTGRES_PASSWORD: postgres
          POSTGRES_DB: testdb
        ports:
          - 15432:5432
    steps:
      - run: pg_isready -h localhost -p 15432
```

### Composite actions

```yaml
# testdata/actions/greet/action.yml
name: Greet
inputs:
  name:
    default: 'World'
outputs:
  greeting:
    value: ${{ steps.greet.outputs.greeting }}
runs:
  using: composite
  steps:
    - id: greet
      run: |
        echo "greeting=Hello, ${{ inputs.name }}!" >> "$GITHUB_OUTPUT"
      shell: bash
```

```yaml
# workflow
steps:
  - uses: ./testdata/actions/greet
    id: greet
    with:
      name: ions
  - run: echo "${{ steps.greet.outputs.greeting }}"
    # prints: Hello, ions!
```

## Architecture

```
                          ┌─────────────┐
                          │   ions CLI   │
                          └──────┬──────┘
                                 │
                          ┌──────▼──────┐
                          │ Orchestrator │
                          └──────┬──────┘
                                 │
              ┌──────────────────┼──────────────────┐
              │                  │                   │
       ┌──────▼──────┐   ┌──────▼──────┐    ┌───────▼──────┐
       │   Broker     │   │   Docker     │    │   Artifact   │
       │  (HTTP API)  │   │  (services)  │    │  + Cache     │
       └──────┬──────┘   └─────────────┘    │  servers     │
              │                              └──────────────┘
       ┌──────▼──────┐
       │   GitHub     │
       │   Actions    │
       │   Runner     │
       │   Binary     │
       └─────────────┘
```

**Orchestrator** (`internal/orchestrator/`) — Parses the workflow, builds the job dependency graph, expands matrix strategies, manages runner lifecycles, and coordinates Docker service containers.

**Broker** (`internal/broker/`) — An HTTP server that implements the protocol the runner binary speaks. The runner thinks it's talking to GitHub; it's actually talking to the broker on localhost. Handles job dispatch, timeline updates, log streaming, action tarball proxying, and job completion events.

**Runner** (`internal/runner/`) — Downloads, installs, configures, and manages runner binary processes. Handles the runner registration flow with JWT tokens and OAuth credentials.

**Expression evaluator** (`internal/expression/`) — Full implementation of GitHub's `${{ }}` expression language with lexer, parser, and evaluator. Supports all operators, type coercion rules, and built-in functions.

**Workflow parser** (`internal/workflow/`) — Parses GitHub Actions workflow YAML into typed Go structs. Handles triggers, jobs, steps, matrix strategies, containers, permissions, defaults, and concurrency.

**Graph** (`internal/graph/`) — Builds a DAG from job `needs:` dependencies. Performs topological sort, detects cycles, identifies parallelizable jobs, and expands matrix strategies into concrete job nodes.

**Context** (`internal/context/`) — Builds the full expression context (`github`, `env`, `steps`, `needs`, `matrix`, `runner`, etc.) from the local git repo, workflow definition, and runtime state.

**Docker** (`internal/docker/`) — Manages service containers via the Docker CLI. Creates networks, starts containers with port mapping and environment injection, and handles health check waiting.

**Cache** (`internal/cache/`) — Local implementation of GitHub's cache API (`/_apis/artifactcache/*`). Supports key matching with restore-key prefix fallback and LRU eviction.

**Artifacts** (`internal/artifacts/`) — Local implementation of GitHub's artifact API. Supports both v4 (twirp + Azure Blob Storage compat) and v3 (pipelines) protocols.

## How the broker works

The runner binary is designed to talk to GitHub's API. ions intercepts this by configuring the runner to point at a local HTTP server (the broker) instead. The broker implements the runner's protocol:

1. **Registration** — Runner sends credentials; broker responds with a session token
2. **Job polling** — Runner long-polls for work; broker responds with an `AgentJobRequestMessage` containing the job definition, steps, context data, and action references
3. **Action download** — Runner requests action tarballs; broker proxies to GitHub's API and patches `action.yml` to resolve expression tokens the runner's legacy parser can't handle
4. **Timeline updates** — Runner reports step progress; broker collects logs and status
5. **Job completion** — Runner reports final result; broker routes it back to the orchestrator

The job message includes everything the runner needs: steps with their type and references, the full expression context serialized as `PipelineContextData`, environment variables as `TemplateToken` layers, mask hints for secrets, and resource endpoints for artifacts and caches.

## Storage

| Path | Contents |
|------|----------|
| `~/.ions/runner/<version>/` | Installed runner binaries |
| `~/.ions/cache/` | Cached dependencies (LRU evicted) |
| `~/.ions/artifacts/<runId>/` | Uploaded artifacts |
| `.ions-work/` | Per-job runner workspace (cleaned between runs) |

## What doesn't work yet

- **Job containers** — `container:` at the job level is not yet implemented
- **Reusable workflows** — `uses:` at the job level is parsed but not wired up
- **GitHub API calls** — Actions that call `api.github.com` directly will fail (a stub is planned)
- **Private action registries** — Only public GitHub actions are supported
- **Self-hosted runner labels** — `runs-on:` is parsed but not matched against real infrastructure

## Development

```bash
# Run all unit tests
go test ./... -short

# Build
go build -o ions ./cmd/ions/

# End-to-end test
./ions run testdata/workflows/hello-world.yml -v
```

## License

TBD
