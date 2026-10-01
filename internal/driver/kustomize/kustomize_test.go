package kustomize_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erid-tech/hearth/internal/driver"
	"github.com/erid-tech/hearth/internal/driver/kustomize"
)

func newDriver(t *testing.T) (*kustomize.Driver, string) {
	t.Helper()
	dir := t.TempDir()
	fixed := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	d, err := kustomize.New(kustomize.Options{
		Outdir: dir,
		Now:    func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, dir
}

func newProfile(tier driver.Tier) driver.ProvisioningProfile {
	return driver.ProvisioningProfile{
		Tier:   tier,
		Driver: driver.DriverKustomize,
		ResourceCaps: driver.ResourceCaps{
			CairnetStorageMB:  5120,
			LoreRetentionDays: 90,
			Seats:             5,
			VectorIndex:       "pgvector",
		},
		DriverFlags: map[string]interface{}{},
	}
}

func TestProvision_EmitsFourFiles(t *testing.T) {
	d, outdir := newDriver(t)
	ctx := context.Background()
	ref, err := d.Provision(ctx, "iris-hq", newProfile(driver.TierTeam))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if ref.WorkspaceSlug != "iris-hq" || ref.Driver != driver.DriverKustomize || ref.Tier != driver.TierTeam {
		t.Fatalf("ref mismatch: %+v", ref)
	}
	if !strings.HasPrefix(ref.Endpoint, "kustomize://") {
		t.Fatalf("endpoint prefix: %q", ref.Endpoint)
	}
	for _, name := range []string{"kustomization.yaml", "namespace.yaml", "cairnet.yaml", "lore.yaml"} {
		if _, err := os.Stat(filepath.Join(outdir, "iris-hq", name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
}

func TestProvision_IdempotentSameTier(t *testing.T) {
	d, outdir := newDriver(t)
	ctx := context.Background()
	ref1, err := d.Provision(ctx, "iris-hq", newProfile(driver.TierTeam))
	if err != nil {
		t.Fatal(err)
	}
	kpath := filepath.Join(outdir, "iris-hq", "kustomization.yaml")
	info1, _ := os.Stat(kpath)
	time.Sleep(20 * time.Millisecond)
	ref2, err := d.Provision(ctx, "iris-hq", newProfile(driver.TierTeam))
	if err != nil {
		t.Fatal(err)
	}
	info2, _ := os.Stat(kpath)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatalf("mtime advanced on idempotent Provision: %v -> %v", info1.ModTime(), info2.ModTime())
	}
	if ref1.Created != ref2.Created {
		t.Fatalf("Created advanced: %v -> %v", ref1.Created, ref2.Created)
	}
	if ref1.Endpoint != ref2.Endpoint {
		t.Fatalf("endpoint drift: %v -> %v", ref1.Endpoint, ref2.Endpoint)
	}
}

func TestProvision_TierMismatch(t *testing.T) {
	d, _ := newDriver(t)
	ctx := context.Background()
	if _, err := d.Provision(ctx, "iris-hq", newProfile(driver.TierTeam)); err != nil {
		t.Fatal(err)
	}
	_, err := d.Provision(ctx, "iris-hq", newProfile(driver.TierStudio))
	if !errors.Is(err, kustomize.ErrTierMismatch) {
		t.Fatalf("expected ErrTierMismatch, got %v", err)
	}
}

func TestProvision_InvalidSlug(t *testing.T) {
	d, _ := newDriver(t)
	_, err := d.Provision(context.Background(), "Iris_HQ!", newProfile(driver.TierTeam))
	if !errors.Is(err, kustomize.ErrInvalidSlug) {
		t.Fatalf("expected ErrInvalidSlug, got %v", err)
	}
}

func TestProvision_CtxCanceled(t *testing.T) {
	d, _ := newDriver(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.Provision(ctx, "iris-hq", newProfile(driver.TierTeam))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestStatus_LifecycleTransitions(t *testing.T) {
	d, outdir := newDriver(t)
	ctx := context.Background()
	ref, err := d.Provision(ctx, "iris-hq", newProfile(driver.TierTeam))
	if err != nil {
		t.Fatal(err)
	}
	// Ready when all four files present.
	if s, _ := d.Status(ctx, ref); s != driver.StatusReady {
		t.Fatalf("post-provision status: %v", s)
	}
	// Provisioning when partial.
	_ = os.Remove(filepath.Join(outdir, "iris-hq", "lore.yaml"))
	if s, _ := d.Status(ctx, ref); s != driver.StatusProvisioning {
		t.Fatalf("partial status: %v", s)
	}
	// TierTornDown when dir absent.
	if err := d.Teardown(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if s, _ := d.Status(ctx, ref); s != driver.StatusTierTornDown {
		t.Fatalf("post-teardown status: %v", s)
	}
	// Teardown is idempotent.
	if err := d.Teardown(ctx, ref); err != nil {
		t.Fatalf("second Teardown: %v", err)
	}
}

func TestUpgrade_PreservesSlugAndCreated(t *testing.T) {
	d, _ := newDriver(t)
	ctx := context.Background()
	ref, err := d.Provision(ctx, "iris-hq", newProfile(driver.TierTeam))
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := d.Upgrade(ctx, ref, newProfile(driver.TierStudio))
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.WorkspaceSlug != ref.WorkspaceSlug {
		t.Fatalf("slug drift: %v -> %v", ref.WorkspaceSlug, upgraded.WorkspaceSlug)
	}
	if !upgraded.Created.Equal(ref.Created) {
		t.Fatalf("Created drift: %v -> %v", ref.Created, upgraded.Created)
	}
	if upgraded.Tier != driver.TierStudio {
		t.Fatalf("tier not upgraded: %v", upgraded.Tier)
	}
}

func TestDriverFlags_OverrideImages(t *testing.T) {
	d, outdir := newDriver(t)
	p := newProfile(driver.TierTeam)
	p.DriverFlags = map[string]interface{}{
		"cairnet_image": "ghcr.io/rocky-hq/cairnet:v1",
		"lore_image":    "ghcr.io/rocky-hq/lore:v1",
	}
	if _, err := d.Provision(context.Background(), "iris-hq", p); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(outdir, "iris-hq", "cairnet.yaml"))
	if !strings.Contains(string(body), "ghcr.io/rocky-hq/cairnet:v1") {
		t.Fatalf("cairnet.yaml did not honor DriverFlags override:\n%s", body)
	}
	body, _ = os.ReadFile(filepath.Join(outdir, "iris-hq", "lore.yaml"))
	if !strings.Contains(string(body), "ghcr.io/rocky-hq/lore:v1") {
		t.Fatalf("lore.yaml did not honor DriverFlags override:\n%s", body)
	}
}

func TestNew_RequiresOutdir(t *testing.T) {
	_, err := kustomize.New(kustomize.Options{})
	if err == nil {
		t.Fatal("expected error on empty Outdir")
	}
}
