package specs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMethodDispatchPolicyFromSettings(t *testing.T) {
	method := MethodWithSettings(
		"eth_sendRawTransaction",
		[]ApiConnectorType{JsonRpcConnector},
		&MethodSettings{Dispatch: DispatchBroadcast},
		nil,
	)

	require.NotNil(t, method)
	assert.Equal(t, DispatchBroadcast, method.DispatchPolicy())
	assert.True(t, method.IsBroadcastDispatch())
	assert.False(t, method.IsMaximumValueDispatch())
}

func TestMethodSettingsValidateDispatch(t *testing.T) {
	tests := []struct {
		name     string
		settings MethodSettings
		wantErr  bool
	}{
		{name: "default", settings: MethodSettings{}, wantErr: false},
		{name: "broadcast", settings: MethodSettings{Dispatch: DispatchBroadcast}, wantErr: false},
		{name: "maximum value", settings: MethodSettings{Dispatch: DispatchMaximumValue}, wantErr: false},
		{name: "unknown", settings: MethodSettings{Dispatch: DispatchPolicy("unknown")}, wantErr: true},
		{name: "local conflict", settings: MethodSettings{Dispatch: DispatchBroadcast, Local: true}, wantErr: true},
		{name: "subscription conflict", settings: MethodSettings{Dispatch: DispatchMaximumValue, Subscription: &Subscription{IsSubscribe: true}}, wantErr: true},
		{name: "send-sticky conflict", settings: MethodSettings{Dispatch: DispatchBroadcast, Sticky: &Sticky{SendSticky: true}}, wantErr: true},
		{name: "create-sticky conflict", settings: MethodSettings{Dispatch: DispatchMaximumValue, Sticky: &Sticky{CreateSticky: true}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.settings.validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEthJsonRpcDispatchPolicies(t *testing.T) {
	require.NoError(t, NewMethodSpecLoader().Load())

	spec := GetSpecMethodsByConnectors("eth", []ApiConnectorType{JsonRpcConnector})
	require.NotNil(t, spec)

	assert.Equal(t, DispatchMaximumValue, spec[DefaultMethodGroup]["eth_getTransactionCount"].DispatchPolicy())
	assert.Equal(t, DispatchBroadcast, spec[DefaultMethodGroup]["eth_sendRawTransaction"].DispatchPolicy())
}
