package agent_test

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"

	contractsagent "github.com/rocky-hq/contracts/go/agent"
	contractshearth "github.com/rocky-hq/contracts/go/hearth"

	"github.com/rocky-hq/hearth/internal/agent"
)

var agentIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]*-(council|sniffer|stratt|ralph|relay|driver)-[a-z0-9-]+$`)

func TestBuildAgentID_LocalDocker(t *testing.T) {
	t.Parallel()
	id := agent.BuildAgentID("iris-hq", contractshearth.LocalDocker)
	if want := "iris-hq-driver-local-docker"; id != want {
		t.Fatalf("id = %q, want %q", id, want)
	}
	if !agentIDRE.MatchString(id) {
		t.Fatalf("id %q does not match spec regex", id)
	}
}

func TestBuildAgentID_NormalizesDisallowedChars(t *testing.T) {
	t.Parallel()
	cases := map[contractshearth.DriverName]string{
		contractshearth.LocalDocker:  "ws-driver-local-docker",
		contractshearth.Kustomize:    "ws-driver-kustomize",
		contractshearth.DevarnoCloud: "ws-driver-devarno-cloud",
	}
	for driver, want := range cases {
		got := agent.BuildAgentID("ws", driver)
		if got != want {
			t.Errorf("driver=%q got %q want %q", driver, got, want)
		}
		if !agentIDRE.MatchString(got) {
			t.Errorf("id %q does not match spec regex", got)
		}
	}
}

func TestCapabilityForVerb(t *testing.T) {
	t.Parallel()
	cases := map[agent.Verb]string{
		agent.VerbProvision: "driver.provision",
		agent.VerbStatus:    "driver.status",
		agent.VerbUpgrade:   "driver.upgrade",
		agent.VerbTeardown:  "driver.teardown",
	}
	for verb, want := range cases {
		if got := agent.CapabilityForVerb(verb); got != want {
			t.Errorf("verb=%q got %q want %q", verb, got, want)
		}
	}
}

func TestBuildRegistration_ShapeAndRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	reg := agent.BuildRegistration("iris-hq", contractshearth.LocalDocker, now)

	if reg.Schema != contractsagent.AgentRegistrationV1 {
		t.Errorf("schema = %q", reg.Schema)
	}
	if reg.Scope != contractsagent.Driver {
		t.Errorf("scope = %q", reg.Scope)
	}
	if reg.Owner.Subsystem != contractsagent.Ss08 {
		t.Errorf("subsystem = %q, want SS-08", reg.Owner.Subsystem)
	}
	if reg.Owner.WorkspaceSlug != "iris-hq" {
		t.Errorf("workspace_slug = %q", reg.Owner.WorkspaceSlug)
	}
	if reg.Approval.Required {
		t.Error("approval.required should be false in v1")
	}
	if reg.Rate.TierFloor != contractsagent.Solo || reg.Rate.SeatsRequired != 0 {
		t.Errorf("rate = %+v", reg.Rate)
	}
	if !reg.DeclaredAt.Equal(now) {
		t.Errorf("declared_at = %v, want %v", reg.DeclaredAt, now)
	}
	wantCaps := []string{"driver.provision", "driver.status", "driver.upgrade", "driver.teardown"}
	if len(reg.Capabilities) != len(wantCaps) {
		t.Fatalf("capabilities = %v", reg.Capabilities)
	}
	for i, c := range wantCaps {
		if reg.Capabilities[i] != c {
			t.Errorf("capabilities[%d] = %q, want %q", i, reg.Capabilities[i], c)
		}
	}

	// Round-trip through JSON to prove the tags line up with the
	// wire contract exactly.
	blob, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back contractsagent.AgentRegistration
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.AgentID != reg.AgentID {
		t.Errorf("round-trip agent_id: got %q want %q", back.AgentID, reg.AgentID)
	}
}
