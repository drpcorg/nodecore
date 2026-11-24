package http_server_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/http_server"
	servernodecore "github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const routingRequest = `{"jsonrpc":"2.0","id":1,"method":"eth_call"}`

var routingHeaders = http.Header{
	"X-Nodecore-Group-Label": {"archive"},
	"X-Nodecore-Upstream-Id": {"a, b"},
}

var routingSelectors = []protocol.RequestSelector{
	protocol.RequestGroupLabelSelector{Labels: []string{"archive"}, FromRoutingHeader: true},
	protocol.RequestUpstreamIdSelector{Ids: mapset.NewThreadUnsafeSet("a", "b")},
}

// capturedSelectors records the selectors of every request holder that reaches
// key validation - the first thing HandleRequest does with a decoded holder
// after the routing selectors are put on it.
type capturedSelectors struct {
	mu   sync.Mutex
	seen [][]protocol.RequestSelector
}

func (c *capturedSelectors) add(selectors []protocol.RequestSelector) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, selectors)
}

func (c *capturedSelectors) all() [][]protocol.RequestSelector {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]protocol.RequestSelector(nil), c.seen...)
}

// routingServer serves polygon with the routing-headers option set as asked.
// Key validation fails on purpose: that is what keeps a request away from the
// execution flow, which this bare app context cannot run. HandleRequest stops
// at the first failing holder, so allowFirst lets that many holders through
// first - a batch of N needs N-1.
func routingServer(t *testing.T, enabled bool, allowFirst int) (*httptest.Server, *capturedSelectors) {
	t.Helper()
	captured := &capturedSelectors{}
	capture := func(args mock.Arguments) {
		captured.add(args.Get(2).(protocol.RequestHolder).Selectors())
	}

	authProc := mocks.NewMockAuthProcessor()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	authProc.On("PreKeyValidate", mock.Anything, mock.Anything).Return(nil, nil)
	authProc.On("GetKeyValue", mock.Anything).Return("")
	if allowFirst > 0 {
		authProc.On("PostKeyValidate", mock.Anything, mock.Anything, mock.Anything).Run(capture).Return(nil).Times(allowFirst)
	}
	authProc.On("PostKeyValidate", mock.Anything, mock.Anything, mock.Anything).Run(capture).Return(errors.New("stop"))

	supervisor := mocks.NewUpstreamSupervisorMock()
	supervisor.On("GetChainSupervisor", chains.POLYGON).Return(test_utils.CreateChainSupervisor())
	appConfig := &config.AppConfig{ServerConfig: &config.ServerConfig{RoutingHeaders: enabled}}
	appCtx := servernodecore.NewApplicationServerContext(supervisor, nil, nil, authProc, appConfig, nil, nil, nil, nil, nil)

	ts := httptest.NewServer(http_server.NewHttpServer(appCtx))
	t.Cleanup(ts.Close)
	return ts, captured
}

func sendRouted(t *testing.T, method, url, body string, header http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	for name, values := range header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

func TestRoutingHeadersReachJsonRpcRequest(t *testing.T) {
	ts, captured := routingServer(t, true, 0)

	sendRouted(t, http.MethodPost, ts.URL+"/queries/polygon", routingRequest, routingHeaders)

	assert.Equal(t, [][]protocol.RequestSelector{routingSelectors}, captured.all())
}

func TestRoutingHeadersReachEveryBatchItem(t *testing.T) {
	ts, captured := routingServer(t, true, 2)
	batch := `[` + routingRequest + `,` + routingRequest + `,` + routingRequest + `]`

	sendRouted(t, http.MethodPost, ts.URL+"/queries/polygon", batch, routingHeaders)

	assert.Equal(t, [][]protocol.RequestSelector{routingSelectors, routingSelectors, routingSelectors}, captured.all())
}

// an empty-path GET is a REST call, built by the REST handler
func TestRoutingHeadersReachRestRequest(t *testing.T) {
	ts, captured := routingServer(t, true, 0)

	sendRouted(t, http.MethodGet, ts.URL+"/queries/polygon", "", routingHeaders)

	assert.Equal(t, [][]protocol.RequestSelector{routingSelectors}, captured.all())
}

func TestRoutingHeadersIgnoredWhenOptionOff(t *testing.T) {
	ts, captured := routingServer(t, false, 0)

	sendRouted(t, http.MethodPost, ts.URL+"/queries/polygon", routingRequest, routingHeaders)

	seen := captured.all()
	require.Len(t, seen, 1)
	assert.Empty(t, seen[0])
}

func TestRoutingHeadersAbsentAddNothing(t *testing.T) {
	ts, captured := routingServer(t, true, 0)

	sendRouted(t, http.MethodPost, ts.URL+"/queries/polygon", routingRequest, nil)

	seen := captured.all()
	require.Len(t, seen, 1)
	assert.Empty(t, seen[0])
}

// the handshake fixes the restriction for the connection: it must hold for
// every message, not only the first
func TestRoutingHeadersApplyToEveryWsMessage(t *testing.T) {
	ts, captured := routingServer(t, true, 0)
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/queries/polygon", routingHeaders)
	require.NoError(t, err)
	_ = resp.Body.Close()
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	for range 2 {
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(routingRequest)))
		_, _, err = conn.ReadMessage()
		require.NoError(t, err)
	}

	assert.Equal(t, [][]protocol.RequestSelector{routingSelectors, routingSelectors}, captured.all())
}
