package fake_test

import (
	"context"
	"testing"
	"time"

	"github.com/erid-tech/hearth/internal/driver"
	"github.com/erid-tech/hearth/internal/driver/fake"
)

func newProfile() driver.ProvisioningProfile {
	return driver.ProvisioningProfile{
		Tier:   driver.TierSolo,
		Driver: driver.DriverLocalDocker,
		ResourceCaps: driver.ResourceCaps{
			CairnetStorageMB:  1024,
			LoreRetentionDays: 30,
			Seats:             1,
			VectorIndex:       "faiss-local",
		},
		DriverFlags: map[string]interface{}{},
	}
}

func TestProvision_Idempotent_OnSlugTierDriver(t *testing.T) {
	d := fake.New()
	ctx := context.Background()
	p := newProfile()

	ref1, err := d.Provision(ctx, "alpha", p)
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	ref2, err := d.Provision(ctx, "alpha", p)
	if err != nil {
		t.Fatalf("second Provision: %v", err)
	}
	if ref1 != ref2 {
		t.Fatalf("Provision not idempotent: %+v vs %+v", ref1, ref2)
	}
	if got := d.ProvisionCallCount(); got != 2 {
		t.Fatalf("expected 2 Provision calls recorded, got %d", got)
	}
	if got := d.UnderlyingCreateCount(); got != 1 {
		t.Fatalf("idempotent Provision should create once, got %d creates", got)
	}
}

func TestStatus_ReadOnly(t *testing.T) {
	d := fake.New()
	ctx := context.Background()
	ref, err := d.Provision(ctx, "beta", newProfile())
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	for i := 0; i < 5; i++ {
		s, err := d.Status(ctx, ref)
		if err != nil {
			t.Fatalf("Status call %d: %v", i, err)
		}
		if s != driver.StatusReady {
			t.Fatalf("Status call %d: want %q got %q", i, driver.StatusReady, s)
		}
	}
	if got := d.UnderlyingCreateCount(); got != 1 {
		t.Fatalf("Status must not mutate state; underlying creates went from 1 to %d", got)
	}
}

func TestUpgrade_PreservesSlug(t *testing.T) {
	d := fake.New()
	ctx := context.Background()
	ref, err := d.Provision(ctx, "gamma", newProfile())
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	upgraded := newProfile()
	upgraded.Tier = driver.TierTeam
	upgraded.ResourceCaps.Seats = 5

	newRef, err := d.Upgrade(ctx, ref, upgraded)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if newRef.WorkspaceSlug != ref.WorkspaceSlug {
		t.Fatalf("Upgrade must preserve WorkspaceSlug: was %q got %q",
			ref.WorkspaceSlug, newRef.WorkspaceSlug)
	}
	if newRef.Tier != driver.TierTeam {
		t.Fatalf("Upgrade should reflect new tier: got %q", newRef.Tier)
	}
}

func TestTeardown_Terminal(t *testing.T) {
	d := fake.New()
	ctx := context.Background()
	ref, err := d.Provision(ctx, "delta", newProfile())
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := d.Teardown(ctx, ref); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	s, err := d.Status(ctx, ref)
	if err != nil {
		t.Fatalf("Status after Teardown should not error, got: %v", err)
	}
	if s != driver.StatusTierTornDown {
		t.Fatalf("Status after Teardown: want %q got %q", driver.StatusTierTornDown, s)
	}
}

func TestAllMethods_HonourCtxCancel(t *testing.T) {
	d := fake.New()
	ref := driver.DeploymentRef{WorkspaceSlug: "epsilon", Tier: driver.TierSolo}

	cases := []struct {
		name string
		call func(ctx context.Context) error
	}{
		{"Provision", func(ctx context.Context) error { _, err := d.Provision(ctx, "epsilon", newProfile()); return err }},
		{"Status", func(ctx context.Context) error { _, err := d.Status(ctx, ref); return err }},
		{"Upgrade", func(ctx context.Context) error { _, err := d.Upgrade(ctx, ref, newProfile()); return err }},
		{"Teardown", func(ctx context.Context) error { return d.Teardown(ctx, ref) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			start := time.Now()
			err := tc.call(ctx)
			if err == nil {
				t.Fatalf("%s with canceled ctx must return error", tc.name)
			}
			if time.Since(start) > 100*time.Millisecond {
				t.Fatalf("%s did not return promptly on ctx cancel", tc.name)
			}
		})
	}
}
