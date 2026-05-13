package chains

import (
	"fmt"
)

type BlockchainType string

const (
	Algorand            BlockchainType = "avm"
	Bitcoin             BlockchainType = "bitcoin"
	Cosmos              BlockchainType = "cosmos"
	Ethereum            BlockchainType = "eth"
	EthereumBeaconChain BlockchainType = "eth-beacon-chain"
	Near                BlockchainType = "near"
	Polkadot            BlockchainType = "polkadot"
	Solana              BlockchainType = "solana"
	Starknet            BlockchainType = "starknet"
	Ton                 BlockchainType = "ton"
	Aztec               BlockchainType = "aztec"
)

type ApiConnectorType int

const (
	UnknownType ApiConnectorType = iota
	JsonRpcConnector
	RestConnector
	GrpcConnector
	WebsocketConnector
)

func (a ApiConnectorType) String() string {
	switch a {
	case JsonRpcConnector:
		return "json-rpc"
	case RestConnector:
		return "rest"
	case GrpcConnector:
		return "grpc"
	case WebsocketConnector:
		return "websocket"
	case UnknownType:
		return "unknown"
	}
	return ""
}

var apiConnectors = map[string]ApiConnectorType{
	"json-rpc":  JsonRpcConnector,
	"rest":      RestConnector,
	"grpc":      GrpcConnector,
	"websocket": WebsocketConnector,
}

func GetApiConnectorType(name string) ApiConnectorType {
	connector, ok := apiConnectors[name]
	if !ok {
		return UnknownType
	}
	return connector
}

func ValidateApiConnectorType(connectorName string) error {
	_, ok := apiConnectors[connectorName]
	if !ok {
		return fmt.Errorf("invalid connector type - '%s'", connectorName)
	}
	return nil
}
