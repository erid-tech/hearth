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
		ref := d.refFor(slug, profile, driver.StatusReady)
		ref.Created = d.cairnetCreated(ctx, existing)
		return ref, nil
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

	ref := d.refFor(slug, profile, driver.StatusReady)
	if fresh, err := d.lookupContainers(ctx, slug); err == nil {
		ref.Created = d.cairnetCreated(ctx, fresh)
	}
	return ref, nil
}

// cairnetCreated returns the canonical Created timestamp for a deployment:
// the cairnet container's inspect.Created. Falls back to d.now() if no
// cairnet container is present (e.g. mid-Provision lookup races) or if
// inspect/parse fails. This keeps DeploymentRef.Created stable across
// idempotent re-Provisions instead of advancing on every call.
func (d *Driver) cairnetCreated(ctx context.Context, summaries []containerSummary) time.Time {
	for _, c := range summaries {
		if c.Labels[LabelRole] != RoleCairnet {
			continue
		}
		inspect, err := d.api.ContainerInspect(ctx, c.ID)
		if err == nil && inspect.ContainerJSONBase != nil && inspect.Created != "" {
			if t, perr := time.Parse(time.RFC3339Nano, inspect.Created); perr == nil {
				return t
			}
		}
		break
	}
	return d.now()
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

// Status aggregates the per-container daemon state for a workspace into a
// single Status value. Absent containers map to StatusTierTornDown so the
// post-Teardown read returns the D8-required terminal state.
func (d *Driver) Status(ctx context.Context, ref driver.DeploymentRef) (driver.Status, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	containers, err := d.lookupContainers(ctx, ref.WorkspaceSlug)
	if err != nil {
		return "", err
	}
	if len(containers) == 0 {
		return driver.StatusTierTornDown, nil
	}
	running := 0
	for _, c := range containers {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		inspect, err := d.api.ContainerInspect(ctx, c.ID)
		if err != nil {
			continue
		}
		if inspect.ContainerJSONBase == nil || inspect.State == nil {
			continue
		}
		switch inspect.State.Status {
		case "removing":
			return driver.StatusTearingDown, nil
		case "created", "restarting":
			return driver.StatusProvisioning, nil
		case "exited", "dead":
			if inspect.State.ExitCode != 0 {
				return driver.StatusFailed, nil
			}
		case "running":
			running++
		}
	}
	if running == len(containers) {
		return driver.StatusReady, nil
	}
	return driver.StatusProvisioning, nil
}

// Upgrade re-pulls the role images (honoring DriverFlags overrides) and
// recreates the CAIRNET+LORE containers. Volumes and the shared network are
// preserved across the call. The returned DeploymentRef keeps the original
// WorkspaceSlug and provisioning timestamp.
func (d *Driver) Upgrade(ctx context.Context, ref driver.DeploymentRef, profile driver.ProvisioningProfile) (driver.DeploymentRef, error) {
	if err := ctx.Err(); err != nil {
		return driver.DeploymentRef{}, err
	}
	containers, err := d.lookupContainers(ctx, ref.WorkspaceSlug)
	if err != nil {
		return driver.DeploymentRef{}, err
	}
	for _, c := range containers {
		if err := ctx.Err(); err != nil {
			return driver.DeploymentRef{}, err
		}
		_ = d.api.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
	}
	for _, role := range []string{RoleCairnet, RoleLore} {
		if err := ctx.Err(); err != nil {
			return driver.DeploymentRef{}, err
		}
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
		labels := roleLabels(ref.WorkspaceSlug, role)
		labels[LabelTier] = string(profile.Tier)
		resp, err := d.api.ContainerCreate(ctx, &container.Config{Image: img, Labels: labels}, &container.HostConfig{}, &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{networkName(ref.WorkspaceSlug): {}},
		}, nil, containerName(ref.WorkspaceSlug, role))
		if err != nil {
			return driver.DeploymentRef{}, fmt.Errorf("upgrade container create %s: %w", role, err)
		}
		if err := d.api.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
			return driver.DeploymentRef{}, fmt.Errorf("upgrade container start %s: %w", role, err)
		}
	}
	out := d.refFor(ref.WorkspaceSlug, profile, driver.StatusReady)
	out.Created = ref.Created
	return out, nil
}

// Teardown removes every resource labeled for the workspace: containers
// first, then the network, then volumes (D8 invariant — clean slate). The
// call is idempotent: missing resources are not an error, and a second
// invocation succeeds with nothing left to remove.
func (d *Driver) Teardown(ctx context.Context, ref driver.DeploymentRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	containers, err := d.lookupContainers(ctx, ref.WorkspaceSlug)
	if err != nil {
		return err
	}
	for _, c := range containers {
		if err := ctx.Err(); err != nil {
			return err
		}
		_ = d.api.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true, RemoveVolumes: false})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	nets, err := d.api.NetworkList(ctx, network.ListOptions{Filters: workspaceFilter(ref.WorkspaceSlug)})
	if err == nil {
		for _, n := range nets {
			if err := ctx.Err(); err != nil {
				return err
			}
			_ = d.api.NetworkRemove(ctx, n.ID)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	vols, err := d.api.VolumeList(ctx, volume.ListOptions{Filters: workspaceFilter(ref.WorkspaceSlug)})
	if err == nil {
		for _, v := range vols.Volumes {
			if err := ctx.Err(); err != nil {
				return err
			}
			_ = d.api.VolumeRemove(ctx, v.Name, true)
		}
	}
	return nil
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
