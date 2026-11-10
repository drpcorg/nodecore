package compatible_clients_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/upstreams/compatible_clients"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeVersion(t *testing.T) {
	assert.Equal(t, "1.4.0", compatible_clients.NormalizeVersion(" V1.4.0 "))
	assert.Equal(t, "", compatible_clients.NormalizeVersion("v"))
	assert.Equal(t, "v1", compatible_clients.NormalizeVersion("vv1"))
}

func TestVersionMatches(t *testing.T) {
	tests := []struct {
		rule, label string
		want        bool
	}{
		{"v1.4.0", "v1.4.0-5e4e5c6", true},
		{"v1.4.0-5e4e5c6", "v1.4.0-5e4e5c6", true},
		{"v1.4.0-5e4e5c6", "v1.4.0-9a1b2c3", false},
		{"v2.40.0", "2.40.0", true},
		{"1.25.4", "v1.25.4+20b10b35", true},
		{"v1.13.14", "v1.13.14-stable-2bd6bd01", true},
		{"v1.4", "v1.40.0-abc", false},
		{"1.4.0", "1.4.0.1", false},
	}
	for _, test := range tests {
		assert.Equal(t, test.want, compatible_clients.VersionMatches(test.rule, test.label), "rule %q label %q", test.rule, test.label)
	}
}

func TestCheckWhitelistBeforeBlacklist(t *testing.T) {
	rules := decodeRules(t, `
rules:
  - client: besu
    whitelist: [v24.1.0, v24.1.2]
    blacklist: [v23.10.0]
`)

	assert.Equal(t, compatible_clients.Allowed, rules.Check("besu", "v24.1.2-abc", ethereum))
	assert.Equal(t, compatible_clients.NotWhitelisted, rules.Check("besu", "v24.2.0", ethereum))
	assert.Equal(t, compatible_clients.NotWhitelisted, rules.Check("besu", "v23.10.0", ethereum))
}
