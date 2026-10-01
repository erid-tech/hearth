package agent

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	contractsagent "github.com/erid-tech/contracts/go/agent"
)

// tierRank encodes the spec's ordered ladder for
// contractsagent.TierFloor. Higher rank ⇒ higher tier.
// Kept private: consumers speak in the typed enum, not in ints.
func tierRank(t contractsagent.TierFloor) int {
	switch t {
	case contractsagent.Solo:
		return 0
	case contractsagent.Team:
		return 1
	case contractsagent.Fleet:
		return 2
	case contractsagent.Enterprise:
		return 3
	default:
		return 0
	}
}

// EntitlementInput mirrors the console-side shape verbatim so a future
// swap to a real Polar SDK keeps the same signature.
type EntitlementInput struct {
	WorkspaceSlug string
	TierFloor     contractsagent.TierFloor
	SeatsRequired int64
}

// EntitlementResult is the boolean allow/deny with a reason string
// suitable for the HTTP error body.
type EntitlementResult struct {
	Allowed bool
	Reason  string
}

func envKey(prefix, slug string) string {
	up := strings.ToUpper(slug)
	up = strings.ReplaceAll(up, "-", "_")
	return prefix + up
}

func readTier(slug string) contractsagent.TierFloor {
	raw := os.Getenv(envKey("ROCKY_POLAR_TIER_", slug))
	switch raw {
	case string(contractsagent.Solo):
		return contractsagent.Solo
	case string(contractsagent.Team):
		return contractsagent.Team
	case string(contractsagent.Fleet):
		return contractsagent.Fleet
	case string(contractsagent.Enterprise):
		return contractsagent.Enterprise
	case "":
		return contractsagent.Solo
	default:
		// Unknown tier string floors to solo. Deliberately conservative:
		// a misconfigured env var MUST NOT silently grant a higher tier.
		return contractsagent.Solo
	}
}

func readSeats(slug string) int64 {
	raw := os.Getenv(envKey("ROCKY_POLAR_SEATS_", slug))
	if raw == "" {
		return 1
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 1
	}
	return n
}

// CheckPolarEntitlement is the Go seam matching the console-side
// checkPolarEntitlement. Env-driven stub today; 7c-c-b swaps the body
// for a real Polar SDK call.
func CheckPolarEntitlement(in EntitlementInput) EntitlementResult {
	currentTier := readTier(in.WorkspaceSlug)
	currentSeats := readSeats(in.WorkspaceSlug)
	if tierRank(currentTier) < tierRank(in.TierFloor) {
		return EntitlementResult{
			Allowed: false,
			Reason:  fmt.Sprintf("tier below floor (%s < %s)", currentTier, in.TierFloor),
		}
	}
	if currentSeats < in.SeatsRequired {
		return EntitlementResult{
			Allowed: false,
			Reason:  fmt.Sprintf("insufficient seats (%d < %d)", currentSeats, in.SeatsRequired),
		}
	}
	return EntitlementResult{Allowed: true}
}
