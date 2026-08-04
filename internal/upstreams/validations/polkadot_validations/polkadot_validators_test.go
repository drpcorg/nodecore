package polkadot_validations_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/polkadot_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func healthResponse(body string) protocol.ResponseHolder {
	return protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.JsonRpc)
}

func newHealthValidator(
	connector *mocks.ConnectorMock,
	validateSyncing, validatePeers bool,
	minPeers int64,
) *polkadot_validations.PolkadotHealthValidator {
	return polkadot_validations.NewPolkadotHealthValidator(
		"id",
		connector,
		chains.GetChain("polkadot").Chain,
		5*time.Second,
		validateSyncing,
		validatePeers,
		minPeers,
	)
}

func TestPolkadotHealthValidator(t *testing.T) {
	tests := []struct {
		name            string
		body            string
		validateSyncing bool
		validatePeers   bool
		minPeers        int64
		expected        protocol.AvailabilityStatus
	}{
		{
			name:            "healthy",
			body:            `{"peers":42,"isSyncing":false,"shouldHavePeers":true}`,
			validateSyncing: true,
			validatePeers:   true,
			minPeers:        3,
			expected:        protocol.Available,
		},
		{
			name:            "syncing",
			body:            `{"peers":42,"isSyncing":true,"shouldHavePeers":true}`,
			validateSyncing: true,
			validatePeers:   true,
			minPeers:        3,
			expected:        protocol.Syncing,
		},
		{
			name:            "syncing ignored when the check is off",
			body:            `{"peers":42,"isSyncing":true,"shouldHavePeers":true}`,
			validateSyncing: false,
			validatePeers:   true,
			minPeers:        3,
			expected:        protocol.Available,
		},
		{
			name:            "too few peers",
			body:            `{"peers":1,"isSyncing":false,"shouldHavePeers":true}`,
			validateSyncing: true,
			validatePeers:   true,
			minPeers:        3,
			expected:        protocol.Immature,
		},
		{
			// A node that reports shouldHavePeers=false is intentionally isolated
			// (light client, --dev), so zero peers is not a fault.
			name:            "no peers but node should not have peers",
			body:            `{"peers":0,"isSyncing":false,"shouldHavePeers":false}`,
			validateSyncing: true,
			validatePeers:   true,
			minPeers:        3,
			expected:        protocol.Available,
		},
		{
			name:            "too few peers ignored when the check is off",
			body:            `{"peers":0,"isSyncing":false,"shouldHavePeers":true}`,
			validateSyncing: true,
			validatePeers:   false,
			minPeers:        3,
			expected:        protocol.Available,
		},
		{
			name:            "syncing wins over peers",
			body:            `{"peers":0,"isSyncing":true,"shouldHavePeers":true}`,
			validateSyncing: true,
			validatePeers:   true,
			minPeers:        3,
			expected:        protocol.Syncing,
		},
		{
			name:            "malformed payload",
			body:            `not json`,
			validateSyncing: true,
			validatePeers:   true,
			minPeers:        3,
			expected:        protocol.Unavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			connector.On("SendRequest", mock.Anything, mock.Anything).Return(healthResponse(tt.body))

			assert.Equal(t, tt.expected, newHealthValidator(connector, tt.validateSyncing, tt.validatePeers, tt.minPeers).Validate())
		})
	}
}

func TestPolkadotHealthValidatorUpstreamError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))

	assert.Equal(t, protocol.Unavailable, newHealthValidator(connector, true, true, 3).Validate())
}

// system_health carries both signals, so one call must serve both arms.
func TestPolkadotHealthValidatorIssuesOneCall(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		return req.Method() == "system_health"
	})).Return(healthResponse(`{"peers":42,"isSyncing":false,"shouldHavePeers":true}`)).Once()

	assert.Equal(t, protocol.Available, newHealthValidator(connector, true, true, 3).Validate())
	connector.AssertExpectations(t)
}

func TestPolkadotChainValidator(t *testing.T) {
	tests := []struct {
		name     string
		chain    string
		body     string
		expected validations.ValidationSettingResult
	}{
		{"match", "polkadot", `"Polkadot"`, validations.Valid},
		{"match ignoring case", "kusama", `"kusama"`, validations.Valid},
		{"match with a space", "vara", `"Vara Network"`, validations.Valid},
		{"mismatch is fatal", "polkadot", `"Kusama"`, validations.FatalSettingError},
		{"empty chain name is fatal", "polkadot", `""`, validations.FatalSettingError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
				return req.Method() == "system_chain"
			})).Return(healthResponse(tt.body))

			validator := polkadot_validations.NewPolkadotChainValidator(
				"id", connector, chains.GetChain(tt.chain), 5*time.Second,
			)
			assert.Equal(t, tt.expected, validator.Validate())
		})
	}
}

// A transport failure says nothing about which network the node is on, so it is
// a retryable SettingsError rather than a fatal one.
func TestPolkadotChainValidatorUpstreamErrorIsNotFatal(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))

	validator := polkadot_validations.NewPolkadotChainValidator(
		"id", connector, chains.GetChain("polkadot"), 5*time.Second,
	)
	assert.Equal(t, validations.SettingsError, validator.Validate())
}
