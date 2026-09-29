package protocol_test

import (
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
)

// config cannot import protocol, so it keeps its own list of lower-bounds keys; this keeps both in sync.
func TestParseLowerBoundTypeAcceptsEveryConfigName(t *testing.T) {
	parsed := make(map[protocol.LowerBoundType]bool)
	for _, name := range config.LowerBoundTypeNames {
		boundType, ok := protocol.ParseLowerBoundType(name)
		if assert.True(t, ok, name) {
			assert.Equal(t, strings.ToUpper(name), boundType.String())
			parsed[boundType] = true
		}
	}
	for boundType := protocol.SlotBound; boundType <= protocol.BlobBound; boundType++ {
		assert.True(t, parsed[boundType], "no config name for %s", boundType)
	}
}

func TestParseLowerBoundTypeRejectsUnknown(t *testing.T) {
	for _, name := range []string{"unknown", "UNKNOWN", "", "foo"} {
		_, ok := protocol.ParseLowerBoundType(name)
		assert.False(t, ok, name)
	}
}
