package localdocker

func networkName(slug string) string         { return "rocky-hearth_" + slug }
func containerName(slug, role string) string { return "rocky-hearth_" + slug + "_" + role }
func volumeName(slug, role string) string    { return "rocky-hearth_" + slug + "_" + role + "-data" }
