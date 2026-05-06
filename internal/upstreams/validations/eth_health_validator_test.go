package validations_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestEthPeersValidatorReturnsUnavailableOnConnectorError(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "net_peerCount",
		protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()),
	)
	validator := validations.NewEthPeersValidator("upstream-1", chains.ETHEREUM, connector, &chains.Options{
		InternalTimeout: time.Second,
		MinPeers:        1,
	})

	status := validator.Validate()

	assert.Equal(t, protocol.Unavailable, status)
	connector.AssertExpectations(t)
}

func TestEthPeersValidatorReturnsUnavailableOnInvalidJSON(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "net_peerCount",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{`), protocol.JsonRpc),
	)
	validator := validations.NewEthPeersValidator("upstream-1", chains.ETHEREUM, connector, &chains.Options{
		InternalTimeout: time.Second,
		MinPeers:        1,
	})

	status := validator.Validate()

	assert.Equal(t, protocol.Unavailable, status)
	connector.AssertExpectations(t)
}

func TestEthPeersValidatorReturnsUnavailableOnInvalidPeerCount(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "net_peerCount",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"not-a-number"`), protocol.JsonRpc),
	)
	validator := validations.NewEthPeersValidator("upstream-1", chains.ETHEREUM, connector, &chains.Options{
		InternalTimeout: time.Second,
		MinPeers:        1,
	})

	status := validator.Validate()

	assert.Equal(t, protocol.Unavailable, status)
	connector.AssertExpectations(t)
}

func TestEthPeersValidatorReturnsImmatureWhenBelowMinPeers(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "net_peerCount",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc),
	)
	validator := validations.NewEthPeersValidator("upstream-1", chains.ETHEREUM, connector, &chains.Options{
		InternalTimeout: time.Second,
		MinPeers:        2,
	})

	status := validator.Validate()

	assert.Equal(t, protocol.Immature, status)
	connector.AssertExpectations(t)
}

func TestEthPeersValidatorReturnsAvailableWhenPeerCountIsEqualOrGreaterThanMinPeers(t *testing.T) {
	tests := []struct {
		name     string
		response []byte
		minPeers int64
	}{
		{
			name:     "equal to min peers",
			response: []byte(`"0x2"`),
			minPeers: 2,
		},
		{
			name:     "greater than min peers",
			response: []byte(`"0x3"`),
			minPeers: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connector := newEthHealthConnectorMock(t, "net_peerCount",
				protocol.NewSimpleHttpUpstreamResponse("1", test.response, protocol.JsonRpc),
			)
			validator := validations.NewEthPeersValidator("upstream-1", chains.ETHEREUM, connector, &chains.Options{
				InternalTimeout: time.Second,
				MinPeers:        test.minPeers,
			})

			status := validator.Validate()

			assert.Equal(t, protocol.Available, status)
			connector.AssertExpectations(t)
		})
	}
}

func TestEthSyncingValidatorReturnsUnavailableOnConnectorError(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "eth_syncing",
		protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()),
	)
	validator := validations.NewEthSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Unavailable, status)
	connector.AssertExpectations(t)
}

func TestEthSyncingValidatorReturnsUnavailableOnInvalidJSON(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "eth_syncing",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{`), protocol.JsonRpc),
	)
	validator := validations.NewEthSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Unavailable, status)
	connector.AssertExpectations(t)
}

func TestEthSyncingValidatorReturnsSyncingForBooleanTrue(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "eth_syncing",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`true`), protocol.JsonRpc),
	)
	validator := validations.NewEthSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Syncing, status)
	connector.AssertExpectations(t)
}

func TestEthSyncingValidatorReturnsAvailableForBooleanFalse(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "eth_syncing",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`false`), protocol.JsonRpc),
	)
	validator := validations.NewEthSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Available, status)
	connector.AssertExpectations(t)
}

func TestEthSyncingValidatorReturnsSyncingWhenLagExceedsThreshold(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "eth_syncing",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"currentBlock":"0x64","highestBlock":"0x6a"}`), protocol.JsonRpc),
	)
	validator := validations.NewEthSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Syncing, status)
	connector.AssertExpectations(t)
}

func TestEthSyncingValidatorReturnsAvailableWhenLagEqualsThreshold(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "eth_syncing",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"currentBlock":"0x64","highestBlock":"0x69"}`), protocol.JsonRpc),
	)
	validator := validations.NewEthSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Available, status)
	connector.AssertExpectations(t)
}

func TestEthSyncingValidatorReturnsAvailableOnInvalidHexLagPayload(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "eth_syncing",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"currentBlock":"oops","highestBlock":"0x69"}`), protocol.JsonRpc),
	)
	validator := validations.NewEthSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Available, status)
	connector.AssertExpectations(t)
}

func TestEthSyncingValidatorReturnsSyncingForOpNodeStylePayload(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "eth_syncing",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"batchProcessed":"0x1","batchSeen":"0x2","syncTargetMsgCount":"0x3"}`), protocol.JsonRpc),
	)
	validator := validations.NewEthSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Syncing, status)
	connector.AssertExpectations(t)
}

func TestEthSyncingValidatorReturnsAvailableWhenStructuredPayloadHasNoKnownSyncFields(t *testing.T) {
	connector := newEthHealthConnectorMock(t, "eth_syncing",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"status":"ok"}`), protocol.JsonRpc),
	)
	validator := validations.NewEthSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	status := validator.Validate()

	assert.Equal(t, protocol.Available, status)
	connector.AssertExpectations(t)
}

func newEthHealthConnectorMock(t *testing.T, method string, response protocol.ResponseHolder) *mocks.ConnectorMock {
	t.Helper()

	request := test_utils.NewUpstreamRequest(t, method, nil)
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(response).
		Once()

	return connector
}

func testConfiguredChain(syncingLag int64) *chains.ConfiguredChain {
	return &chains.ConfiguredChain{
		Chain: chains.ETHEREUM,
		Settings: chains.Settings{
			Lags: chains.LagConfig{
				Syncing: syncingLag,
			},
		},
	}
}
