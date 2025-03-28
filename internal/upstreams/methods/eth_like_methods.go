package methods

import (
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dshaltie/pkg/chains"
)

var filterMethods = mapset.NewThreadUnsafeSet[string](
	"eth_getFilterChanges",
	"eth_getFilterLogs",
	"eth_uninstallFilter",
	"eth_newFilter",
	"eth_newBlockFilter",
	"eth_newPendingTransactionFilter",
)

var traceMethods = mapset.NewThreadUnsafeSet[string](
	"trace_call",
	"trace_callMany",
	"trace_rawTransaction",
	"trace_replayBlockTransactions",
	"trace_replayTransaction",
	"trace_block",
	"trace_filter",
	"trace_get",
	"trace_transaction",
)

var arbitrumTraceMethods = mapset.NewThreadUnsafeSet[string](
	"arbtrace_call",
	"arbtrace_callMany",
	"arbtrace_replayBlockTransactions",
	"arbtrace_replayTransaction",
	"arbtrace_block",
	"arbtrace_filter",
	"arbtrace_get",
	"arbtrace_transaction",
)

var debugMethods = mapset.NewThreadUnsafeSet[string](
	"debug_storageRangeAt",
	"debug_traceBlock",
	"debug_traceBlockByHash",
	"debug_traceBlockByNumber",
	"debug_traceCall",
	"debug_traceCallMany",
	"debug_traceTransaction",
)

var commonMethods = mapset.NewThreadUnsafeSet[string](
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
)

var harmonyMethods = mapset.NewThreadUnsafeSet[string](
	"hmy_getStakingTransactionByBlockHashAndIndex",
	"hmy_getStakingTransactionByHash",
	"hmy_getCXReceiptByHash",
	"hmy_getBlockTransactionCountByHash",
	"hmy_getTransactionByHash",
	"hmy_getTransactionByBlockHashAndIndex",
	"hmy_getBlockByHash",
	"hmy_getStakingTransactionByBlockNumberAndIndex",
	"hmy_getTransactionReceipt",
	"hmy_getBlockTransactionCountByNumber",
	"hmy_getTransactionByBlockNumberAndIndex",
	"hmy_getBlockByNumber",
	"hmy_getAllValidatorInformationByBlockNumber",
	"hmyv2_getStakingTransactionByBlockHashAndIndex",
	"hmyv2_getStakingTransactionByBlockNumberAndIndex",
	"hmyv2_getStakingTransactionByHash",
	"hmyv2_getTransactionReceipt",
	"hmyv2_getBlockTransactionCountByHash",
	"hmyv2_getBlockTransactionCountByNumber",
	"hmyv2_getTransactionByHash",
	"hmyv2_getTransactionByBlockNumberAndIndex",
	"hmyv2_getTransactionByBlockHashAndIndex",
	"hmyv2_getBlockByNumber",
	"hmyv2_getBlockByHash",
	"hmyv2_getCXReceiptByHash",
	"hmy_sendRawStakingTransaction",
	"hmy_sendRawTransaction",
	"hmy_getTransactionCount",
	"hmy_newFilter",
	"hmy_newBlockFilter",
	"hmy_newPendingTransactionFilter",
	"hmy_getFilterLogs",
	"hmy_getFilterChanges",
	"hmy_getPendingCrossLinks",
	"hmy_getCurrentTransactionErrorSink",
	"hmy_getPendingCXReceipts",
	"hmy_pendingTransactions",
	"hmy_getTransactionsHistory",
	"hmy_getBlocks",
	"hmy_getBalanceByBlockNumber",
	"hmy_getBalance",
	"hmy_getLogs",
	"hmy_estimateGas",
	"hmy_getStorageAt",
	"hmy_call",
	"hmy_getCode",
	"hmy_isLastBlock",
	"hmy_latestHeader",
	"hmy_blockNumber",
	"hmy_gasPrice",
	"hmy_getEpoch",
	"hmy_epochLastBlock",
	"hmy_getShardingStructure",
	"hmy_syncing",
	"hmy_getLeader",
	"hmy_getCirculatingSupply",
	"hmy_getTotalSupply",
	"hmy_getStakingNetworkInfo",
	"hmy_getAllValidatorInformation",
	"hmy_getDelegationsByValidator",
	"hmy_getDelegationsByDelegatorAndValidator",
	"hmy_getDelegationsByDelegator",
	"hmy_getValidatorMetrics",
	"hmy_getMedianRawStakeSnapshot",
	"hmy_getElectedValidatorAddresses",
	"hmy_getAllValidatorAddresses",
	"hmy_getCurrentStakingErrorSink",
	"hmy_getValidatorInformation",
	"hmy_getSignedBlocks",
	"hmy_getValidators",
	"hmy_isBlockSigner",
	"hmy_getBlockSigners",
	"hmyv2_getBalanceByBlockNumber",
	"hmyv2_getTransactionCount",
	"hmyv2_getBalance",
	"hmyv2_getCurrentTransactionErrorSink",
	"hmyv2_getPendingCrossLinks",
	"hmyv2_getPendingCXReceipts",
	"hmyv2_pendingTransactions",
	"hmyv2_getTransactionsHistory",
	"hmyv2_getBlocks",
	"hmyv2_blockNumber",
	"hmyv2_gasPrice",
	"hmyv2_getEpoch",
	"hmyv2_getValidators",
)

