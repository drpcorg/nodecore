package http_server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

// corsContext builds a context whose response already carries the
// Vary: Accept-Encoding the compression middleware adds on the way in.
func corsContext(origin string) echo.Context {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Origin", origin)
	c := echo.New().NewContext(req, httptest.NewRecorder())
	c.Response().Header().Add(echo.HeaderVary, echo.HeaderAcceptEncoding)
	return c
}

// Two codings are now negotiable, so a shared cache that stops keying on
// Accept-Encoding will eventually hand a zstd body to a gzip-only client.
// Announcing Origin must therefore add to Vary, not replace what is there.
func TestSetCorsHeadersKeepsTheAcceptEncodingVary(t *testing.T) {
	tests := []struct {
		name        string
		corsOrigins []string
	}{
		{"configured origins", []string{"http://localhost:123"}},
		{"wildcard origins", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			c := corsContext("http://localhost:123")

			setCorsHeaders(c, tt.corsOrigins)

			assert.Contains(te, c.Response().Header().Values(echo.HeaderVary), echo.HeaderAcceptEncoding,
				"the coding this response was compressed with must stay part of the cache key")
		})
	}
}
