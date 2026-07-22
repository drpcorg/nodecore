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

func isHorizonRoot(r protocol.RequestHolder) bool   { return r.Method() == "GET#/" }
func isHorizonHealth(r protocol.RequestHolder) bool { return r.Method() == "GET#/health" }

func horizonRootBody(passphrase string) []byte {
	return []byte(fmt.Sprintf(
		`{"horizon_version":"27.0.0-a5df6aaa4b2b5e21e5a9d3e77d0344e57197b1d6",`+
			`"core_version":"stellar-core 27.1.0 (bd64dbb0f508f21c8ed3374615d4c9e1a1e5cb9a)",`+
			`"ingest_latest_ledger":63562891,"history_latest_ledger":63562891,`+
			`"history_latest_ledger_closed_at":"2026-07-20T09:58:54Z",`+
			`"history_elder_ledger":57255121,"core_latest_ledger":63562891,`+
			`"network_passphrase":"%s","current_protocol_version":27,`+
			`"supported_protocol_version":27,"core_supported_protocol_version":27}`,
		passphrase,
	))
}

func horizonHealthBody(databaseConnected, coreUp, coreSynced bool) []byte {
	return []byte(fmt.Sprintf(
		`{"database_connected":%t,"core_up":%t,"core_synced":%t}`,
		databaseConnected, coreUp, coreSynced,
	))
}

func TestStellarHorizonChainValidatorValidOnMatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonRoot)).
		Return(protocol.NewHttpUpstreamResponse("1", horizonRootBody("Public Global Stellar Network ; September 2015"), 200, protocol.Rest))
	v := stellar_validations.NewStellarHorizonChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestStellarHorizonChainValidatorFatalOnTestnetPassphraseBehindMainnetConfig(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonRoot)).
		Return(protocol.NewHttpUpstreamResponse("1", horizonRootBody("Test SDF Network ; September 2015"), 200, protocol.Rest))
	v := stellar_validations.NewStellarHorizonChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestStellarHorizonChainValidatorRetriesOnEmptyPassphrase(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonRoot)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"horizon_version":"27.0.0"}`), 200, protocol.Rest))
	v := stellar_validations.NewStellarHorizonChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestStellarHorizonChainValidatorRetriesOnUnparseableBody(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonRoot)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"network_passphrase":`), 200, protocol.Rest))
	v := stellar_validations.NewStellarHorizonChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestStellarHorizonChainValidatorRetriesOnFetchError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonRoot)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := stellar_validations.NewStellarHorizonChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestStellarHorizonHealthAvailableOnAllTrue(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonHealth)).
		Return(protocol.NewHttpUpstreamResponse("1", horizonHealthBody(true, true, true), 200, protocol.Rest))
	v := stellar_validations.NewStellarHorizonHealthValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestStellarHorizonHealthSyncingOnCoreNotSynced(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonHealth)).
		Return(protocol.NewHttpUpstreamResponse("1", horizonHealthBody(true, true, false), 200, protocol.Rest))
	v := stellar_validations.NewStellarHorizonHealthValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

// A live syncing Horizon answers /health with HTTP 503 while the body still
// carries the booleans - the validator must read the body through the
// transport error and report Syncing, not Unavailable (observed on a real
// node during its initial ingest).
func TestStellarHorizonHealthSyncingOnCoreNotSyncedWith503(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonHealth)).
		Return(protocol.NewHttpUpstreamResponse("1", horizonHealthBody(true, true, false), 503, protocol.Rest))
	v := stellar_validations.NewStellarHorizonHealthValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

func TestStellarHorizonHealthUnavailableOnDatabaseDisconnected(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonHealth)).
		Return(protocol.NewHttpUpstreamResponse("1", horizonHealthBody(false, true, true), 200, protocol.Rest))
	v := stellar_validations.NewStellarHorizonHealthValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestStellarHorizonHealthUnavailableOnCoreDown(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonHealth)).
		Return(protocol.NewHttpUpstreamResponse("1", horizonHealthBody(true, false, false), 200, protocol.Rest))
	v := stellar_validations.NewStellarHorizonHealthValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestStellarHorizonHealthUnavailableOnUnparseableBody(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonHealth)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"database_connected":`), 200, protocol.Rest))
	v := stellar_validations.NewStellarHorizonHealthValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestStellarHorizonHealthUnavailableOnFetchError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonHealth)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := stellar_validations.NewStellarHorizonHealthValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}