var klayMethods = mapset.NewThreadUnsafeSet[string](
	"klay_blockNumber",
	"klay_getBlockByHash",
	"klay_getBlockReceipts",
	"klay_getBlockTransactionCountByNumber",
	"klay_getBlockWithConsensusInfoByNumber",
	"klay_getBlockByNumber",
	"klay_getBlockTransactionCountByHash",
	"klay_getHeaderByNumber",
	"klay_getHeaderByHash",
	"klay_getBlockWithConsensusInfoByNumberRange",
	"klay_getBlockWithConsensusInfoByHash",
	"klay_getDecodedAnchoringTransactionByHash",
	"klay_getRawTransactionByBlockNumberAndIndex",
	"klay_getRawTransactionByBlockHashAndIndex",
	"klay_getRawTransactionByHash",
	"klay_getTransactionByBlockNumberAndIndex",
	"klay_getTransactionBySenderTxHash",
	"klay_getTransactionByBlockHashAndIndex",
	"klay_getTransactionByHash",
	"klay_getTransactionReceipt",
	"klay_getTransactionReceiptBySenderTxHash",
	"klay_sendRawTransaction",
	"klay_getTransactionCount",
	"klay_accountCreated",
	"klay_accounts",
	"klay_decodeAccountKey",
	"klay_getAccountKey",
	"klay_getCode",
	"klay_encodeAccountKey",
	"klay_getAccount",
	"klay_getAccount",
	"klay_sign",
	"klay_isContractAccount",
	"klay_getCommittee",
	"klay_getCommitteeSize",
	"klay_getCouncil",
	"klay_getCouncilSize",
	"klay_getRewards",
	"klay_getStorageAt",
	"klay_syncing",
	"klay_call",
	"klay_estimateGas",
	"klay_estimateComputationCost",
	"klay_pendingTransactions",
	"klay_createAccessList",
	"klay_resend",
	"klay_chainID",
	"klay_clientVersion",
	"klay_gasPriceAt",
	"klay_gasPrice",
	"klay_protocolVersion",
	"klay_getChainConfig",
	"klay_forkStatus",
	"klay_getFilterChanges",
	"klay_getFilterLogs",
	"klay_newBlockFilter",
	"klay_newPendingTransactionFilter",
	"klay_uninstallFilter",
	"klay_unsubscribe",
	"klay_getLogs",
	"klay_subscribe",
	"klay_newFilter",
	"klay_feeHistory",
	"klay_lowerBoundGasPrice",
	"klay_upperBoundGasPrice",
	"klay_maxPriorityFeePerGas",
	"klay_getStakingInfo",
	"klay_sha3",
	"klay_recoverFromTransaction",
	"klay_recoverFromMessage",
	"klay_getProof",
	"klay_nodeAddress",
	"kaia_blockNumber",
	"kaia_getBlockByHash",
	"kaia_getBlockReceipts",
	"kaia_getBlockTransactionCountByNumber",
	"kaia_getBlockWithConsensusInfoByNumber",
	"kaia_getBlockByNumber",
	"kaia_getBlockTransactionCountByHash",
	"kaia_getHeaderByNumber",
	"kaia_getHeaderByHash",
	"kaia_getBlockWithConsensusInfoByNumberRange",
	"kaia_getBlockWithConsensusInfoByHash",
	"kaia_getDecodedAnchoringTransactionByHash",
	"kaia_getRawTransactionByBlockNumberAndIndex",
	"kaia_getRawTransactionByBlockHashAndIndex",
	"kaia_getRawTransactionByHash",
	"kaia_getTransactionByBlockNumberAndIndex",
	"kaia_getTransactionBySenderTxHash",
	"kaia_getTransactionByBlockHashAndIndex",
	"kaia_getTransactionByHash",
	"kaia_getTransactionReceipt",
	"kaia_getTransactionReceiptBySenderTxHash",
	"kaia_sendRawTransaction",
	"kaia_getTransactionCount",
	"kaia_accountCreated",
	"kaia_accounts",
	"kaia_decodeAccountKey",
	"kaia_getAccountKey",
	"kaia_getCode",
	"kaia_encodeAccountKey",
	"kaia_getAccount",
	"kaia_getAccount",
	"kaia_sign",
	"kaia_isContractAccount",
	"kaia_getCommittee",
	"kaia_getCommitteeSize",
	"kaia_getCouncil",
	"kaia_getCouncilSize",
	"kaia_getRewards",
	"kaia_getStorageAt",
	"kaia_syncing",
	"kaia_call",
	"kaia_estimateGas",
	"kaia_estimateComputationCost",
	"kaia_pendingTransactions",
	"kaia_createAccessList",
	"kaia_resend",
	"kaia_chainID",
	"kaia_clientVersion",
	"kaia_gasPriceAt",
	"kaia_gasPrice",
	"kaia_protocolVersion",
	"kaia_getChainConfig",
	"kaia_forkStatus",
	"kaia_getFilterChanges",
	"kaia_getFilterLogs",
	"kaia_newBlockFilter",
	"kaia_newPendingTransactionFilter",
	"kaia_uninstallFilter",
	"kaia_unsubscribe",
	"kaia_getLogs",
	"kaia_subscribe",
	"kaia_newFilter",
	"kaia_feeHistory",
	"kaia_lowerBoundGasPrice",
	"kaia_upperBoundGasPrice",
	"kaia_maxPriorityFeePerGas",
	"kaia_getStakingInfo",
	"kaia_sha3",
	"kaia_recoverFromTransaction",
	"kaia_recoverFromMessage",
	"kaia_getProof",
	"kaia_nodeAddress",
)

