package evm_specific_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific/evm_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/eth_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestChainValidator(t *testing.T) {
	options := &chains.Options{
		DisableChainValidation:    new(true),
		DisableLogIndexValidation: new(true),
		ValidateCallLimit:         new(false),
	}
	connector := mocks.NewConnectorMock()
	chain := chains.GetChain("ethereum")

	validators := specific.NewEvmChainSpecific(context.Background(), "id", connector, nil, chain, 1*time.Second, options).SettingsValidators()

	assert.Len(t, validators, 0)

	options.DisableChainValidation = new(false)
	options.ValidateCallLimit = new(true)

	validators = specific.NewEvmChainSpecific(context.Background(), "id", connector, nil, chain, 1*time.Second, options).SettingsValidators()

	assert.Len(t, validators, 2)
	assert.True(t, lo.SomeBy(validators, func(item validations.Validator[validations.ValidationSettingResult]) bool {
		_, ok := item.(*eth_validations.EthChainValidator)
		return ok
	}))
	assert.True(t, lo.SomeBy(validators, func(item validations.Validator[validations.ValidationSettingResult]) bool {
		_, ok := item.(*eth_validations.EthCallLimitValidator)
		return ok
	}))
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

	expected := protocol.Block{
		Height:     uint64(69195275),
		Hash:       blockchain.NewHashIdFromString("0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"),
		ParentHash: blockchain.NewHashIdFromString("0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"),
		// the full header JSON is retained so newHeads can be served locally
		RawData: body,
	}

	assert.Equal(t, expected, block)
}

func TestEvmParseBlockHasNoRawData(t *testing.T) {
	body := []byte(`{
      "number": "0x41fd60b",
      "hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18",
	  "parentHash": "0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"
    }`)

	block, err := test_utils.NewEvmChainSpecific(nil).ParseBlock(body)
	assert.Nil(t, err)
	assert.Nil(t, block.RawData)
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

	expected := protocol.Block{
		Height:     uint64(69195275),
		Hash:       blockchain.NewHashIdFromString("0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"),
		ParentHash: blockchain.NewHashIdFromString("0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"),
	}
	assert.Equal(t, expected, block)
}

func TestEvmGetLatestBlockWithError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "block error", nil))

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewEvmChainSpecific(connector).GetLatestBlock(ctx)

	connector.AssertExpectations(t)
	assert.True(t, block.IsFullEmpty())

	var upErr *protocol.ResponseError
	ok := errors.As(err, &upErr)
	assert.True(t, ok)

	assert.Equal(t, 1, upErr.Code)
	assert.Equal(t, "block error", upErr.Message)
}

func TestEvmLowerBoundProcessor(t *testing.T) {
	processor := test_utils.NewEvmChainSpecific(mocks.NewConnectorMock()).LowerBoundProcessor()

	assert.NotNil(t, processor)
}

func TestEvmLowerBoundProcessorIncludesProofDetectorWhenSpecSupportsGetProof(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	processor := newEvmChainSpecificForChain("ethereum").LowerBoundProcessor()

	assert.Equal(t, 5, lowerBoundDetectorCount(t, processor))
}

func TestEvmLowerBoundProcessorSkipsProofDetectorWhenSpecDisablesGetProof(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	for _, chainName := range []string{"viction", "viction-testnet", "hyperliquid", "hyperliquid-testnet"} {
		t.Run(chainName, func(t *testing.T) {
			processor := newEvmChainSpecificForChain(chainName).LowerBoundProcessor()

			assert.Equal(t, 4, lowerBoundDetectorCount(t, processor))
		})
	}
}

func newEvmChainSpecificForChain(chainName string) *specific.EvmChainSpecificObject {
	return specific.NewEvmChainSpecific(
		context.Background(),
		"id",
		mocks.NewConnectorMock(),
		nil,
		chains.GetChain(chainName),
		time.Second,
		&chains.Options{InternalTimeout: time.Second},
	)
}

func lowerBoundDetectorCount(t *testing.T, processor lower_bounds.LowerBoundProcessor) int {
	t.Helper()
	base, ok := processor.(*lower_bounds.BaseLowerBoundProcessor)
	require.True(t, ok)

	detectors := reflect.ValueOf(base).Elem().FieldByName("lowerBoundsDetectors")
	require.True(t, detectors.IsValid())
	return detectors.Len()
}

func TestEvmGetSafeBlockUsesSafeTag(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{
	  "jsonrpc": "2.0",
	  "result": {
		"number": "0x5a",
		"hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18",
		"parentHash": "0x1eeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d11"
	  }
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	connector.On("SendRequest", ctx, mock.MatchedBy(func(request protocol.RequestHolder) bool {
		reqBody, err := request.Body()
		return err == nil && assert.JSONEq(t, `{"id":"1","jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["safe",false]}`, string(reqBody))
	})).Return(response)

	block, err := test_utils.NewEvmChainSpecific(connector).GetSafeBlock(ctx)

	assert.NoError(t, err)
	connector.AssertExpectations(t)
	assert.Equal(t, uint64(90), block.Height)
}
