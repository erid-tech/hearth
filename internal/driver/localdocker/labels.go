package localdocker

import "github.com/docker/docker/api/types/filters"

const (
	LabelManagedBy = "rocky-hq.io/managed-by"
	LabelWorkspace = "rocky-hq.io/workspace"
	LabelRole      = "rocky-hq.io/role"
	// LabelTier records the deployment tier on every managed resource so we
	// can detect a tier mismatch on idempotent Provision calls and force the
	// caller to invoke Upgrade explicitly.
	LabelTier = "rocky-hq.io/tier"

	ManagedByValue = "hearth"

	RoleCairnet = "cairnet"
	RoleLore    = "lore"
	RoleNetwork = "network"
	RoleVolume  = "volume"
)

func baseLabels(slug string) map[string]string {
	return map[string]string{
		LabelManagedBy: ManagedByValue,
		LabelWorkspace: slug,
	}
}

func roleLabels(slug, role string) map[string]string {
	m := baseLabels(slug)
	m[LabelRole] = role
	return m
}

func workspaceFilter(slug string) filters.Args {
	args := filters.NewArgs()
	args.Add("label", LabelManagedBy+"="+ManagedByValue)
	args.Add("label", LabelWorkspace+"="+slug)
	return args
}
