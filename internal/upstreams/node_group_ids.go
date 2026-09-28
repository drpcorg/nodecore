package upstreams

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"sync"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/public/pkg/dshackle"
)

// SeparationLevel says how finely the upstreams of a chain are split into node groups.
type SeparationLevel int

const (
	SeparationLabels SeparationLevel = iota + 1
	SeparationGroups
	SeparationFull
)

var SeparationLevels = []SeparationLevel{SeparationLabels, SeparationGroups, SeparationFull}

func SeparationLevelFromProto(level dshackle.SeparationLevel) (SeparationLevel, bool) {
	switch level {
	case dshackle.SeparationLevel_SEPARATION_UNSPECIFIED, dshackle.SeparationLevel_SEPARATION_GROUPS:
		return SeparationGroups, true
	case dshackle.SeparationLevel_SEPARATION_LABELS:
		return SeparationLabels, true
	case dshackle.SeparationLevel_SEPARATION_FULL:
		return SeparationFull, true
	}
	return 0, false
}

func (l SeparationLevel) Proto() dshackle.SeparationLevel {
	switch l {
	case SeparationLabels:
		return dshackle.SeparationLevel_SEPARATION_LABELS
	case SeparationFull:
		return dshackle.SeparationLevel_SEPARATION_FULL
	}
	return dshackle.SeparationLevel_SEPARATION_GROUPS
}

func (l SeparationLevel) String() string {
	switch l {
	case SeparationLabels:
		return "labels"
	case SeparationGroups:
		return "groups"
	case SeparationFull:
		return "full"
	}
	return "unknown"
}

// NodeGroupLabel is the selector name a client uses to pin a request to node
// groups. It is not a stored label: the matcher derives the id from the
// upstream state.
const NodeGroupLabel = "node_group"

const unknownClientType = "unknown"

// id prefixes tag the level, so a matcher can tell which level an id addresses
var levelPrefixes = map[SeparationLevel]string{
	SeparationLabels: "l:",
	SeparationGroups: "g:",
	SeparationFull:   "n:",
}

func NodeGroupLevel(id string) (SeparationLevel, bool) {
	for level, prefix := range levelPrefixes {
		if strings.HasPrefix(id, prefix) {
			return level, true
		}
	}
	return 0, false
}

// nodeGroupId derives the id of the group the upstream belongs to at the level.
// A pure function of the state: the partition, the pin gate and the reply
// stamp agree as long as they read the same snapshot, the chain supervisor's.
func nodeGroupId(level SeparationLevel, upstreamId string, state *protocol.UpstreamState) string {
	switch level {
	case SeparationLabels:
		return levelPrefixes[level] + clientTypeOf(state) + ":" + labelsHash(state.Labels)
	case SeparationGroups:
		return levelPrefixes[level] + clientTypeOf(state) + ":" + labelsHash(state.Labels) + ":" + methodsHash(state.UpstreamMethods)
	case SeparationFull:
		return levelPrefixes[level] + upstreamHash(upstreamId)
	}
	return ""
}

// The client_type label is detected asynchronously, so an upstream may start
// under "unknown" and move once the label lands.
func clientTypeOf(state *protocol.UpstreamState) string {
	clientType := ""
	if state.Labels != nil {
		clientType, _ = state.Labels.GetLabel("client_type")
	}
	if clientType == "" {
		clientType = unknownClientType
	}
	return clientType
}

// labelsHash covers every routing label but NodeGroupLabel, which would make
// the id self-referential.
func labelsHash(labels *protocol.Labels) string {
	var pairs []string
	if labels != nil {
		for name, value := range labels.GetAllLabels() {
			if name != NodeGroupLabel {
				pairs = append(pairs, name+"="+value)
			}
		}
	}
	return shortHash(pairs)
}

// methodsHash covers call methods only: a websocket-capable node shares a
// group with its http-only twin.
func methodsHash(m methods.Methods) string {
	var names []string
	if m != nil {
		names = slices.DeleteFunc(m.GetSupportedMethods().ToSlice(), func(name string) bool {
			method := m.GetMethod(name)
			return method != nil && method.IsSubscribe()
		})
	}
	return shortHash(names)
}

func shortHash(values []string) string {
	slices.Sort(values)
	sum := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return hex.EncodeToString(sum[:4])
}

func upstreamHash(upstreamId string) string {
	sum := sha256.Sum256([]byte(upstreamId))
	return hex.EncodeToString(sum[:6])
}

// nodeGroupIdCache memoizes NodeGroupId per upstream. Labels and UpstreamMethods
// are copy-on-write, so pointer identity tells when the ids must be recomputed;
// hashing the method set on every match is too slow for the request path.
var nodeGroupIdCache sync.Map // upstream id -> *cachedNodeGroupIds

func forgetNodeGroupIds(upstreamId string) {
	nodeGroupIdCache.Delete(upstreamId)
}

type cachedNodeGroupIds struct {
	methods methods.Methods
	labels  *protocol.Labels
	ids     [SeparationFull + 1]string // indexed by level
}

func CachedNodeGroupId(level SeparationLevel, upstreamId string, state *protocol.UpstreamState) string {
	if state == nil || level < SeparationLabels || level > SeparationFull {
		return ""
	}
	if cached, ok := nodeGroupIdCache.Load(upstreamId); ok {
		entry := cached.(*cachedNodeGroupIds)
		if entry.methods == state.UpstreamMethods && entry.labels == state.Labels {
			return entry.ids[level]
		}
	}
	entry := &cachedNodeGroupIds{methods: state.UpstreamMethods, labels: state.Labels}
	for _, l := range SeparationLevels {
		entry.ids[l] = nodeGroupId(l, upstreamId, state)
	}
	nodeGroupIdCache.Store(upstreamId, entry)
	return entry.ids[level]
}
