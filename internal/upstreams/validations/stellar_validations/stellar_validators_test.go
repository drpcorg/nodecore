package stellar_validations_test

import (
	"fmt"
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

const (
	mainnetPassphrase = "Public Global Stellar Network ; September 2015"
	testnetPassphrase = "Test SDF Network ; September 2015"
)

func isGetNetwork(r protocol.RequestHolder) bool { return r.Method() == "getNetwork" }
func isGetHealth(r protocol.RequestHolder) bool  { return r.Method() == "getHealth" }

func networkBody(passphrase string) []byte {
	return []byte(fmt.Sprintf(`{"passphrase":"%s"}`, passphrase))
}

func healthyBody() []byte {
	return []byte(`{"status":"healthy","latestLedger":63525714,"latestLedgerCloseTime":"1784332881","oldestLedger":63404755,"oldestLedgerCloseTime":"1784332000","ledgerRetentionWindow":120960}`)
}

func TestStellarChainValidatorFatalOnEmptyPassphrase(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetNetwork)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", networkBody(""), protocol.JsonRpc))
	v := stellar_validations.NewStellarChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestStellarChainValidatorValidOnMatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetNetwork)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", networkBody(mainnetPassphrase), protocol.JsonRpc))
	v := stellar_validations.NewStellarChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestStellarChainValidatorFatalOnMismatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// a testnet node behind a config that says mainnet
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetNetwork)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", networkBody(testnetPassphrase), protocol.JsonRpc))
	v := stellar_validations.NewStellarChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestStellarChainValidatorRetriesOnFetchError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetNetwork)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := stellar_validations.NewStellarChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestStellarHealthAvailable(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", healthyBody(), protocol.JsonRpc))
	v := stellar_validations.NewStellarSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestStellarHealthSyncingWhileBootstrapping(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewHttpUpstreamResponseWithError(
			protocol.ResponseErrorWithData(-32603, "data stores are not initialized, try again later", nil)))
	v := stellar_validations.NewStellarSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

func TestStellarHealthUnavailableOnStaleness(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// the node polices its own head staleness
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewHttpUpstreamResponseWithError(
			protocol.ResponseErrorWithData(-32603, "latency (45s) since last known ledger closed is too high (>30s)", nil)))
	v := stellar_validations.NewStellarSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestStellarHealthUnavailableOnTransportError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(500, "boom", nil)))
	v := stellar_validations.NewStellarSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestStellarHealthUnavailableOnNonHealthyStatus(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isGetHealth)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"status":"degraded","latestLedger":63525714}`), protocol.JsonRpc))
	v := stellar_validations.NewStellarSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}
