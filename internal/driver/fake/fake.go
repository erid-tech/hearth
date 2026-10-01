// Package fake provides a deterministic in-memory Driver implementation
// used (1) to exercise the locked protocol contract in fake_test.go,
// which every future concrete driver must also satisfy, and (2) as a
// no-Docker stand-in for console-side development in Phases 5d+.
package fake

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/erid-tech/hearth/internal/driver"
)

// FakeDriver is a deterministic, in-memory driver.Driver.
type FakeDriver struct {
	mu sync.Mutex

	// keyed by (slug, tier, driver) — the idempotency triple from spec §5.
	deployments map[string]driver.DeploymentRef
	// keyed by workspace slug — tracks terminal teardown state.
	tornDown map[string]bool

	provisionCalls        int
	underlyingCreateCount int

	// now is injectable for deterministic Created timestamps.
	now func() time.Time
}

// New returns a fresh FakeDriver.
func New() *FakeDriver {
	return &FakeDriver{
		deployments: map[string]driver.DeploymentRef{},
		tornDown:    map[string]bool{},
		now:         func() time.Time { return time.Unix(0, 0).UTC() },
	}
}

// Provision is idempotent on (slug, tier, driver). See driver.Driver godoc.
func (f *FakeDriver) Provision(ctx context.Context, slug string, profile driver.ProvisioningProfile) (driver.DeploymentRef, error) {
	if err := ctx.Err(); err != nil {
		return driver.DeploymentRef{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.provisionCalls++

	key := provisionKey(slug, profile.Tier, profile.Driver)
	if existing, ok := f.deployments[key]; ok {
		return existing, nil
	}
	ref := driver.DeploymentRef{
		WorkspaceSlug:    slug,
		Tier:             profile.Tier,
		Driver:           profile.Driver,
		Endpoint:         fmt.Sprintf("fake://%s", slug),
		SecretsVaultPath: fmt.Sprintf("vault://hearth/%s", slug),
		Created:          f.now(),
		LastStatus:       driver.StatusReady,
	}
	f.deployments[key] = ref
	f.underlyingCreateCount++
	return ref, nil
}

// Status is read-only. Returns StatusTierTornDown for any slug that has
// been torn down, regardless of the (tier, driver) on the supplied ref.
//
// FakeDriver returns StatusReady as the default for any ref that was
// neither provisioned through this instance nor torn down — useful for
// unit tests that construct a synthetic DeploymentRef without a prior
// Provision call. Real drivers (e.g. LocalDocker in Phase 5c) should
// instead surface an error for unknown refs so misuse is detectable.
func (f *FakeDriver) Status(ctx context.Context, ref driver.DeploymentRef) (driver.Status, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tornDown[ref.WorkspaceSlug] {
		return driver.StatusTierTornDown, nil
	}
	if cur, ok := f.deployments[provisionKey(ref.WorkspaceSlug, ref.Tier, ref.Driver)]; ok {
		return cur.LastStatus, nil
	}
	return driver.StatusReady, nil
}

// Upgrade replaces the deployment row for the slug; preserves WorkspaceSlug.
func (f *FakeDriver) Upgrade(ctx context.Context, ref driver.DeploymentRef, profile driver.ProvisioningProfile) (driver.DeploymentRef, error) {
	if err := ctx.Err(); err != nil {
		return driver.DeploymentRef{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Drop any pre-existing key for this slug — Upgrade may change tier/driver.
	for k, v := range f.deployments {
		if v.WorkspaceSlug == ref.WorkspaceSlug {
			delete(f.deployments, k)
		}
	}
	newRef := driver.DeploymentRef{
		WorkspaceSlug:    ref.WorkspaceSlug,
		Tier:             profile.Tier,
		Driver:           profile.Driver,
		Endpoint:         fmt.Sprintf("fake://%s", ref.WorkspaceSlug),
		SecretsVaultPath: fmt.Sprintf("vault://hearth/%s", ref.WorkspaceSlug),
		Created:          f.now(),
		LastStatus:       driver.StatusReady,
	}
	f.deployments[provisionKey(newRef.WorkspaceSlug, newRef.Tier, newRef.Driver)] = newRef
	return newRef, nil
}

// Teardown removes deployment state for the slug; subsequent Status
// returns StatusTierTornDown (terminal).
func (f *FakeDriver) Teardown(ctx context.Context, ref driver.DeploymentRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range f.deployments {
		if v.WorkspaceSlug == ref.WorkspaceSlug {
			delete(f.deployments, k)
		}
	}
	f.tornDown[ref.WorkspaceSlug] = true
	return nil
}

// ProvisionCallCount returns the number of Provision invocations
// (test-only inspection — counts BOTH cache hits and underlying creates).
func (f *FakeDriver) ProvisionCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.provisionCalls
}

// UnderlyingCreateCount returns the number of times Provision actually
// minted a new DeploymentRef (test-only inspection — idempotent hits do
// not increment this).
func (f *FakeDriver) UnderlyingCreateCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.underlyingCreateCount
}

func provisionKey(slug string, tier driver.Tier, name driver.DriverName) string {
	return fmt.Sprintf("%s|%s|%s", slug, tier, name)
}

// Compile-time assertion: *FakeDriver satisfies driver.Driver.
var _ driver.Driver = (*FakeDriver)(nil)
