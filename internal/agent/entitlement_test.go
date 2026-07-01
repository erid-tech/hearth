package agent

import (
	"testing"

	contractsagent "github.com/rocky-hq/contracts/go/agent"
)

func TestPolarEntitlement_DefaultSoloAllowsSoloFloor(t *testing.T) {
	r := CheckPolarEntitlement(EntitlementInput{
		WorkspaceSlug: "ws",
		TierFloor:     contractsagent.Solo,
		SeatsRequired: 0,
	})
	if !r.Allowed {
		t.Fatalf("expected allowed; got reason=%q", r.Reason)
	}
}

func TestPolarEntitlement_DefaultSoloDeniesTeamFloor(t *testing.T) {
	r := CheckPolarEntitlement(EntitlementInput{
		WorkspaceSlug: "ws",
		TierFloor:     contractsagent.Team,
		SeatsRequired: 0,
	})
	if r.Allowed {
		t.Fatal("expected deny")
	}
	if r.Reason != "tier below floor (solo < team)" {
		t.Fatalf("unexpected reason: %q", r.Reason)
	}
}

func TestPolarEntitlement_TierEnvOverride(t *testing.T) {
	t.Setenv("ROCKY_POLAR_TIER_WS", "team")
	r := CheckPolarEntitlement(EntitlementInput{
		WorkspaceSlug: "ws",
		TierFloor:     contractsagent.Team,
		SeatsRequired: 0,
	})
	if !r.Allowed {
		t.Fatalf("expected allowed; got %q", r.Reason)
	}
}

func TestPolarEntitlement_EnterpriseAllowsAll(t *testing.T) {
	t.Setenv("ROCKY_POLAR_TIER_WS", "enterprise")
	for _, floor := range []contractsagent.TierFloor{
		contractsagent.Solo, contractsagent.Team, contractsagent.Fleet, contractsagent.Enterprise,
	} {
		r := CheckPolarEntitlement(EntitlementInput{
			WorkspaceSlug: "ws", TierFloor: floor, SeatsRequired: 0,
		})
		if !r.Allowed {
			t.Errorf("floor=%s: expected allowed; got %q", floor, r.Reason)
		}
	}
}

func TestPolarEntitlement_UnknownTierFloorsToSolo(t *testing.T) {
	t.Setenv("ROCKY_POLAR_TIER_WS", "godmode")
	r := CheckPolarEntitlement(EntitlementInput{
		WorkspaceSlug: "ws", TierFloor: contractsagent.Team, SeatsRequired: 0,
	})
	if r.Allowed {
		t.Fatal("unknown tier must not silently grant")
	}
	if r.Reason != "tier below floor (solo < team)" {
		t.Fatalf("expected conservative floor; got %q", r.Reason)
	}
}

func TestPolarEntitlement_HyphensMapToUnderscores(t *testing.T) {
	t.Setenv("ROCKY_POLAR_TIER_WS_A", "fleet")
	r := CheckPolarEntitlement(EntitlementInput{
		WorkspaceSlug: "ws-a", TierFloor: contractsagent.Fleet, SeatsRequired: 0,
	})
	if !r.Allowed {
		t.Fatalf("expected allowed; got %q", r.Reason)
	}
}

func TestPolarEntitlement_DefaultSeatsAllowsZeroOrOne(t *testing.T) {
	for _, need := range []int64{0, 1} {
		r := CheckPolarEntitlement(EntitlementInput{
			WorkspaceSlug: "ws", TierFloor: contractsagent.Solo, SeatsRequired: need,
		})
		if !r.Allowed {
			t.Errorf("seats=%d: expected allowed; got %q", need, r.Reason)
		}
	}
}

func TestPolarEntitlement_DefaultSeatsDeniesTwo(t *testing.T) {
	r := CheckPolarEntitlement(EntitlementInput{
		WorkspaceSlug: "ws", TierFloor: contractsagent.Solo, SeatsRequired: 2,
	})
	if r.Allowed {
		t.Fatal("expected deny")
	}
	if r.Reason != "insufficient seats (1 < 2)" {
		t.Fatalf("unexpected reason: %q", r.Reason)
	}
}

func TestPolarEntitlement_SeatsEnvOverride(t *testing.T) {
	t.Setenv("ROCKY_POLAR_SEATS_WS", "5")
	r := CheckPolarEntitlement(EntitlementInput{
		WorkspaceSlug: "ws", TierFloor: contractsagent.Solo, SeatsRequired: 5,
	})
	if !r.Allowed {
		t.Fatalf("expected allowed; got %q", r.Reason)
	}
}

func TestPolarEntitlement_NonNumericSeatsFloorToDefault(t *testing.T) {
	t.Setenv("ROCKY_POLAR_SEATS_WS", "abc")
	r := CheckPolarEntitlement(EntitlementInput{
		WorkspaceSlug: "ws", TierFloor: contractsagent.Solo, SeatsRequired: 2,
	})
	if r.Allowed {
		t.Fatal("non-numeric seats must not silently grant")
	}
}

func TestPolarEntitlement_NegativeSeatsFloorToDefault(t *testing.T) {
	t.Setenv("ROCKY_POLAR_SEATS_WS", "-3")
	r := CheckPolarEntitlement(EntitlementInput{
		WorkspaceSlug: "ws", TierFloor: contractsagent.Solo, SeatsRequired: 2,
	})
	if r.Allowed {
		t.Fatal("negative seats must not silently grant")
	}
}

func TestPolarEntitlement_TierBeforeSeats(t *testing.T) {
	t.Setenv("ROCKY_POLAR_SEATS_WS", "0")
	r := CheckPolarEntitlement(EntitlementInput{
		WorkspaceSlug: "ws", TierFloor: contractsagent.Team, SeatsRequired: 100,
	})
	if r.Allowed {
		t.Fatal("expected deny")
	}
	if got, want := r.Reason, "tier below floor"; !contains(got, want) {
		t.Fatalf("expected %q in reason; got %q", want, got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