var filecoinMethods = mapset.NewThreadUnsafeSet[string](
	"Filecoin.ChainBlockstoreInfo",
	"Filecoin.ChainExport",
	"Filecoin.ChainGetBlock",
	"Filecoin.ChainGetBlockMessages",
	"Filecoin.ChainGetEvents",
	"Filecoin.ChainGetGenesis",
	"Filecoin.ChainGetMessage",
	"Filecoin.ChainGetMessagesInTipset",
	"Filecoin.ChainGetNode",
	"Filecoin.ChainGetParentMessages",
	"Filecoin.ChainGetParentReceipts",
	"Filecoin.ChainGetPath",
	"Filecoin.ChainGetTipSet",
	"Filecoin.ChainGetTipSetAfterHeight",
	"Filecoin.ChainGetTipSetByHeight",
	"Filecoin.ChainHasObj",
	"Filecoin.ChainHead",
	"Filecoin.ChainHotGC",
	"Filecoin.ChainNotify",
	"Filecoin.ChainReadObj",
	"Filecoin.ChainStatObj",
	"Filecoin.ChainTipSetWeight",
	"Filecoin.ClientDealPieceCID",
	"Filecoin.ClientDealSize",
	"Filecoin.ClientFindData",
	"Filecoin.ClientGetDealInfo",
	"Filecoin.ClientGetDealStatus",
	"Filecoin.ClientMinerQueryOffer",
	"Filecoin.ClientQueryAsk",
	"Filecoin.GasEstimateFeeCap",
	"Filecoin.GasEstimateGasLimit",
	"Filecoin.GasEstimateGasPremium",
	"Filecoin.GasEstimateMessageGas",
	"Filecoin.ID",
	"Filecoin.MinerGetBaseInfo",
	"Filecoin.MpoolCheckMessages",
	"Filecoin.MpoolCheckPendingMessages",
	"Filecoin.MpoolCheckReplaceMessages",
	"Filecoin.MpoolGetConfig",
	"Filecoin.MpoolGetNonce",
	"Filecoin.MpoolPending",
	"Filecoin.MpoolPush",
	"Filecoin.MpoolSelect",
	"Filecoin.MpoolSub",
	"Filecoin.MsigGetAvailableBalance",
	"Filecoin.MsigGetPending",
	"Filecoin.MsigGetVested",
	"Filecoin.MsigGetVestingSchedule",
	"Filecoin.NetAddrsListen",
	"Filecoin.NetAgentVersion",
	"Filecoin.NetAutoNatStatus",
	"Filecoin.NetBandwidthStats",
	"Filecoin.NetBandwidthStatsByPeer",
	"Filecoin.NetBandwidthStatsByProtocol",
	"Filecoin.NetBlockList",
	"Filecoin.NetConnectedness",
	"Filecoin.NetFindPeer",
	"Filecoin.NetLimit",
	"Filecoin.NetListening",
	"Filecoin.NetPeerInfo",
	"Filecoin.NetPeers",
	"Filecoin.NetPing",
	"Filecoin.NetProtectList",
	"Filecoin.NetPubsubScores",
	"Filecoin.NetStat",
	"Filecoin.NetVersion",
	"Filecoin.NodeStatus",
	"Filecoin.PaychList",
	"Filecoin.PaychStatus",
	"Filecoin.PaychVoucherCheckSpendable",
	"Filecoin.PaychVoucherCheckValid",
	"Filecoin.RaftLeader",
	"Filecoin.RaftState",
	"Filecoin.StartTime",
	"Filecoin.StateAccountKey",
	"Filecoin.StateActorCodeCIDs",
	"Filecoin.StateActorManifestCID",
	"Filecoin.StateAllMinerFaults",
	"Filecoin.StateCall",
	"Filecoin.StateChangedActors",
	"Filecoin.StateCirculatingSupply",
	"Filecoin.StateCompute",
	"Filecoin.StateComputeDataCID",
	"Filecoin.StateDealProviderCollateralBounds",
	"Filecoin.StateDecodeParams",
	"Filecoin.StateEncodeParams",
	"Filecoin.StateGetActor",
	"Filecoin.StateGetAllocation",
	"Filecoin.StateGetAllocationForPendingDeal",
	"Filecoin.StateGetAllocations",
	"Filecoin.StateGetBeaconEntry",
	"Filecoin.StateGetClaim",
	"Filecoin.StateGetClaims",
	"Filecoin.StateGetNetworkParams",
	"Filecoin.StateGetRandomnessDigestFromBeacon",
	"Filecoin.StateGetRandomnessDigestFromTickets",
	"Filecoin.StateGetRandomnessFromBeacon",
	"Filecoin.StateGetRandomnessFromTickets",
	"Filecoin.StateGetReceipt",
	"Filecoin.StateListActors",
	"Filecoin.StateListMessages",
	"Filecoin.StateListMiners",
	"Filecoin.StateLookupID",
	"Filecoin.StateLookupRobustAddress",
	"Filecoin.StateMarketBalance",
	"Filecoin.StateMarketDeals",
	"Filecoin.StateMarketParticipants",
	"Filecoin.StateMarketStorageDeal",
	"Filecoin.StateMinerActiveSectors",
	"Filecoin.StateMinerAllocated",
	"Filecoin.StateMinerAvailableBalance",
	"Filecoin.StateMinerDeadlines",
	"Filecoin.StateMinerFaults",
	"Filecoin.StateMinerInfo",
	"Filecoin.StateMinerInitialPledgeCollateral",
	"Filecoin.StateMinerPartitions",
	"Filecoin.StateMinerPower",
	"Filecoin.StateMinerPreCommitDepositForPower",
	"Filecoin.StateMinerProvingDeadline",
	"Filecoin.StateMinerRecoveries",
	"Filecoin.StateMinerSectorAllocated",
	"Filecoin.StateMinerSectorCount",
	"Filecoin.StateMinerSectors",
	"Filecoin.StateNetworkName",
	"Filecoin.StateNetworkVersion",
	"Filecoin.StateReadState",
	"Filecoin.StateReplay",
	"Filecoin.StateSearchMsg",
	"Filecoin.StateSearchMsgLimited",
	"Filecoin.StateSectorExpiration",
	"Filecoin.StateSectorGetInfo",
	"Filecoin.StateSectorPartition",
	"Filecoin.StateSectorPreCommitInfo",
	"Filecoin.StateVerifiedClientStatus",
	"Filecoin.StateVerifiedRegistryRootKey",
	"Filecoin.StateVerifierStatus",
	"Filecoin.StateVMCirculatingSupplyInternal",
	"Filecoin.StateWaitMsg",
	"Filecoin.StateWaitMsgLimited",
	"Filecoin.SyncCheckBad",
	"Filecoin.SyncIncomingBlocks",
	"Filecoin.SyncState",
	"Filecoin.SyncValidateTipset",
	"Filecoin.Version",
	"Filecoin.WalletBalance",
	"Filecoin.WalletValidateAddress",
	"Filecoin.WalletVerify",
	"Filecoin.Web3ClientVersion",
)

