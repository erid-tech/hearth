package localdocker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"

	"github.com/rocky-hq/hearth/internal/driver"
)

// LabelTier records the deployment tier on every managed resource so we can
// detect a tier mismatch on idempotent Provision calls and force the caller
// to invoke Upgrade explicitly.
const LabelTier = "rocky-hq.io/tier"

// ErrTierMismatch is returned when Provision is called for a workspace
// that already has resources labeled with a different tier. Callers
// must invoke Upgrade explicitly.
var ErrTierMismatch = errors.New("localdocker: existing deployment has a different tier; call Upgrade")

// Options configures a LocalDocker Driver.
type Options struct {
	Client              dockerAPI
	DefaultCairnetImage string
	DefaultLoreImage    string
	Now                 func() time.Time
}

// Driver is the LocalDocker concrete implementation of the Phase 5 driver
// protocol. It is intentionally unexported in its fields so callers must go
// through the public Provision / Status / Upgrade / Teardown methods.
type Driver struct {
	api  dockerAPI
	now  func() time.Time
	imgs struct{ cairnet, lore string }
}

// New constructs a Driver. opts.Client is required.
func New(opts Options) (*Driver, error) {
	if opts.Client == nil {
		return nil, errors.New("localdocker: Options.Client is required")
	}
	d := &Driver{api: opts.Client, now: opts.Now}
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

// Provision creates the CAIRNET+LORE container pair (plus shared network and
// per-role volumes) for the given workspace slug. The call is idempotent:
// re-invoking it with the same tier returns the existing DeploymentRef
// without touching the daemon. A different tier returns ErrTierMismatch.
// Any partial-state failure rolls back resources created during this call.
func (d *Driver) Provision(ctx context.Context, slug string, profile driver.ProvisioningProfile) (driver.DeploymentRef, error) {
	if err := ctx.Err(); err != nil {
		return driver.DeploymentRef{}, err
	}

	existing, err := d.lookupContainers(ctx, slug)
	if err != nil {
		return driver.DeploymentRef{}, fmt.Errorf("container list: %w", err)
	}
	if len(existing) > 0 {
		for _, c := range existing {
			if got := c.Labels[LabelTier]; got != "" && got != string(profile.Tier) {
				return driver.DeploymentRef{}, fmt.Errorf("%w: existing=%s requested=%s", ErrTierMismatch, got, profile.Tier)
			}
		}
		return d.refFor(slug, profile, driver.StatusReady), nil
	}

	rollback := func() {
		bgctx := context.Background()
		summaries, _ := d.api.ContainerList(bgctx, container.ListOptions{All: true, Filters: workspaceFilter(slug)})
		for _, s := range summaries {
			_ = d.api.ContainerRemove(bgctx, s.ID, container.RemoveOptions{Force: true})
		}
		_ = d.api.NetworkRemove(bgctx, networkName(slug))
		_ = d.api.VolumeRemove(bgctx, volumeName(slug, RoleCairnet), true)
		_ = d.api.VolumeRemove(bgctx, volumeName(slug, RoleLore), true)
	}

	netLabels := roleLabels(slug, RoleNetwork)
	netLabels[LabelTier] = string(profile.Tier)
	if _, err := d.api.NetworkCreate(ctx, networkName(slug), network.CreateOptions{
		Driver: "bridge",
		Labels: netLabels,
	}); err != nil {
		return driver.DeploymentRef{}, fmt.Errorf("network create: %w", err)
	}

	for _, role := range []string{RoleCairnet, RoleLore} {
		labels := roleLabels(slug, role)
		labels[LabelTier] = string(profile.Tier)
		if _, err := d.api.VolumeCreate(ctx, volume.CreateOptions{
			Name:   volumeName(slug, role),
			Labels: labels,
		}); err != nil {
			rollback()
			return driver.DeploymentRef{}, fmt.Errorf("volume create %s: %w", role, err)
		}
	}

	for _, role := range []string{RoleCairnet, RoleLore} {
		img := d.imgs.cairnet
		flagKey := "cairnet_image"
		if role == RoleLore {
			img = d.imgs.lore
			flagKey = "lore_image"
		}
		if v, ok := profile.DriverFlags[flagKey].(string); ok && v != "" {
			img = v
		}
		if rc, err := d.api.ImagePull(ctx, img, image.PullOptions{}); err == nil {
			_, _ = io.Copy(io.Discard, rc)
			_ = rc.Close()
		}
		labels := roleLabels(slug, role)
		labels[LabelTier] = string(profile.Tier)
		resp, err := d.api.ContainerCreate(ctx, &container.Config{
			Image:  img,
			Labels: labels,
			Env: []string{
				fmt.Sprintf("ROCKY_CAIRNET_STORAGE_MB=%d", profile.ResourceCaps.CairnetStorageMB),
				fmt.Sprintf("ROCKY_LORE_RETENTION_DAYS=%d", profile.ResourceCaps.LoreRetentionDays),
				fmt.Sprintf("ROCKY_SEATS=%d", profile.ResourceCaps.Seats),
				fmt.Sprintf("ROCKY_VECTOR_INDEX=%s", profile.ResourceCaps.VectorIndex),
			},
		}, &container.HostConfig{
			Mounts: nil, // nginx stand-ins ignore mounts; real volume binding lands in the testcontainer step
		}, &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				networkName(slug): {},
			},
		}, nil, containerName(slug, role))
		if err != nil {
			rollback()
			return driver.DeploymentRef{}, fmt.Errorf("container create %s: %w", role, err)
		}
		if err := d.api.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
			rollback()
			return driver.DeploymentRef{}, fmt.Errorf("container start %s: %w", role, err)
		}
	}

	return d.refFor(slug, profile, driver.StatusReady), nil
}

func (d *Driver) lookupContainers(ctx context.Context, slug string) ([]containerSummary, error) {
	out, err := d.api.ContainerList(ctx, container.ListOptions{All: true, Filters: workspaceFilter(slug)})
	if err != nil {
		return nil, err
	}
	summaries := make([]containerSummary, 0, len(out))
	for _, c := range out {
		summaries = append(summaries, containerSummary{ID: c.ID, Labels: c.Labels})
	}
	return summaries, nil
}

// containerSummary is a SDK-version-agnostic projection of the fields we read
// from ContainerList results, so the rest of the package never touches the
// SDK's drifting types.Container shape directly.
type containerSummary struct {
	ID     string
	Labels map[string]string
}

func (d *Driver) refFor(slug string, profile driver.ProvisioningProfile, status driver.Status) driver.DeploymentRef {
	return driver.DeploymentRef{
		WorkspaceSlug:    slug,
		Tier:             profile.Tier,
		Driver:           driver.DriverLocalDocker,
		Endpoint:         "unix:///var/run/rocky-hearth.sock",
		SecretsVaultPath: "",
		LastStatus:       status,
		Created:          d.now(),
	}
}
