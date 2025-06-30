package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/internal/upstreams/methods"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/drpcorg/dsheltie/pkg/errors_config"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"io"
	"math"
)

func IsRetryable(response ResponseHolder) bool {
	shouldRetry := false

	switch resp := response.(type) {
	case *BaseUpstreamResponse:
		shouldRetry = response.HasError() && errors_config.IsRetryable(response.GetError().Message)
	case *ReplyError:
		shouldRetry = resp.ErrorKind == PartialFailure
	}

	return shouldRetry
}

func IsStream(method string) bool {
	// TODO: implement logic to determine if a method is streaming or not
	return method == "eth_getLogs" || method == "getProgramAccounts"
}

type ResponseErrorKind int

const (
	PartialFailure ResponseErrorKind = iota
	TotalFailure
)

type BlockType int

const (
	FinalizedBlock BlockType = iota
)

type ApiConnectorType int

const (
	JsonRpcConnector ApiConnectorType = iota
	RestConnector
	GrpcConnector
	WsConnector
)

func (a ApiConnectorType) String() string {
	switch a {
	case JsonRpcConnector:
		return "JsonRpc"
	case RestConnector:
		return "REST"
	case GrpcConnector:
		return "GRPC"
	case WsConnector:
		return "WS"
	default:
		panic(fmt.Sprintf("unknown connector type %d", a))
	}
}

type JsonRpcRequestBody struct {
	Id      json.RawMessage `json:"id"`
	Jsonrpc string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func newJsonRpcRequestBody(id json.RawMessage, method string, params json.RawMessage) *JsonRpcRequestBody {
	return &JsonRpcRequestBody{
		Id:      id,
		Method:  method,
		Params:  params,
		Jsonrpc: "2.0",
	}
}

type RequestHolder interface {
	Id() string
	Method() string
	Headers() map[string]string
	Body() []byte
	ParseParams(ctx context.Context, method *specs.Method) specs.MethodParam
	IsStream() bool
	IsSubscribe() bool
	RequestType() RequestType
	RequestHash() string
}

type ResponseHolder interface {
	ResponseResult() []byte
	GetError() *ResponseError
	EncodeResponse(realId []byte) io.Reader
	HasError() bool
	HasStream() bool
	Id() string
}

type UpstreamSubscriptionResponse interface {
	ResponseChan() chan *WsResponse
}

type ResponseHolderWrapper struct {
	UpstreamId string
	RequestId  string
	Response   ResponseHolder
}

type AvailabilityStatus int

const (
	Available AvailabilityStatus = iota
	Unavailable

	UnknownStatus = math.MaxInt
)

func (a AvailabilityStatus) String() string {
	switch a {
	case Available:
		return "AVAILABLE"
	case Unavailable:
		return "UNAVAILABLE"
	case UnknownStatus:
		return "UNKNOWN"
	default:
		panic(fmt.Sprintf("unknown status %d", a))
	}
}

type UpstreamEvent struct {
	Id    string
	Chain chains.Chain
	State *UpstreamState
}

type Cap int

const (
	WsCap Cap = iota
)

type UpstreamState struct {
	Status          AvailabilityStatus
	HeadData        *BlockData
	UpstreamMethods methods.Methods
	BlockInfo       *BlockInfo
	Caps            mapset.Set[Cap]
}

func DefaultUpstreamState(upstreamMethods methods.Methods, caps mapset.Set[Cap]) UpstreamState {
	return UpstreamState{
		Status:          Available,
		UpstreamMethods: upstreamMethods,
		BlockInfo:       NewBlockInfo(),
		Caps:            caps,
		HeadData:        &BlockData{},
	}
}

type BlockInfo struct {
	blocks *utils.CMap[BlockType, BlockData]
}

func NewBlockInfo() *BlockInfo {
	return &BlockInfo{
		blocks: utils.NewCMap[BlockType, BlockData](),
	}
}

func (b *BlockInfo) GetBlocks() map[BlockType]*BlockData {
	blocks := map[BlockType]*BlockData{}

	b.blocks.Range(func(key BlockType, val *BlockData) bool {
		blocks[key] = val
		return true
	})

	return blocks
}

func (b *BlockInfo) AddBlock(data *BlockData, blockType BlockType) {
	b.blocks.Store(blockType, data)
}

func (b *BlockInfo) GetBlock(blockType BlockType) *BlockData {
	block, ok := b.blocks.Load(blockType)
	if !ok {
		return &BlockData{}
	}
	return block
}

type AbstractUpstreamStateEvent interface {
	event()
}

type HeadUpstreamStateEvent struct {
	HeadData *BlockData
}

func (h *HeadUpstreamStateEvent) event() {}

type BlockUpstreamStateEvent struct {
	BlockData *BlockData
	BlockType BlockType
}

func (f *BlockUpstreamStateEvent) event() {
}
