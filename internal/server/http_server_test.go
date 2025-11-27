package server_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	servernodecore "github.com/drpcorg/nodecore/internal/server"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestHttpServerOptionsRequest(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		code    int
		hasBody bool
		headers map[string]string
	}{
		{
			name: "json-rpc path",
			path: "/queries/optimism",
			code: http.StatusNoContent,
			headers: map[string]string{
				"Origin":                         "http://localhost:123",
				"Access-Control-Request-Headers": "test",
				"Access-Control-Request-Method":  "post",
			},
		},
		{
			name: "rest path",
			path: "/queries/optimism/eth/v1",
			code: http.StatusNoContent,
			headers: map[string]string{
				"Origin":                         "http://localhost:123",
				"Access-Control-Request-Headers": "test",
				"Access-Control-Request-Method":  "post",
			},
		},
		{
			name: "json-rpc path with key",
			path: "/queries/optimism/api-key/123",
			code: http.StatusNoContent,
			headers: map[string]string{
				"Origin":                         "http://localhost:123",
				"Access-Control-Request-Headers": "test",
				"Access-Control-Request-Method":  "post",
			},
		},
		{
			name: "rest path with key",
			path: "/queries/optimism/api-key/123/eth/v1",
			code: http.StatusNoContent,
			headers: map[string]string{
				"Origin":                         "http://localhost:123",
				"Access-Control-Request-Headers": "test",
				"Access-Control-Request-Method":  "post",
			},
		},
		{
			name:    "invalid path",
			path:    "/path",
			code:    http.StatusNotFound,
			hasBody: true,
		},
	}

	server := servernodecore.NewHttpServer(context.Background(), nil)
	ts := httptest.NewServer(server)
	defer ts.Close()
	client := http.DefaultClient

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			url := ts.URL + test.path

			req, err := http.NewRequest(http.MethodOptions, url, nil)
			assert.NoError(te, err)

			for k, v := range test.headers {
				req.Header.Set(k, v)
			}

			resp, err := client.Do(req)
			assert.NoError(te, err)

			assert.Equal(te, test.code, resp.StatusCode)
			assert.Equal(te, test.hasBody, resp.ContentLength > 0)
			if len(test.headers) > 0 {
				assert.Equal(t, "http://localhost:123", resp.Header.Get("Access-Control-Allow-Origin"))
				assert.Equal(t, "test", resp.Header.Get("Access-Control-Allow-Headers"))
				assert.Equal(t, "post", resp.Header.Get("Access-Control-Allow-Methods"))
			} else {
				assert.Empty(te, resp.Header.Get("Access-Control-Allow-Origin"))
				assert.Empty(te, resp.Header.Get("Access-Control-Allow-Headers"))
				assert.Empty(te, resp.Header.Get("Access-Control-Allow-Methods"))
			}
		})
	}
}

func TestHttpServerCantAuthenticate(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		expectedBody string
	}{
		{
			name:         "json-rpc",
			path:         "/queries/optimism",
			expectedBody: `{"id":0,"jsonrpc":"2.0","error":{"message":"auth error - fatal error","code":403}}`,
		},
		{
			name:         "rest",
			path:         "/queries/optimism/eth/v1",
			expectedBody: `{"message":"auth error - fatal error"}`,
		},
	}

	authProc := mocks.NewMockAuthProcessor()
	appCtx := servernodecore.NewApplicationContext(nil, nil, nil, authProc, nil, nil)
	server := servernodecore.NewHttpServer(context.Background(), appCtx)
	ts := httptest.NewServer(server)
	defer ts.Close()
	client := http.DefaultClient

	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(errors.New("fatal error"))

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL+test.path, nil)
			assert.NoError(t, err)

			resp, err := client.Do(req)
			assert.NoError(t, err)

			body, err := io.ReadAll(resp.Body)
			assert.NoError(t, err)

			authProc.AssertExpectations(t)

			assert.Equal(t, test.expectedBody, string(body))
			assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		})
	}
}

