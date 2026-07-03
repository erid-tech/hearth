package kustomize

import (
	"path/filepath"
	"regexp"
)

const (
	fileKustomization = "kustomization.yaml"
	fileNamespace     = "namespace.yaml"
	fileCairnet       = "cairnet.yaml"
	fileLore          = "lore.yaml"
)

var slugRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// workspaceDir returns the absolute per-workspace emit directory.
func (d *Driver) workspaceDir(slug string) string {
	return filepath.Join(d.outdir, slug)
}

// namespaceName is the DNS-1123 namespace emitted into namespace.yaml.
func namespaceName(slug string) string {
	return "rocky-" + slug
}

// deploymentName / serviceName are derived from role.
func deploymentName(role string) string { return role }
func serviceName(role string) string    { return role }
