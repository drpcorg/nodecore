package http_server_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/http_server"
	servernodecore "github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	kb = 1024
	mb = 1024 * kb
)

// -------------------- request size: the body reader --------------------

// countingReader reports how many bytes were pulled out of a request body.
type countingReader struct {
	reader io.Reader
	read   int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.read += int64(n)
	return n, err
}

// postWithLimit sends body through the request-body middleware configured with
// maxRequestBytes and reports how much the handler managed to read, how much
// was pulled from the wire, and the error the handler stopped on. A negative
// contentLength sends the body without a declared length.
func postWithLimit(
	t *testing.T,
	maxRequestBytes int64,
	contentEncoding string,
	body []byte,
	contentLength int64,
) (handlerRead int64, wireRead int64, rec *httptest.ResponseRecorder, readErr error) {
	t.Helper()
	e := echo.New()
	e.Use(http_server.Decompress(maxRequestBytes))
	e.POST("/", func(c *echo.Context) error {
		handlerRead, readErr = io.Copy(io.Discard, c.Request().Body)
		if readErr != nil {
			return readErr
		}
		return c.NoContent(http.StatusOK)
	})

	wire := &countingReader{reader: bytes.NewReader(body)}
	req := httptest.NewRequest(http.MethodPost, "/", wire)
	req.ContentLength = contentLength
	if contentEncoding != "" {
		req.Header.Set(echo.HeaderContentEncoding, contentEncoding)
	}
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return handlerRead, wire.read, rec, readErr
}

func TestRequestBodyOverTheConfiguredLimitFailsTheRead(t *testing.T) {
	body := bytes.Repeat([]byte("A"), kb+1)

	read, _, rec, readErr := postWithLimit(t, kb, "", body, -1)

	require.Error(t, readErr, "the handler read a body past the limit without noticing")
	assert.LessOrEqual(t, read, int64(kb))
	assert.NotEqual(t, http.StatusOK, rec.Code)
}

// The limit is a ceiling, not a trim: a body of exactly the limit is served whole.
func TestRequestBodyAtTheConfiguredLimitIsServed(t *testing.T) {
	plain := bytes.Repeat([]byte("A"), kb)
	tests := []struct {
		name            string
		contentEncoding string
		body            []byte
	}{
		{name: "plain", body: plain},
		{name: "gzip", contentEncoding: "gzip", body: compress(t, compression.Gzip, plain)},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			read, _, rec, readErr := postWithLimit(te, kb, test.contentEncoding, test.body, int64(len(test.body)))

			require.NoError(te, readErr)
			assert.Equal(te, int64(kb), read)
			assert.Equal(te, http.StatusOK, rec.Code)
		})
	}
}

// A request that declares more than the limit is turned away on the
// declaration alone - nothing is pulled off the wire to find out.
func TestRequestBodyDeclaredOverTheLimitIsNotRead(t *testing.T) {
	body := bytes.Repeat([]byte("A"), 4*kb)

	_, wireRead, rec, readErr := postWithLimit(t, kb, "", body, int64(len(body)))

	require.Error(t, readErr)
	assert.Zero(t, wireRead, "the body was read although its declared length already exceeded the limit")
	assert.NotEqual(t, http.StatusOK, rec.Code)
}

// A coding nodecore does not speak is passed through undecoded, so its raw
// bytes are what the handler reads - and what the limit bounds.
func TestRequestBodyInAnUnknownCodingIsBoundByTheLimit(t *testing.T) {
	body := bytes.Repeat([]byte("A"), kb+1)

	_, _, rec, readErr := postWithLimit(t, kb, "x-unknown-coding", body, -1)

	require.Error(t, readErr)
	assert.NotEqual(t, http.StatusOK, rec.Code)
}

// A configured limit takes the place of the built-in cap on decoded bodies, in
// both directions: a smaller limit binds sooner, a larger one lets through
// what the built-in cap would have stopped.
func TestConfiguredLimitReplacesTheBuiltInCapOnDecodedBodies(t *testing.T) {
	t.Run("a limit below the built-in cap binds the decoded size", func(te *testing.T) {
		body := compress(te, compression.Gzip, bytes.Repeat([]byte("A"), 2*mb))

		read, _, rec, readErr := postWithLimit(te, mb, "gzip", body, int64(len(body)))

		require.Error(te, readErr)
		assert.LessOrEqual(te, read, int64(mb))
		assert.NotEqual(te, http.StatusOK, rec.Code)
	})

	t.Run("a limit above the built-in cap lifts it", func(te *testing.T) {
		decodedSize := http_server.MaxDecodedRequestBytes + mb
		body := compress(te, compression.Gzip, bytes.Repeat([]byte("A"), decodedSize))

		read, _, rec, readErr := postWithLimit(te, int64(decodedSize)+mb, "gzip", body, int64(len(body)))

		require.NoError(te, readErr)
		assert.Equal(te, int64(decodedSize), read)
		assert.Equal(te, http.StatusOK, rec.Code)
	})
}

