package flow_test

import (
	"context"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestNoopIntegrityHandler(t *testing.T) {
	handler := flow.NewNoopIntegrityHandler()

	result := handler.CanBeProcessed(context.Background(), nil)
	assert.False(t, result)

	shouldSentReq, ups, block := handler.HandleResponse(context.Background(), nil, nil, nil)
	assert.False(t, shouldSentReq)
	assert.Nil(t, ups)
	assert.Nil(t, block)
}

func TestHandlersProcessed(t *testing.T) {

	tests := []struct {
		name    string
		result  bool
		method  string
		handler flow.IntegrityHandler
	}{
		{
			name:    "success blockNumber processed",
			result:  true,
			method:  specs.EthBlockNumber,
			handler: flow.NewEthBlockNumberIntegrityHandler(),
		},
		{
			name:    "failure blockNumber processed",
			result:  false,
			method:  "other",
			handler: flow.NewEthBlockNumberIntegrityHandler(),
		},
		{
			name:    "success getBlockByNumber processed",
			result:  true,
			method:  specs.EthGetBlockByNumber,
			handler: flow.NewEthGetBlockByNumberIntegrityHandler(),
		},
		{
			name:    "failure getBlockByNumber processed",
			result:  false,
			method:  "other",
			handler: flow.NewEthGetBlockByNumberIntegrityHandler(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			req, _ := protocol.NewInternalUpstreamJsonRpcRequest(test.method, nil)
			result := test.handler.CanBeProcessed(context.Background(), req)
			assert.Equal(te, test.result, result)
		})
	}
}

func TestEthBlockNumberIntegrityHandlerNotHandledIfError(t *testing.T) {
	tests := []struct {
		name    string
		handler flow.IntegrityHandler
		method  string
	}{
		{
			name:    "error blockNumber",
			handler: flow.NewEthBlockNumberIntegrityHandler(),
			method:  specs.EthBlockNumber,
		},
		{
			name:    "error getBlockByNumber",
			handler: flow.NewEthGetBlockByNumberIntegrityHandler(),
			method:  specs.EthGetBlockByNumber,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			respErr := protocol.NoAvailableUpstreamsError()
			req, _ := protocol.NewInternalUpstreamJsonRpcRequest(test.method, nil)
			response := protocol.NewPartialFailure(req, respErr)
			respWrapper := &protocol.ResponseHolderWrapper{Response: response}

			shouldSentReq, ups, block := test.handler.HandleResponse(context.Background(), nil, req, respWrapper)
			assert.False(t, shouldSentReq)
			assert.Nil(t, ups)
			assert.Nil(t, block)
		})
	}
}

func TestEthBlockNumberIntegrityHandlerNotHandledIfCantParseNumber(t *testing.T) {
	req, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthBlockNumber, nil)
	response := protocol.NewSimpleHttpUpstreamResponse("22", []byte("false"), protocol.JsonRpc)
	respWrapper := &protocol.ResponseHolderWrapper{Response: response}
	handler := flow.NewEthBlockNumberIntegrityHandler()

	shouldSentReq, ups, block := handler.HandleResponse(context.Background(), nil, req, respWrapper)
	assert.False(t, shouldSentReq)
	assert.Nil(t, ups)
	assert.Nil(t, block)
}

func TestEthBlockNumberIntegrityHandlerResponseBlockIsGreaterThanHead(t *testing.T) {
	chainSupervisor, newMethods := createChainSupervisor()
	go chainSupervisor.Start()
	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Available, 100, newMethods))
	time.Sleep(3 * time.Millisecond)

	req, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthBlockNumber, nil)
	response := protocol.NewSimpleHttpUpstreamResponse("22", []byte(`"0x1111"`), protocol.JsonRpc)
	respWrapper := &protocol.ResponseHolderWrapper{Response: response}
	handler := flow.NewEthBlockNumberIntegrityHandler()

	shouldSentReq, ups, block := handler.HandleResponse(context.Background(), chainSupervisor, req, respWrapper)
	assert.False(t, shouldSentReq)
	assert.Nil(t, ups)
	assert.Equal(t, flow.NewHeadBlock(4369), block)
}

func TestEthBlockNumberIntegrityHandlerShouldSentRequest(t *testing.T) {
	chainSupervisor, newMethods := createChainSupervisor()
	go chainSupervisor.Start()
	chainSupervisor.Publish(test_utils.CreateEvent("22", protocol.Available, 5, newMethods))
	chainSupervisor.Publish(test_utils.CreateEvent("100", protocol.Available, 4, newMethods))
	chainSupervisor.Publish(test_utils.CreateEvent("23", protocol.Available, 108, newMethods))
	chainSupervisor.Publish(test_utils.CreateEvent("25", protocol.Available, 150, newMethods))
	time.Sleep(3 * time.Millisecond)

	req, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthBlockNumber, nil)
	response := protocol.NewSimpleHttpUpstreamResponse("22", []byte(`"0x5"`), protocol.JsonRpc)
	respWrapper := &protocol.ResponseHolderWrapper{Response: response, UpstreamId: "22"}
	handler := flow.NewEthBlockNumberIntegrityHandler()

	shouldSentReq, ups, block := handler.HandleResponse(context.Background(), chainSupervisor, req, respWrapper)
	assert.True(t, shouldSentReq)
	assert.Equal(t, []string{"25", "23"}, ups)
	assert.Nil(t, block)
}

