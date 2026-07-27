package flow

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func bitcoinJsonRpcRequest(t *testing.T, method string, params string) protocol.RequestHolder {
	t.Helper()
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	body := protocol.JsonRpcRequestBody{Id: []byte(`5`), Method: method, Params: json.RawMessage(params)}
	return protocol.NewUpstreamJsonRpcRequest("223", body, false, "bitcoin")
}

func TestGetBlockNumberTranslatesToGetBlockCount(t *testing.T) {
	request := bitcoinJsonRpcRequest(t, "getblocknumber", `[]`)
	translator := getMethodTranslator("bitcoin", "getblocknumber")
	require.NotNil(t, translator)

	translated, err := translator.TranslateRequest(context.Background(), request)

	require.NoError(t, err)
	assert.Equal(t, "getblockcount", translated.Method())
	assert.Equal(t, "223", translated.Id())
	body, err := translated.Body()
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":5,"jsonrpc":"2.0","method":"getblockcount","params":[]}`, string(body))

	response := protocol.NewSimpleHttpUpstreamResponse("223", []byte(`850000`), protocol.JsonRpc)
	assert.Same(t, response, translator.TranslateResponse(request, translated, 850_000, response))
}

func TestListUnspentTranslatesToEsploraUtxoRequest(t *testing.T) {
	translator := getMethodTranslator("bitcoin", "listunspent")
	require.NotNil(t, translator)

	for name, params := range map[string]string{
		"bitcoind style":  `[1, 9999999, ["bc1qaddress"]]`,
		"address in args": `[1, 9999999, "bc1qaddress"]`,
		"single address":  `["bc1qaddress"]`,
	} {
		t.Run(name, func(t *testing.T) {
			request := bitcoinJsonRpcRequest(t, "listunspent", params)

			translated, err := translator.TranslateRequest(context.Background(), request)

			require.NoError(t, err)
			assert.Equal(t, "GET#/address/*/utxo", translated.Method())
			assert.Equal(t, protocol.Rest, translated.RequestType())
			assert.Equal(t, "223", translated.Id())
			require.NotNil(t, translated.RequestParams())
			assert.Equal(t, []string{"bc1qaddress"}, translated.RequestParams().PathParams)
		})
	}
}

