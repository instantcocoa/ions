You are helping me build a tool called "ions" — a local GitHub Actions/workflow runner that achieves high fidelity by using the official GitHub Actions runner binary (github.com/actions/runner) for step execution, and implementing all orchestration around it.

The goal is to be able to run GitHub workflows and Actions locally end-to-end with much higher fidelity than `act`, which reimplements execution itself. We use the real runner for step execution and own the orchestration layer.

The project is in Go. The runner binary is C#/.NET and we treat it as a subprocess we manage.

## Repository structure to create

ions/
  cmd/ions/          # CLI entrypoint (cobra)
  internal/
    workflow/          # YAML parsing, validation, expression evaluation
    graph/             # job dependency graph, scheduling
    broker/            # local HTTP server that speaks runner protocol
    runner/            # runner binary download, install, lifecycle
    orchestrator/      # top-level coordinator
    docker/            # container management (services, job containers)
    artifacts/         # local artifact server
    cache/             # local cache server
    githubstub/        # stub of api.github.com for actions that call it
    context/           # github/env/steps/needs context building
    expression/        # ${{ }} expression parser and evaluator
  testdata/
    workflows/         # sample workflows for integration testing

## Phase 1: Foundation (implement this first)

### 1. Workflow parser (internal/workflow)

Parse GitHub Actions workflow YAML into typed Go structs. Handle:
- `on:` triggers (we don't need to act on them locally, just parse)
- `jobs:` with all fields: runs-on, needs, if, strategy/matrix, env, steps
- `steps:` with uses, run, with, env, if, id, name, continue-on-error
- `env:` at workflow and job level
- `defaults:`
- `permissions:`
- Reusable workflows (`uses:` at job level, not step level)

Write thorough unit tests with real workflow YAML examples.

### 2. Expression evaluator (internal/expression)

Implement GitHub's `${{ }}` expression language. This is critical for correctness.

Contexts to implement:
- `github` (repo, ref, sha, event_name, actor, workflow, job, run_id, run_number, etc.)
- `env`
- `steps.<id>.outputs`, `steps.<id>.outcome`, `steps.<id>.conclusion`
- `needs.<job>.outputs`, `needs.<job>.result`
- `matrix`
- `secrets`
- `inputs`
- `vars`
- `runner`

Functions to implement:
- contains, startsWith, endsWith, format, join, toJSON, fromJSON, hashFiles
- success(), failure(), always(), cancelled()

Operator support: ==, !=, <, <=, >, >=, &&, ||, !

Write extensive unit tests. Pull test cases from GitHub's documentation and from the actions/runner source to ensure your implementation matches theirs.

### 3. Job graph (internal/graph)

Build a DAG from job `needs:` relationships. Implement:
- Topological sort for execution order
- Detection of circular dependencies (error clearly)
- Identification of jobs that can run in parallel
- Matrix expansion: take a job with `strategy.matrix` and expand it into N concrete jobs, each with their own matrix context
- `if:` evaluation at job level to skip jobs

### 4. Context builder (internal/context)

Build the full context object that gets passed into expression evaluation and ultimately into the job payload. This needs to produce values that match what GitHub would produce:
- Populate `github` context from local git repo (use go-git to read .git/)
- Handle `env` context with proper precedence (workflow > job > step)
- Build `runner` context (OS, arch, temp, workspace paths)
- Manage `steps` context accumulation as steps complete
- Manage `needs` context from completed jobs

## Phase 2: Broker and Runner Integration (the core)

### 5. Runner binary manager (internal/runner)

Implement downloading and managing the official runner binary:
- Detect platform (linux/mac/windows, amd64/arm64)
- Download from github.com/actions/runner/releases — check latest release via GitHub API
- Verify checksum
- Extract to a managed directory (~/.ions/runner/)
- Handle version management (cache, update)

The runner needs .NET runtime. Detect if it's present; if not, either error clearly or bundle the self-contained runner package (the releases include self-contained builds that include the runtime).

### 6. Local broker server (internal/broker)

This is the most critical and complex piece. Implement an HTTP server that the runner talks to instead of api.github.com.

Study the runner source at github.com/actions/runner, specifically:
- src/Runner.Common/Util/ApiUtil.cs — API client
- src/Runner.Worker/ — the worker that executes jobs
- src/Runner.Common/JobDispatcher.cs

Implement these endpoints that the runner calls:

**Registration flow:**
- POST /actions/runner-registration
  - Runner sends: RunnerConfig (name, labels, etc.)
  - We respond with: RunnerCredential (JWT token, URL, agent ID)
  
- POST /actions/runner-oauth-token  
  - Exchange for shorter-lived token

**Job polling:**
- GET /actions/runner-jobs (long poll, ~50s timeout)
  - When a job is ready, respond with AgentJobRequestMessage
  - If no job, respond with 204 after timeout

**Job lifecycle:**
- PATCH /actions/runner-jobs/{jobId} — runner reports job started, step updates, job completed
- POST /actions/runner-jobs/{jobId}/logs — log streaming

**AgentJobRequestMessage structure** — study the runner source to understand this payload. Key fields:
- jobId, jobDisplayName, jobContainer
- steps[] — each with type (action/script), reference, inputs, environment, condition
- resources (action references resolved to SHAs and download URLs)
- contextData — the serialized context (github, env, matrix, etc.)
- variables — key/value pairs the runner makes available

Implement the message queue: orchestrator pushes job payloads in, broker serves them to runner, broker collects results and sends them back to orchestrator.

### 7. Runner process manager (internal/runner, extend)

Launch and manage runner processes:
- Configure runner to point at local broker (via --url and --token flags, or config file)
- One runner process per concurrent job (or reuse runners)
- Capture stdout/stderr for debugging
- Handle runner crashes and report them as job failures
- Clean up runner processes on exit (handle signals)

The runner registration needs a URL it can reach. Use localhost with a dynamically assigned port. Pass the URL to the runner via its config.

## Phase 3: Container Orchestration

### 8. Docker manager (internal/docker)

Use the Docker Go SDK (github.com/docker/docker/client).

Implement:
- **Job containers**: when `runs-on` is a container spec or when job has `container:`, run the runner worker inside that container. This requires mounting the runner into the container and running it there.
- **Service containers**: spin up `services:` before job starts, on the same Docker network, inject hostnames into job environment
- **Networking**: create a Docker network per job, attach containers, clean up after
- **Volume management**: workspace volume shared between steps, temp directory
- **Environment injection**: pass all required env vars into containers
- **Image pulling**: pull images before starting, handle auth for private registries (respect ~/.docker/config.json)

Note on runner + containers: the official runner supports running inside a container via the `ACTIONS_RUNNER_CONTAINER_HOOKS` mechanism. Research this in the runner source — it's how GitHub's hosted runners support `container:` at the job level. You may be able to leverage this rather than reimplementing it.

## Phase 4: Supporting Services

### 9. Artifact server (internal/artifacts)

Implement the GitHub artifact API locally. The runner uses `actions/upload-artifact` and `actions/download-artifact` which call:
- POST /_apis/pipelines/workflows/{runId}/artifacts — create artifact
- GET /_apis/pipelines/workflows/{runId}/artifacts — list artifacts  
- PATCH /_apis/pipelines/workflows/{runId}/artifacts — finalize
- GET/PUT for actual blob upload/download (chunked)

Store artifacts in ~/.ions/artifacts/{runId}/. Expose download links that resolve to local files.

This needs to be served on a known URL that you inject into the job environment via `ACTIONS_RUNTIME_URL` and `ACTIONS_RUNTIME_TOKEN` environment variables — the runner and actions toolkit use these to find the artifact/cache service.

### 10. Cache server (internal/cache)

Implement the GitHub cache API:
- POST /_apis/artifactcache/caches — reserve cache entry
- PATCH /_apis/artifactcache/caches/{cacheId} — upload cache content (chunked)
- POST /_apis/artifactcache/caches/{cacheId} — commit cache
- GET /_apis/artifactcache/cache?keys=...&version=... — lookup cache by key

Cache key matching must exactly match GitHub's algorithm — primary key exact match first, then restore-key prefix matching. Store cache in ~/.ions/cache/. Respect cache size limits (implement LRU eviction).

The `hashFiles()` expression function must match GitHub's implementation exactly for cache keys to be consistent. Study the runner source for this.

## Phase 5: CLI and UX

### 11. CLI (cmd/ions)

Use cobra for CLI. Commands:

`ions run [workflow-file] [--job job-name] [--event event-name]`
- Main command: parse workflow, orchestrate, stream logs

`ions list [workflow-file]`  
- List jobs in a workflow

`ions validate [workflow-file]`
- Validate workflow YAML and expressions without running

`ions clean`
- Clean up artifacts, caches, stopped containers

`ions runner install [--version v2.x.x]`
- Explicitly install/update runner binary

Flags:
- `--secret KEY=VALUE` (repeatable) — inject secrets
- `--var KEY=VALUE` (repeatable) — inject vars  
- `--env KEY=VALUE` (repeatable) — inject env
- `--input KEY=VALUE` (repeatable) — workflow_dispatch inputs
- `--dry-run` — resolve and print job plan without running
- `--verbose` / `-v` — show runner output, debug logs
- `--platform linux/amd64` — override platform detection
- `--reuse-containers` — don't remove containers after run (for debugging)
- `--artifact-dir PATH` — override artifact storage location

Log output: stream logs from broker in real time, prefix with job name, step name. Color by job. Show step pass/fail indicators. Show job summary at end.

## Implementation notes and constraints

**Start with Phase 1** entirely before touching the broker. The expression evaluator and graph logic can be tested independently and getting them right is foundational.

**For the broker protocol**, the most reliable approach is to run the actual runner binary against a local server with full request/response logging, observe what it sends and receives, and implement accordingly. Don't guess — instrument.

**Error messages** should be excellent. When a workflow fails, show exactly which step failed, the exit code, and the last N lines of output. When an expression fails to evaluate, show the expression and what was in context.

**The runner binary path** and version should be stored in ~/.ions/config.json. First run should auto-install.

**Secrets** should never be logged. Implement the same secret masking the real runner does (any value provided as a secret gets replaced with *** in log output).

**Do not implement** a full GitHub API stub in Phase 1-5. Many actions will call api.github.com and fail — that's okay for now. Log a clear warning when outbound calls to api.github.com are detected (you can see this via the job's network activity or by checking common patterns). A stub can be Phase 6.

## Testing strategy

- Unit tests for workflow parser, expression evaluator, graph resolver — these should be thorough with table-driven tests
- Integration tests that actually launch the broker + runner and run simple workflows end to end
- testdata/workflows/ should contain progressively complex workflow examples:
  - hello-world.yml: single job, single run step
  - multi-job.yml: jobs with needs: dependencies  
  - matrix.yml: matrix strategy
  - services.yml: postgres service container
  - composite-action.yml: uses: a local composite action
  - js-action.yml: uses: actions/checkout (real action from marketplace)

## Go dependencies to use

- gopkg.in/yaml.v3 — YAML parsing
- github.com/spf13/cobra — CLI
- github.com/docker/docker — Docker SDK
- github.com/go-git/go-git/v5 — read git context
- github.com/gorilla/mux or net/http with chi — broker HTTP server
- github.com/stretchr/testify — testing
- github.com/fatih/color — colored output

Begin by scaffolding the full repository structure, then implement Phase 1 completely with tests before moving on. Ask me before making significant architectural decisions that aren't covered here.
