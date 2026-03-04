package local_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration/local"
	"github.com/stretchr/testify/assert"
)

func TestLocalKeyGetValues(t *testing.T) {
	keyCfg := &config.LocalKeyConfig{
		Key: "secret-key",
	}
	key := local.NewLocalKey("key-id", keyCfg)

	assert.Equal(t, "key-id", key.Id())
	assert.Equal(t, "secret-key", key.GetKeyValue())
}
