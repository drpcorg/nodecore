package specific_helpers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/drpcorg/nodecore/internal/protocol"
)

// TronBlockIntervalMs - TRON produces a block every ~3s, so block timestamps
// (ms) advance by roughly this much between consecutive blocks. Used to
// project how many blocks a node "should" have produced since a block time.
const TronBlockIntervalMs int64 = 3000

// ParseTronSolidityHeight reads the height out of java-tron's
// NodeInfo.solidityBlock, rendered as "Num:75000000,ID:<hex>" by both the
// HTTP and the gRPC API.
func ParseTronSolidityHeight(solidityBlock string) (uint64, error) {
	numPart, _, _ := strings.Cut(solidityBlock, ",")
	_, value, found := strings.Cut(numPart, ":")
	if !found {
		return 0, fmt.Errorf("invalid solidity block %q", solidityBlock)
	}
	height, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid solidity block %q: %w", solidityBlock, err)
	}
	return height, nil
}

// TronSyncStatus projects how many blocks the chain produced since the
// upstream's latest block (drift / block interval) and reports Syncing once
// that projection exceeds the chain's syncing lag. A future timestamp (clock
// skew) projects zero.
func TronSyncStatus(blockTimestampMs, nowMs, syncingLag int64) protocol.AvailabilityStatus {
	expectedAhead := int64(0)
	if drift := nowMs - blockTimestampMs; drift > 0 {
		expectedAhead = drift / TronBlockIntervalMs
	}
	if expectedAhead > syncingLag {
		return protocol.Syncing
	}
	return protocol.Available
}
