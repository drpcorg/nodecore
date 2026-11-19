package http_server_test

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"math/rand/v2"
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

// incompressible returns size bytes that no coding can shrink: compressed, they
// take more room on the wire than they decode to.
func incompressible(size int) []byte {
	noise := make([]byte, size)
	_, _ = rand.NewChaCha8([32]byte{}).Read(noise)
	return noise
}

var decodedCodings = []struct {
	contentEncoding string
	scheme          compression.Scheme
}{
	{contentEncoding: "gzip", scheme: compression.Gzip},
	{contentEncoding: "zstd", scheme: compression.Zstd},
	{contentEncoding: "br", scheme: compression.Brotli},
}

// The limit bounds a compressed body twice: what it decodes to, and what it
// takes on the wire. A body that decodes to no more than the limit is still
// turned away when its compressed form is larger than the limit.
func TestCompressedBodyOverTheLimitOnTheWireFailsTheRead(t *testing.T) {
	for _, coding := range decodedCodings {
		body := compress(t, coding.scheme, incompressible(kb))
		require.Greater(t, len(body), kb, "the body must be larger compressed than decoded")

		t.Run(coding.contentEncoding+" with a declared length is not read", func(te *testing.T) {
			_, wireRead, rec, readErr := postWithLimit(te, kb, coding.contentEncoding, body, int64(len(body)))

			require.Error(te, readErr, "the handler read a body larger than the limit on the wire")
			assert.Zero(te, wireRead, "the body was read although its declared length already exceeded the limit")
			assert.NotEqual(te, http.StatusOK, rec.Code)
		})

		t.Run(coding.contentEncoding+" without a declared length is read up to the limit", func(te *testing.T) {
			_, wireRead, rec, readErr := postWithLimit(te, kb, coding.contentEncoding, body, -1)

			require.Error(te, readErr, "the handler read a body larger than the limit on the wire")
			// the limit, and the one byte that shows the body runs past it
			assert.LessOrEqual(te, wireRead, int64(kb+1))
			assert.NotEqual(te, http.StatusOK, rec.Code)
		})
	}
}

// A gzip body may be any number of members, and an empty member decodes to
// nothing. Bounding only the decoded size would let megabytes of them through
// in front of a small request.
func TestGzipBodyPaddedWithEmptyMembersIsBoundOnTheWire(t *testing.T) {
	emptyMember := compress(t, compression.Gzip, nil)
	require.NotEmpty(t, emptyMember)
	request := compress(t, compression.Gzip, []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`))
	body := append(bytes.Repeat(emptyMember, 2*mb/len(emptyMember)), request...)

	_, wireRead, rec, readErr := postWithLimit(t, kb, "gzip", body, -1)

	require.Error(t, readErr, "the handler read 2MB off the wire under a 1KB limit")
	assert.LessOrEqual(t, wireRead, int64(kb+1))
	assert.NotEqual(t, http.StatusOK, rec.Code)
}

// The wire is bounded only by a configured limit: without one a compressed
// body is capped on its decoded size alone, as before.
func TestNoConfiguredLimitLeavesCompressedBodiesUnboundedOnTheWire(t *testing.T) {
	emptyMember := compress(t, compression.Gzip, nil)
	plain := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`)
	body := append(bytes.Repeat(emptyMember, 2*mb/len(emptyMember)), compress(t, compression.Gzip, plain)...)

	read, wireRead, rec, readErr := postWithLimit(t, 0, "gzip", body, int64(len(body)))

	require.NoError(t, readErr)
	assert.Equal(t, int64(len(plain)), read)
	assert.Equal(t, int64(len(body)), wireRead)
	assert.Equal(t, http.StatusOK, rec.Code)
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
	ts, authProc, _ := serverWithLimitsAndSupervisor(limits)
	return ts, authProc
}

