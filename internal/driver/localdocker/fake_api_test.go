package localdocker

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type fakeContainer struct {
	id       string
	name     string
	labels   map[string]string
	running  bool
	exitCode int
}

type fakeAPI struct {
	containers map[string]*fakeContainer // keyed by name
	networks   map[string]map[string]string
	volumes    map[string]map[string]string

	containerCreateCalls  int
	failOnContainerCreate string // container name that should error
	failContainerInspect  bool
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		containers: map[string]*fakeContainer{},
		networks:   map[string]map[string]string{},
		volumes:    map[string]map[string]string{},
	}
}

func (f *fakeAPI) hasNetwork(name string) bool   { _, ok := f.networks[name]; return ok }
func (f *fakeAPI) hasContainer(name string) bool { _, ok := f.containers[name]; return ok }
func (f *fakeAPI) hasVolume(name string) bool    { _, ok := f.volumes[name]; return ok }

func (f *fakeAPI) ContainerCreate(_ context.Context, config *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, name string) (container.CreateResponse, error) {
	f.containerCreateCalls++
	if name == f.failOnContainerCreate {
		return container.CreateResponse{}, errors.New("synthetic create failure")
	}
	id := "fake-" + name
	labels := map[string]string{}
	if config != nil {
		for k, v := range config.Labels {
			labels[k] = v
		}
	}
	f.containers[name] = &fakeContainer{id: id, name: name, labels: labels, running: false}
	return container.CreateResponse{ID: id}, nil
}

func (f *fakeAPI) ContainerStart(_ context.Context, id string, _ container.StartOptions) error {
	for _, c := range f.containers {
		if c.id == id {
			c.running = true
			return nil
		}
	}
	return errors.New("not found")
}

func (f *fakeAPI) ContainerInspect(_ context.Context, id string) (types.ContainerJSON, error) {
	if f.failContainerInspect {
		return types.ContainerJSON{}, errors.New("synthetic inspect failure")
	}
	for _, c := range f.containers {
		if c.id == id || c.name == strings.TrimPrefix(id, "/") {
			state := "exited"
			if c.running {
				state = "running"
			}
			return types.ContainerJSON{
				ContainerJSONBase: &types.ContainerJSONBase{
					ID:    c.id,
					Name:  "/" + c.name,
					State: &types.ContainerState{Status: state, ExitCode: c.exitCode},
				},
				Config: &container.Config{Labels: c.labels},
			}, nil
		}
	}
	return types.ContainerJSON{}, errors.New("not found")
}

func (f *fakeAPI) ContainerRemove(_ context.Context, id string, _ container.RemoveOptions) error {
	for name, c := range f.containers {
		if c.id == id || c.name == id {
			delete(f.containers, name)
			return nil
		}
	}
	return nil
}

func (f *fakeAPI) ContainerList(_ context.Context, opts container.ListOptions) ([]types.Container, error) {
	want := opts.Filters.Get("label")
	out := []types.Container{}
	for _, c := range f.containers {
		if matchLabels(c.labels, want) {
			out = append(out, types.Container{ID: c.id, Names: []string{"/" + c.name}, Labels: c.labels})
		}
	}
	return out, nil
}

func (f *fakeAPI) NetworkCreate(_ context.Context, name string, opts network.CreateOptions) (network.CreateResponse, error) {
	f.networks[name] = opts.Labels
	return network.CreateResponse{ID: "fake-net-" + name}, nil
}

func (f *fakeAPI) NetworkRemove(_ context.Context, id string) error {
	for name := range f.networks {
		if "fake-net-"+name == id || name == id {
			delete(f.networks, name)
			return nil
		}
	}
	return nil
}

func (f *fakeAPI) NetworkList(_ context.Context, opts network.ListOptions) ([]network.Summary, error) {
	want := opts.Filters.Get("label")
	out := []network.Summary{}
	for name, labels := range f.networks {
		if matchLabels(labels, want) {
			out = append(out, network.Summary{ID: "fake-net-" + name, Name: name, Labels: labels})
		}
	}
	return out, nil
}

func (f *fakeAPI) VolumeCreate(_ context.Context, opts volume.CreateOptions) (volume.Volume, error) {
	f.volumes[opts.Name] = opts.Labels
	return volume.Volume{Name: opts.Name, Labels: opts.Labels}, nil
}

func (f *fakeAPI) VolumeRemove(_ context.Context, id string, _ bool) error {
	delete(f.volumes, id)
	return nil
}

func (f *fakeAPI) VolumeList(_ context.Context, opts volume.ListOptions) (volume.ListResponse, error) {
	want := opts.Filters.Get("label")
	out := []*volume.Volume{}
	for name, labels := range f.volumes {
		if matchLabels(labels, want) {
			out = append(out, &volume.Volume{Name: name, Labels: labels})
		}
	}
	return volume.ListResponse{Volumes: out}, nil
}

func (f *fakeAPI) ImagePull(_ context.Context, _ string, _ image.PullOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeAPI) Close() error { return nil }

func matchLabels(labels map[string]string, want []string) bool {
	for _, w := range want {
		eq := strings.SplitN(w, "=", 2)
		if len(eq) != 2 || labels[eq[0]] != eq[1] {
			return false
		}
	}
	return true
}