var seiMethods = mapset.NewThreadUnsafeSet[string](
	"sei_getCosmosTx",
	"sei_getEvmTx",
	"sei_getEVMAddress",
	"sei_getSeiAddress",
)

var zksyncMethods = mapset.NewThreadUnsafeSet[string](
	"zks_estimateFee",
	"zks_estimateGasL1ToL2",
	"zks_getAllAccountBalances",
	"zks_getBlockDetails",
	"zks_getBridgeContracts",
	"zks_getBytecodeByHash",
	"zks_getConfirmedTokens",
	"zks_getL1BatchBlockRange",
	"zks_getL1BatchDetails",
	"zks_getL2ToL1LogProof",
	"zks_getL2ToL1MsgProof",
	"zks_getMainContract",
	"zks_getRawBlockTransactions",
	"zks_getTestnetPaymaster",
	"zks_getTokenPrice",
	"zks_getTransactionDetails",
	"zks_L1BatchNumber",
	"zks_L1ChainId",
)

var optimismMethods = mapset.NewThreadUnsafeSet[string](
	"optimism_outputAtBlock",
	"optimism_syncStatus",
	"optimism_rollupConfig",
	"optimism_version",
	"rollup_gasPrices",
)

var scrollMethods = mapset.NewThreadUnsafeSet[string](
	"scroll_estimateL1DataFee",
)

