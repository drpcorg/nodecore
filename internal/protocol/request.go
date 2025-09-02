package protocol

import (
	"encoding/json"
	"fmt"

	"github.com/bytedance/sonic"
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

func calculateHash(bytes []byte) string {
	hash := blake2b.Sum256(bytes)
	return fmt.Sprintf("%x", hash)
}

func jsonRpcRequestBytes(id json.RawMessage, method string, params json.RawMessage) ([]byte, error) {
	request := newJsonRpcRequestBody(id, method, params)
	requestBytes, err := sonic.Marshal(request)
	if err != nil {
		return nil, err
	}
	return requestBytes, nil
}
