package main

import (
	"strings"

	"github.com/drpcorg/nodecore/pkg/chains"
)

// resolveChain finds the chain a dshackle config names and returns nodecore's canonical
// spelling of it. dshackle matches a chain name case-insensitively, treats '_' as '-' and
// accepts any alias; nodecore matches exactly and ties chain-defaults to an upstream by the
// very same string, so both sides are written with the chain's first short name.
//
// ok is false when nodecore does not serve the chain: it is not in the registry, or it is
// there without a method spec.
func resolveChain(name string) (canonical string, chain *chains.ConfiguredChain, ok bool) {
	normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "_", "-")
	found := chains.GetChain(normalized)
	if found == chains.UnknownChain || found.MethodSpec == "" || len(found.ShortNames) == 0 {
		return "", nil, false
	}
	return found.ShortNames[0], found, true
}

// primaryConnectorType is the nodecore connector type of a dshackle `rpc.url`. dshackle puts
// every endpoint under `rpc` and decides the protocol from the chain family; nodecore wants
// the type spelled out.
func primaryConnectorType(chainType chains.BlockchainType) string {
	switch chainType {
	case chains.EthereumBeaconChain, chains.Algorand, chains.Ton:
		return "rest"
	case chains.Cosmos:
		return "tendermint"
	default:
		return "json-rpc"
	}
}
