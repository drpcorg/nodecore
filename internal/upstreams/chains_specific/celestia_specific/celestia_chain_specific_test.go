package celestia_specific_test

import (
	"context"
	"errors"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const extendedHeader = `{
	"header": {
		"chain_id": "celestia",
		"height": "6273422",
		"last_block_id": {"hash": "A72B2BFDBBFFBBAAF25957AAFD5D6C921D45B5AF83A5E7469557BE0DC53AF320"}
	},
	"commit": {
		"height": "6273422",
		"block_id": {"hash": "5E5EC8DDA2B34E4E97E663845AA47C282766AA79FA815DFC0FF2CFC22D07B4BD"}
	},
	"validator_set": {},
	"dah": {}
}`

func TestCelestiaSubscribeHeadRequest(t *testing.T) {
	req, err := test_utils.NewCelestiaChainSpecific(context.Background(), nil).SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.EqualError(t, err, "celestia does not support websocket subscriptions")
}

func TestCelestiaParseSubscriptionBlock(t *testing.T) {
	block, err := test_utils.NewCelestiaChainSpecific(context.Background(), nil).ParseSubscriptionBlock([]byte(`{}`))
	assert.True(t, block.IsFullEmpty())
	assert.EqualError(t, err, "celestia does not support websocket subscriptions")
}

func TestCelestiaParseBlock(t *testing.T) {
	block, err := test_utils.NewCelestiaChainSpecific(context.Background(), nil).ParseBlock([]byte(extendedHeader))
	assert.Nil(t, err)

	expected := protocol.NewBlock(
		6273422,
		0,
		blockchain.NewHashIdFromString("5E5EC8DDA2B34E4E97E663845AA47C282766AA79FA815DFC0FF2CFC22D07B4BD"),
		blockchain.NewHashIdFromString("A72B2BFDBBFFBBAAF25957AAFD5D6C921D45B5AF83A5E7469557BE0DC53AF320"),
	)
	assert.Equal(t, expected, block)
}

func TestCelestiaParseBlockInvalidJSON(t *testing.T) {
	block, err := test_utils.NewCelestiaChainSpecific(context.Background(), nil).ParseBlock([]byte(`not json`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the celestia extended header")
}

func TestCelestiaParseBlockNoHeight(t *testing.T) {
	block, err := test_utils.NewCelestiaChainSpecific(context.Background(), nil).ParseBlock([]byte(`{"header": {}}`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the celestia extended header")
}

func TestCelestiaGetLatestBlock(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{"jsonrpc": "2.0", "result": ` + extendedHeader + `}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewCelestiaChainSpecific(context.Background(), connector).GetLatestBlock(ctx)
	assert.Nil(t, err)

	connector.AssertExpectations(t)
	assert.Equal(t, uint64(6273422), block.Height)
}

func TestCelestiaGetLatestBlockWithError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "rpc error", nil))

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewCelestiaChainSpecific(context.Background(), connector).GetLatestBlock(ctx)

	connector.AssertExpectations(t)
	assert.True(t, block.IsFullEmpty())

	var upErr *protocol.ResponseError
	assert.True(t, errors.As(err, &upErr))
	assert.Equal(t, 1, upErr.Code)
	assert.Equal(t, "rpc error", upErr.Message)
}
