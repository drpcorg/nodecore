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

func beaconChain() chains.Chain {
	return chains.GetChain("eth-beacon-chain").Chain
}

func restOk(body string) protocol.ResponseHolder {
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.Rest)
}

func expectRestCall(connector *mocks.ConnectorMock, method string, resp protocol.ResponseHolder) {
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == method
	})).Return(resp).Once()
}

// --- health ---

func TestBeaconHealthAvailable(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectRestCall(connector, "GET#/eth/v1/node/health", restOk(""))

	validator := beacon_validations.NewBeaconChainHealthValidator("id", connector, beaconChain(), 5*time.Second)
	assert.Equal(t, protocol.Available, validator.Validate())
	connector.AssertExpectations(t)
}

func TestBeaconHealthUnavailableOn503(t *testing.T) {
	connector := mocks.NewConnectorMock()
	// A 503 (not initialized) is surfaced by the REST connector as an error.
	expectRestCall(connector, "GET#/eth/v1/node/health", protocol.NewHttpUpstreamResponse("1", []byte(""), 503, protocol.Rest))

	validator := beacon_validations.NewBeaconChainHealthValidator("id", connector, beaconChain(), 5*time.Second)
	assert.Equal(t, protocol.Unavailable, validator.Validate())
	connector.AssertExpectations(t)
}

// --- syncing ---

func newSyncing(connector *mocks.ConnectorMock) *beacon_validations.BeaconChainSyncingValidator {
	return beacon_validations.NewBeaconChainSyncingValidator("id", connector, beaconChain(), 5*time.Second)
}

func TestBeaconSyncingAvailable(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectRestCall(connector, "GET#/eth/v1/node/syncing", restOk(`{"data":{"is_syncing":false,"el_offline":false}}`))

	assert.Equal(t, protocol.Available, newSyncing(connector).Validate())
	connector.AssertExpectations(t)
}

func TestBeaconSyncingSyncing(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectRestCall(connector, "GET#/eth/v1/node/syncing", restOk(`{"data":{"is_syncing":true,"el_offline":false}}`))

	assert.Equal(t, protocol.Syncing, newSyncing(connector).Validate())
	connector.AssertExpectations(t)
}

func TestBeaconSyncingElOfflineUnavailable(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectRestCall(connector, "GET#/eth/v1/node/syncing", restOk(`{"data":{"is_syncing":false,"el_offline":true}}`))

	assert.Equal(t, protocol.Unavailable, newSyncing(connector).Validate())
	connector.AssertExpectations(t)
}

func TestBeaconSyncingRequestErrorUnavailable(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectRestCall(connector, "GET#/eth/v1/node/syncing", protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))

	assert.Equal(t, protocol.Unavailable, newSyncing(connector).Validate())
	connector.AssertExpectations(t)
}

// --- peers ---

func newPeers(connector *mocks.ConnectorMock, minPeers int64) *beacon_validations.BeaconChainPeersValidator {
	return beacon_validations.NewBeaconChainPeersValidator("id", connector, beaconChain(), minPeers, 5*time.Second)
}

func TestBeaconPeersAvailable(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectRestCall(connector, "GET#/eth/v1/node/peer_count", restOk(`{"data":{"connected":"64","disconnected":"3"}}`))

	assert.Equal(t, protocol.Available, newPeers(connector, 8).Validate())
	connector.AssertExpectations(t)
}

func TestBeaconPeersImmatureBelowMin(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectRestCall(connector, "GET#/eth/v1/node/peer_count", restOk(`{"data":{"connected":"3"}}`))

	assert.Equal(t, protocol.Immature, newPeers(connector, 8).Validate())
	connector.AssertExpectations(t)
}

func TestBeaconPeersUnparseableUnavailable(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectRestCall(connector, "GET#/eth/v1/node/peer_count", restOk(`{"data":{"connected":"abc"}}`))

	assert.Equal(t, protocol.Unavailable, newPeers(connector, 8).Validate())
	connector.AssertExpectations(t)
}
