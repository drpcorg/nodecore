package stellar_validations_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/stellar_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func isGetNetwork(r protocol.RequestHolder) bool { return r.Method() == "getNetwork" }
func isGetHealth(r protocol.RequestHolder) bool  { return r.Method() == "getHealth" }

func healthyBody(status string, latest, oldest uint64) []byte {
	return []byte(`{"status":"` + status + `","latestLedger":` + itoa(latest) +
		`,"oldestLedger":` + itoa(oldest) + `,"latestLedgerCloseTime":"1784332881",` +
		`"oldestLedgerCloseTime":"1784200000","ledgerRetentionWindow":120960}`)
}

func itoa(v uint64) string {
	return strconv.FormatUint(v, 10)
}

func TestStellarChainValidatorValidOnPassphraseMatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetNetwork)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1",
			[]byte(`{"passphrase":"Public Global Stellar Network ; September 2015","protocolVersion":27}`),
			protocol.JsonRpc))

	v := stellar_validations.NewStellarChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

// The registry lowercases every chain-id, so the compare has to be case-insensitive.
func TestStellarChainValidatorValidOnTestnetPassphrase(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetNetwork)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1",
			[]byte(`{"passphrase":"Test SDF Network ; September 2015"}`), protocol.JsonRpc))

	v := stellar_validations.NewStellarChainValidator("id", conn, chains.GetChain("stellar-testnet"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestStellarChainValidatorFatalOnMismatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// a testnet node behind a config that says mainnet
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetNetwork)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1",
			[]byte(`{"passphrase":"Test SDF Network ; September 2015"}`), protocol.JsonRpc))

	v := stellar_validations.NewStellarChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestStellarChainValidatorFatalOnEmptyPassphrase(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetNetwork)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"protocolVersion":27}`), protocol.JsonRpc))

	v := stellar_validations.NewStellarChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestStellarChainValidatorSettingsErrorOnFetchFailure(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetNetwork)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(-32603, "boom", nil)))

	v := stellar_validations.NewStellarChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestStellarSyncingValidatorAvailableOnHealthy(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", healthyBody("healthy", 63525714, 63404755), protocol.JsonRpc))

	v := stellar_validations.NewStellarSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Available, v.Validate())
}

// While its data stores bootstrap, stellar-rpc rejects getHealth with -32603
// "data stores are not initialized..." - that is a syncing node, not a dead one.
func TestStellarSyncingValidatorSyncingWhileDataStoresNotInitialized(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewHttpUpstreamResponseWithError(
			protocol.ResponseErrorWithData(-32603, "data stores are not initialized", nil)))

	v := stellar_validations.NewStellarSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

// The node polices its own staleness and rejects getHealth once the head is
// older than 30s. That is an unusable upstream, not a syncing one.
func TestStellarSyncingValidatorUnavailableOnStalenessRejection(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(
			-32603, "latency (42s) since last known ledger closed is too high (>30s)", nil)))

	v := stellar_validations.NewStellarSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestStellarSyncingValidatorUnavailableOnNonHealthyStatus(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", healthyBody("degraded", 1, 1), protocol.JsonRpc))

	v := stellar_validations.NewStellarSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestStellarSyncingValidatorUnavailableOnUnparseableBody(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`not json`), protocol.JsonRpc))

	v := stellar_validations.NewStellarSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}