var mantleMethods = mapset.NewThreadUnsafeSet[string](
	"rollup_gasPrices",
	"eth_getBlockRange",
)

var polygonMethods = mapset.NewThreadUnsafeSet[string](
	"bor_getAuthor",
	"bor_getCurrentValidators",
	"bor_getCurrentProposer",
	"bor_getRootHash",
	"bor_getSignersAtHash",
	"eth_getRootHash",
)

var polygonZkEvmMethods = mapset.NewThreadUnsafeSet[string](
	"zkevm_consolidatedBlockNumber",
	"zkevm_isBlockConsolidated",
	"zkevm_isBlockVirtualized",
	"zkevm_batchNumberByBlockNumber",
	"zkevm_batchNumber",
	"zkevm_virtualBatchNumber",
	"zkevm_verifiedBatchNumber",
	"zkevm_getBatchByNumber",
	"zkevm_getBroadcastURI",
)

var harmonyShard0Methods = harmonyMethods.Union(mapset.NewThreadUnsafeSet[string]("hmy_getCurrentUtilityMetrics"))

var lineaMethods = mapset.NewThreadUnsafeSet[string](
	"linea_estimateGas",
	"linea_getProof",
)

var rootstockMethods = mapset.NewThreadUnsafeSet[string](
	"rsk_getRawTransactionReceiptByHash",
	"rsk_getTransactionReceiptNodesByHash",
	"rsk_getRawBlockHeaderByHash",
	"rsk_getRawBlockHeaderByNumber",
	"rsk_protocolVersion",
)

var tronMethods = mapset.NewThreadUnsafeSet[string](
	"buildTransaction",
)

var cronosZkEvmMethods = zksyncMethods.Union(mapset.NewThreadUnsafeSet[string]("zk_estimateFee"))

var victionMethods = mapset.NewThreadUnsafeSet[string](
	"posv_getNetworkInformation",
)

