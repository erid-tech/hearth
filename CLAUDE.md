# CLAUDE.md — hearth/

Per the parent superproject's push-down policy (rocky-hq decision 2026-05-02-redesign-bootstrap §3), per-language build/test/lint instructions for HEARTH live here, not in parent `CLAUDE.md`.

## What hearth is

The Go submodule implementing the per-workspace CAIRNET+LORE provisioner from `rocky-hq/docs/specs/2026-05-02-rocky-system-redesign.md` §SS-08. Surface: the four-method `Driver` interface in `internal/driver/driver.go`. Phase 5a ships the interface + `FakeDriver`; Phase 5c ships `LocalDocker`; Phases 6a/6b ship `Kustomize` and `DevarnoCloud`.

## Build

```bash
go build ./...
```

## Test

Unit (no Docker required):

```bash
go test ./... -race -count=1
```

Integration (requires Docker daemon; lands in Phase 5c):

```bash
ROCKY_HEARTH_INTEGRATION=1 go test ./test/integration/... -race -count=1
```

## Lint

```bash
gofumpt -l .              # exits clean (no diff)
go vet ./...
golangci-lint run         # uses .golangci.yml
go mod tidy && git diff --exit-code go.mod go.sum
```

## Conventions

Locked in `rocky-hq/docs/decisions/2026-05-03-phase-5-go-conventions.md`:
- Module: `github.com/rocky-hq/hearth`
- License: MIT
- Go: 1.25
- Layout: `cmd/` (entrypoints — Phase 5c+), `internal/` (private packages), `test/integration/` (gated)
- Formatter: `gofumpt`
- Linter: `golangci-lint` with the baseline `.golangci.yml`
- No vendoring, no `pkg/` directory

## Driver wire-format types

The Phase 5 spec §6 shapes (`Tier`, `DriverName`, `Status`, `ResourceCaps`,
`ProvisioningProfile`, `DeploymentRef`, `HearthHatchEvent`) are NOT
defined in this repo. They are generated from the zod source in
`@rocky-hq/contracts/src/hearth/` and imported as Go bindings from
`github.com/rocky-hq/contracts/go/hearth`. The re-exports under
`package driver` (`internal/driver/types.go`) are a thin convenience layer.
`internal/driver/types_local.go` was removed in Phase 5b — it no longer exists.

**To add or modify a wire-format type:**

1. Edit the zod schema in `contracts/src/hearth/`.
2. Bump the contracts package version and tag (both `vX.Y.Z` and `go/vX.Y.Z`).
3. `go get github.com/rocky-hq/contracts/go@<new-tag>` here.

Never edit `internal/driver/types.go` to introduce a new shape.

## Running locally (Phase 5c)

The `hearth` binary at `cmd/hearth/main.go` is the operator entrypoint.
It exposes the four `Driver` verbs as JSON-over-HTTP on a Unix socket.

Env knobs:

| Var | Default | Notes |
|---|---|---|
| `ROCKY_HEARTH_DRIVER` | `local-docker` | Also accepts `fake` for smoke tests. |
| `ROCKY_HEARTH_SOCKET` | `/var/run/rocky-hearth.sock` | Mode 0600. Removed on clean exit. |
| `DOCKER_HOST` | (docker SDK default) | Passed through to the docker client. |
| `ROCKY_HEARTH_IMAGE_CAIRNET` | `nginx:alpine` | Stand-in until CAIRNET image ships. |
| `ROCKY_HEARTH_IMAGE_LORE` | `nginx:alpine` | Stand-in until LORE image ships. |

Quick smoke test:

```bash
go build -o /tmp/hearth ./cmd/hearth
ROCKY_HEARTH_DRIVER=fake ROCKY_HEARTH_SOCKET=/tmp/h.sock /tmp/hearth &
curl --unix-socket /tmp/h.sock http://x/v1/healthz   # -> {"ok":true}
```

Auth in 5c is filesystem permissions on the socket. TCP transport with
bearer tokens lands in Phase 6.

## SS-08 driver agent projection (Phase 7b)

The RPC server (`internal/server/server.go`) emits `agent.{registered,invoked,completed}` per
`agent-registration.v1` (contracts `>=0.3.0`, subpath `github.com/rocky-hq/contracts/go/agent`)
per RPC verb call. Builders live in `internal/agent/`; the emitter is
hearth-native (POSTs to `$HEARTH_AGENT_HATCH_URL`) because the console
SS-08 wrapper (Phase 5d) that would otherwise own the emit is not built yet.

`agent_id` format: `<workspace_slug>-driver-<driver-name>` (driver-name normalized to `[a-z0-9-]`; e.g. `iris-hq-driver-local-docker`). One agent per `(workspace, driver)` pair. `capabilities = ["driver.provision","driver.status","driver.upgrade","driver.teardown"]` (fixed). Per-invocation `capability = driver.<verb>` (one of the four). `invocation_id = uuid.NewString()` per request. `outcome`: `nil` err → `ok`; error → `error`. `duration_ms` is wall-clock around the driver call. Slug source: `req.Slug` (Provision) or `req.Ref.WorkspaceSlug` (Status/Upgrade/Teardown). Empty slug → zero emission (mirrors the workspace-header gate used by every console-side producer).

Emitter env knobs:

| Var | Default | Notes |
|---|---|---|
| `HEARTH_AGENT_HATCH_URL` | *(empty)* | Full URL for `POST /api/relay/agent` on the console host. Empty → `NopEmitter` (no fault). |
| `HEARTH_AGENT_HATCH_TIMEOUT_MS` | `2000` | Wall-clock cap on each Emit's HTTP round trip. |

Emit is non-blocking (each Emit fires a goroutine bounded by the timeout) and best-effort: transport errors + non-2xx responses log at `slog.Warn` and are dropped. A hatch outage must never fault an RPC. Nop is the default so pre-5d hearth deployments emit nothing until an operator opts in via env.

Phase 5d re-eval: when the console SS-08 wrapper (`console/src/lib/hearth/`) lands, decide whether to also emit console-side (double-emit is idempotent on `agent_id`) or flip a flag to prefer the console emit. Phase 7c hardens the driver `Teardown` verb to `approval.required: true` with `airlock_verb: agent.teardown` — deferred here.

## CI secret: `ROCKY_HQ_RO_TOKEN`

`rocky-hq/contracts` is a private repo. The `lint`, `test`, and `integration`
CI jobs in `.github/workflows/ci.yml` configure `GOPRIVATE=github.com/rocky-hq/*`
and authenticate `go mod download` using a fine-grained PAT stored as the
org-level GitHub Actions secret **`ROCKY_HQ_RO_TOKEN`**.

If you are setting up a fresh CI runner or forking this repo:

- Create a fine-grained PAT with **read-only Contents** scope on
  `rocky-hq/contracts` (and any future private rocky-hq Go modules hearth
  might depend on).
- Store it as an org-level (or repo-level) GitHub Actions secret named
  `ROCKY_HQ_RO_TOKEN`.
- Without it, CI fails at `go mod download` with a 404 from the Go module proxy.
- Public-fork PRs cannot access org secrets and will fail CI — this tradeoff
  is documented in `.github/workflows/ci.yml` and is accepted.
