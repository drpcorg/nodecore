package compatible_clients

import (
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
)

// Verdict is what the rules say about one client version on one chain.
type Verdict int

const (
	// NoRule means the client has no entry for the chain and no default entry.
	NoRule Verdict = iota
	Allowed
	NotWhitelisted
	Blacklisted
)

// NormalizeVersion trims the version, drops one leading "v" and lowercases it.
func NormalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	version = strings.TrimPrefix(version, "V")
	return strings.ToLower(version)
}

// VersionMatches reports whether a rule entry matches a reported version: both are
// normalized, and the entry must equal either the whole version or its core, the part
// before the first "-" or "+". A bare entry such as "1.4.0" therefore matches every
// build of that release, while "1.4.0-5e4e5c6" matches only that build. Entries are
// never prefixes: "1.4" does not match "1.40.0".
func VersionMatches(ruleVersion, label string) bool {
	return matches(mapset.NewThreadUnsafeSet(NormalizeVersion(ruleVersion)), label)
}

// check judges a version: a non-empty whitelist that does not match it is
// NotWhitelisted, a blacklist match is Blacklisted, anything else is Allowed.
func (e *rule) check(clientVersion string) Verdict {
	if !e.whitelist.IsEmpty() && !matches(e.whitelist, clientVersion) {
		return NotWhitelisted
	}
	if matches(e.blacklist, clientVersion) {
		return Blacklisted
	}
	return Allowed
}

// matches reports whether a set of normalized entries holds the version or its core.
func matches(entries mapset.Set[string], version string) bool {
	normalized := NormalizeVersion(version)
	return entries.ContainsOne(normalized) || entries.ContainsOne(versionCore(normalized))
}

func versionCore(normalized string) string {
	if i := strings.IndexAny(normalized, "-+"); i != -1 {
		return normalized[:i]
	}
	return normalized
}
