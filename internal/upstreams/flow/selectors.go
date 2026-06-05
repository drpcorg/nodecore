package flow

import (
	"fmt"
	"sort"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
)

type sortKind int

const (
	sortDefault sortKind = iota
	sortHeadDesc
	sortSafeDesc
	sortFinalizedDesc
	sortPredictedLowerBoundAsc
)

type sortSpec struct {
	kind           sortKind
	lowerBoundType protocol.LowerBoundType
	timeOffset     int64
}

func buildSelectorRouting(selectors []protocol.RequestSelector, supervisor upstreams.UpstreamSupervisor, chainSupervisor upstreams.ChainSupervisor) ([]Matcher, UpstreamOrder) {
	predict := func(upstreamId string, boundType protocol.LowerBoundType, timeOffset int64) int64 {
		if supervisor == nil {
			return 0
		}
		up := supervisor.GetUpstream(upstreamId)
		if up == nil {
			return 0
		}
		return up.PredictLowerBound(boundType, timeOffset)
	}

	matchers := make([]Matcher, 0, len(selectors))
	var orderSpec *sortSpec
	for _, selector := range selectors {
		matcher, sort := compileSelector(selector, predict)
		if matcher != nil {
			matchers = append(matchers, matcher)
		}
		if sort != nil {
			// Top-level conflicts are rejected when dshackle request/item selectors are
			// mapped. If a caller constructs protocol selectors directly, keep the old
			// simple behavior and let the last top-level ordering selector win.
			orderSpec = sort
		}
	}
	return matchers, buildSelectorOrder(orderSpec, supervisor, chainSupervisor)
}

func compileSelector(selector protocol.RequestSelector, predict LowerHeightPredictor) (Matcher, *sortSpec) {
	sortOnly := func(spec sortSpec) (Matcher, *sortSpec) {
		return nil, &spec
	}
	unsupported := func(reason string) (Matcher, *sortSpec) {
		return &UnsupportedSelectorMatcher{reason: reason}, nil
	}

	switch s := selector.(type) {
	case nil, protocol.RequestAnySelector:
		return nil, nil
	case protocol.RequestLabelSelector:
		return NewLabelMatcher(s.Name, s.Values), nil
	case protocol.RequestExistsSelector:
		return NewLabelExistsMatcher(s.Name), nil
	case protocol.RequestAndSelector:
		children := make([]Matcher, 0, len(s.Children))
		var combinedSort *sortSpec
		for _, child := range s.Children {
			matcher, sort := compileSelector(child, predict)
			if matcher != nil {
				children = append(children, matcher)
			}
			if sort != nil {
				if combinedSort != nil && *combinedSort != *sort {
					children = append(children, &UnsupportedSelectorMatcher{reason: fmt.Sprintf("conflicting selector sort hints %d and %d are not supported", combinedSort.kind, sort.kind)})
					continue
				}
				combinedSort = sort
			}
		}
		return &SelectorAndMatcher{matchers: children}, combinedSort
	case protocol.RequestOrSelector:
		children := make([]Matcher, 0, len(s.Children))
		for _, child := range s.Children {
			matcher, sort := compileSelector(child, predict)
			if sort != nil {
				return unsupported("sort-bearing selectors inside OR are not supported")
			}
			if matcher != nil {
				children = append(children, matcher)
			}
		}
		return &SelectorOrMatcher{matchers: children}, nil
	case protocol.RequestNotSelector:
		matcher, sort := compileSelector(s.Child, predict)
		if sort != nil {
			return unsupported("sort-bearing selectors inside NOT are not supported")
		}
		if matcher == nil {
			return nil, nil
		}
		return &SelectorNotMatcher{matcher: matcher}, nil
	case protocol.RequestHeightSelector:
		return NewHeightMatcher(s.Height), nil
	case protocol.RequestBlockTagSelector:
		switch s.Tag {
		case protocol.BlockTagLatest:
			return sortOnly(sortSpec{kind: sortHeadDesc})
		case protocol.BlockTagSafe:
			return sortOnly(sortSpec{kind: sortSafeDesc})
		case protocol.BlockTagFinalized:
			return sortOnly(sortSpec{kind: sortFinalizedDesc})
		default:
			return unsupported("unsupported block tag selector")
		}
	case protocol.RequestSlotHeightSelector:
		return NewSlotHeightMatcher(s.SlotHeight), nil
	case protocol.RequestLowerHeightSelector:
		if s.Height == 0 {
			return sortOnly(sortSpec{kind: sortPredictedLowerBoundAsc, lowerBoundType: s.LowerBoundType, timeOffset: s.TimeOffset})
		}
		return NewLowerHeightMatcher(s.Height, s.LowerBoundType, s.TimeOffset, s.HeightDelta, predict), nil
	case protocol.RequestUnsupportedSelector:
		return unsupported(s.Reason)
	default:
		return unsupported(fmt.Sprintf("unsupported selector %T", selector))
	}
}

type UpstreamOrder func([]string) []string

func buildSelectorOrder(spec *sortSpec, supervisor upstreams.UpstreamSupervisor, chainSupervisor upstreams.ChainSupervisor) UpstreamOrder {
	if spec == nil || spec.kind == sortDefault {
		return nil
	}
	return func(ids []string) []string {
		ordered := append([]string(nil), ids...)
		sortKeys := make(map[string]int64, len(ordered))
		for _, id := range ordered {
			sortKeys[id] = selectorSortKey(*spec, supervisor, chainSupervisor, id)
		}
		sort.SliceStable(ordered, func(i, j int) bool {
			left := sortKeys[ordered[i]]
			right := sortKeys[ordered[j]]
			switch spec.kind {
			case sortHeadDesc, sortSafeDesc, sortFinalizedDesc:
				return left > right
			case sortPredictedLowerBoundAsc:
				return left < right
			default:
				return false
			}
		})
		return ordered
	}
}

func selectorSortKey(spec sortSpec, supervisor upstreams.UpstreamSupervisor, chainSupervisor upstreams.ChainSupervisor, id string) int64 {
	if spec.kind == sortPredictedLowerBoundAsc {
		return predictedLowerBoundOf(supervisor, id, spec.lowerBoundType, spec.timeOffset)
	}
	if chainSupervisor == nil {
		return 0
	}
	state := chainSupervisor.GetUpstreamState(id)
	switch spec.kind {
	case sortHeadDesc:
		return int64(heightOf(state))
	case sortSafeDesc:
		return int64(blockHeightOf(state, protocol.SafeBlock))
	case sortFinalizedDesc:
		return int64(blockHeightOf(state, protocol.FinalizedBlock))
	default:
		return 0
	}
}

func heightOf(state *protocol.UpstreamState) uint64 {
	if state == nil {
		return 0
	}
	return state.HeadData.Height
}

func blockHeightOf(state *protocol.UpstreamState, blockType protocol.BlockType) uint64 {
	if state == nil || state.BlockInfo == nil {
		return 0
	}
	return state.BlockInfo.GetBlock(blockType).Height
}

func predictedLowerBoundOf(supervisor upstreams.UpstreamSupervisor, id string, boundType protocol.LowerBoundType, offset int64) int64 {
	if supervisor == nil {
		return 1 << 62
	}
	up := supervisor.GetUpstream(id)
	if up == nil {
		return 1 << 62
	}
	value := up.PredictLowerBound(boundType, offset)
	if value == 0 {
		return 1 << 62
	}
	return value
}
