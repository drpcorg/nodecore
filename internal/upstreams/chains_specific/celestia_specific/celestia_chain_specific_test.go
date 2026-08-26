package celestia_specific_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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

func headerResponse(body string) protocol.ResponseHolder {
	return protocol.NewHttpUpstreamResponse("1", []byte(`{"jsonrpc":"2.0","result":`+body+`}`), 200, protocol.JsonRpc)
}

func TestCelestiaProcessors(t *testing.T) {
	specific := test_utils.NewCelestiaChainSpecific(context.Background(), nil)

	assert.Nil(t, specific.CapDetectors(caps.DetectorInput{}))
	assert.Nil(t, specific.MethodsProcessor())
	assert.Nil(t, specific.LabelsProcessor())
	assert.NotNil(t, specific.BlockProcessor())
	assert.NotNil(t, specific.LowerBoundProcessor())
}

func TestCelestiaFinalizedBlockIsTheHead(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", ctx, mock.Anything).Return(headerResponse(extendedHeader)).Once()

	block, err := test_utils.NewCelestiaChainSpecific(ctx, connector).GetFinalizedBlock(ctx)
	assert.Nil(t, err)
	assert.Equal(t, uint64(6273422), block.Height)
}

func TestCelestiaHealthValidator(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()

	validators := test_utils.NewCelestiaChainSpecific(ctx, conn).HealthValidators()
	require.Len(t, validators, 1)

	conn.On("SendRequest", mock.Anything, mock.Anything).Return(headerResponse("true")).Once()
	assert.Equal(t, protocol.Available, validators[0].Validate())

	conn.On("SendRequest", mock.Anything, mock.Anything).Return(headerResponse("false")).Once()
	assert.Equal(t, protocol.Unavailable, validators[0].Validate())

	// no auth token: celestia-node answers every call with code 1 "missing permission"
	conn.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "missing permission to invoke 'Ready' (need 'read')", nil))).Once()
	assert.Equal(t, protocol.Unavailable, validators[0].Validate())
}

func TestCelestiaChainValidator(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()

	validators := test_utils.NewCelestiaChainSpecific(ctx, conn).SettingsValidators()
	require.Len(t, validators, 1)

	conn.On("SendRequest", mock.Anything, mock.Anything).Return(headerResponse(extendedHeader)).Once()
	assert.Equal(t, validations.Valid, validators[0].Validate())

	mocha := strings.Replace(extendedHeader, `"chain_id": "celestia"`, `"chain_id": "mocha-4"`, 1)
	conn.On("SendRequest", mock.Anything, mock.Anything).Return(headerResponse(mocha)).Once()
	assert.Equal(t, validations.FatalSettingError, validators[0].Validate())

	conn.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "rpc error", nil))).Once()
	assert.Equal(t, validations.SettingsError, validators[0].Validate())
}
