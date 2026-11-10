package starknet_specific_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestStarknetClientLabelsDetectorIsNotNil(t *testing.T) {
	detector := test_utils.NewStarknetChainSpecific(context.Background(), mocks.NewConnectorMock()).ClientLabelsDetector()

	assert.NotNil(t, detector)
}
