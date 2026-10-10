package reloadsignal_test

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/reloadsignal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Without the subscription made at package initialization this SIGHUP would
// terminate the test binary.
func TestSighupIsDeliveredInsteadOfTerminatingTheProcess(t *testing.T) {
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGHUP))

	select {
	case sig := <-reloadsignal.Signals():
		assert.Equal(t, syscall.SIGHUP, sig)
	case <-time.After(5 * time.Second):
		t.Fatal("SIGHUP was not delivered")
	}
}

// The package subscribes in its init, and how early that init runs depends on
// what it imports: it must stay a leaf of the standard library.
func TestPackageImportsOnlyTheStandardLibrary(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "reload_signal.go", nil, parser.ImportsOnly)
	require.NoError(t, err)

	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		require.NoError(t, err)
		assert.Falsef(t, strings.Contains(strings.Split(path, "/")[0], "."), "%s is not a standard library package", path)
	}
}
