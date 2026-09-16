package protocol

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/bytedance/sonic"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/ethereum/go-ethereum/crypto/blake2b"
)

const MethodSeparator = "#"

type HttpMethod int

const (
	Get HttpMethod = iota
	Post
)

func (h HttpMethod) String() string {
	switch h {
	case Post:
		return "POST"
	case Get:
		return "GET"
	}
	return ""
}

type RequestType int

const (
	Rest RequestType = iota
	JsonRpc
	Ws
	Grpc
	Unknown
)

func (r RequestType) String() string {
	switch r {
	case Rest:
		return "rest"
	case JsonRpc:
		return "json-rpc"
	case Ws:
		return "ws"
	case Unknown:
		return "unknown"
	case Grpc:
		return "grpc"
	}
	panic(fmt.Sprintf("unknown RequestType - %d", r))
}

// CanBeServedBy reports whether a request of this wire shape can be sent
// through at least one of the given connector types. A method spec binds a
// method to connector types; the request body is what the client sent, so a
// JSON-RPC body must never reach a grpc connector and proto bytes must never
// reach an HTTP one. The tendermint connector accepts both JSON-RPC and REST
// forms of the same method.
func (r RequestType) CanBeServedBy(connectorTypes []specs.ApiConnectorType) bool {
	return slices.ContainsFunc(connectorTypes, r.servedBy)
}

func (r RequestType) servedBy(connectorType specs.ApiConnectorType) bool {
	switch r {
	case JsonRpc, Ws:
		return connectorType == specs.JsonRpcConnector ||
			connectorType == specs.WebsocketConnector ||
			connectorType == specs.TendermintConnector
	case Rest:
		return connectorType == specs.RestConnector ||
			connectorType == specs.RestIndexer ||
			connectorType == specs.RestAdditional ||
			connectorType == specs.TendermintConnector
	case Grpc:
		return connectorType == specs.GrpcConnector
	default:
		return false
	}
}

func calculateHash(b []byte) string {
	hash := blake2b.Sum256(b)
	return hex.EncodeToString(hash[:])
}

func jsonRpcRequestBytes(id json.RawMessage, method string, params json.RawMessage) ([]byte, error) {
	request := newJsonRpcRequestBody(id, method, params)
	requestBytes, err := sonic.Marshal(request)
	if err != nil {
		return nil, err
	}
	return requestBytes, nil
}
