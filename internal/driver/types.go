package driver

// The hearth driver protocol shapes are defined canonically as zod
// schemas in @rocky-hq/contracts/src/hearth/ and consumed in Go via the
// generated bindings at github.com/erid-tech/contracts/go/hearth.
//
// This file re-exports the generated types and named constants under
// `package driver` so the rest of hearth (driver.go, fake/, future
// localdocker/) keeps the short names `Tier`, `DeploymentRef`, etc.
// DO NOT add new types here; extend the zod source.

import contractshearth "github.com/erid-tech/contracts/go/hearth"

// Generated type re-exports (Phase 5 spec §6).
type (
	Tier                = contractshearth.Tier
	DriverName          = contractshearth.DriverName
	Status              = contractshearth.Status
	ResourceCaps        = contractshearth.ResourceCaps
	ProvisioningProfile = contractshearth.ProvisioningProfile
	DeploymentRef       = contractshearth.DeploymentRef
	HearthHatchEvent    = contractshearth.HearthHatchEvent
)

// Named string constants for the enum schemas. Kept here (rather than
// imported, since Go does not support const aliasing across packages)
// so existing call sites compile unchanged.
const (
	TierSolo    Tier = "solo"
	TierTeam    Tier = "team"
	TierStudio  Tier = "studio"
	TierBespoke Tier = "bespoke"
)

const (
	DriverLocalDocker  DriverName = "local-docker"
	DriverKustomize    DriverName = "kustomize"
	DriverDevarnoCloud DriverName = "devarno-cloud"
)

const (
	StatusProvisioning Status = "provisioning"
	StatusReady        Status = "ready"
	StatusUpgrading    Status = "upgrading"
	StatusTearingDown  Status = "tearing_down"
	StatusFailed       Status = "failed"
	StatusTierTornDown Status = "tier_torn_down"
)
