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
// through at least one of the given connector types. The only boundary policed
// is gRPC: proto frames and JSON bodies are different codecs and nothing
// translates between them, so a gRPC request needs a grpc connector and a
// non-gRPC request needs any other connector. Within the HTTP family the shapes
// are deliberately not checked - one connector may serve two forms (tendermint),
// a spec may declare one form and ship another, and method translators rewrite
// a JSON-RPC method into a REST call right before the connector.
func (r RequestType) CanBeServedBy(connectorTypes []specs.ApiConnectorType) bool {
	switch r {
	case Grpc:
		return slices.Contains(connectorTypes, specs.GrpcConnector)
	case JsonRpc, Ws, Rest:
		return slices.ContainsFunc(connectorTypes, func(connectorType specs.ApiConnectorType) bool {
			return connectorType != specs.GrpcConnector
		})
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
