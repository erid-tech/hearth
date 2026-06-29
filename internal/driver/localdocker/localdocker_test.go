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
