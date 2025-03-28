package methods_test

import (
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dshaltie/internal/config"
	"github.com/drpcorg/dshaltie/internal/upstreams/methods"
	"github.com/drpcorg/dshaltie/pkg/chains"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestDefaultEthereumMethodsAreCallable(t *testing.T) {
	defaultEthMethods := []string{
		"eth_gasPrice",
		"eth_estimateGas",
		"eth_getTransactionByHash",
		"eth_getTransactionReceipt",
		"eth_getBlockTransactionCountByHash",
		"eth_getBlockByHash",
		"eth_getBlockByNumber",
		"eth_getTransactionByBlockHashAndIndex",
		"eth_getTransactionByBlockNumberAndIndex",
		"eth_getUncleByBlockHashAndIndex",
		"eth_getUncleCountByBlockHash",
		"eth_call",
		"eth_getStorageAt",
		"eth_getCode",
		"eth_getLogs",
		"eth_maxPriorityFeePerGas",
		"eth_getProof",
		"eth_createAccessList",
		"eth_getBlockReceipts",
		"eth_getTransactionCount",
		"eth_blockNumber",
		"eth_getBalance",
		"eth_sendRawTransaction",
		"eth_getBlockTransactionCountByNumber",
		"eth_getUncleCountByBlockNumber",
		"eth_getUncleByBlockNumberAndIndex",
		"eth_feeHistory",
		"net_version",
		"net_peerCount",
		"net_listening",
		"web3_clientVersion",
		"eth_protocolVersion",
		"eth_syncing",
		"eth_coinbase",
		"eth_mining",
		"eth_hashrate",
		"eth_accounts",
		"eth_chainId",
	}

	ethMethods := methods.NewEthereumLikeMethods(chains.ETHEREUM)

	for _, method := range defaultEthMethods {
		t.Run(fmt.Sprintf("check %s", method), func(te *testing.T) {
			assert.True(te, ethMethods.HasMethod(method))
		})
	}
}

func TestEthMethodGroups(t *testing.T) {
	tests := []struct {
		name    string
		methods mapset.Set[string]
		group   string
		chain   chains.Chain
	}{
		{
			name:  "filter methods",
			group: "filter",
			methods: mapset.NewThreadUnsafeSet[string](
				"eth_getFilterChanges",
				"eth_getFilterLogs",
				"eth_uninstallFilter",
				"eth_newFilter",
				"eth_newBlockFilter",
				"eth_newPendingTransactionFilter",
			),
			chain: chains.ETHEREUM,
		},
		{
			name:  "trace methods",
			group: "trace",
			methods: mapset.NewThreadUnsafeSet[string](
				"trace_call",
				"trace_callMany",
				"trace_rawTransaction",
				"trace_replayBlockTransactions",
				"trace_replayTransaction",
				"trace_block",
				"trace_filter",
				"trace_get",
				"trace_transaction",
			),
			chain: chains.ETHEREUM,
		},
		{
			name:  "arbitrum trace methods",
			group: "trace",
			methods: mapset.NewThreadUnsafeSet[string](
				"arbtrace_call",
				"arbtrace_callMany",
				"arbtrace_replayBlockTransactions",
				"arbtrace_replayTransaction",
				"arbtrace_block",
				"arbtrace_filter",
				"arbtrace_get",
				"arbtrace_transaction",
			),
			chain: chains.ARBITRUM,
		},
		{
			name:  "debug methods",
			group: "debug",
			methods: mapset.NewThreadUnsafeSet[string](
				"debug_storageRangeAt",
				"debug_traceBlock",
				"debug_traceBlockByHash",
				"debug_traceBlockByNumber",
				"debug_traceCall",
				"debug_traceCallMany",
				"debug_traceTransaction",
			),
			chain: chains.ETHEREUM,
		},
	}

	for _, test := range tests {
		ethLikeMethods := methods.NewEthereumLikeMethods(test.chain)
		t.Run(test.name, func(te *testing.T) {
			assert.Equal(te, ethLikeMethods.GetGroupMethods(test.group), test.methods)
		})
	}
}

func TestChangingReceivedMethodsNotChangeMethodsGlobally(t *testing.T) {
	tests := []struct {
		name    string
		methods methods.Methods
	}{
		{
			name:    "test eth-like methods",
			methods: methods.NewEthereumLikeMethods(chains.ETHEREUM),
		},
		{
			name:    "test solana methods",
			methods: methods.NewSolanaMethods(),
		},
		{
			name:    "test upstream methods",
			methods: methods.NewUpstreamMethods(methods.NewEthereumLikeMethods(chains.ETHEREUM), &config.MethodsConfig{}),
		},
		{
			name:    "test chain methods",
			methods: methods.NewChainMethods([]methods.Methods{methods.NewSolanaMethods()}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			supportedMethods := test.methods.GetSupportedMethods()
			supportedMethods.Add("newMethod")

			assert.NotEqual(t, test.methods.GetSupportedMethods(), supportedMethods)
		})
	}
}
