// Package integration holds Phase 5c integration tests gated at runtime
// by ROCKY_HEARTH_INTEGRATION=1. They require a reachable Docker daemon.
//
// There is intentionally NO `//go:build integration` tag — gating is
// runtime-only (per Phase 5c plan §29) so plain `go test ./...` always
// compiles the suite and reports SKIP when the env var is unset.
package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"

	"github.com/erid-tech/hearth/internal/driver/localdocker"
)

// requireIntegration skips a test unless ROCKY_HEARTH_INTEGRATION=1.
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("ROCKY_HEARTH_INTEGRATION") != "1" {
		t.Skip("ROCKY_HEARTH_INTEGRATION!=1; skipping integration test")
	}
}

// slugCounter disambiguates slugs minted in the same nanosecond by the
// same test (e.g. subtests sharing t.Name()).
var slugCounter uint64

// slug builds a workspace slug unique to this test invocation. The
// shape mirrors what Phase 5 callers send and is short enough to keep
// Docker container/network/volume names under the 64-char limit.
func slug(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "_", "-")
	n := atomic.AddUint64(&slugCounter, 1)
	return fmt.Sprintf("it-%s-%d-%d", name, time.Now().UnixNano(), n)
}

// newClient builds a docker client from the ambient environment
// (DOCKER_HOST etc.) and verifies the daemon is reachable. If it is
// not, the test is skipped with a clear message rather than failing —
// dev machines without Docker should still pass `go test`.
func newClient(t *testing.T) *dockerclient.Client {
	t.Helper()
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		t.Skipf("docker client init failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		t.Skipf("docker daemon not reachable: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// cleanup label-sweeps any containers, networks, and volumes left
// behind for the given slug. It is idempotent and must always be
// registered via t.Cleanup (else leaked Docker resources).
func cleanup(t *testing.T, cli *dockerclient.Client, s string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	filter := localdocker.WorkspaceFilterForTests(s)

	cs, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filter})
	if err != nil {
		t.Logf("cleanup: container list: %v", err)
	}
	for _, c := range cs {
		if rmErr := cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{
			Force:         true,
			RemoveVolumes: true,
		}); rmErr != nil {
			t.Logf("cleanup: rm container %s: %v", c.ID, rmErr)
		}
	}

	nets, err := cli.NetworkList(ctx, network.ListOptions{Filters: filter})
	if err != nil {
		t.Logf("cleanup: network list: %v", err)
	}
	for _, n := range nets {
		if rmErr := cli.NetworkRemove(ctx, n.ID); rmErr != nil {
			t.Logf("cleanup: rm network %s: %v", n.ID, rmErr)
		}
	}

	vols, err := cli.VolumeList(ctx, volume.ListOptions{Filters: filter})
	if err != nil {
		t.Logf("cleanup: volume list: %v", err)
	}
	if vols.Volumes != nil {
		for _, v := range vols.Volumes {
			if rmErr := cli.VolumeRemove(ctx, v.Name, true); rmErr != nil {
				t.Logf("cleanup: rm volume %s: %v", v.Name, rmErr)
			}
		}
	}
}