// serverWithLimitsAndSupervisor also hands out the upstream supervisor, the
// first thing a request reaches once it is past decoding. A test of a request
// that must be turned away before that asserts the supervisor was never asked.
// It has no chains, so a request that does get through is answered with an
// error instead of taking the test down.
func serverWithLimitsAndSupervisor(
	limits *config.LimitsConfig,
) (*httptest.Server, *mocks.MockAuthProcessor, *mocks.UpstreamSupervisorMock) {
	authProc := mocks.NewMockAuthProcessor()
	supervisor := mocks.NewUpstreamSupervisorMock()
	supervisor.On("GetChainSupervisor", mock.Anything).Return(nil)
	var appConfig *config.AppConfig
	if limits != nil {
		appConfig = &config.AppConfig{ServerConfig: &config.ServerConfig{Limits: limits}}
	}
	appCtx := servernodecore.NewApplicationServerContext(supervisor, nil, nil, authProc, appConfig, nil, nil, nil, nil, nil)
	return httptest.NewServer(http_server.NewHttpServer(appCtx)), authProc, supervisor
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

// A request turned away before it is handled is still an answer a browser
// client has to be able to read. Without the CORS header the browser hides the
// response and reports a CORS failure instead of the error nodecore sent.
func TestHttpServerRequestRejectedOnItsBodyIsReadableByBrowsers(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		expectedCode int
	}{
		{
			name:         "a body over the limit",
			body:         `{"jsonrpc":"2.0","id":1,"method":"eth_call","params":["` + strings.Repeat("a", 2*kb) + `"]}`,
			expectedCode: http.StatusRequestEntityTooLarge,
		},
		{
			name:         "a body that does not parse",
			body:         `{"jsonrpc":`,
			expectedCode: http.StatusBadRequest,
		},
	}

	ts, authProc := serverWithLimits(&config.LimitsConfig{MaxRequestSize: "1KB"})
	defer ts.Close()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/queries/optimism", strings.NewReader(test.body))
			require.NoError(te, err)
			req.Header.Set("Origin", "https://dapp.example")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(te, err)
			defer func() { _ = resp.Body.Close() }()

			assert.Equal(te, test.expectedCode, resp.StatusCode)
			assert.Equal(te, "*", resp.Header.Get("Access-Control-Allow-Origin"))
		})
	}
}

// A compressed body over the limit on the wire is answered like any other
// oversized body, whichever decoder was reading it when the limit was hit.
func TestHttpServerCompressedBodyOverLimitOnTheWireThen413(t *testing.T) {
	ts, authProc := serverWithLimits(&config.LimitsConfig{MaxRequestSize: "1KB"})
	defer ts.Close()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)

	for _, coding := range decodedCodings {
		payload := compress(t, coding.scheme, incompressible(kb))
		require.Greater(t, len(payload), kb)

		for _, declaresLength := range []bool{true, false} {
			name := coding.contentEncoding + " without a declared length"
			if declaresLength {
				name = coding.contentEncoding + " with a declared length"
			}
			t.Run(name, func(te *testing.T) {
				var body io.Reader = bytes.NewReader(payload)
				if !declaresLength {
					// hiding the concrete reader type makes the client send it chunked
					body = io.NopCloser(body)
				}
				req, err := http.NewRequest(http.MethodPost, ts.URL+"/queries/optimism", body)
				require.NoError(te, err)
				req.Header.Set("Content-Encoding", coding.contentEncoding)

				resp, err := http.DefaultClient.Do(req)
				require.NoError(te, err)
				defer func() { _ = resp.Body.Close() }()
				respBody, err := io.ReadAll(resp.Body)
				require.NoError(te, err)

				assert.Equal(te, http.StatusRequestEntityTooLarge, resp.StatusCode)
				assert.Equal(te, `{"id":0,"jsonrpc":"2.0","error":{"message":"request body is too large","code":413}}`, string(respBody))
			})
		}
	}
}

