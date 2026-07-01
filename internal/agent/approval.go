package agent

import (
	"fmt"
	"os"
	"strings"

	contractsagent "github.com/rocky-hq/contracts/go/agent"
)

// ApprovalInput mirrors the console-side shape.
type ApprovalInput struct {
	AgentID          string
	RequiresApproval bool
	AirlockVerb      string // empty ⇒ "agent.approve"
}

// ApprovalResult carries the boolean + reason for the HTTP error body.
type ApprovalResult struct {
	Approved bool
	Reason   string
}

// CheckAirlockApproval is the Go seam matching the console-side
// checkAirlockApproval. Env-driven stub today; 7c-c-c swaps the body
// for a real airlock signed-token verification against the
// agent.approve verb.
func CheckAirlockApproval(in ApprovalInput) ApprovalResult {
	if !in.RequiresApproval {
		return ApprovalResult{Approved: true}
	}
	key := "ROCKY_AGENT_APPROVED_" + strings.ReplaceAll(strings.ToUpper(in.AgentID), "-", "_")
	raw := os.Getenv(key)
	if raw == "1" || raw == "true" {
		return ApprovalResult{Approved: true}
	}
	verb := in.AirlockVerb
	if verb == "" {
		verb = "agent.approve"
	}
	return ApprovalResult{
		Approved: false,
		Reason:   fmt.Sprintf("pending airlock %s", verb),
	}
}

// _unused prevents the unused import when contractsagent is not
// referenced (keeps signatures aligned with the console side even if
// we never call into typed fields).
var _ = contractsagent.AgentRegistrationV1
