package ripple_validations_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/ripple_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func isServerState(r protocol.RequestHolder) bool { return r.Method() == "server_state" }

// real rippled shape: the payload lives under result.state; network_id and
// amendment_blocked are optional fields injected via extra
func serverStateBody(serverState string, peers int, extra string) []byte {
	return []byte(fmt.Sprintf(
		`{"state":{"server_state":"%s","complete_ledgers":"105660494-105662737","build_version":"3.2.0","peers":%d,"validated_ledger":{"seq":105662737,"hash":"5C48A7D2107534E19C7A741AD8B9C3C6D6C36F73641AB6D33B4C4E45E4C8FEDE"}%s},"status":"success"}`,
		serverState, peers, extra,
	))
}

func serverStateConn(body []byte) *mocks.ConnectorMock {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isServerState)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", body, protocol.JsonRpc))
	return conn
}

func TestRippleChainValidatorValidOnMainnetWithAbsentNetworkId(t *testing.T) {
	// mainnet omits network_id entirely; absent means 0
	conn := serverStateConn(serverStateBody("full", 23, ""))
	v := ripple_validations.NewRippleChainValidator("id", conn, chains.GetChain("ripple"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestRippleChainValidatorValidOnTestnetMatch(t *testing.T) {
	conn := serverStateConn(serverStateBody("full", 23, `,"network_id":1`))
	v := ripple_validations.NewRippleChainValidator("id", conn, chains.GetChain("ripple-testnet"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestRippleChainValidatorFatalOnMismatch(t *testing.T) {
	// a testnet node behind a config that says mainnet
	conn := serverStateConn(serverStateBody("full", 23, `,"network_id":1`))
	v := ripple_validations.NewRippleChainValidator("id", conn, chains.GetChain("ripple"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestRippleChainValidatorRetriesOnFetchError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isServerState)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := ripple_validations.NewRippleChainValidator("id", conn, chains.GetChain("ripple"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestRippleHealthByServerState(t *testing.T) {
	cases := []struct {
		serverState string
		expected    protocol.AvailabilityStatus
	}{
		{"full", protocol.Available},
		{"tracking", protocol.Available},
		{"validating", protocol.Available},
		{"proposing", protocol.Available},
		{"connected", protocol.Syncing},
		{"syncing", protocol.Syncing},
		{"disconnected", protocol.Unavailable},
		{"", protocol.Unavailable},
	}
	for _, c := range cases {
		t.Run(c.serverState, func(t *testing.T) {
			conn := serverStateConn(serverStateBody(c.serverState, 23, ""))
			v := ripple_validations.NewRippleHealthValidator("id", conn, chains.GetChain("ripple"), time.Second, false)
			assert.Equal(t, c.expected, v.Validate())
		})
	}
}

func TestRippleHealthUnavailableOnAmendmentBlocked(t *testing.T) {
	// amendment_blocked wins even over server_state 'full'
	conn := serverStateConn(serverStateBody("full", 23, `,"amendment_blocked":true`))
	v := ripple_validations.NewRippleHealthValidator("id", conn, chains.GetChain("ripple"), time.Second, false)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestRippleHealthUnavailableOnZeroPeers(t *testing.T) {
	conn := serverStateConn(serverStateBody("full", 0, ""))
	v := ripple_validations.NewRippleHealthValidator("id", conn, chains.GetChain("ripple"), time.Second, true)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestRippleHealthIgnoresPeersWhenDisabled(t *testing.T) {
	conn := serverStateConn(serverStateBody("full", 0, ""))
	v := ripple_validations.NewRippleHealthValidator("id", conn, chains.GetChain("ripple"), time.Second, false)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestRippleHealthUnavailableOnError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isServerState)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := ripple_validations.NewRippleHealthValidator("id", conn, chains.GetChain("ripple"), time.Second, false)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}
