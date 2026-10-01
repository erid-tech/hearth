// Package kustomize is the Phase 6a HEARTH driver that emits kustomization
// manifests to a local directory. Self-hosters apply / delete out-of-band
// via `kubectl {apply,delete} -k <dir>`. See docs/plans/2026-07-03-phase-6a-kustomize.md.
package kustomize

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/erid-tech/hearth/internal/driver"
)

// ErrTierMismatch mirrors LocalDocker: Provision on an existing workspace
// dir whose kustomization tier does not match forces the caller to Upgrade.
var ErrTierMismatch = errors.New("kustomize: existing deployment has a different tier; call Upgrade")

// ErrInvalidSlug is returned when slug fails DNS-1123 shape.
var ErrInvalidSlug = errors.New("kustomize: slug must match DNS-1123 (a-z, 0-9, hyphen)")

// Options configures a kustomize Driver.
type Options struct {
	Outdir              string
	DefaultCairnetImage string
	DefaultLoreImage    string
	Now                 func() time.Time
}

// Driver is the Phase 6a concrete kustomize implementation.
type Driver struct {
	outdir string
	now    func() time.Time
	imgs   struct{ cairnet, lore string }
}

// New constructs a Driver. opts.Outdir is required.
func New(opts Options) (*Driver, error) {
	if opts.Outdir == "" {
		return nil, errors.New("kustomize: Options.Outdir is required")
	}
	abs, err := filepath.Abs(opts.Outdir)
	if err != nil {
		return nil, fmt.Errorf("kustomize: resolve outdir: %w", err)
	}
	d := &Driver{outdir: abs, now: opts.Now}
	if d.now == nil {
		d.now = func() time.Time { return time.Now().UTC() }
	}
	d.imgs.cairnet = opts.DefaultCairnetImage
	d.imgs.lore = opts.DefaultLoreImage
	if d.imgs.cairnet == "" {
		d.imgs.cairnet = "nginx:alpine"
	}
	if d.imgs.lore == "" {
		d.imgs.lore = "nginx:alpine"
	}
	return d, nil
}

// Provision emits the four manifest files under <outdir>/<slug>/.
// Idempotent on (slug, tier): a repeat call with the same tier returns
// the existing DeploymentRef without touching disk. A different tier
// returns ErrTierMismatch.
func (d *Driver) Provision(ctx context.Context, slug string, profile driver.ProvisioningProfile) (driver.DeploymentRef, error) {
	if err := ctx.Err(); err != nil {
		return driver.DeploymentRef{}, err
	}
	if !slugRE.MatchString(slug) {
		return driver.DeploymentRef{}, ErrInvalidSlug
	}

	dir := d.workspaceDir(slug)
	existingTier, exists, err := d.readExistingTier(dir)
	if err != nil {
		return driver.DeploymentRef{}, err
	}
	if exists {
		if existingTier != string(profile.Tier) {
			return driver.DeploymentRef{}, fmt.Errorf("%w: existing=%s requested=%s", ErrTierMismatch, existingTier, profile.Tier)
		}
		return d.refFor(slug, profile, driver.StatusReady, d.emitCreated(dir)), nil
	}

	if err := d.emitAll(ctx, dir, slug, profile); err != nil {
		_ = os.RemoveAll(dir)
		return driver.DeploymentRef{}, err
	}
	return d.refFor(slug, profile, driver.StatusReady, d.emitCreated(dir)), nil
}

// Status is read-only. Returns StatusTierTornDown for a missing dir,
// StatusReady when all four files are present, StatusProvisioning for
// any partial state.
func (d *Driver) Status(ctx context.Context, ref driver.DeploymentRef) (driver.Status, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir := d.workspaceDir(ref.WorkspaceSlug)
	present, err := d.countPresent(dir)
	if err != nil {
		return "", err
	}
	switch present {
	case 0:
		return driver.StatusTierTornDown, nil
	case 4:
		return driver.StatusReady, nil
	default:
		return driver.StatusProvisioning, nil
	}
}

