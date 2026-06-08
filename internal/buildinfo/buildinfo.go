package buildinfo

import "strings"

var (
	Version = "dev"
	GitSHA  = ""
)

func ProductVersion() string {
	version := strings.TrimSpace(Version)
	sha := strings.TrimSpace(GitSHA)
	if version == "" {
		version = "dev"
	}

	if strings.HasPrefix(version, "nodecore/") {
		if sha != "" && !strings.Contains(version, sha) && version == "nodecore/dev" {
			return version + "-" + sha
		}
		return version
	}

	if version == "dev" && sha != "" {
		return "nodecore/dev-" + sha
	}

	return "nodecore/" + version
}
