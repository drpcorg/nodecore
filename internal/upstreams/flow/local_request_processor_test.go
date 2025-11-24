package flow_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
)

func TestLocalRequestProcessorUnsubscribe(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs")).Load()
	assert.NoError(t, err)
	subCtx := flow.NewSubCtx()
	processor := flow.NewLocalRequestProcessor(chains.POLYGON, subCtx)
	subId := "0x112"
	ctx, cancel := context.WithCancel(context.Background())
	request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_unsubscribe", []byte(fmt.Sprintf(`["%s"]`, subId)), false, nil)
	subCtx.AddSub(subId, cancel)

	response := processor.ProcessRequest(ctx, nil, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)

	unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper

	assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.False(t, unaryRespWrapper.Response.HasError())
	assert.False(t, unaryRespWrapper.Response.HasStream())
	assert.Nil(t, unaryRespWrapper.Response.GetError())
	assert.True(t, bytes.Equal(flow.ResultTrue, unaryRespWrapper.Response.ResponseResult()))
	assert.False(t, subCtx.Exists(subId))
}

func TestLocalRequestProcessorCantParseUnsubReqThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs")).Load()
	assert.NoError(t, err)
	subCtx := flow.NewSubCtx()
	processor := flow.NewLocalRequestProcessor(chains.POLYGON, subCtx)
	subId := "0x112"
	ctx, cancel := context.WithCancel(context.Background())
	request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), "eth_unsubscribe", nil, false, nil)
	subCtx.AddSub(subId, cancel)

	response := processor.ProcessRequest(ctx, nil, request)

	assert.IsType(t, &flow.UnaryResponse{}, response)

	unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper

	assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.True(t, unaryRespWrapper.Response.HasError())
	assert.False(t, unaryRespWrapper.Response.HasStream())
	assert.ErrorContains(t, unaryRespWrapper.Response.GetError(), "internal server error")
	assert.True(t, subCtx.Exists(subId))
}

func TestLocalRequestProcessorNoLocalHandlerError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs")).Load()
	assert.NoError(t, err)

	tests := []struct {
		name   string
		method string
		errMsg string
	}{
		{
			name:   "no local method",
			method: "eth_blockNumber",
			errMsg: "method 'eth_blockNumber' is not local",
		},
		{
			name:   "no local handler",
			method: "super_method",
			errMsg: "there is no local handler for method 'super_method'",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			processor := flow.NewLocalRequestProcessor(chains.ETHEREUM, nil)
			ctx := context.Background()
			request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), test.method, nil, false, nil)

			response := processor.ProcessRequest(ctx, nil, request)

			assert.IsType(t, &flow.UnaryResponse{}, response)

			unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper

			assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
			assert.Equal(t, "223", unaryRespWrapper.RequestId)
			assert.True(t, unaryRespWrapper.Response.HasError())
			assert.False(t, unaryRespWrapper.Response.HasStream())
			assert.ErrorContains(t, unaryRespWrapper.Response.GetError(), test.errMsg)
		})
	}
}

func TestLocalRequestProcessorChainIdAndNetVersion(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs")).Load()
	assert.NoError(t, err)

	tests := []struct {
		name   string
		method string
		result []byte
	}{
		{
			name:   "chainId",
			method: specs.EthChainId,
			result: []byte(`"0x1"`),
		},
		{
			name:   "chainId",
			method: specs.NetVersion,
			result: []byte(`"1"`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			processor := flow.NewLocalRequestProcessor(chains.ETHEREUM, nil)
			ctx := context.Background()
			request := protocol.NewUpstreamJsonRpcRequest("223", []byte(`1`), test.method, nil, false, nil)

			response := processor.ProcessRequest(ctx, nil, request)

			assert.IsType(t, &flow.UnaryResponse{}, response)

			unaryRespWrapper := response.(*flow.UnaryResponse).ResponseWrapper

			assert.Equal(t, flow.NoUpstream, unaryRespWrapper.UpstreamId)
			assert.Equal(t, "223", unaryRespWrapper.RequestId)
			assert.False(t, unaryRespWrapper.Response.HasError())
			assert.False(t, unaryRespWrapper.Response.HasStream())
			assert.Nil(t, unaryRespWrapper.Response.GetError())
			assert.True(t, bytes.Equal(test.result, unaryRespWrapper.Response.ResponseResult()))
		})
	}
}
