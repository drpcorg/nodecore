package evm_bounds

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

// The state anchor tells the real state of an old block from the state of a later block
// served in its place. The plain state probes cannot: they accept any non-empty answer, so
// a node that answers every block below its snapshot with the state of the snapshot block
// looks like an archive.
//
// The anchor is the EIP-2935 history contract, where every block stores the hash of its
// parent and the last evmHistoryWindow hashes are kept. In the state of block N the entry
// of block N-evmStateAnchorDepth still holds the hash of that block, and block N+2 replaces
// it. So the real state of block N answers that block number with the hash its header has,
// and the state of a block from N+2 on answers with another hash. The state of block N+1 is
// the one the anchor cannot tell from the real one.
//
// The oldest entry, one block deeper, would leave no such gap, but it is not the same on
// every client: some run eth_call on top of the block, after the contract has replaced that
// entry with the hash of block N itself (the nodes of Gnosis, a part of Hoodi).
//
// Another hash than the header has is not yet the state of a later block: the contract of a
// chain may disagree with its headers at old blocks (Cronos). The state of a later block has
// one more sign. The block one window below uses the same entry of the contract, real history
// has replaced the hash in between, and a state that answers for both blocks has not.

const (
	evmHistoryContract = "0x0000f90827f1c53a10cb7a02335b175320002935"
	evmHistoryWindow   = 8191
	// evmStateAnchorDepth is how far below a block lies the block whose hash is asked for:
	// the second oldest the contract keeps
	evmStateAnchorDepth = evmHistoryWindow - 1

	// evmStateAnchorRecheckInterval is how long the verdict on the anchor of an upstream is
	// kept: a chain gets the contract with a fork, a node gets replaced behind its URL
	evmStateAnchorRecheckInterval = time.Hour
)

type stateAnchorVerdict int

const (
	// stateAnchorSilent: the contract keeps no hash for the block, as on a chain or before a
	// fork without it
	stateAnchorSilent stateAnchorVerdict = iota
	// stateAnchorReal: the contract keeps the hash the header has
	stateAnchorReal
	// stateAnchorFrozen: the block is answered with the state of a later block
	stateAnchorFrozen
	// stateAnchorForeign: the contract keeps another hash than the header has, and the state
	// is not the one of a later block
	stateAnchorForeign
)

// stateAnchorAt judges the state of the given block by the history contract. answered is
// false when the upstream has nothing to judge by: a pruned state, a missing header, a block
// closer to genesis than the depth of the anchor. A call that failed in another way than with
// a "no data" answer is an error: it says nothing about the block, and such a block must not
// be left to the plain probes alone, which take the state of a later block for the real one.
func (e *EvmLowerBoundDetector) stateAnchorAt(ctx context.Context, height int64) (verdict stateAnchorVerdict, answered bool, err error) {
	anchor := height - evmStateAnchorDepth
	if anchor < 0 {
		return stateAnchorSilent, false, nil
	}
	kept, answered, err := e.historyHashAt(ctx, anchor, height)
	if err != nil || !answered {
		return stateAnchorSilent, false, err
	}
	if kept == "" {
		return stateAnchorSilent, true, nil
	}
	block, available, err := e.blockAt(ctx, anchor)
	if err != nil || !available {
		return stateAnchorSilent, false, err
	}
	header, isHash := evmHash(block.Hash)
	if !isHash {
		return stateAnchorSilent, false, nil
	}
	if kept == header {
		return stateAnchorReal, true, nil
	}
	if anchor >= evmHistoryWindow {
		keptBelow, _, err := e.historyHashAt(ctx, anchor-evmHistoryWindow, height-evmHistoryWindow)
		if err != nil {
			return stateAnchorSilent, false, err
		}
		if keptBelow == kept {
			return stateAnchorFrozen, true, nil
		}
	}
	return stateAnchorForeign, true, nil
}

// historyHashAt asks the history contract, in the state of block height, for the hash of
// block number. The hash is empty when the contract keeps none. answered is false for a
// pruned state. A call that failed in another way is an error.
func (e *EvmLowerBoundDetector) historyHashAt(ctx context.Context, number, height int64) (hash string, answered bool, err error) {
	raw, available, err := e.call(ctx, "eth_call", []any{
		map[string]any{"to": evmHistoryContract, "data": fmt.Sprintf("0x%064x", number)},
		evmBlockTag(height),
	})
	if err != nil || !available {
		return "", false, err
	}
	var result string
	if err := sonic.Unmarshal(raw, &result); err != nil {
		return "", false, nil
	}
	hash, isHash := evmHash(result)
	if !isHash || hash == evmZeroHash {
		return "", true, nil
	}
	return hash, true, nil
}

// refreshStateAnchor lets the anchor judge the upstream only after it has confirmed the
// state of the head block, which every upstream has. A chain whose history contract answers
// otherwise than its headers has nothing to confirm the real state with.
// A check without an answer, a failed call included, keeps the previous verdict and is
// repeated in the next cycle.
func (e *EvmLowerBoundDetector) refreshStateAnchor(ctx context.Context, head int64) {
	checkedAt := e.stateAnchorCheckedAt.Load()
	if checkedAt != 0 && time.Since(time.Unix(0, checkedAt)) < e.stateAnchorRecheckInterval {
		return
	}
	verdict, answered, err := e.stateAnchorAt(ctx, head)
	if err != nil || !answered {
		return
	}
	e.stateAnchorTrusted.Store(verdict == stateAnchorReal)
	e.stateAnchorCheckedAt.Store(time.Now().UnixNano())
}

// SetStateAnchorRecheckInterval overrides how long the verdict on the state anchor is kept.
// Production relies on the default; tests shrink it.
func (e *EvmLowerBoundDetector) SetStateAnchorRecheckInterval(interval time.Duration) {
	e.stateAnchorRecheckInterval = interval
}

// evmHash normalizes a 32-byte hash; ok is false for a value of another shape.
func evmHash(value string) (hash string, ok bool) {
	hash = strings.ToLower(strings.TrimSpace(value))
	return hash, len(hash) == len(evmZeroHash) && strings.HasPrefix(hash, "0x")
}