// A coding's own framing can carry any number of bytes that decode to nothing,
// in front of a request that is small both compressed and decoded. A decoder
// is free to report the cut-off read in its own words, so the answer must not
// depend on which error comes out of it: the body was over the limit on the
// wire, and that is what the client is told.
func TestHttpServerCompressedBodyPaddedOverLimitThen413(t *testing.T) {
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`)
	emptyGzipMember := compress(t, compression.Gzip, nil)
	// RFC 8878 3.1.2: a magic number, the size of the user data, the user data
	skippableZstdFrame := binary.LittleEndian.AppendUint32(nil, 0x184D2A50)
	skippableZstdFrame = binary.LittleEndian.AppendUint32(skippableZstdFrame, 2*mb)
	skippableZstdFrame = append(skippableZstdFrame, make([]byte, 2*mb)...)

	tests := []struct {
		name            string
		contentEncoding string
		payload         []byte
	}{
		{
			name:            "gzip, empty members",
			contentEncoding: "gzip",
			payload:         append(bytes.Repeat(emptyGzipMember, 2*mb/len(emptyGzipMember)), compress(t, compression.Gzip, request)...),
		},
		{
			name:            "zstd, a skippable frame",
			contentEncoding: "zstd",
			payload:         append(skippableZstdFrame, compress(t, compression.Zstd, request)...),
		},
	}

	ts, authProc := serverWithLimits(&config.LimitsConfig{MaxRequestSize: "1KB"})
	defer ts.Close()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)

	for _, test := range tests {
		for _, declaresLength := range []bool{true, false} {
			name := test.name + ", without a declared length"
			if declaresLength {
				name = test.name + ", with a declared length"
			}
			t.Run(name, func(te *testing.T) {
				var body io.Reader = bytes.NewReader(test.payload)
				if !declaresLength {
					// hiding the concrete reader type makes the client send it chunked
					body = io.NopCloser(body)
				}
				req, err := http.NewRequest(http.MethodPost, ts.URL+"/queries/optimism", body)
				require.NoError(te, err)
				req.Header.Set("Content-Encoding", test.contentEncoding)

				resp, err := http.DefaultClient.Do(req)
				require.NoError(te, err)
				defer func() { _ = resp.Body.Close() }()
				respBody, err := io.ReadAll(resp.Body)
				require.NoError(te, err)

				assert.Equal(te, http.StatusRequestEntityTooLarge, resp.StatusCode)
				assert.Equal(te, `{"id":0,"jsonrpc":"2.0","error":{"message":"request body is too large","code":413}}`, string(respBody))
			})
		}
	}
}

// The decoders read the head of a body before the handler reads anything, to
// check that it is what its coding says. A head larger than the limit hits the
// limit right there, while the decoder is still being set up - and that is an
// oversized body, not a malformed one. A gzip header may carry up to 64KB in
// its extra field, which is enough to do it.
func TestHttpServerCompressedBodyWithHeaderOverLimitThen413(t *testing.T) {
	var payload bytes.Buffer
	writer := gzip.NewWriter(&payload)
	writer.Extra = make([]byte, 2*kb)
	_, err := writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	ts, authProc := serverWithLimits(&config.LimitsConfig{MaxRequestSize: "1KB"})
	defer ts.Close()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)

	// no declared length: the limit is found by reading, not by the header
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/queries/optimism", io.NopCloser(&payload))
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
	ts, authProc, supervisor := serverWithLimitsAndSupervisor(&config.LimitsConfig{MaxBatchSize: 2})
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
	supervisor.AssertNotCalled(t, "GetChainSupervisor", mock.Anything)
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
	ts, authProc, supervisor := serverWithLimitsAndSupervisor(&config.LimitsConfig{MaxBatchSize: 2})
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
	supervisor.AssertNotCalled(t, "GetChainSupervisor", mock.Anything)
}

func TestWsMessageOverLimitClosesConnection(t *testing.T) {
	ts, authProc := serverWithLimits(&config.LimitsConfig{MaxRequestSize: "1KB"})
	defer ts.Close()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	conn := dialWs(t, ts)
	// one byte over, so that a limit that is off by any amount fails the test
	const head, tail = `{"jsonrpc":"2.0","id":1,"method":"eth_call","params":["`, `"]}`
	message := head + strings.Repeat("a", kb+1-len(head)-len(tail)) + tail
	require.Len(t, message, kb+1)

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(message)))
	_, _, err := conn.ReadMessage()

	var closeErr *websocket.CloseError
	require.ErrorAs(t, err, &closeErr)
	assert.Equal(t, websocket.CloseMessageTooBig, closeErr.Code)
}

// A message of exactly the limit is read and handled. The reply used to tell
// is the batch error: it needs no upstream and proves the message got through.
func TestWsMessageAtLimitIsServed(t *testing.T) {
	ts, authProc, supervisor := serverWithLimitsAndSupervisor(&config.LimitsConfig{MaxRequestSize: "1KB", MaxBatchSize: 2})
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
	supervisor.AssertNotCalled(t, "GetChainSupervisor", mock.Anything)
}
