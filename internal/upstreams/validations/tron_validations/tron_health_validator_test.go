package tron_validations_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/tron_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// ---------- TronPeersValidator ----------

func TestTronPeersValidatorReturnsUnavailableOnConnectorError(t *testing.T) {
	connector := newTronRestConnectorMock(t, "POST", "/wallet/getnodeinfo",
		protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()),
	)
	validator := tron_validations.NewTronPeersValidator("upstream-1", chains.TRON, connector, &chains.Options{
		InternalTimeout: time.Second,
		MinPeers:        1,
	})

	assert.Equal(t, protocol.Unavailable, validator.Validate())
	connector.AssertExpectations(t)
}

func TestTronPeersValidatorReturnsUnavailableOnInvalidJSON(t *testing.T) {
	connector := newTronRestConnectorMock(t, "POST", "/wallet/getnodeinfo",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{`), protocol.Rest),
	)
	validator := tron_validations.NewTronPeersValidator("upstream-1", chains.TRON, connector, &chains.Options{
		InternalTimeout: time.Second,
		MinPeers:        1,
	})

	assert.Equal(t, protocol.Unavailable, validator.Validate())
	connector.AssertExpectations(t)
}

func TestTronPeersValidatorReturnsImmatureWhenPeerListMissing(t *testing.T) {
	connector := newTronRestConnectorMock(t, "POST", "/wallet/getnodeinfo",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"otherField":42}`), protocol.Rest),
	)
	validator := tron_validations.NewTronPeersValidator("upstream-1", chains.TRON, connector, &chains.Options{
		InternalTimeout: time.Second,
		MinPeers:        1,
	})

	assert.Equal(t, protocol.Immature, validator.Validate())
	connector.AssertExpectations(t)
}

func TestTronPeersValidatorReturnsImmatureWhenBelowMinPeers(t *testing.T) {
	connector := newTronRestConnectorMock(t, "POST", "/wallet/getnodeinfo",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"peerList":[{"host":"a"}]}`), protocol.Rest),
	)
	validator := tron_validations.NewTronPeersValidator("upstream-1", chains.TRON, connector, &chains.Options{
		InternalTimeout: time.Second,
		MinPeers:        3,
	})

	assert.Equal(t, protocol.Immature, validator.Validate())
	connector.AssertExpectations(t)
}

func TestTronPeersValidatorReturnsAvailableWhenPeerCountMeetsOrExceedsMinPeers(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		minPeers int64
	}{
		{name: "equal to min peers", body: `{"peerList":[{"host":"a"},{"host":"b"}]}`, minPeers: 2},
		{name: "greater than min peers", body: `{"peerList":[{"host":"a"},{"host":"b"},{"host":"c"}]}`, minPeers: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			connector := newTronRestConnectorMock(t, "POST", "/wallet/getnodeinfo",
				protocol.NewSimpleHttpUpstreamResponse("1", []byte(tc.body), protocol.Rest),
			)
			validator := tron_validations.NewTronPeersValidator("upstream-1", chains.TRON, connector, &chains.Options{
				InternalTimeout: time.Second,
				MinPeers:        tc.minPeers,
			})

			assert.Equal(t, protocol.Available, validator.Validate())
			connector.AssertExpectations(t)
		})
	}
}

// ---------- TronSyncingValidator ----------

func TestTronSyncingValidatorReturnsUnavailableOnConnectorError(t *testing.T) {
	connector := newTronRestConnectorMock(t, "POST", "/wallet/getblock",
		protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()),
	)
	validator := tron_validations.NewTronSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	assert.Equal(t, protocol.Unavailable, validator.Validate())
	connector.AssertExpectations(t)
}

func TestTronSyncingValidatorReturnsUnavailableOnInvalidJSON(t *testing.T) {
	connector := newTronRestConnectorMock(t, "POST", "/wallet/getblock",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{`), protocol.Rest),
	)
	validator := tron_validations.NewTronSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	assert.Equal(t, protocol.Unavailable, validator.Validate())
	connector.AssertExpectations(t)
}

func TestTronSyncingValidatorReturnsAvailableForFreshBlock(t *testing.T) {
	body := tronBlockBody(100, time.Now().UnixMilli())
	connector := newTronRestConnectorMock(t, "POST", "/wallet/getblock",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.Rest),
	)
	validator := tron_validations.NewTronSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	assert.Equal(t, protocol.Available, validator.Validate())
	connector.AssertExpectations(t)
}

func TestTronSyncingValidatorReturnsSyncingWhenProjectedLagExceedsThreshold(t *testing.T) {
	// 5 blocks tolerance, drift back ~6 blocks worth of time (6 * 3000ms = 18s).
	body := tronBlockBody(100, time.Now().UnixMilli()-18_000)
	connector := newTronRestConnectorMock(t, "POST", "/wallet/getblock",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.Rest),
	)
	validator := tron_validations.NewTronSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	assert.Equal(t, protocol.Syncing, validator.Validate())
	connector.AssertExpectations(t)
}

func TestTronSyncingValidatorReturnsAvailableForFutureBlockTimestamp(t *testing.T) {
	// Block timestamp in the future (clock skew) — drift is negative so
	// the max(0, ...) clamp keeps projected lag at zero.
	body := tronBlockBody(100, time.Now().UnixMilli()+60_000)
	connector := newTronRestConnectorMock(t, "POST", "/wallet/getblock",
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.Rest),
	)
	validator := tron_validations.NewTronSyncingValidator("upstream-1", testConfiguredChain(5), connector, time.Second)

	assert.Equal(t, protocol.Available, validator.Validate())
	connector.AssertExpectations(t)
}

// ---------- helpers ----------

func tronBlockBody(number, timestamp int64) string {
	return fmt.Sprintf(`{"block_header":{"raw_data":{"number":%d,"timestamp":%d}}}`, number, timestamp)
}

func testConfiguredChain(syncingLag int64) *chains.ConfiguredChain {
	return &chains.ConfiguredChain{
		Chain: chains.TRON,
		Settings: chains.Settings{
			Lags: chains.LagConfig{Syncing: syncingLag},
		},
	}
}

func newTronRestConnectorMock(t *testing.T, verb, path string, response protocol.ResponseHolder) *mocks.ConnectorMock {
	t.Helper()
	connector := mocks.NewConnectorMock()
	expectedMethod := verb + "#" + path
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
			r, ok := req.(*protocol.UpstreamRestRequest)
			return ok && r.Method() == expectedMethod
		})).
		Return(response).
		Once()
	return connector
}