// The limit is opt-in: with none configured a plain body of any size is
// served, including one larger than the built-in cap on decoded bodies.
func TestNoConfiguredLimitLeavesPlainBodiesUnbounded(t *testing.T) {
	body := bytes.Repeat([]byte("A"), http_server.MaxDecodedRequestBytes+mb)

	read, _, rec, readErr := postWithLimit(t, 0, "", body, int64(len(body)))

	require.NoError(t, readErr)
	assert.Equal(t, int64(len(body)), read)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// -------------------- request size: what an http client sees --------------------

func serverWithLimits(limits *config.LimitsConfig) (*httptest.Server, *mocks.MockAuthProcessor) {
	authProc := mocks.NewMockAuthProcessor()
	var appConfig *config.AppConfig
	if limits != nil {
		appConfig = &config.AppConfig{ServerConfig: &config.ServerConfig{Limits: limits}}
	}
	appCtx := servernodecore.NewApplicationServerContext(nil, nil, nil, authProc, appConfig, nil, nil, nil, nil, nil)
	return httptest.NewServer(http_server.NewHttpServer(appCtx)), authProc
}

func TestHttpServerRequestBodyOverLimitThen413(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		declaresLength bool
		expectedBody   string
	}{
		{
			name:           "json-rpc with a declared length",
			path:           "/queries/optimism",
			declaresLength: true,
			expectedBody:   `{"id":0,"jsonrpc":"2.0","error":{"message":"request body is too large","code":413}}`,
		},
		{
			name:         "json-rpc without a declared length",
			path:         "/queries/optimism",
			expectedBody: `{"id":0,"jsonrpc":"2.0","error":{"message":"request body is too large","code":413}}`,
		},
		{
			name:           "rest with a declared length",
			path:           "/queries/optimism/eth/v1",
			declaresLength: true,
			expectedBody:   `{"message":"request body is too large"}`,
		},
		{
			name:         "rest without a declared length",
			path:         "/queries/optimism/eth/v1",
			expectedBody: `{"message":"request body is too large"}`,
		},
	}

	ts, authProc := serverWithLimits(&config.LimitsConfig{MaxRequestSize: "1KB"})
	defer ts.Close()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	// a well-formed request, so that the size is the only thing wrong with it
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_call","params":["` + strings.Repeat("a", 2*kb) + `"]}`)

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			var body io.Reader = bytes.NewReader(payload)
			if !test.declaresLength {
				// hiding the concrete reader type makes the client send it chunked
				body = io.NopCloser(body)
			}
			req, err := http.NewRequest(http.MethodPost, ts.URL+test.path, body)
			require.NoError(te, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(te, err)
			defer func() { _ = resp.Body.Close() }()
			respBody, err := io.ReadAll(resp.Body)
			require.NoError(te, err)

			assert.Equal(te, http.StatusRequestEntityTooLarge, resp.StatusCode)
			assert.Equal(te, test.expectedBody, string(respBody))
		})
	}
}

// With no limit configured the built-in cap on decoded bodies still applies,
// and a body that runs past it is reported as too large, not as unparseable.
func TestHttpServerCompressedBodyOverBuiltInCapThen413(t *testing.T) {
	ts, authProc := serverWithLimits(nil)
	defer ts.Close()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	bomb := compress(t, compression.Gzip, bytes.Repeat([]byte(" "), http_server.MaxDecodedRequestBytes+mb))

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/queries/optimism", bytes.NewReader(bomb))
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	assert.Equal(t, `{"id":0,"jsonrpc":"2.0","error":{"message":"request body is too large","code":413}}`, string(respBody))
}

// -------------------- batch size --------------------

const batchOfThree = `[` +
	`{"jsonrpc":"2.0","id":1,"method":"eth_chainId"},` +
	`{"jsonrpc":"2.0","id":2,"method":"eth_chainId"},` +
	`{"jsonrpc":"2.0","id":3,"method":"eth_chainId"}]`