type EthereumLikeMethods struct {
	chain            chains.Chain
	availableMethods mapset.Set[string]
}

func NewEthereumLikeMethods(chain chains.Chain) *EthereumLikeMethods {
	availableMethods := commonMethods.
		Union(chainMethods(chain)).
		Union(traceMethods).
		Union(debugMethods).
		Union(filterMethods)
	availableMethods.RemoveAll(chainUnsupportedMethods(chain).ToSlice()...)

	return &EthereumLikeMethods{
		chain:            chain,
		availableMethods: availableMethods,
	}
}

func (e *EthereumLikeMethods) GetGroupMethods(group string) mapset.Set[string] {
	var groupMethods mapset.Set[string]
	switch group {
	case TraceGroup:
		groupMethods = e.getTraceMethods()
	case DebugGroup:
		groupMethods = debugMethods
	case FilterGroup:
		groupMethods = filterMethods
	case DefaultGroup:
		groupMethods = e.availableMethods
	default:
		groupMethods = mapset.NewThreadUnsafeSet[string]()
	}
	return groupMethods.Clone()
}

func (e *EthereumLikeMethods) GetSupportedMethods() mapset.Set[string] {
	return e.availableMethods.Clone()
}

func (e *EthereumLikeMethods) HasMethod(method string) bool {
	return e.availableMethods.ContainsOne(method)
}

func chainMethods(chain chains.Chain) mapset.Set[string] {
	switch chain {
	case chains.ARBITRUM:
		return arbitrumTraceMethods
	case chains.OPTIMISM, chains.OPTIMISM_SEPOLIA:
		return optimismMethods
	case chains.SCROLL, chains.SCROLL_SEPOLIA:
		return scrollMethods
	case chains.KLAYTN, chains.KLAYTN_BAOBAB:
		return klayMethods
	case chains.MANTLE, chains.MANTLE_SEPOLIA:
		return mantleMethods
	case chains.POLYGON:
		return polygonMethods
	case chains.POLYGON_ZKEVM, chains.POLYGON_ZKEVM_CARDONA:
		return polygonZkEvmMethods
	case chains.ZKSYNC, chains.ZKSYNC_SEPOLIA:
		return zksyncMethods
	case chains.HARMONY_0:
		return harmonyShard0Methods
	case chains.HARMONY_1:
		return harmonyMethods
	case chains.LINEA, chains.LINEA_SEPOLIA:
		return lineaMethods
	case chains.ROOTSTOCK, chains.ROOTSTOCK_TESTNET:
		return rootstockMethods
	case chains.TRON, chains.TRON_SHASTA:
		return tronMethods
	case chains.FILECOIN, chains.FILECOIN_CALIBRATION:
		return filecoinMethods
	case chains.SEI, chains.SEI_TESTNET, chains.SEI_DEVNET:
		return seiMethods
	case chains.CRONOS_ZKEVM, chains.CRONOS_ZKEVM_TESTNET:
		return cronosZkEvmMethods
	case chains.VICTION, chains.VICTION_TESTNET:
		return victionMethods
	default:
		return mapset.NewThreadUnsafeSet[string]()
	}
}

func (e *EthereumLikeMethods) getTraceMethods() mapset.Set[string] {
	if e.chain == chains.ARBITRUM {
		return arbitrumTraceMethods
	}
	return traceMethods
}

func chainUnsupportedMethods(chain chains.Chain) mapset.Set[string] {
	switch chain {
	case chains.ARBITRUM:
		return traceMethods
	case chains.OPTIMISM, chains.OPTIMISM_SEPOLIA:
		return mapset.NewThreadUnsafeSet[string]("eth_getAccounts")
	case chains.ZKSYNC, chains.ZKSYNC_SEPOLIA, chains.POLYGON_ZKEVM, chains.POLYGON_ZKEVM_CARDONA:
		return mapset.NewThreadUnsafeSet[string]("eth_maxPriorityFeePerGas", "zks_getBytecodeByHash")
	case chains.TELOS, chains.TELOS_TESTNET:
		return mapset.NewThreadUnsafeSet[string]("eth_syncing", "net_peerCount")
	default:
		return mapset.NewThreadUnsafeSet[string]()
	}
}

var _ Methods = (*EthereumLikeMethods)(nil)
