package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"

	contractsagent "github.com/erid-tech/contracts/go/agent"

	"github.com/erid-tech/hearth/internal/agent"
	"github.com/erid-tech/hearth/internal/driver"
	"github.com/erid-tech/hearth/internal/driver/fake"
)

// recordingEmitter is a synchronous test double for agent.Emitter.
// It captures every event a handler emits so tests can assert on
// ordering + shape without racing goroutines.
type recordingEmitter struct {
	mu     sync.Mutex
	events []contractsagent.AgentHatchEvent
}

func (r *recordingEmitter) Emit(_ context.Context, ev contractsagent.AgentHatchEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingEmitter) snapshot() []contractsagent.AgentHatchEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]contractsagent.AgentHatchEvent, len(r.events))
	copy(out, r.events)
	return out
}

func newServerWithEmitter(t *testing.T, d driver.Driver, e *recordingEmitter) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(d).WithEmitter(e).Mux())
	t.Cleanup(srv.Close)
	return srv
}

func TestProvision_EmitsTriple_OK(t *testing.T) {
	emit := &recordingEmitter{}
	srv := newServerWithEmitter(t, fake.New(), emit)

	body, _ := json.Marshal(ProvisionReq{Slug: "iris-hq", Profile: soloProfile()})
	resp, err := httpPost(t, srv.URL+"/v1/provision", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	events := emit.snapshot()
	if got := len(events); got != 3 {
		t.Fatalf("emitted %d events, want 3", got)
	}
	if events[0].Kind != contractsagent.AgentRegistered {
		t.Errorf("events[0].Kind = %q", events[0].Kind)
	}
	if events[1].Kind != contractsagent.AgentInvoked {
		t.Errorf("events[1].Kind = %q", events[1].Kind)
	}
	if events[2].Kind != contractsagent.AgentCompleted {
		t.Errorf("events[2].Kind = %q", events[2].Kind)
	}
	if events[0].AgentID != "iris-hq-driver-local-docker" {
		t.Errorf("agent_id = %q", events[0].AgentID)
	}
	if got := events[1].Capability; got == nil || *got != "driver.provision" {
		t.Errorf("invoked.capability = %v", got)
	}
	if got := events[2].Outcome; got == nil || *got != contractsagent.Ok {
		t.Errorf("completed.outcome = %v", got)
	}
	if events[1].InvocationID == nil || events[2].InvocationID == nil ||
		*events[1].InvocationID != *events[2].InvocationID {
		t.Errorf("invocation_id mismatch: invoked=%v completed=%v", events[1].InvocationID, events[2].InvocationID)
	}
}

func TestProvision_EmitsTriple_ErrorOutcome(t *testing.T) {
	emit := &recordingEmitter{}
	d := &alwaysErrDriver{err: errors.New("boom")}
	srv := newServerWithEmitter(t, d, emit)

	body, _ := json.Marshal(ProvisionReq{Slug: "iris-hq", Profile: soloProfile()})
	resp, err := httpPost(t, srv.URL+"/v1/provision", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}

	events := emit.snapshot()
	if got := len(events); got != 3 {
		t.Fatalf("emitted %d events, want 3", got)
	}
	if got := events[2].Outcome; got == nil || *got != contractsagent.Error {
		t.Errorf("completed.outcome = %v, want error", got)
	}
}

func TestProvision_EmptySlug_ZeroEvents(t *testing.T) {
	emit := &recordingEmitter{}
	srv := newServerWithEmitter(t, fake.New(), emit)

	// Empty slug: the fake driver returns an error here (slug required),
	// but the agent emission itself must be gated on slug before any
	// handler side-effect. Assertion is on emit count regardless of
	// driver outcome.
	body, _ := json.Marshal(ProvisionReq{Slug: "", Profile: soloProfile()})
	resp, err := httpPost(t, srv.URL+"/v1/provision", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if got := len(emit.snapshot()); got != 0 {
		t.Fatalf("emitted %d events with empty slug, want 0", got)
	}
}

func TestTeardown_EmitsTeardownCapability(t *testing.T) {
	emit := &recordingEmitter{}
	d := fake.New()
	srv := newServerWithEmitter(t, d, emit)

	// Provision first, then tear down using the returned ref so
	// WorkspaceSlug + Driver flow into the teardown emission.
	pbody, _ := json.Marshal(ProvisionReq{Slug: "iris-hq", Profile: soloProfile()})
	presp, err := httpPost(t, srv.URL+"/v1/provision", "application/json", bytes.NewReader(pbody))
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	var ref driver.DeploymentRef
	if err := json.NewDecoder(presp.Body).Decode(&ref); err != nil {
		t.Fatalf("decode ref: %v", err)
	}
	presp.Body.Close()

	// Clear the provision events; we only want teardown's triple.
	emit.mu.Lock()
	emit.events = nil
	emit.mu.Unlock()

	tbody, _ := json.Marshal(TeardownReq{Ref: ref})
	tresp, err := httpPost(t, srv.URL+"/v1/teardown", "application/json", bytes.NewReader(tbody))
	if err != nil {
		t.Fatalf("teardown: %v", err)
	}
	defer tresp.Body.Close()
	if tresp.StatusCode != 200 {
		t.Fatalf("teardown status = %d", tresp.StatusCode)
	}

	events := emit.snapshot()
	if got := len(events); got != 3 {
		t.Fatalf("emitted %d events, want 3", got)
	}
	if got := events[1].Capability; got == nil || *got != "driver.teardown" {
		t.Errorf("teardown invoked.capability = %v", got)
	}
	if got := events[2].Outcome; got == nil || *got != contractsagent.Ok {
		t.Errorf("teardown completed.outcome = %v", got)
	}
}

func TestStatus_UsesRefWorkspaceSlug(t *testing.T) {
	emit := &recordingEmitter{}
	srv := newServerWithEmitter(t, fake.New(), emit)

	ref := driver.DeploymentRef{
		WorkspaceSlug: "iris-hq",
		Driver:        driver.DriverLocalDocker,
	}
	body, _ := json.Marshal(StatusReq{Ref: ref})
	resp, err := httpPost(t, srv.URL+"/v1/status", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()

	events := emit.snapshot()
	if got := len(events); got != 3 {
		t.Fatalf("emitted %d events, want 3", got)
	}
	if events[0].AgentID != "iris-hq-driver-local-docker" {
		t.Errorf("agent_id = %q", events[0].AgentID)
	}
	if got := events[1].Capability; got == nil || *got != "driver.status" {
		t.Errorf("status invoked.capability = %v", got)
	}
}

func TestServer_NilEmitter_DefaultsToNop(t *testing.T) {
	srv := httptest.NewServer(New(fake.New()).WithEmitter(nil).Mux())
	defer srv.Close()

	body, _ := json.Marshal(ProvisionReq{Slug: "iris-hq", Profile: soloProfile()})
	resp, err := httpPost(t, srv.URL+"/v1/provision", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("nil-emitter path status = %d", resp.StatusCode)
	}
}

// alwaysErrDriver returns err from every Driver call. Used to force an
// error-path emission in TestProvision_EmitsTriple_ErrorOutcome. The
// existing errDriver fixture in server_test.go wraps a fake and only
// errors for specific verbs; this one is unconditional.
type alwaysErrDriver struct{ err error }

func (e *alwaysErrDriver) Provision(context.Context, string, driver.ProvisioningProfile) (driver.DeploymentRef, error) {
	return driver.DeploymentRef{}, e.err
}

func (e *alwaysErrDriver) Status(context.Context, driver.DeploymentRef) (driver.Status, error) {
	return "", e.err
}

func (e *alwaysErrDriver) Upgrade(context.Context, driver.DeploymentRef, driver.ProvisioningProfile) (driver.DeploymentRef, error) {
	return driver.DeploymentRef{}, e.err
}

func (e *alwaysErrDriver) Teardown(context.Context, driver.DeploymentRef) error {
	return e.err
}

// silence unused agent import warning if any refactor removes the direct use.
var _ = agent.VerbProvision
