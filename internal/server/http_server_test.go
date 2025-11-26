package server_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	servernodecore "github.com/drpcorg/nodecore/internal/server"
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

			assert.Equal(t, test.expectedBody, string(body))
			assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		})
	}
}
