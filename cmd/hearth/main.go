// Command hearth is the Phase 5c JSON-over-HTTP RPC binary that exposes
// the driver.Driver verbs on a Unix socket.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	dockerclient "github.com/docker/docker/client"

	"github.com/rocky-hq/hearth/internal/agent"
	"github.com/rocky-hq/hearth/internal/driver"
	"github.com/rocky-hq/hearth/internal/driver/fake"
	"github.com/rocky-hq/hearth/internal/driver/kustomize"
	"github.com/rocky-hq/hearth/internal/driver/localdocker"
	"github.com/rocky-hq/hearth/internal/server"
)

func main() {
	if err := run(); err != nil {
		emitLog("fatal", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
}

func run() error {
	drvName := envDefault("ROCKY_HEARTH_DRIVER", "local-docker")
	sockPath := envDefault("ROCKY_HEARTH_SOCKET", "/var/run/rocky-hearth.sock")

	var d driver.Driver
	switch drvName {
	case "fake":
		d = fake.New()
	case "local-docker":
		cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
		if err != nil {
			return fmt.Errorf("docker client: %w", err)
		}
		ld, err := localdocker.New(localdocker.Options{
			Client:              cli,
			DefaultCairnetImage: envDefault("ROCKY_HEARTH_IMAGE_CAIRNET", "nginx:alpine"),
			DefaultLoreImage:    envDefault("ROCKY_HEARTH_IMAGE_LORE", "nginx:alpine"),
		})
		if err != nil {
			return fmt.Errorf("localdocker driver: %w", err)
		}
		d = ld
	case "kustomize":
		km, err := kustomize.New(kustomize.Options{
			Outdir:              envDefault("ROCKY_HEARTH_KUSTOMIZE_OUTDIR", "/var/lib/rocky-hearth/manifests"),
			DefaultCairnetImage: envDefault("ROCKY_HEARTH_IMAGE_CAIRNET", "nginx:alpine"),
			DefaultLoreImage:    envDefault("ROCKY_HEARTH_IMAGE_LORE", "nginx:alpine"),
		})
		if err != nil {
			return fmt.Errorf("kustomize driver: %w", err)
		}
		d = km
	default:
		return fmt.Errorf("unknown ROCKY_HEARTH_DRIVER=%q (want fake|local-docker|kustomize)", drvName)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	agentEmitter := agent.EmitterFromEnv()
	kahnEmitter := agent.KAHNEmitterFromEnv()
	emitLog("startup", map[string]any{
		"driver":          drvName,
		"socket":          sockPath,
		"agent_hatch_url": os.Getenv(agent.EnvHatchURL),
		"kahn_ingest_url": os.Getenv(agent.EnvKAHNIngestURL),
	})
	srv := server.New(d).WithEmitter(agentEmitter).WithKAHNEmitter(kahnEmitter)
	if err := server.Listen(ctx, sockPath, srv.Mux()); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	emitLog("shutdown", map[string]any{"socket": sockPath})
	return nil
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func emitLog(event string, fields map[string]any) {
	fields["event"] = event
	fields["binary"] = "hearth"
	out, err := json.Marshal(fields)
	if err != nil {
		return
	}
	fmt.Fprintln(os.Stdout, string(out))
}