func TestListUnspentInvalidParams(t *testing.T) {
	translator := getMethodTranslator("bitcoin", "listunspent")
	require.NotNil(t, translator)

	for name, params := range map[string]string{
		"no params":          `[]`,
		"no address":         `[1, 9999999]`,
		"multiple addresses": `[1, 9999999, ["addr1", "addr2"]]`,
		"non-string address": `[1, 9999999, [42]]`,
		"not an array":       `{"minconf": 1}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := bitcoinJsonRpcRequest(t, "listunspent", params)

			_, err := translator.TranslateRequest(context.Background(), request)

			require.Error(t, err)
			respErr, ok := err.(*protocol.ResponseError)
			require.True(t, ok)
			assert.Equal(t, protocol.InvalidParams, respErr.Code)
		})
	}
}

func TestListUnspentReshapesEsploraResponse(t *testing.T) {
	translator := getMethodTranslator("bitcoin", "listunspent")
	require.NotNil(t, translator)
	request := bitcoinJsonRpcRequest(t, "listunspent", `[1, 9999999, ["bc1qaddress"]]`)
	translated, err := translator.TranslateRequest(context.Background(), request)
	require.NoError(t, err)

	esploraBody := `[
		{"txid":"aa11","vout":1,"status":{"confirmed":true,"block_height":849000,"block_hash":"00","block_time":1},"value":927},
		{"txid":"bb22","vout":0,"status":{"confirmed":true,"block_height":850000},"value":150000000},
		{"txid":"cc33","vout":2,"status":{"confirmed":false},"value":100000000}
	]`
	response := protocol.NewSimpleHttpUpstreamResponse("223", []byte(esploraBody), protocol.Rest)

	reshaped := translator.TranslateResponse(request, translated, 850_000, response)

	require.False(t, reshaped.HasError())
	assert.Equal(t, "223", reshaped.Id())
	assert.JSONEq(t, `[
		{"txid":"aa11","vout":1,"address":"bc1qaddress","amount":0.00000927,"confirmations":1001},
		{"txid":"bb22","vout":0,"address":"bc1qaddress","amount":1.50000000,"confirmations":1},
		{"txid":"cc33","vout":2,"address":"bc1qaddress","amount":1.00000000,"confirmations":0}
	]`, string(reshaped.ResponseResult()))
	assert.Contains(t, string(reshaped.ResponseResult()), `"amount":0.00000927`)
}

func TestListUnspentPassesThroughUpstreamError(t *testing.T) {
	translator := getMethodTranslator("bitcoin", "listunspent")
	require.NotNil(t, translator)
	request := bitcoinJsonRpcRequest(t, "listunspent", `[1, 9999999, ["bc1qaddress"]]`)
	translated, err := translator.TranslateRequest(context.Background(), request)
	require.NoError(t, err)

	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("esplora is down"))

	assert.Same(t, response, translator.TranslateResponse(request, translated, 850_000, response))
}

func TestSatsToBtc(t *testing.T) {
	assert.Equal(t, "0.00000927", string(satsToBtc(927)))
	assert.Equal(t, "0.00000000", string(satsToBtc(0)))
	assert.Equal(t, "1.00000000", string(satsToBtc(100_000_000)))
	assert.Equal(t, "21.12345678", string(satsToBtc(2_112_345_678)))
}

func bitcoinTestUpstream(connector connectors.ApiConnector, headHeight uint64) *upstreams.BaseUpstream {
	upState := utils.NewAtomic[protocol.UpstreamState]()
	state := protocol.DefaultUpstreamState(mocks.NewMethodsMock(), mapset.NewThreadUnsafeSet[protocol.Cap](), "00012", nil, nil)
	state.HeadData = protocol.NewBlockWithHeight(headHeight)
	upState.Store(state)

	return upstreams.NewBaseUpstreamWithParams(
		"id",
		chains.BITCOIN,
		[]connectors.ApiConnector{connector},
		&config.Upstream{Id: "id", PollInterval: 10 * time.Millisecond, Options: &chains.Options{InternalTimeout: 5 * time.Second}},
		"00012",
		upState,
		nil,
		nil,
		nil,
	)
}

func TestUnaryRequestProcessorTranslatesListUnspent(t *testing.T) {
	request := bitcoinJsonRpcRequest(t, "listunspent", `[1, 9999999, ["bc1qaddress"]]`)

	apiConnector := mocks.NewConnectorMockWithType(specs.RestAdditional)
	upstream := bitcoinTestUpstream(apiConnector, 850_000)
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()

	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		return req.Method() == "GET#/address/*/utxo" &&
			req.RequestType() == protocol.Rest &&
			req.RequestParams() != nil &&
			len(req.RequestParams().PathParams) == 1 &&
			req.RequestParams().PathParams[0] == "bc1qaddress"
	})).Return(protocol.NewSimpleHttpUpstreamResponse("223", []byte(`[{"txid":"aa11","vout":1,"status":{"confirmed":true,"block_height":849000},"value":927}]`), protocol.Rest))

	processor := NewUnaryRequestProcessor(chains.BITCOIN, upSupervisor)
	response := processor.ProcessRequest(context.Background(), strategy, request)

	unaryRespWrapper := response.(*UnaryResponse).ResponseWrapper
	apiConnector.AssertExpectations(t)
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	assert.Equal(t, "id", unaryRespWrapper.UpstreamId)
	require.False(t, unaryRespWrapper.Response.HasError())
	assert.JSONEq(t, `[{"txid":"aa11","vout":1,"address":"bc1qaddress","amount":0.00000927,"confirmations":1001}]`, string(unaryRespWrapper.Response.ResponseResult()))
}

func TestUnaryRequestProcessorTranslatesGetBlockNumber(t *testing.T) {
	request := bitcoinJsonRpcRequest(t, "getblocknumber", `[]`)

	apiConnector := mocks.NewConnectorMockWithType(specs.JsonRpcConnector)
	upstream := bitcoinTestUpstream(apiConnector, 850_000)
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()

	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)
	apiConnector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		body, err := req.Body()
		return err == nil && req.Method() == "getblockcount" && string(body) == `{"id":5,"jsonrpc":"2.0","method":"getblockcount","params":[]}`
	})).Return(protocol.NewSimpleHttpUpstreamResponse("223", []byte(`850000`), protocol.JsonRpc))

	processor := NewUnaryRequestProcessor(chains.BITCOIN, upSupervisor)
	response := processor.ProcessRequest(context.Background(), strategy, request)

	unaryRespWrapper := response.(*UnaryResponse).ResponseWrapper
	apiConnector.AssertExpectations(t)
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	require.False(t, unaryRespWrapper.Response.HasError())
	assert.Equal(t, `850000`, string(unaryRespWrapper.Response.ResponseResult()))
}

func TestUnaryRequestProcessorListUnspentInvalidParamsNoUpstreamCall(t *testing.T) {
	request := bitcoinJsonRpcRequest(t, "listunspent", `[1, 9999999]`)

	apiConnector := mocks.NewConnectorMockWithType(specs.RestAdditional)
	upstream := bitcoinTestUpstream(apiConnector, 850_000)
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	strategy := mocks.NewMockStrategy()

	upSupervisor.On("GetExecutor").Return(test_utils.CreateExecutor())
	strategy.On("SelectUpstream", request).Return("id", nil)
	upSupervisor.On("GetUpstream", "id").Return(upstream)

	processor := NewUnaryRequestProcessor(chains.BITCOIN, upSupervisor)
	response := processor.ProcessRequest(context.Background(), strategy, request)

	unaryRespWrapper := response.(*UnaryResponse).ResponseWrapper
	apiConnector.AssertNotCalled(t, "SendRequest")
	assert.Equal(t, "223", unaryRespWrapper.RequestId)
	require.True(t, unaryRespWrapper.Response.HasError())
	assert.Equal(t, protocol.InvalidParams, unaryRespWrapper.Response.GetError().Code)
}
