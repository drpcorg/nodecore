package specific_test

import (
	"context"
	"errors"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestChainValidator(t *testing.T) {
	options := &config.UpstreamOptions{
		DisableChainValidation: lo.ToPtr(false),
	}
	connector := mocks.NewConnectorMock()

	validators := specific.NewEvmChainSpecific("id", connector, chains.UnknownChain, options).SettingsValidators()

	_, ok := lo.Find(validators, func(item validations.SettingsValidator) bool {
		_, ok := item.(*validations.ChainValidator)
		return ok
	})
	assert.True(t, ok)

	options.DisableChainValidation = lo.ToPtr(true)
	validators = specific.NewEvmChainSpecific("id", connector, chains.UnknownChain, options).SettingsValidators()

	_, ok = lo.Find(validators, func(item validations.SettingsValidator) bool {
		_, ok := item.(*validations.ChainValidator)
		return ok
	})

	assert.False(t, ok)
}

func TestEvmSubscribeHeadRequest(t *testing.T) {
	req, err := test_utils.NewEvmChainSpecific(nil).SubscribeHeadRequest()
	assert.Nil(t, err)

	body, reqErr := req.Body()
	assert.Nil(t, reqErr)

	assert.Equal(t, "1", req.Id())
	assert.Equal(t, "eth_subscribe", req.Method())
	assert.False(t, req.IsStream())
	require.JSONEq(t, `{"id":"1","jsonrpc":"2.0","method":"eth_subscribe","params":["newHeads"]}`, string(body))
}

func TestEvmParseSubBLock(t *testing.T) {
	body := []byte(`{
      "number": "0x41fd60b",
      "hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18",
	  "parentHash": "0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"
    }`)

	block, err := test_utils.NewEvmChainSpecific(nil).ParseSubscriptionBlock(body)
	assert.Nil(t, err)

	expected := &protocol.BlockData{
		Height:     uint64(69195275),
		Hash:       blockchain.NewHashIdFromString("0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"),
		ParentHash: blockchain.NewHashIdFromString("0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"),
	}

	assert.Equal(t, expected, block.BlockData)
}

func TestEvmGetLatestBlock(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{
	  "jsonrpc": "2.0",
	  "result": {
		"number": "0x41fd60b",
		"hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18",
		"parentHash": "0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"
	  }
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewEvmChainSpecific(connector).GetLatestBlock(ctx)
	assert.Nil(t, err)

	connector.AssertExpectations(t)

	expected := &protocol.BlockData{
		Height:     uint64(69195275),
		Hash:       blockchain.NewHashIdFromString("0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"),
		ParentHash: blockchain.NewHashIdFromString("0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"),
	}
	assert.Equal(t, expected, block.BlockData)
}

func TestEvmGetLatestBlockWithError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "block error", nil))

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewEvmChainSpecific(connector).GetLatestBlock(ctx)

	connector.AssertExpectations(t)
	assert.Nil(t, block)

	var upErr *protocol.ResponseError
	ok := errors.As(err, &upErr)
	assert.True(t, ok)

	assert.Equal(t, 1, upErr.Code)
	assert.Equal(t, "block error", upErr.Message)
}
