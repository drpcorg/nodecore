package polkadot_specific_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestPolkadotClientLabelsDetectorIsNil(t *testing.T) {
	detector := test_utils.NewPolkadotChainSpecific(context.Background(), mocks.NewConnectorMock()).ClientLabelsDetector()

	assert.Nil(t, detector)
}
