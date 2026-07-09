package beacon_validations_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/beacon_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func newValidator(connector *mocks.ConnectorMock) *beacon_validations.BeaconChainHealthValidator {
	return beacon_validations.NewBeaconChainHealthValidator(
		"id", connector, chains.GetChain("eth-beacon-chain").Chain, 5*time.Second,
	)
}

func syncingResponse(body string) *protocol.BaseUpstreamResponse {
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.Rest)
}

func expectSyncingCall(connector *mocks.ConnectorMock, resp protocol.ResponseHolder) {
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/eth/v1/node/syncing"
	})).Return(resp).Once()
}

func TestBeaconHealthAvailable(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectSyncingCall(connector, syncingResponse(`{"data":{"is_syncing":false,"el_offline":false}}`))

	assert.Equal(t, protocol.Available, newValidator(connector).Validate())
	connector.AssertExpectations(t)
}

func TestBeaconHealthSyncing(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectSyncingCall(connector, syncingResponse(`{"data":{"is_syncing":true,"el_offline":false}}`))

	assert.Equal(t, protocol.Syncing, newValidator(connector).Validate())
	connector.AssertExpectations(t)
}

func TestBeaconHealthElOfflineUnavailable(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectSyncingCall(connector, syncingResponse(`{"data":{"is_syncing":false,"el_offline":true}}`))

	assert.Equal(t, protocol.Unavailable, newValidator(connector).Validate())
	connector.AssertExpectations(t)
}

func TestBeaconHealthRequestErrorUnavailable(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectSyncingCall(connector, protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))

	assert.Equal(t, protocol.Unavailable, newValidator(connector).Validate())
	connector.AssertExpectations(t)
}

func TestBeaconHealthUnparseableUnavailable(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectSyncingCall(connector, syncingResponse(`not json`))

	assert.Equal(t, protocol.Unavailable, newValidator(connector).Validate())
	connector.AssertExpectations(t)
}
