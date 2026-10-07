package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKeyUpstreamsValidate(t *testing.T) {
	upstreamConfig := &UpstreamConfig{Upstreams: []*Upstream{
		{Id: "up1", GroupLabels: []string{"full"}},
		{Id: "up2", GroupLabels: []string{"archive", "fast"}},
	}}
	tests := []struct {
		name      string
		labels    []string
		errSubstr string
	}{
		{name: "empty labels", labels: nil, errSubstr: "upstreams.group-labels must contain at least one label"},
		{name: "empty label", labels: []string{"archive", ""}, errSubstr: "upstreams.group-labels must not contain an empty label"},
		{name: "duplicate label", labels: []string{"archive", "fast", "archive"}, errSubstr: "upstreams.group-labels contains a duplicate label 'archive'"},
		{name: "unknown label", labels: []string{"archive", "archvie"}, errSubstr: "upstreams.group-labels has 'archvie', which no upstream carries"},
		{name: "valid", labels: []string{"archive", "full"}, errSubstr: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			authConfig := &AuthConfig{
				Enabled: true,
				KeyConfigs: []*KeyConfig{
					{Id: "no-settings", Type: Local, LocalKeyConfig: &LocalKeyConfig{Key: "a"}},
					{Id: "restricted", Type: Local, LocalKeyConfig: &LocalKeyConfig{
						Key:               "b",
						KeySettingsConfig: &KeySettingsConfig{Upstreams: &KeyUpstreams{GroupLabels: test.labels}},
					}},
				},
			}
			err := authConfig.validate(nil, upstreamConfig)
			if test.errSubstr == "" {
				assert.NoError(te, err)
			} else {
				assert.ErrorContains(te, err, "error during 'restricted' key config validation, cause: "+test.errSubstr)
			}
		})
	}
}

func TestKeyUpstreamsValidateSkippedWhenAuthDisabled(t *testing.T) {
	authConfig := &AuthConfig{
		KeyConfigs: []*KeyConfig{
			{Id: "restricted", Type: Local, LocalKeyConfig: &LocalKeyConfig{
				Key:               "b",
				KeySettingsConfig: &KeySettingsConfig{Upstreams: &KeyUpstreams{GroupLabels: []string{"unknown"}}},
			}},
		},
	}
	assert.NoError(t, authConfig.validate(nil, &UpstreamConfig{}))
}
