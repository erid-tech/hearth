# HEARTH

Per-workspace CAIRNET+LORE provisioner for the [Rocky](https://github.com/rocky-hq) superproject.

> **Status:** Phase 5c — `LocalDocker` driver + JSON-over-HTTP RPC. The `Driver` interface, `FakeDriver`, and `LocalDocker` are in place; `Kustomize` and `DevarnoCloud` land in Phases 6a/6b.

## Running locally

The `hearth` binary at [`cmd/hearth/main.go`](./cmd/hearth/main.go) is the
operator entrypoint. It exposes the four `Driver` verbs as JSON-over-HTTP
on a Unix socket (mode 0600).

```bash
go build -o /tmp/hearth ./cmd/hearth
ROCKY_HEARTH_DRIVER=fake ROCKY_HEARTH_SOCKET=/tmp/h.sock /tmp/hearth &
curl --unix-socket /tmp/h.sock http://x/v1/healthz   # -> {"ok":true}
```

Env knobs (`ROCKY_HEARTH_DRIVER`, `ROCKY_HEARTH_SOCKET`, `DOCKER_HOST`,
`ROCKY_HEARTH_IMAGE_CAIRNET`, `ROCKY_HEARTH_IMAGE_LORE`) are documented
in [`CLAUDE.md`](./CLAUDE.md#running-locally-phase-5c).

## Quickstart

```go
import (
    "context"
    "github.com/rocky-hq/hearth/internal/driver"
    "github.com/rocky-hq/hearth/internal/driver/fake"
)

d := fake.New()
ref, err := d.Provision(context.Background(), "demo", driver.ProvisioningProfile{
    Tier: driver.TierSolo,
})
```

## Build & test

See [`CLAUDE.md`](./CLAUDE.md) — the canonical build/test/lint instructions per the parent superproject's push-down policy.

## License

MIT. See [`LICENSE`](./LICENSE).

## Related

- Parent superproject: [`rocky-hq/rocky-hq`](https://github.com/rocky-hq/rocky-hq)
- Phase 5 spec: `rocky-hq/docs/specs/2026-05-04-rocky-phase-5.md`
- Go conventions: `rocky-hq/docs/decisions/2026-05-03-phase-5-go-conventions.md`
