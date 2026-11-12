package evm_specific_test

import (
	"context"
	"testing"
	"time"

	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific/evm_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestEvmClientLabelsDetectorIsNotNil(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{InternalTimeout: time.Second}

	detector := specific.NewEvmChainSpecific(context.Background(), "id", connector, []connectors.ApiConnector{connector}, chains.GetChain("ethereum"), time.Second, options, nil).ClientLabelsDetector()

	assert.NotNil(t, detector)
}
