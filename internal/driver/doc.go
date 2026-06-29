// Package driver defines the locked HEARTH provisioning protocol
// (Phase 5 spec §5) and re-exports the wire-format types from
// @rocky-hq/contracts/go/hearth. Concrete drivers live in sibling
// packages (fake/, localdocker/, ...).
//
// Concrete implementations: fake/ (Phase 5a), localdocker/ (Phase 5c).
package driver
