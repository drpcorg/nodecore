package config_test

import (
	"os"
	"testing"

	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
)

// Connector validation consults the chain's method spec, so the specs must be
// loaded before any config is parsed - the same order main follows.
func TestMain(m *testing.M) {
	specs_utils.LoadMethodSpecs()
	os.Exit(m.Run())
}
