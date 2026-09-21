package specific_helpers_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const stellarHealthJSON = `{"status":"healthy","latestLedger":63525714,` +
	`"oldestLedger":63404755,"latestLedgerCloseTime":"1784332881",` +
	`"oldestLedgerCloseTime":"1784200000","ledgerRetentionWindow":120960}`

const horizonRootJSON = `{"horizon_version":"27.0.0-abc123",` +
	`"core_version":"stellar-core 27.1.0","ingest_latest_ledger":63563999,` +
	`"history_latest_ledger":63563999,"history_latest_ledger_closed_at":"2026-07-20T10:00:00Z",` +
	`"history_elder_ledger":63563520,"core_latest_ledger":63564000,` +
	`"network_passphrase":"Public Global Stellar Network ; September 2015"}`

func matchStellarMethod(method string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == method
	}
}

func TestFetchStellarHealth(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchStellarMethod("getHealth"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(stellarHealthJSON), protocol.JsonRpc))

	health, err := specific_helpers.FetchStellarHealth(
		context.Background(), connector, chains.GetChain("stellar").Chain)
	require.NoError(t, err)

	assert.Equal(t, "healthy", health.Status)
	assert.Equal(t, uint64(63525714), health.LatestLedger)
	assert.Equal(t, uint64(63404755), health.OldestLedger)
	assert.Equal(t, uint64(120960), health.LedgerRetentionWindow)
	connector.AssertExpectations(t)
}

// An unhealthy stellar-rpc answers getHealth with a JSON-RPC error rather than a
// degraded result, and callers classify that error themselves - so it must come
// back verbatim.
func TestFetchStellarHealthSurfacesTheUpstreamError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(
			protocol.ResponseErrorWithData(-32603, "data stores are not initialized", nil)))

	_, err := specific_helpers.FetchStellarHealth(
		context.Background(), connector, chains.GetChain("stellar").Chain)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestParseStellarHealthRejectsEmptyAndUnparseableBodies(t *testing.T) {
	_, err := specific_helpers.ParseStellarHealth(nil)
	assert.ErrorIs(t, err, specific_helpers.ErrStellarEmptyHealth)

	_, err = specific_helpers.ParseStellarHealth([]byte(`not json`))
	assert.Error(t, err)
}

func TestFetchStellarHorizonRootParsesEveryConsumedField(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchStellarMethod("GET#/"))).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(horizonRootJSON), 200, protocol.Rest))

	root, err := specific_helpers.FetchStellarHorizonRoot(
		context.Background(), connector, chains.GetChain("stellar").Chain)
	require.NoError(t, err)

	assert.Equal(t, "27.0.0-abc123", root.HorizonVersion)
	assert.Equal(t, "Public Global Stellar Network ; September 2015", root.NetworkPassphrase)
	assert.Equal(t, uint64(63563999), root.HistoryLatestLedger)
	assert.Equal(t, uint64(63563520), root.HistoryElderLedger)
	assert.Equal(t, uint64(63564000), root.CoreLatestLedger)
	connector.AssertExpectations(t)
}

func TestParseStellarHorizonRootRejectsEmptyAndUnparseableBodies(t *testing.T) {
	_, err := specific_helpers.ParseStellarHorizonRoot(nil)
	assert.ErrorIs(t, err, specific_helpers.ErrStellarHorizonEmptyRoot)

	_, err = specific_helpers.ParseStellarHorizonRoot([]byte(`<html>`))
	assert.Error(t, err)
}

func TestFetchStellarHorizonHealth(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchStellarMethod("GET#/health"))).
		Return(protocol.NewHttpUpstreamResponse("1",
			[]byte(`{"database_connected":true,"core_up":true,"core_synced":true}`), 200, protocol.Rest))

	health, err := specific_helpers.FetchStellarHorizonHealth(
		context.Background(), connector, chains.GetChain("stellar").Chain)
	require.NoError(t, err)

	assert.True(t, health.DatabaseConnected)
	assert.True(t, health.CoreUp)
	assert.True(t, health.CoreSynced)
	connector.AssertExpectations(t)
}

// Horizon answers /health with 503 while unhealthy but still sends the booleans,
// so the error body is parsed rather than discarded - that is what keeps "core
// still syncing" distinguishable from "horizon is down".
func TestFetchStellarHorizonHealthParsesThe503Body(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponse("1",
			[]byte(`{"database_connected":true,"core_up":true,"core_synced":false}`), 503, protocol.Rest))

	health, err := specific_helpers.FetchStellarHorizonHealth(
		context.Background(), connector, chains.GetChain("stellar").Chain)
	require.NoError(t, err)

	assert.True(t, health.CoreUp)
	assert.False(t, health.CoreSynced)
}

func TestFetchStellarHorizonHealthSurfacesAnUnparseableError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`<html>502 Bad Gateway</html>`), 502, protocol.Rest))

	_, err := specific_helpers.FetchStellarHorizonHealth(
		context.Background(), connector, chains.GetChain("stellar").Chain)
	assert.Error(t, err)
}

// A rate-limited Horizon answers 429 with an RFC-7807 problem+json body. That
// body unmarshals into StellarHorizonHealth without error - every boolean
// absent, so every boolean false - which would report a node whose database is
// down and swallow the real cause. The transport error has to win instead.
func TestFetchStellarHorizonHealthDoesNotTreatA429AsAHealthDocument(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponse("1",
			[]byte(`{"type":"https://stellar.org/horizon-errors/rate_limit_exceeded",`+
				`"title":"Rate limit exceeded","status":429}`), 429, protocol.Rest))

	_, err := specific_helpers.FetchStellarHorizonHealth(
		context.Background(), connector, chains.GetChain("stellar").Chain)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate_limit_exceeded")
}

// A 503 whose body is not the health document (e.g. a proxy's problem+json) is
// also a transport error, not an all-false verdict.
func TestFetchStellarHorizonHealthRejectsANon503ShapedBodyOn503(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponse("1",
			[]byte(`{"type":"about:blank","title":"Service Unavailable","status":503}`), 503, protocol.Rest))

	_, err := specific_helpers.FetchStellarHorizonHealth(
		context.Background(), connector, chains.GetChain("stellar").Chain)
	assert.Error(t, err)
}

// A 200 that omits the booleans is not a health document either - without the
// presence check it would read as a maximally unhealthy node.
func TestParseStellarHorizonHealthRequiresAllThreeBooleans(t *testing.T) {
	_, err := specific_helpers.ParseStellarHorizonHealth([]byte(`{"database_connected":true,"core_up":true}`))
	assert.ErrorIs(t, err, specific_helpers.ErrStellarHorizonNotHealthDocument)

	_, err = specific_helpers.ParseStellarHorizonHealth([]byte(`{}`))
	assert.ErrorIs(t, err, specific_helpers.ErrStellarHorizonNotHealthDocument)

	health, err := specific_helpers.ParseStellarHorizonHealth(
		[]byte(`{"database_connected":true,"core_up":true,"core_synced":false}`))
	require.NoError(t, err)
	assert.True(t, health.DatabaseConnected)
	assert.False(t, health.CoreSynced)
}