func TestEthBlockNumberIntegrityHandlerNoUpstreams(t *testing.T) {
	chainSupervisor, newMethods := createChainSupervisor()
	go chainSupervisor.Start()
	chainSupervisor.Publish(test_utils.CreateEvent("22", protocol.Available, 10, newMethods))
	time.Sleep(3 * time.Millisecond)

	req, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthBlockNumber, nil)
	response := protocol.NewSimpleHttpUpstreamResponse("22", []byte(`"0x5"`), protocol.JsonRpc)
	respWrapper := &protocol.ResponseHolderWrapper{Response: response, UpstreamId: "22"}
	handler := flow.NewEthBlockNumberIntegrityHandler()

	shouldSentReq, ups, block := handler.HandleResponse(context.Background(), chainSupervisor, req, respWrapper)
	assert.True(t, shouldSentReq)
	assert.Equal(t, []string{}, ups)
	assert.Nil(t, block)
}

func TestEthGetBlockByNumberIntegrityHandlerNumberFieldErrors(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "no number",
			body: []byte(`{"key": "value"}`),
		},
		{
			name: "no string value",
			body: []byte(`{"number": false}`),
		},
		{
			name: "incorrect number",
			body: []byte(`{"number": "wrong"}`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			req, _ := protocol.NewInternalUpstreamJsonRpcRequest(specs.EthGetBlockByNumber, nil)
			response := protocol.NewSimpleHttpUpstreamResponse("22", test.body, protocol.JsonRpc)
			respWrapper := &protocol.ResponseHolderWrapper{Response: response, UpstreamId: "22"}
			handler := flow.NewEthGetBlockByNumberIntegrityHandler()

			shouldSentReq, ups, block := handler.HandleResponse(context.Background(), nil, req, respWrapper)
			assert.False(t, shouldSentReq)
			assert.Nil(t, ups)
			assert.Nil(t, block)
		})
	}
}

func TestEthGetBlockByNumberIntegrityHandlerLatestResponseBlockIsGreaterThanHead(t *testing.T) {
	tests := []struct {
		name   string
		events func(newMethods methods.Methods) []protocol.UpstreamEvent
		param  string
		block  flow.IntegrityBlock
	}{
		{
			name: "latest is greater than head",
			events: func(newMethods methods.Methods) []protocol.UpstreamEvent {
				return []protocol.UpstreamEvent{
					test_utils.CreateEvent("id", protocol.Available, 100, newMethods),
				}
			},
			param: "latest",
			block: flow.NewHeadBlock(4369),
		},
		{
			name: "finalized is greater than current finalization",
			events: func(newMethods methods.Methods) []protocol.UpstreamEvent {
				blockInfo := protocol.NewBlockInfo()
				blockInfo.AddBlock(protocol.NewBlockDataWithHeight(100), protocol.FinalizedBlock)
				return []protocol.UpstreamEvent{
					test_utils.CreateEventWithBlockData("id", protocol.Available, 100, newMethods, blockInfo),
				}
			},
			param: "finalized",
			block: flow.NewFinalizedBlock(4369),
		},
		{
			name: "finalized is greater than current finalization without blockInfo",
			events: func(newMethods methods.Methods) []protocol.UpstreamEvent {
				return []protocol.UpstreamEvent{
					test_utils.CreateEvent("id", protocol.Available, 100, newMethods),
				}
			},
			param: "finalized",
			block: flow.NewFinalizedBlock(4369),
		},
	}

	err := specs.NewMethodSpecLoader().Load()
	assert.Nil(t, err)

	spec := specs.GetSpecMethod("eth", specs.EthGetBlockByNumber)

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			chainSupervisor, newMethods := createChainSupervisor()
			go chainSupervisor.Start()
			for _, event := range test.events(newMethods) {
				chainSupervisor.Publish(event)
			}
			time.Sleep(3 * time.Millisecond)

			req, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod(specs.EthGetBlockByNumber, []any{test.param, false}, spec)
			response := protocol.NewSimpleHttpUpstreamResponse("22", []byte(`{"number": "0x1111"}`), protocol.JsonRpc)
			respWrapper := &protocol.ResponseHolderWrapper{Response: response}
			handler := flow.NewEthGetBlockByNumberIntegrityHandler()

			shouldSentReq, ups, block := handler.HandleResponse(context.Background(), chainSupervisor, req, respWrapper)
			assert.False(t, shouldSentReq)
			assert.Nil(t, ups)
			assert.Equal(t, test.block, block)
		})
	}
}

