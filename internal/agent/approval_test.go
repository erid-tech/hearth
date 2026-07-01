package agent

import "testing"

func TestApproval_RequiresFalseShortCircuits(t *testing.T) {
	r := CheckAirlockApproval(ApprovalInput{AgentID: "ws-driver-local-docker", RequiresApproval: false})
	if !r.Approved {
		t.Fatalf("required:false must short-circuit; got %q", r.Reason)
	}
}

func TestApproval_RequiresTrueWithoutEnvDenies(t *testing.T) {
	r := CheckAirlockApproval(ApprovalInput{AgentID: "ws-driver-local-docker", RequiresApproval: true})
	if r.Approved {
		t.Fatal("expected deny")
	}
	if r.Reason != "pending airlock agent.approve" {
		t.Fatalf("unexpected reason: %q", r.Reason)
	}
}

func TestApproval_EnvOneApproves(t *testing.T) {
	t.Setenv("ROCKY_AGENT_APPROVED_WS_DRIVER_LOCAL_DOCKER", "1")
	r := CheckAirlockApproval(ApprovalInput{AgentID: "ws-driver-local-docker", RequiresApproval: true})
	if !r.Approved {
		t.Fatalf("expected approved; got %q", r.Reason)
	}
}

func TestApproval_EnvTrueApproves(t *testing.T) {
	t.Setenv("ROCKY_AGENT_APPROVED_WS_DRIVER_LOCAL_DOCKER", "true")
	r := CheckAirlockApproval(ApprovalInput{AgentID: "ws-driver-local-docker", RequiresApproval: true})
	if !r.Approved {
		t.Fatalf("expected approved; got %q", r.Reason)
	}
}

func TestApproval_EnvZeroDenies(t *testing.T) {
	t.Setenv("ROCKY_AGENT_APPROVED_WS_DRIVER_LOCAL_DOCKER", "0")
	r := CheckAirlockApproval(ApprovalInput{AgentID: "ws-driver-local-docker", RequiresApproval: true})
	if r.Approved {
		t.Fatal("env=0 must not approve")
	}
}

func TestApproval_CustomAirlockVerbInReason(t *testing.T) {
	r := CheckAirlockApproval(ApprovalInput{
		AgentID: "ws-driver-local-docker", RequiresApproval: true, AirlockVerb: "agent.teardown",
	})
	if r.Approved {
		t.Fatal("expected deny")
	}
	if r.Reason != "pending airlock agent.teardown" {
		t.Fatalf("unexpected reason: %q", r.Reason)
	}
}