// Upgrade re-emits every manifest with the new profile values.
// Preserves WorkspaceSlug and Created.
func (d *Driver) Upgrade(ctx context.Context, ref driver.DeploymentRef, profile driver.ProvisioningProfile) (driver.DeploymentRef, error) {
	if err := ctx.Err(); err != nil {
		return driver.DeploymentRef{}, err
	}
	dir := d.workspaceDir(ref.WorkspaceSlug)
	if err := d.emitAll(ctx, dir, ref.WorkspaceSlug, profile); err != nil {
		return driver.DeploymentRef{}, err
	}
	out := d.refFor(ref.WorkspaceSlug, profile, driver.StatusReady, ref.Created)
	return out, nil
}

// Teardown removes <outdir>/<slug>. Idempotent. Operator is responsible
// for `kubectl delete -k` against the cluster out-of-band.
func (d *Driver) Teardown(ctx context.Context, ref driver.DeploymentRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.RemoveAll(d.workspaceDir(ref.WorkspaceSlug))
}

// emitAll writes all four files to dir, creating the dir first.
func (d *Driver) emitAll(ctx context.Context, dir, slug string, profile driver.ProvisioningProfile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir workspace: %w", err)
	}
	writes := []struct {
		name string
		body []byte
	}{
		{fileNamespace, buildNamespace(slug)},
		{fileCairnet, buildRoleManifest(slug, roleCairnet, profile.Tier, d.imageFor(roleCairnet, profile), profile.ResourceCaps)},
		{fileLore, buildRoleManifest(slug, roleLore, profile.Tier, d.imageFor(roleLore, profile), profile.ResourceCaps)},
		{fileKustomization, buildKustomization(slug, profile.Tier)},
	}
	for _, w := range writes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, w.name), w.body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", w.name, err)
		}
	}
	return nil
}

// readExistingTier returns the tier recorded in <dir>/kustomization.yaml,
// or (_, false, nil) if the dir/file is absent.
func (d *Driver) readExistingTier(dir string) (string, bool, error) {
	body, err := os.ReadFile(filepath.Join(dir, fileKustomization))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read kustomization: %w", err)
	}
	return parseTier(body), true, nil
}

// parseTier extracts the tier from the emitted header comment.
// Returns "" if the marker is not found — treated by callers as
// tier-mismatch material.
func parseTier(body []byte) string {
	const marker = "tier="
	s := string(body)
	i := indexOf(s, marker)
	if i < 0 {
		return ""
	}
	rest := s[i+len(marker):]
	end := 0
	for end < len(rest) && rest[end] != '\n' && rest[end] != ' ' {
		end++
	}
	return rest[:end]
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// emitCreated returns the mtime of <dir>/kustomization.yaml, falling back
// to d.now() when stat fails. Keeps Created stable across idempotent
// re-Provisions.
func (d *Driver) emitCreated(dir string) time.Time {
	info, err := os.Stat(filepath.Join(dir, fileKustomization))
	if err != nil {
		return d.now()
	}
	return info.ModTime().UTC()
}

// countPresent returns how many of the four expected files exist under dir.
// Returns 0 if the dir itself is missing.
func (d *Driver) countPresent(dir string) (int, error) {
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	names := []string{fileKustomization, fileNamespace, fileCairnet, fileLore}
	n := 0
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			n++
		}
	}
	return n, nil
}

func (d *Driver) refFor(slug string, profile driver.ProvisioningProfile, status driver.Status, created time.Time) driver.DeploymentRef {
	return driver.DeploymentRef{
		WorkspaceSlug:    slug,
		Tier:             profile.Tier,
		Driver:           driver.DriverKustomize,
		Endpoint:         fmt.Sprintf("kustomize://%s", d.workspaceDir(slug)),
		SecretsVaultPath: "",
		LastStatus:       status,
		Created:          created,
	}
}
