package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKeyUpstreamsValidate(t *testing.T) {
	tests := []struct {
		name      string
		labels    []string
		errSubstr string
	}{
		{name: "empty labels", labels: nil, errSubstr: "at least one label"},
		{name: "empty label", labels: []string{"archive", ""}, errSubstr: "must not contain an empty label"},
		{name: "duplicate label", labels: []string{"archive", "fast", "archive"}, errSubstr: "duplicate label 'archive'"},
		{name: "valid", labels: []string{"archive", "fast"}, errSubstr: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			err := (&LocalKeyConfig{
				Key:               "secret",
				KeySettingsConfig: &KeySettingsConfig{Upstreams: &KeyUpstreams{GroupLabels: test.labels}},
			}).validate()
			if test.errSubstr == "" {
				assert.NoError(te, err)
			} else {
				assert.ErrorContains(te, err, test.errSubstr)
			}
		})
	}
}

func TestValidateKeyUpstreamsRequiresKnownGroupLabels(t *testing.T) {
	upstreamConfig := &UpstreamConfig{Upstreams: []*Upstream{
		{Id: "up1", GroupLabels: []string{"full"}},
		{Id: "up2", GroupLabels: []string{"archive", "fast"}},
	}}
	authConfig := func(labels ...string) *AuthConfig {
		return &AuthConfig{
			Enabled: true,
			KeyConfigs: []*KeyConfig{
				{Id: "no-settings", Type: Local, LocalKeyConfig: &LocalKeyConfig{Key: "a"}},
				{Id: "restricted", Type: Local, LocalKeyConfig: &LocalKeyConfig{
					Key:               "b",
					KeySettingsConfig: &KeySettingsConfig{Upstreams: &KeyUpstreams{GroupLabels: labels}},
				}},
			},
		}
	}

	assert.NoError(t, authConfig("archive", "full").validateKeyUpstreams(upstreamConfig))
	assert.ErrorContains(t,
		authConfig("archive", "archvie").validateKeyUpstreams(upstreamConfig),
		"error during 'restricted' key config validation, cause: upstreams.group-labels has 'archvie', which no upstream carries",
	)

	disabled := authConfig("unknown")
	disabled.Enabled = false
	assert.NoError(t, disabled.validateKeyUpstreams(upstreamConfig))
}
