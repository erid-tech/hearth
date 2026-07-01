package agent

import (
	"net/http"

	contractsagent "github.com/rocky-hq/contracts/go/agent"
)

// GateResult composes the approval + entitlement checks per
// docs/decisions/2026-06-29-phase-7c-c-a-close.md §Locked decision 4
// (auth-shape 403 precedes billing-shape 402). Denial short-circuits
// before any downstream work (RPC handler must NOT call the driver
// or Emit any agent.* event when Allowed=false).
type GateResult struct {
	Allowed bool
	Status  int    // 402 or 403 on denial; 0 when Allowed
	Reason  string // Human-readable reason on denial; empty when Allowed
}

// GateInvocation runs the approval check first, then the entitlement
// check. First denial wins. workspaceSlug is passed separately because
// registration.Owner.WorkspaceSlug is the source of truth for identity
// but the entitlement check reads env-scoped by the caller-known slug —
// keeps parity with the console signature.
func GateInvocation(reg contractsagent.AgentRegistration, workspaceSlug string) GateResult {
	approval := CheckAirlockApproval(ApprovalInput{
		AgentID:          reg.AgentID,
		RequiresApproval: reg.Approval.Required,
		AirlockVerb:      derefString(reg.Approval.AirlockVerb),
	})
	if !approval.Approved {
		return GateResult{Allowed: false, Status: http.StatusForbidden, Reason: approval.Reason}
	}
	entitlement := CheckPolarEntitlement(EntitlementInput{
		WorkspaceSlug: workspaceSlug,
		TierFloor:     reg.Rate.TierFloor,
		SeatsRequired: reg.Rate.SeatsRequired,
	})
	if !entitlement.Allowed {
		return GateResult{Allowed: false, Status: http.StatusPaymentRequired, Reason: entitlement.Reason}
	}
	return GateResult{Allowed: true}
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
