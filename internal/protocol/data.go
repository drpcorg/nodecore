package protocol

import (
	"encoding/json"
	"fmt"
	"github.com/drpcorg/dsheltie/internal/upstreams/methods"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"io"
	"math"
)

func IsStream(method string) bool {
	// TODO: implement logic to determine if a method is streaming or not
	return method == "eth_getLogs" || method == "getProgramAccounts"
}

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
	IsStream() bool
	Count() int
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

type UpstreamState struct {
	Status          AvailabilityStatus
	HeadData        *BlockData
	UpstreamMethods methods.Methods
	BlockInfo       *BlockInfo
}

func DefaultUpstreamState(upstreamMethods methods.Methods) UpstreamState {
	return UpstreamState{
		Status:          Available,
		UpstreamMethods: upstreamMethods,
		BlockInfo:       NewBlockInfo(),
	}
}

type BlockInfo struct {
	blocks utils.CMap[BlockType, BlockData]
}

func NewBlockInfo() *BlockInfo {
	return &BlockInfo{
		blocks: utils.CMap[BlockType, BlockData]{},
	}
}

func (b *BlockInfo) AddBlock(data *BlockData, blockType BlockType) {
	b.blocks.Store(blockType, data)
}

func (b *BlockInfo) GetBlock(blockType BlockType) *BlockData {
	block, _ := b.blocks.Load(blockType)
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
