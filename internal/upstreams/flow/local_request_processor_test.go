package flow_test

import (
	"bytes"
	"context"
	"fmt"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams/flow"
	"github.com/drpcorg/dsheltie/pkg/chains"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestLocalRequestProcessorUnsubscribe(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs")
	err := specs.Load()
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
	t.Setenv(specs.SpecPathVar, "test_specs")
	err := specs.Load()
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
