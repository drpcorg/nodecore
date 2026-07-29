package evm_bounds

import (
	"strings"

	"github.com/drpcorg/nodecore/internal/protocol"
)

// isEvmNoDataError reports whether an upstream error means "this historical data
// isn't available here" (pruned/archival gaps) rather than a genuine failure. A
// no-data error is treated as "no data at this height" so the search moves on
// instead of aborting.
func isEvmNoDataError(err *protocol.ResponseError) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Message)
	for _, hint := range evmNoDataErrorHints {
		if strings.Contains(message, hint) {
			return true
		}
	}
	return false
}

var evmNoDataErrorHints = []string{
	"no state available for block",
	"missing trie node",
	"header not found",
	"metadata is not found",
	"distance to target block exceeds maximum proof window",
	"node state is pruned",
	"is not available, lowest height is",
	"state already discarded for",
	"your node is running with state pruning",
	"failed to compute tipset state",
	"bad tipset height",
	"body not found for block",
	"request beyond head block",
	"block not found",
	"could not find block",
	"unknown block",
	"header for hash not found",
	"after last accepted block",
	"version has either been pruned, or is for a future block",
	"no historical rpc is available for this historical",
	"historical backend error",
	"load state tree: failed to load state tree",
	"purged for block",
	"no state data",
	"state is not available",
	"block with such an id is pruned",
	"state at block",
	"unsupported block number",
	"unexpected state root",
	"evm module does not exist on height",
	"failed to load state at height",
	"no state found for block",
	"old data not available due",
	"state not found for block",
	"state does not maintain archive data",
	"access to archival, debug, or trace data is not included in your current plan",
	"empty reader set",
	"request might be querying historical state that is not available",
	"no receipts data",
	"no tx data",
	"no block data",
	"historical state not available in path scheme yet",
	"historical state is not available",
	"required historical state unavailable",
	"state histories haven't been fully indexed yet",
	"but it out-of-bounds",
	"has been pruned; earliest available is",
	"error loading messages for tipset",
	"height is not available",
	"method handler crashed",
	"unexpected error",
	"invalid block height",
	"pruned history unavailable",
	"no transactions snapshot file for",
	"could not find block for height",
	"transaction indexing is in progress",
	"old data not available due to pruning",
	"requested epoch was a null round",
}
