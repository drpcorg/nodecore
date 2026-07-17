package near_validations_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/near_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func isStatus(r protocol.RequestHolder) bool      { return r.Method() == "status" }
func isNetworkInfo(r protocol.RequestHolder) bool { return r.Method() == "network_info" }

func statusBody(chainId string, latestBlockTime string, syncing bool) []byte {
	return []byte(fmt.Sprintf(
		`{"chain_id":"%s","version":{"version":"2.13.1"},"sync_info":{"latest_block_height":207365026,"latest_block_time":"%s","earliest_block_height":207157933,"syncing":%t}}`,
		chainId, latestBlockTime, syncing,
	))
}

func freshBlockTime() string {
	return time.Now().Add(-time.Second).Format(time.RFC3339Nano)
}

func TestNearChainValidatorValidOnMatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isStatus)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", statusBody("mainnet", freshBlockTime(), false), protocol.JsonRpc))
	v := near_validations.NewNearChainValidator("id", conn, chains.GetChain("near"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestNearChainValidatorFatalOnMismatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// a testnet node behind a config that says mainnet
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isStatus)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", statusBody("testnet", freshBlockTime(), false), protocol.JsonRpc))
	v := near_validations.NewNearChainValidator("id", conn, chains.GetChain("near"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestNearChainValidatorRetriesOnFetchError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isStatus)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := near_validations.NewNearChainValidator("id", conn, chains.GetChain("near"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestNearHealthAvailable(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isStatus)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", statusBody("mainnet", freshBlockTime(), false), protocol.JsonRpc))
	v := near_validations.NewNearHealthValidator("id", conn, chains.GetChain("near"), time.Second, false)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestNearHealthSyncingOnSyncFlag(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isStatus)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", statusBody("mainnet", freshBlockTime(), true), protocol.JsonRpc))
	v := near_validations.NewNearHealthValidator("id", conn, chains.GetChain("near"), time.Second, false)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

func TestNearHealthSyncingOnStaleHead(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// near's stale-head threshold is 1s * 40; the head is 5 minutes old
	staleTime := time.Now().Add(-5 * time.Minute).Format(time.RFC3339Nano)
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isStatus)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", statusBody("mainnet", staleTime, false), protocol.JsonRpc))
	v := near_validations.NewNearHealthValidator("id", conn, chains.GetChain("near"), time.Second, false)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

func TestNearHealthUnavailableOnZeroPeers(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isStatus)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", statusBody("mainnet", freshBlockTime(), false), protocol.JsonRpc))
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isNetworkInfo)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"num_active_peers":0,"peer_max_count":40}`), protocol.JsonRpc))
	v := near_validations.NewNearHealthValidator("id", conn, chains.GetChain("near"), time.Second, true)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestNearHealthAvailableWithPeers(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isStatus)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", statusBody("mainnet", freshBlockTime(), false), protocol.JsonRpc))
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isNetworkInfo)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"num_active_peers":36,"peer_max_count":40}`), protocol.JsonRpc))
	v := near_validations.NewNearHealthValidator("id", conn, chains.GetChain("near"), time.Second, true)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestNearHealthIgnoresPeersWhenDisabled(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isStatus)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", statusBody("mainnet", freshBlockTime(), false), protocol.JsonRpc))
	v := near_validations.NewNearHealthValidator("id", conn, chains.GetChain("near"), time.Second, false)
	assert.Equal(t, protocol.Available, v.Validate())
	conn.AssertNotCalled(t, "SendRequest", mock.Anything, mock.MatchedBy(isNetworkInfo))
}

func TestNearHealthUnavailableOnError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isStatus)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := near_validations.NewNearHealthValidator("id", conn, chains.GetChain("near"), time.Second, false)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}
