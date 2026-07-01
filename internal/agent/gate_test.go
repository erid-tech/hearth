package agent

import (
	"net/http"
	"testing"
	"time"

	contractsagent "github.com/rocky-hq/contracts/go/agent"
	contractshearth "github.com/rocky-hq/contracts/go/hearth"
)

func regWithOverrides(t *testing.T, mutate func(*contractsagent.AgentRegistration)) contractsagent.AgentRegistration {
	t.Helper()
	r := BuildRegistration("ws", contractshearth.DriverName("local-docker"), time.Now().UTC())
	if mutate != nil {
		mutate(&r)
	}
	return r
}

func TestGate_DefaultRegistrationAllows(t *testing.T) {
	r := GateInvocation(regWithOverrides(t, nil), "ws")
	if !r.Allowed {
		t.Fatalf("expected allowed; got status=%d reason=%q", r.Status, r.Reason)
	}
}

func TestGate_ApprovalBeforeEntitlement(t *testing.T) {
	verb := "agent.approve"
	reg := regWithOverrides(t, func(r *contractsagent.AgentRegistration) {
		r.Approval = contractsagent.AgentRegistrationApproval{Required: true, AirlockVerb: &verb}
		r.Rate = contractsagent.AgentRegistrationRate{TierFloor: contractsagent.Fleet, SeatsRequired: 999}
	})
	got := GateInvocation(reg, "ws")
	if got.Allowed {
		t.Fatal("expected deny")
	}
	if got.Status != http.StatusForbidden {
		t.Fatalf("approval must win: expected 403; got %d (reason=%q)", got.Status, got.Reason)
	}
}

func TestGate_TierDeny402(t *testing.T) {
	reg := regWithOverrides(t, func(r *contractsagent.AgentRegistration) {
		r.Rate.TierFloor = contractsagent.Fleet
	})
	got := GateInvocation(reg, "ws")
	if got.Allowed {
		t.Fatal("expected deny")
	}
	if got.Status != http.StatusPaymentRequired {
		t.Fatalf("expected 402; got %d", got.Status)
	}
	if !contains(got.Reason, "tier below floor") {
		t.Fatalf("unexpected reason: %q", got.Reason)
	}
}

func TestGate_SeatsDeny402(t *testing.T) {
	t.Setenv("ROCKY_POLAR_TIER_WS", "enterprise")
	reg := regWithOverrides(t, func(r *contractsagent.AgentRegistration) {
		r.Rate.SeatsRequired = 99
	})
	got := GateInvocation(reg, "ws")
	if got.Allowed {
		t.Fatal("expected deny")
	}
	if got.Status != http.StatusPaymentRequired {
		t.Fatalf("expected 402; got %d", got.Status)
	}
}

func TestGate_EnvApprovalAllowsWhenTierSatisfies(t *testing.T) {
	t.Setenv("ROCKY_AGENT_APPROVED_WS_DRIVER_LOCAL_DOCKER", "1")
	verb := "agent.approve"
	reg := regWithOverrides(t, func(r *contractsagent.AgentRegistration) {
		r.Approval = contractsagent.AgentRegistrationApproval{Required: true, AirlockVerb: &verb}
	})
	got := GateInvocation(reg, "ws")
	if !got.Allowed {
		t.Fatalf("expected allowed; got status=%d reason=%q", got.Status, got.Reason)
	}
}
