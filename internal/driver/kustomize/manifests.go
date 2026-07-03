package kustomize

import (
	"fmt"
	"strings"

	"github.com/rocky-hq/hearth/internal/driver"
)

const (
	roleCairnet = "cairnet"
	roleLore    = "lore"
)

// buildKustomization returns the kustomization.yaml bytes for a workspace.
// The header comment tells operators how to apply and delete out-of-band.
func buildKustomization(slug string, tier driver.Tier) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# emitted by rocky-hearth kustomize driver — workspace=%s tier=%s\n", slug, tier)
	b.WriteString("# apply:  kubectl apply -k .\n")
	b.WriteString("# delete: kubectl delete -k .\n")
	b.WriteString("apiVersion: kustomize.config.k8s.io/v1beta1\n")
	b.WriteString("kind: Kustomization\n")
	fmt.Fprintf(&b, "namespace: %s\n", namespaceName(slug))
	b.WriteString("commonLabels:\n")
	fmt.Fprintf(&b, "  rocky.workspace: %s\n", slug)
	fmt.Fprintf(&b, "  rocky.tier: %s\n", tier)
	b.WriteString("resources:\n")
	fmt.Fprintf(&b, "  - %s\n", fileNamespace)
	fmt.Fprintf(&b, "  - %s\n", fileCairnet)
	fmt.Fprintf(&b, "  - %s\n", fileLore)
	return []byte(b.String())
}

// buildNamespace emits the Namespace manifest.
func buildNamespace(slug string) []byte {
	var b strings.Builder
	b.WriteString("apiVersion: v1\n")
	b.WriteString("kind: Namespace\n")
	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", namespaceName(slug))
	b.WriteString("  labels:\n")
	fmt.Fprintf(&b, "    rocky.workspace: %s\n", slug)
	return []byte(b.String())
}

// buildRoleManifest emits a Deployment + Service pair for a role
// (cairnet | lore).
func buildRoleManifest(slug, role string, tier driver.Tier, image string, caps driver.ResourceCaps) []byte {
	var b strings.Builder
	b.WriteString("apiVersion: apps/v1\n")
	b.WriteString("kind: Deployment\n")
	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", deploymentName(role))
	b.WriteString("  labels:\n")
	fmt.Fprintf(&b, "    rocky.role: %s\n", role)
	fmt.Fprintf(&b, "    rocky.tier: %s\n", tier)
	b.WriteString("spec:\n")
	b.WriteString("  replicas: 1\n")
	b.WriteString("  selector:\n")
	b.WriteString("    matchLabels:\n")
	fmt.Fprintf(&b, "      rocky.role: %s\n", role)
	b.WriteString("  template:\n")
	b.WriteString("    metadata:\n")
	b.WriteString("      labels:\n")
	fmt.Fprintf(&b, "        rocky.role: %s\n", role)
	fmt.Fprintf(&b, "        rocky.workspace: %s\n", slug)
	b.WriteString("    spec:\n")
	b.WriteString("      containers:\n")
	fmt.Fprintf(&b, "        - name: %s\n", role)
	fmt.Fprintf(&b, "          image: %s\n", image)
	b.WriteString("          env:\n")
	fmt.Fprintf(&b, "            - name: ROCKY_CAIRNET_STORAGE_MB\n              value: \"%d\"\n", caps.CairnetStorageMB)
	fmt.Fprintf(&b, "            - name: ROCKY_LORE_RETENTION_DAYS\n              value: \"%d\"\n", caps.LoreRetentionDays)
	fmt.Fprintf(&b, "            - name: ROCKY_SEATS\n              value: \"%d\"\n", caps.Seats)
	fmt.Fprintf(&b, "            - name: ROCKY_VECTOR_INDEX\n              value: \"%s\"\n", caps.VectorIndex)
	b.WriteString("---\n")
	b.WriteString("apiVersion: v1\n")
	b.WriteString("kind: Service\n")
	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", serviceName(role))
	b.WriteString("spec:\n")
	b.WriteString("  selector:\n")
	fmt.Fprintf(&b, "    rocky.role: %s\n", role)
	b.WriteString("  ports:\n")
	b.WriteString("    - port: 80\n")
	b.WriteString("      targetPort: 80\n")
	return []byte(b.String())
}

// imageFor resolves the image for a role, honoring DriverFlags overrides
// (`cairnet_image` / `lore_image`) exactly like LocalDocker.
func (d *Driver) imageFor(role string, profile driver.ProvisioningProfile) string {
	img := d.imgs.cairnet
	flagKey := "cairnet_image"
	if role == roleLore {
		img = d.imgs.lore
		flagKey = "lore_image"
	}
	if v, ok := profile.DriverFlags[flagKey].(string); ok && v != "" {
		img = v
	}
	return img
}
