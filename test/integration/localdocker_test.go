package integration

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/rocky-hq/hearth/internal/driver"
	"github.com/rocky-hq/hearth/internal/driver/localdocker"
)

func soloProfile() driver.ProvisioningProfile {
	return driver.ProvisioningProfile{
		Tier:   driver.TierSolo,
		Driver: driver.DriverLocalDocker,
		ResourceCaps: driver.ResourceCaps{
			Seats:             1,
			CairnetStorageMB:  64,
			LoreRetentionDays: 1,
			VectorIndex:       "faiss-local",
		},
		DriverFlags: map[string]any{},
	}
}

// TestLocalDockerE2E exercises Provision/Status/Upgrade/Teardown against
// a real Docker daemon, asserting idempotence and post-Teardown cleanup.
func TestLocalDockerE2E(t *testing.T) {
	requireIntegration(t)
	cli := newClient(t)
	s := slug(t)
	t.Cleanup(func() { cleanup(t, cli, s) })

	d, err := localdocker.New(localdocker.Options{
		Client:              cli,
		DefaultCairnetImage: "nginx:alpine",
		DefaultLoreImage:    "nginx:alpine",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Provision.
	ref, err := d.Provision(ctx, s, soloProfile())
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if ref.WorkspaceSlug != s {
		t.Errorf("ref.WorkspaceSlug = %q, want %q", ref.WorkspaceSlug, s)
	}
	if ref.LastStatus != driver.StatusReady {
		t.Errorf("ref.LastStatus = %q, want %q", ref.LastStatus, driver.StatusReady)
	}

	// Idempotence: second Provision returns the same ref and creates
	// no new resources.
	before, err := cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: localdocker.WorkspaceFilterForTests(s),
	})
	if err != nil {
		t.Fatalf("ContainerList before idempotent Provision: %v", err)
	}
	ref2, err := d.Provision(ctx, s, soloProfile())
	if err != nil {
		t.Fatalf("idempotent Provision: %v", err)
	}
	// Full ref identity — idempotent Provision reads Created from the
	// existing cairnet container's inspect, so every field is stable
	// across calls. Drift in Created indicates the idempotence path
	// regressed back to d.now().
	if ref2 != ref {
		t.Errorf("idempotent Provision ref drift:\n got  %+v\n want %+v", ref2, ref)
	}
	after, err := cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: localdocker.WorkspaceFilterForTests(s),
	})
	if err != nil {
		t.Fatalf("ContainerList after idempotent Provision: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("idempotent Provision changed container count: before=%d after=%d",
			len(before), len(after))
	}

	// Status.
	st, err := d.Status(ctx, ref)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st != driver.StatusReady {
		t.Errorf("Status = %q, want %q", st, driver.StatusReady)
	}

	// Upgrade (no-op same-tier upgrade should still succeed and
	// preserve the slug).
	up, err := d.Upgrade(ctx, ref, soloProfile())
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if up.WorkspaceSlug != ref.WorkspaceSlug {
		t.Errorf("Upgrade WorkspaceSlug = %q, want %q", up.WorkspaceSlug, ref.WorkspaceSlug)
	}

	// Teardown.
	if err := d.Teardown(ctx, ref); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	post, err := d.Status(ctx, ref)
	if err != nil {
		t.Fatalf("post-Teardown Status: %v", err)
	}
	if post != driver.StatusTierTornDown {
		t.Errorf("post-Teardown Status = %q, want %q", post, driver.StatusTierTornDown)
	}

	// Teardown leaves nothing labeled with this slug.
	remaining, err := cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: localdocker.WorkspaceFilterForTests(s),
	})
	if err != nil {
		t.Fatalf("ContainerList post-Teardown: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("post-Teardown containers remain: %d", len(remaining))
	}
}
