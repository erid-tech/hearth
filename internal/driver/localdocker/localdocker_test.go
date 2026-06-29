package localdocker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rocky-hq/hearth/internal/driver"
)

func newTestDriver(t *testing.T, api *fakeAPI) *Driver {
	t.Helper()
	d, err := New(Options{
		Client:              api,
		DefaultCairnetImage: "nginx:alpine",
		DefaultLoreImage:    "nginx:alpine",
		Now:                 func() time.Time { return time.Unix(0, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func soloProfile() driver.ProvisioningProfile {
	return driver.ProvisioningProfile{
		Tier:   driver.TierSolo,
		Driver: driver.DriverLocalDocker,
		ResourceCaps: driver.ResourceCaps{
			Seats:             1,
			CairnetStorageMB:  256,
			LoreRetentionDays: 7,
			VectorIndex:       "faiss-local",
		},
		DriverFlags: map[string]any{},
	}
}

func TestProvisionCreatesNetworkAndContainers(t *testing.T) {
	api := newFakeAPI()
	d := newTestDriver(t, api)

	ref, err := d.Provision(context.Background(), "alex-solo", soloProfile())
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if ref.WorkspaceSlug != "alex-solo" || ref.Tier != driver.TierSolo || ref.LastStatus != driver.StatusReady {
		t.Errorf("ref = %+v", ref)
	}
	if !api.hasNetwork("rocky-hearth_alex-solo") {
		t.Errorf("network not created")
	}
	if !api.hasContainer("rocky-hearth_alex-solo_cairnet") {
		t.Errorf("cairnet container not created")
	}
	if !api.hasContainer("rocky-hearth_alex-solo_lore") {
		t.Errorf("lore container not created")
	}
}

func TestProvisionIsIdempotent(t *testing.T) {
	api := newFakeAPI()
	d := newTestDriver(t, api)
	ctx := context.Background()

	first, err := d.Provision(ctx, "alex-solo", soloProfile())
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	beforeContainers := api.containerCreateCalls

	second, err := d.Provision(ctx, "alex-solo", soloProfile())
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first != second {
		t.Errorf("idempotence: first=%+v second=%+v", first, second)
	}
	if api.containerCreateCalls != beforeContainers {
		t.Errorf("idempotent Provision created %d extra containers", api.containerCreateCalls-beforeContainers)
	}
}

func TestProvisionRejectsTierMismatch(t *testing.T) {
	api := newFakeAPI()
	d := newTestDriver(t, api)
	ctx := context.Background()

	if _, err := d.Provision(ctx, "alex-solo", soloProfile()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	studio := soloProfile()
	studio.Tier = driver.TierStudio
	_, err := d.Provision(ctx, "alex-solo", studio)
	if !errors.Is(err, ErrTierMismatch) {
		t.Errorf("expected ErrTierMismatch, got %v", err)
	}
}

func TestProvisionRollsBackOnFailure(t *testing.T) {
	api := newFakeAPI()
	api.failOnContainerCreate = "rocky-hearth_alex-solo_lore" // CAIRNET succeeds, LORE fails
	d := newTestDriver(t, api)

	_, err := d.Provision(context.Background(), "alex-solo", soloProfile())
	if err == nil {
		t.Fatal("expected Provision to fail")
	}
	if api.hasContainer("rocky-hearth_alex-solo_cairnet") {
		t.Errorf("rollback did not remove CAIRNET")
	}
	if api.hasNetwork("rocky-hearth_alex-solo") {
		t.Errorf("rollback did not remove network")
	}
}

func TestStatusReadyWhenBothRunning(t *testing.T) {
	api := newFakeAPI()
	d := newTestDriver(t, api)
	ref, err := d.Provision(context.Background(), "alex-solo", soloProfile())
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	got, err := d.Status(context.Background(), ref)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got != driver.StatusReady {
		t.Errorf("Status = %q, want ready", got)
	}
}

func TestStatusTornDownWhenAbsent(t *testing.T) {
	api := newFakeAPI()
	d := newTestDriver(t, api)
	ref := driver.DeploymentRef{WorkspaceSlug: "ghost", Tier: driver.TierSolo, Driver: driver.DriverLocalDocker}
	got, err := d.Status(context.Background(), ref)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got != driver.StatusTierTornDown {
		t.Errorf("Status = %q, want tier_torn_down", got)
	}
}

func TestStatusFailedWhenContainerExitedNonzero(t *testing.T) {
	api := newFakeAPI()
	d := newTestDriver(t, api)
	ref, _ := d.Provision(context.Background(), "alex-solo", soloProfile())
	api.containers["rocky-hearth_alex-solo_cairnet"].running = false
	api.containers["rocky-hearth_alex-solo_cairnet"].exitCode = 137
	got, err := d.Status(context.Background(), ref)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got != driver.StatusFailed {
		t.Errorf("Status = %q, want failed", got)
	}
}

func TestUpgradePreservesVolumes(t *testing.T) {
	api := newFakeAPI()
	d := newTestDriver(t, api)
	ref, _ := d.Provision(context.Background(), "alex-solo", soloProfile())
	preVolumes := len(api.volumes)
	newProfile := soloProfile()
	newProfile.DriverFlags["cairnet_image"] = "nginx:1.25-alpine"
	got, err := d.Upgrade(context.Background(), ref, newProfile)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if got.WorkspaceSlug != ref.WorkspaceSlug {
		t.Errorf("Upgrade dropped WorkspaceSlug")
	}
	if len(api.volumes) != preVolumes {
		t.Errorf("Upgrade changed volume count: %d -> %d", preVolumes, len(api.volumes))
	}
	if api.containers["rocky-hearth_alex-solo_cairnet"] == nil {
		t.Errorf("Upgrade did not recreate CAIRNET")
	}
}

func TestTeardownIsTerminal(t *testing.T) {
	api := newFakeAPI()
	d := newTestDriver(t, api)
	ref, _ := d.Provision(context.Background(), "alex-solo", soloProfile())
	if err := d.Teardown(context.Background(), ref); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if len(api.containers) != 0 || len(api.networks) != 0 || len(api.volumes) != 0 {
		t.Errorf("Teardown left resources: containers=%d networks=%d volumes=%d", len(api.containers), len(api.networks), len(api.volumes))
	}
	got, _ := d.Status(context.Background(), ref)
	if got != driver.StatusTierTornDown {
		t.Errorf("post-Teardown Status = %q, want tier_torn_down", got)
	}
}

func TestTeardownIsIdempotent(t *testing.T) {
	api := newFakeAPI()
	d := newTestDriver(t, api)
	ref, _ := d.Provision(context.Background(), "alex-solo", soloProfile())
	if err := d.Teardown(context.Background(), ref); err != nil {
		t.Fatalf("first Teardown: %v", err)
	}
	if err := d.Teardown(context.Background(), ref); err != nil {
		t.Errorf("second Teardown: %v", err)
	}
}

func TestProvisionHonorsContext(t *testing.T) {
	api := newFakeAPI()
	d := newTestDriver(t, api)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.Provision(ctx, "alex-solo", soloProfile())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