const batchTooLargeReply = `{"id":0,"jsonrpc":"2.0","error":{"message":"client error - batch size 3 exceeds the limit of 2","code":400}}`

func decodeBatch(t *testing.T, body string, maxBatchSize int) (*servernodecore.Request, error) {
	t.Helper()
	handler, err := http_server.NewJsonRpcHandler(
		&servernodecore.Request{Chain: "ethereum"},
		strings.NewReader(body),
		false,
		maxBatchSize,
	)
	require.NoError(t, err)
	return handler.RequestDecode(t.Context())
}

func TestJsonRpcHandlerBatchOverLimitThenDecodeError(t *testing.T) {
	request, err := decodeBatch(t, batchOfThree, 2)

	assert.Nil(t, request)
	var responseErr *protocol.ResponseError
	require.ErrorAs(t, err, &responseErr)
	assert.Equal(t, protocol.ClientErrorCode, responseErr.Code)
	assert.Equal(t, "client error - batch size 3 exceeds the limit of 2", responseErr.Message)
}

func TestJsonRpcHandlerBatchWithinLimitIsDecoded(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		maxBatchSize int
		expected     int
	}{
		{name: "a batch at the limit", body: batchOfThree, maxBatchSize: 3, expected: 3},
		{name: "a batch with no limit configured", body: batchOfThree, maxBatchSize: 0, expected: 3},
		{name: "a single request under the smallest limit", body: `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`, maxBatchSize: 1, expected: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			request, err := decodeBatch(te, test.body, test.maxBatchSize)

			require.NoError(te, err)
			assert.Len(te, request.UpstreamRequests, test.expected)
		})
	}
}

func TestHttpServerBatchOverLimitThenOneError(t *testing.T) {
	ts, authProc := serverWithLimits(&config.LimitsConfig{MaxBatchSize: 2})
	defer ts.Close()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	authProc.On("PreKeyValidate", mock.Anything, mock.Anything).Return(nil, nil)

	resp, err := http.Post(ts.URL+"/queries/optimism", "application/json", strings.NewReader(batchOfThree))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// a batch always answers 200 with its errors inside the array
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, `[`+batchTooLargeReply+`]`, string(respBody))
}

// -------------------- websocket --------------------

func dialWs(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/queries/optimism", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	t.Cleanup(func() { _ = conn.Close() })
	// a reply that never comes fails the read instead of hanging the test
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	return conn
}

// An oversized batch is a mistake in one message, not a reason to drop the
// connection: it is answered with an error and the socket keeps serving.
func TestWsBatchOverLimitThenErrorAndConnectionStaysOpen(t *testing.T) {
	ts, authProc := serverWithLimits(&config.LimitsConfig{MaxBatchSize: 2})
	defer ts.Close()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	authProc.On("PreKeyValidate", mock.Anything, mock.Anything).Return(nil, nil)
	conn := dialWs(t, ts)

	for range 2 {
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(batchOfThree)))

		_, reply, err := conn.ReadMessage()

		require.NoError(t, err)
		assert.Equal(t, batchTooLargeReply, string(reply))
	}
}

func TestWsMessageOverLimitClosesConnection(t *testing.T) {
	ts, authProc := serverWithLimits(&config.LimitsConfig{MaxRequestSize: "1KB"})
	defer ts.Close()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	conn := dialWs(t, ts)
	message := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_call","params":["` + strings.Repeat("a", kb) + `"]}`)

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, message))
	_, _, err := conn.ReadMessage()

	var closeErr *websocket.CloseError
	require.ErrorAs(t, err, &closeErr)
	assert.Equal(t, websocket.CloseMessageTooBig, closeErr.Code)
}

// A message of exactly the limit is read and handled. The reply used to tell
// is the batch error: it needs no upstream and proves the message got through.
func TestWsMessageAtLimitIsServed(t *testing.T) {
	ts, authProc := serverWithLimits(&config.LimitsConfig{MaxRequestSize: "1KB", MaxBatchSize: 2})
	defer ts.Close()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	authProc.On("PreKeyValidate", mock.Anything, mock.Anything).Return(nil, nil)
	conn := dialWs(t, ts)
	message := batchOfThree + strings.Repeat(" ", kb-len(batchOfThree))
	require.Len(t, message, kb)

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(message)))
	_, reply, err := conn.ReadMessage()

	require.NoError(t, err)
	assert.Equal(t, batchTooLargeReply, string(reply))
}
