package bitcoin_validations_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/bitcoin_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const (
	bitcoinGenesis  = `"000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f"`
	dogecoinGenesis = `"1a91e3dace36e2be3bf030a65679fe821aa1d6ef92e7c9902eb318182c355691"`
)

func isGetBlockHash(r protocol.RequestHolder) bool       { return r.Method() == "getblockhash" }
func isGetBlockchainInfo(r protocol.RequestHolder) bool  { return r.Method() == "getblockchaininfo" }
func isGetConnectionCount(r protocol.RequestHolder) bool { return r.Method() == "getconnectioncount" }

func blockchainInfoBody(blocks, headers uint64, ibd bool) []byte {
	return []byte(fmt.Sprintf(
		`{"blocks":%d,"headers":%d,"initialblockdownload":%t,"pruned":false}`,
		blocks, headers, ibd,
	))
}

func TestBitcoinChainValidatorValidOnMatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetBlockHash)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(bitcoinGenesis), protocol.JsonRpc))
	v := bitcoin_validations.NewBitcoinChainValidator("id", conn, chains.GetChain("bitcoin"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestBitcoinChainValidatorFatalOnMismatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// a dogecoin node behind a config that says bitcoin
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetBlockHash)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(dogecoinGenesis), protocol.JsonRpc))
	v := bitcoin_validations.NewBitcoinChainValidator("id", conn, chains.GetChain("bitcoin"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestBitcoinChainValidatorSkipsUnmappedChain(t *testing.T) {
	conn := mocks.NewConnectorMock()
	v := bitcoin_validations.NewBitcoinChainValidator("id", conn, chains.GetChain("litecoin"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
	conn.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

func TestBitcoinChainValidatorRetriesOnFetchError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetBlockHash)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := bitcoin_validations.NewBitcoinChainValidator("id", conn, chains.GetChain("bitcoin"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestBitcoinHealthAvailable(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetBlockchainInfo)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", blockchainInfoBody(100, 100, false), protocol.JsonRpc))
	v := bitcoin_validations.NewBitcoinHealthValidator("id", conn, chains.GetChain("bitcoin"), time.Second, false)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestBitcoinHealthSyncingOnInitialBlockDownload(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetBlockchainInfo)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", blockchainInfoBody(100, 100, true), protocol.JsonRpc))
	v := bitcoin_validations.NewBitcoinHealthValidator("id", conn, chains.GetChain("bitcoin"), time.Second, false)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

func TestBitcoinHealthSyncingOnHeadersLag(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// bitcoin's syncing lag is 3; headers are 10 blocks ahead
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetBlockchainInfo)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", blockchainInfoBody(100, 110, false), protocol.JsonRpc))
	v := bitcoin_validations.NewBitcoinHealthValidator("id", conn, chains.GetChain("bitcoin"), time.Second, false)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

func TestBitcoinHealthUnavailableOnZeroPeers(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetBlockchainInfo)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", blockchainInfoBody(100, 100, false), protocol.JsonRpc))
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetConnectionCount)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`0`), protocol.JsonRpc))
	v := bitcoin_validations.NewBitcoinHealthValidator("id", conn, chains.GetChain("bitcoin"), time.Second, true)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestBitcoinHealthAvailableWithPeers(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetBlockchainInfo)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", blockchainInfoBody(100, 100, false), protocol.JsonRpc))
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetConnectionCount)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`8`), protocol.JsonRpc))
	v := bitcoin_validations.NewBitcoinHealthValidator("id", conn, chains.GetChain("bitcoin"), time.Second, true)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestBitcoinHealthIgnoresPeersWhenDisabled(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetBlockchainInfo)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", blockchainInfoBody(100, 100, false), protocol.JsonRpc))
	v := bitcoin_validations.NewBitcoinHealthValidator("id", conn, chains.GetChain("bitcoin"), time.Second, false)
	assert.Equal(t, protocol.Available, v.Validate())
	conn.AssertNotCalled(t, "SendRequest", mock.Anything, mock.MatchedBy(isGetConnectionCount))
}

func TestBitcoinHealthUnavailableOnError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetBlockchainInfo)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := bitcoin_validations.NewBitcoinHealthValidator("id", conn, chains.GetChain("bitcoin"), time.Second, false)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}
