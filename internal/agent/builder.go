// Package agent implements the SS-08 driver-side producer for the
// agent-registration.v1 HATCH projection (Phase 7b, hearth driver slice).
//
// Wire format: contracts/src/agent (zod source), consumed via
// github.com/rocky-hq/contracts/go/agent bindings. See
// docs/decisions/2026-06-29-phase-7b-driver-close.md (in the parent
// superproject) for the shape lockdown.
package agent

import (
	"regexp"
	"strings"
	"time"

	contractsagent "github.com/rocky-hq/contracts/go/agent"
	contractshearth "github.com/rocky-hq/contracts/go/hearth"
)

// Capabilities is the fixed set of verbs registered for every hearth
// driver agent. Each RPC handler resolves its per-invocation capability
// to one of these values via CapabilityForVerb.
var Capabilities = []string{
	"driver.provision",
	"driver.status",
	"driver.upgrade",
	"driver.teardown",
}

// Verb identifies one of the four hearth RPC entry points.
type Verb string

const (
	VerbProvision Verb = "provision"
	VerbStatus    Verb = "status"
	VerbUpgrade   Verb = "upgrade"
	VerbTeardown  Verb = "teardown"
)

// CapabilityForVerb returns the spec-conformant agent.capability string
// for a given RPC verb (e.g. "driver.provision").
func CapabilityForVerb(v Verb) string {
	return "driver." + string(v)
}

var agentIDSlugRE = regexp.MustCompile(`[^a-z0-9-]+`)

// BuildAgentID composes the deterministic per-(workspace, driver)
// agent id. Suffix is normalized to [a-z0-9-] per the spec regex
// ^[a-z][a-z0-9-]*-driver-[a-z0-9-]+$.
func BuildAgentID(workspaceSlug string, driverName contractshearth.DriverName) string {
	suffix := agentIDSlugRE.ReplaceAllString(strings.ToLower(string(driverName)), "-")
	suffix = strings.Trim(suffix, "-")
	return workspaceSlug + "-driver-" + suffix
}

// BuildRegistration returns the AgentRegistration payload for a hearth
// driver. `declared_at` is set to the caller's `now`; production callers
// pass time.Now().UTC().
func BuildRegistration(
	workspaceSlug string,
	driverName contractshearth.DriverName,
	now time.Time,
) contractsagent.AgentRegistration {
	caps := make([]string, len(Capabilities))
	copy(caps, Capabilities)
	return contractsagent.AgentRegistration{
		Schema:       contractsagent.AgentRegistrationV1,
		AgentID:      BuildAgentID(workspaceSlug, driverName),
		Name:         "HEARTH driver (" + string(driverName) + " / " + workspaceSlug + ")",
		Scope:        contractsagent.Driver,
		Owner:        contractsagent.AgentRegistrationOwner{WorkspaceSlug: workspaceSlug, Subsystem: contractsagent.Ss08},
		Capabilities: caps,
		Approval:     contractsagent.AgentRegistrationApproval{Required: false},
		Rate:         contractsagent.AgentRegistrationRate{TierFloor: contractsagent.Solo, SeatsRequired: 0},
		DeclaredAt:   now,
	}
}