func TestHttServerCantParseJsonRpcThenErr(t *testing.T) {
	authProc := mocks.NewMockAuthProcessor()
	appCtx := servernodecore.NewApplicationContext(nil, nil, nil, authProc, nil, nil)
	server := servernodecore.NewHttpServer(context.Background(), appCtx)
	ts := httptest.NewServer(server)
	defer ts.Close()
	client := http.DefaultClient
	body := `{"wrong": json}`

	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/queries/optimism", bytes.NewReader([]byte(body)))
	assert.NoError(t, err)

	resp, err := client.Do(req)
	assert.NoError(t, err)

	respBody, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	authProc.AssertExpectations(t)

	assert.Equal(t, `{"id":0,"jsonrpc":"2.0","error":{"message":"couldn't parse a request","code":400}}`, string(respBody))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHttpServerPreKeyValidateWithErr(t *testing.T) {
	authProc := mocks.NewMockAuthProcessor()
	appCtx := servernodecore.NewApplicationContext(nil, nil, nil, authProc, nil, nil)
	server := servernodecore.NewHttpServer(context.Background(), appCtx)
	ts := httptest.NewServer(server)
	defer ts.Close()
	client := http.DefaultClient
	body := `{"jsonrpc" : "2.0","id" : 42,"method" : "eth_chainId"}`

	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	authProc.On("PreKeyValidate", mock.Anything, mock.Anything).Return(nil, errors.New("pre key validate error"))

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/queries/optimism", bytes.NewReader([]byte(body)))
	assert.NoError(t, err)

	resp, err := client.Do(req)
	assert.NoError(t, err)

	respBody, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	authProc.AssertExpectations(t)

	assert.Equal(t, `{"id":0,"jsonrpc":"2.0","error":{"message":"auth error - pre key validate error","code":403}}`, string(respBody))
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestHttpServerNotSupportedChainThenErr(t *testing.T) {
	authProc := mocks.NewMockAuthProcessor()
	appCtx := servernodecore.NewApplicationContext(nil, nil, nil, authProc, nil, nil)
	server := servernodecore.NewHttpServer(context.Background(), appCtx)
	ts := httptest.NewServer(server)
	defer ts.Close()
	client := http.DefaultClient
	body := `{"jsonrpc" : "2.0","id" : 42,"method" : "eth_chainId"}`

	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	authProc.On("PreKeyValidate", mock.Anything, mock.Anything).Return(nil, nil)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/queries/some-chain", bytes.NewReader([]byte(body)))
	assert.NoError(t, err)

	resp, err := client.Do(req)
	assert.NoError(t, err)

	respBody, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	authProc.AssertExpectations(t)

	assert.Equal(t, `{"id":42,"jsonrpc":"2.0","error":{"message":"chain some-chain is not supported","code":2}}`, string(respBody))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHttpServerChainSupervisorIsNilThenErr(t *testing.T) {
	upSup := mocks.NewUpstreamSupervisorMock()
	authProc := mocks.NewMockAuthProcessor()
	appCtx := servernodecore.NewApplicationContext(upSup, nil, nil, authProc, nil, nil)
	server := servernodecore.NewHttpServer(context.Background(), appCtx)
	ts := httptest.NewServer(server)
	defer ts.Close()
	client := http.DefaultClient
	body := `{"jsonrpc" : "2.0","id" : 42,"method" : "eth_chainId"}`

	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	authProc.On("PreKeyValidate", mock.Anything, mock.Anything).Return(nil, nil)
	upSup.On("GetChainSupervisor", chains.POLYGON).Return(nil)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/queries/polygon", bytes.NewReader([]byte(body)))
	assert.NoError(t, err)

	resp, err := client.Do(req)
	assert.NoError(t, err)

	respBody, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	authProc.AssertExpectations(t)
	upSup.AssertExpectations(t)

	assert.Equal(t, `{"id":42,"jsonrpc":"2.0","error":{"message":"no available upstreams to process a request","code":1}}`, string(respBody))
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestHttpServerPostKeyValidateWithErr(t *testing.T) {
	upSup := mocks.NewUpstreamSupervisorMock()
	authProc := mocks.NewMockAuthProcessor()
	appCtx := servernodecore.NewApplicationContext(upSup, nil, nil, authProc, nil, nil)
	server := servernodecore.NewHttpServer(context.Background(), appCtx)
	ts := httptest.NewServer(server)
	defer ts.Close()
	client := http.DefaultClient
	body := `{"jsonrpc" : "2.0","id" : 42,"method" : "eth_chainId"}`

	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	authProc.On("PreKeyValidate", mock.Anything, mock.Anything).Return(nil, nil)
	authProc.On("PostKeyValidate", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("my err"))
	upSup.On("GetChainSupervisor", chains.POLYGON).Return(test_utils.CreateChainSupervisor())

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/queries/polygon", bytes.NewReader([]byte(body)))
	assert.NoError(t, err)

	resp, err := client.Do(req)
	assert.NoError(t, err)

	respBody, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	authProc.AssertExpectations(t)
	upSup.AssertExpectations(t)

	assert.Equal(t, `{"id":42,"jsonrpc":"2.0","error":{"message":"auth error - my err","code":403}}`, string(respBody))
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}
