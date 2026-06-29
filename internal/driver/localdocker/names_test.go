package localdocker

import "testing"

func TestNames(t *testing.T) {
	if got := networkName("alex-solo"); got != "rocky-hearth_alex-solo" {
		t.Errorf("networkName = %q", got)
	}
	if got := containerName("alex-solo", "cairnet"); got != "rocky-hearth_alex-solo_cairnet" {
		t.Errorf("containerName = %q", got)
	}
	if got := volumeName("alex-solo", "lore"); got != "rocky-hearth_alex-solo_lore-data" {
		t.Errorf("volumeName = %q", got)
	}
}