func TestEthGetBlockByNumberIntegrityHandlerShouldSentRequest(t *testing.T) {
	tests := []struct {
		name  string
		evens func(newMethods methods.Methods) []protocol.UpstreamEvent
		param string
	}{
		{
			name: "latest",
			evens: func(newMethods methods.Methods) []protocol.UpstreamEvent {
				return []protocol.UpstreamEvent{
					test_utils.CreateEvent("100", protocol.Available, 4, newMethods),
					test_utils.CreateEvent("22", protocol.Available, 5, newMethods),
					test_utils.CreateEvent("23", protocol.Available, 108, newMethods),
					test_utils.CreateEvent("25", protocol.Available, 150, newMethods),
				}
			},
			param: "latest",
		},
		{
			name: "finalized",
			evens: func(newMethods methods.Methods) []protocol.UpstreamEvent {
				blockInfo1 := protocol.NewBlockInfo()
				blockInfo1.AddBlock(protocol.NewBlockDataWithHeight(4), protocol.FinalizedBlock)
				blockInfo2 := protocol.NewBlockInfo()
				blockInfo2.AddBlock(protocol.NewBlockDataWithHeight(5), protocol.FinalizedBlock)
				blockInfo3 := protocol.NewBlockInfo()
				blockInfo3.AddBlock(protocol.NewBlockDataWithHeight(108), protocol.FinalizedBlock)
				blockInfo4 := protocol.NewBlockInfo()
				blockInfo4.AddBlock(protocol.NewBlockDataWithHeight(150), protocol.FinalizedBlock)
				return []protocol.UpstreamEvent{
					test_utils.CreateEventWithBlockData("100", protocol.Available, 4, newMethods, blockInfo1),
					test_utils.CreateEventWithBlockData("22", protocol.Available, 5, newMethods, blockInfo2),
					test_utils.CreateEventWithBlockData("23", protocol.Available, 108, newMethods, blockInfo3),
					test_utils.CreateEventWithBlockData("25", protocol.Available, 150, newMethods, blockInfo4),
				}
			},
			param: "finalized",
		},
	}

	err := specs.NewMethodSpecLoader().Load()
	assert.Nil(t, err)

	spec := specs.GetSpecMethod("eth", specs.EthGetBlockByNumber)

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			chainSupervisor, newMethods := createChainSupervisor()
			go chainSupervisor.Start()
			for _, event := range test.evens(newMethods) {
				chainSupervisor.Publish(event)
			}
			time.Sleep(3 * time.Millisecond)

			req, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod(specs.EthGetBlockByNumber, []any{test.param, false}, spec)
			response := protocol.NewSimpleHttpUpstreamResponse("22", []byte(`{"number": "0x15"}`), protocol.JsonRpc)
			respWrapper := &protocol.ResponseHolderWrapper{Response: response, UpstreamId: "22"}
			handler := flow.NewEthGetBlockByNumberIntegrityHandler()

			shouldSentReq, ups, block := handler.HandleResponse(context.Background(), chainSupervisor, req, respWrapper)
			assert.True(t, shouldSentReq)
			assert.Equal(t, []string{"25", "23"}, ups)
			assert.Nil(t, block)
		})
	}
}

func TestEthGetBlockByNumberIntegrityHandlerNumberTag(t *testing.T) {
	err := specs.NewMethodSpecLoader().Load()
	assert.Nil(t, err)

	spec := specs.GetSpecMethod("eth", specs.EthGetBlockByNumber)

	chainSupervisor, newMethods := createChainSupervisor()
	go chainSupervisor.Start()
	chainSupervisor.Publish(test_utils.CreateEvent("22", protocol.Available, 5, newMethods))
	chainSupervisor.Publish(test_utils.CreateEvent("23", protocol.Available, 108, newMethods))
	chainSupervisor.Publish(test_utils.CreateEvent("25", protocol.Available, 150, newMethods))
	time.Sleep(3 * time.Millisecond)

	req, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod(specs.EthBlockNumber, []any{"0x1555", false}, spec)
	response := protocol.NewSimpleHttpUpstreamResponse("22", []byte(`{"number": "0x1555"}`), protocol.JsonRpc)
	respWrapper := &protocol.ResponseHolderWrapper{Response: response, UpstreamId: "22"}
	handler := flow.NewEthGetBlockByNumberIntegrityHandler()

	shouldSentReq, ups, block := handler.HandleResponse(context.Background(), chainSupervisor, req, respWrapper)
	assert.False(t, shouldSentReq)
	assert.Nil(t, ups)
	assert.Equal(t, flow.NewHeadBlock(5461), block)
}

func createChainSupervisor() (*upstreams.ChainSupervisor, methods.Methods) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("method"))

	return chainSupervisor, methodsMock
}
