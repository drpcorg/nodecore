package stellar_validations_test

import (
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

const horizonRootBody = `{"horizon_version":"27.0.0-abc123",` +
	`"core_version":"stellar-core 27.1.0","ingest_latest_ledger":63563999,` +
	`"history_latest_ledger":63563999,"history_latest_ledger_closed_at":"2026-07-20T10:00:00Z",` +
	`"history_elder_ledger":63563520,"core_latest_ledger":63564000,` +
	`"network_passphrase":"Public Global Stellar Network ; September 2015"}`

func horizonHealthBody(db, coreUp, coreSynced bool) []byte {
	return []byte(`{"database_connected":` + btoa(db) +
		`,"core_up":` + btoa(coreUp) + `,"core_synced":` + btoa(coreSynced) + `}`)
}

func btoa(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func TestStellarHorizonChainValidatorValidOnMatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonRoot)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(horizonRootBody), 200, protocol.Rest))

	v := stellar_validations.NewStellarHorizonChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestStellarHorizonChainValidatorFatalOnMismatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// a mainnet horizon behind a config that says testnet
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonRoot)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(horizonRootBody), 200, protocol.Rest))

	v := stellar_validations.NewStellarHorizonChainValidator("id", conn, chains.GetChain("stellar-testnet"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestStellarHorizonChainValidatorFatalOnEmptyPassphrase(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonRoot)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"horizon_version":"27.0.0"}`), 200, protocol.Rest))

	v := stellar_validations.NewStellarHorizonChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestStellarHorizonChainValidatorSettingsErrorOnFetchFailure(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonRoot)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(503, "boom", nil)))

	v := stellar_validations.NewStellarHorizonChainValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestStellarHorizonSyncingValidatorAvailableWhenAllTrue(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonHealth)).
		Return(protocol.NewHttpUpstreamResponse("1", horizonHealthBody(true, true, true), 200, protocol.Rest))

	v := stellar_validations.NewStellarHorizonSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestStellarHorizonSyncingValidatorSyncingWhenCoreNotSynced(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonHealth)).
		Return(protocol.NewHttpUpstreamResponse("1", horizonHealthBody(true, true, false), 200, protocol.Rest))

	v := stellar_validations.NewStellarHorizonSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

// Horizon answers /health with 503 while unhealthy but still sends the
// booleans; parse them so "core still syncing" stays distinguishable from
// "horizon is down".
func TestStellarHorizonSyncingValidatorParsesThe503Body(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonHealth)).
		Return(protocol.NewHttpUpstreamResponse("1", horizonHealthBody(true, true, false), 503, protocol.Rest))

	v := stellar_validations.NewStellarHorizonSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

func TestStellarHorizonSyncingValidatorUnavailableWhenDatabaseDown(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonHealth)).
		Return(protocol.NewHttpUpstreamResponse("1", horizonHealthBody(false, true, true), 503, protocol.Rest))

	v := stellar_validations.NewStellarHorizonSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestStellarHorizonSyncingValidatorUnavailableOnUnparseableError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHorizonHealth)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`<html>502 Bad Gateway</html>`), 502, protocol.Rest))

	v := stellar_validations.NewStellarHorizonSyncingValidator("id", conn, chains.GetChain("stellar"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}
