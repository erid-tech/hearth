package localdocker

import (
	"testing"

	"github.com/docker/docker/api/types/filters"
)

func TestBaseLabels(t *testing.T) {
	got := baseLabels("alex-solo")
	want := map[string]string{
		"rocky-hq.io/managed-by": "hearth",
		"rocky-hq.io/workspace":  "alex-solo",
	}
	if len(got) != len(want) {
		t.Fatalf("baseLabels: got %d entries, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("baseLabels[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestRoleLabels(t *testing.T) {
	got := roleLabels("alex-solo", "cairnet")
	if got["rocky-hq.io/managed-by"] != "hearth" ||
		got["rocky-hq.io/workspace"] != "alex-solo" ||
		got["rocky-hq.io/role"] != "cairnet" {
		t.Errorf("roleLabels(alex-solo, cairnet) = %v", got)
	}
}

func TestWorkspaceFilter(t *testing.T) {
	args := workspaceFilter("alex-solo")
	matched := args.Get("label")
	wantOne, wantTwo := "rocky-hq.io/managed-by=hearth", "rocky-hq.io/workspace=alex-solo"
	hasOne, hasTwo := false, false
	for _, l := range matched {
		if l == wantOne {
			hasOne = true
		}
		if l == wantTwo {
			hasTwo = true
		}
	}
	if !hasOne || !hasTwo {
		t.Errorf("workspaceFilter labels = %v, missing one of %q / %q", matched, wantOne, wantTwo)
	}
	_ = filters.Args{} // ensure import used in any future expansion
}
