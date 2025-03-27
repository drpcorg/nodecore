package methods

import (
	mapset "github.com/deckarep/golang-set/v2"
)

var methods = mapset.NewThreadUnsafeSet[string](
	"getAccountInfo",
	"getBalance",
	"getBlock",
	"getBlockHeight",
	"getBlockProduction",
	"getBlockCommitment",
	"getBlocks",
	"getBlocksWithLimit",
	"getBlockTime",
	"getClusterNodes",
	"getEpochInfo",
	"getEpochSchedule",
	"getFeeForMessage",
	"getFirstAvailableBlock",
	"getGenesisHash",
	"getHealth",
	"getHighestSnapshotSlot",
	"getInflationGovernor",
	"getInflationRate",
	"getInflationReward",
	"getLargestAccounts",
	"getLatestBlockhash",
	"getLeaderSchedule",
	"getMaxRetransmitSlot",
	"getMaxShredInsertSlot",
	"getMinimumBalanceForRentExemption",
	"getMultipleAccounts",
	"getProgramAccounts",
	"getRecentPerformanceSamples",
	"getRecentPrioritizationFees",
	"getConfirmedTransaction",
	"getSignaturesForAddress",
	"getSignatureStatuses",
	"getSlot",
	"getSlotLeader",
	"getSlotLeaders",
	"getStakeActivation",
	"getStakeMinimumDelegation",
	"getSupply",
	"getTokenAccountBalance",
	"getTokenAccountsByDelegate",
	"getTokenAccountsByOwner",
	"getTokenLargestAccounts",
	"getTokenSupply",
	"getTransaction",
	"getTransactionCount",
	"getVersion",
	"getVoteAccounts",
	"isBlockhashValid",
	"minimumLedgerSlot",
	"requestAirdrop",
	"simulateTransaction",
	"getFees",
	"getIdentity",
	"getConfirmedSignaturesForAddress2",
	"getRecentBlockhash",
	"sendTransaction",
)

type SolanaMethods struct {
	availableMethods mapset.Set[string]
}

func NewSolanaMethods() *SolanaMethods {
	return &SolanaMethods{
		availableMethods: methods,
	}
}

func (s *SolanaMethods) GetGroupMethods(group string) mapset.Set[string] {
	if group == DefaultGroup {
		return s.GetSupportedMethods()
	}
	return mapset.NewThreadUnsafeSet[string]()
}

func (s *SolanaMethods) GetSupportedMethods() mapset.Set[string] {
	return s.availableMethods.Clone()
}

func (s *SolanaMethods) HasMethod(method string) bool {
	return s.availableMethods.ContainsOne(method)
}

var _ Methods = (*SolanaMethods)(nil)
