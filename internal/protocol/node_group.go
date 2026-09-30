package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"sync"
)

const NodeGroupLabel = "node_group_id"

// NodeGroupID is shared by discovery and execution. Only call methods affect
// membership. All labels are included so a group cannot mix client capabilities.
// Consumers treat both forms as opaque and never reconstruct them.
func NodeGroupID(id string, state *UpstreamState, full bool) string {
	if full {
		sum := sha256.Sum256([]byte(id))
		return "n:" + hex.EncodeToString(sum[:6])
	}
	if state.nodeGroupID != nil {
		return state.nodeGroupID()
	}
	return calculateNodeGroupID(state)
}

// WithNodeGroupCache prepares an immutable supervisor snapshot. State events
// invalidate the cache; head events retain it. Legacy traffic never hashes.
func (state *UpstreamState) WithNodeGroupCache() *UpstreamState {
	if state == nil {
		return nil
	}
	next := *state
	next.nodeGroupID = sync.OnceValue(func() string { return calculateNodeGroupID(&next) })
	return &next
}

func calculateNodeGroupID(state *UpstreamState) string {
	labels := []string{}
	if state.Labels != nil {
		for key, value := range state.Labels.GetAllLabels() {
			labels = append(labels, key+"="+value)
		}
	}
	slices.Sort(labels)
	names := []string{}
	if state.UpstreamMethods != nil {
		for _, name := range state.UpstreamMethods.GetSupportedMethods().ToSlice() {
			method := state.UpstreamMethods.GetMethod(name)
			if method == nil || (!method.IsSubscribe() && method.Subscription == nil) {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	sum := sha256.Sum256([]byte(strings.Join(labels, "\x00") + "\x00\x01" + strings.Join(names, "\x00") + "\x00"))
	return "g:" + hex.EncodeToString(sum[:8])
}
