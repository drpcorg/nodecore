package config

import (
	"testing"
	"time"

	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every connector type must be classified as http-backed or not, so that adding a type
// forces a decision here instead of silently accepting http settings that nothing reads.
func TestIsHttpConnectorTypeCoversEveryConnectorType(t *testing.T) {
	httpBacked := map[string]bool{
		"json-rpc":        true,
		"tendermint":      true,
		"rest":            true,
		"rest-indexer":    true,
		"rest-additional": true,
		"websocket":       false,
		"grpc":            false,
	}

	for name, wantHttp := range httpBacked {
		connectorType := specs.GetApiConnectorType(name)
		require.NotEqual(t, specs.UnknownType, connectorType, "unknown connector type %q", name)
		assert.Equal(t, wantHttp, isHttpConnectorType(connectorType), "connector type %q", name)
	}
}

func TestConnectorSettingsValidate(t *testing.T) {
	negative := -5 * time.Second
	zero := time.Duration(0)
	finite := 30 * time.Second

	tests := []struct {
		name          string
		connectorName string
		settings      *ConnectorSettings
		wantErr       string
	}{
		{
			name:          "nil settings are always fine",
			connectorName: "grpc",
			settings:      nil,
		},
		{
			name:          "empty settings are fine on a non-http connector",
			connectorName: "grpc",
			settings:      &ConnectorSettings{},
		},
		{
			name:          "http settings on an http connector",
			connectorName: "rest",
			settings:      &ConnectorSettings{Http: &HttpConnectorSettings{ResponseTimeout: &finite}},
		},
		{
			name:          "explicit zero is allowed",
			connectorName: "rest",
			settings:      &ConnectorSettings{Http: &HttpConnectorSettings{ResponseTimeout: &zero}},
		},
		{
			name:          "unset response timeout is allowed",
			connectorName: "rest",
			settings:      &ConnectorSettings{Http: &HttpConnectorSettings{}},
		},
		{
			name:          "negative response timeout is rejected",
			connectorName: "rest",
			settings:      &ConnectorSettings{Http: &HttpConnectorSettings{ResponseTimeout: &negative}},
			wantErr:       "http response timeout can't be less than 0",
		},
		{
			name:          "http settings on grpc are rejected",
			connectorName: "grpc",
			settings:      &ConnectorSettings{Http: &HttpConnectorSettings{ResponseTimeout: &finite}},
			wantErr:       "http settings are not applicable to the 'grpc' connector",
		},
		{
			name:          "http settings on websocket are rejected",
			connectorName: "websocket",
			settings:      &ConnectorSettings{Http: &HttpConnectorSettings{ResponseTimeout: &finite}},
			wantErr:       "http settings are not applicable to the 'websocket' connector",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.settings.validate(test.connectorName, specs.GetApiConnectorType(test.connectorName))
			if test.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, test.wantErr)
			}
		})
	}
}

// The accessor is what the connector actually reads, so it must yield the default for
// every shape of missing settings rather than panicking on a nil hop.
func TestApiConnectorConfigHttpResponseTimeout(t *testing.T) {
	zero := time.Duration(0)
	finite := 120 * time.Second

	var nilConfig *ApiConnectorConfig
	assert.Equal(t, DefaultHttpResponseTimeout, nilConfig.HttpResponseTimeout())
	assert.Equal(t, DefaultHttpResponseTimeout, (&ApiConnectorConfig{}).HttpResponseTimeout())
	assert.Equal(t, DefaultHttpResponseTimeout,
		(&ApiConnectorConfig{Settings: &ConnectorSettings{}}).HttpResponseTimeout())
	assert.Equal(t, DefaultHttpResponseTimeout,
		(&ApiConnectorConfig{Settings: &ConnectorSettings{Http: &HttpConnectorSettings{}}}).HttpResponseTimeout())

	configured := &ApiConnectorConfig{Settings: &ConnectorSettings{Http: &HttpConnectorSettings{ResponseTimeout: &finite}}}
	assert.Equal(t, finite, configured.HttpResponseTimeout())

	disabled := &ApiConnectorConfig{Settings: &ConnectorSettings{Http: &HttpConnectorSettings{ResponseTimeout: &zero}}}
	assert.Equal(t, time.Duration(0), disabled.HttpResponseTimeout())
	assert.Equal(t, 60*time.Second, DefaultHttpResponseTimeout)
}
