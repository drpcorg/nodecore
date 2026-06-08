package flow

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/rs/zerolog"
)

type FanoutRequestProcessor struct {
	upstreamSupervisor upstreams.UpstreamSupervisor
	dispatch           specs.DispatchPolicy
}

func NewFanoutRequestProcessor(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	dispatch specs.DispatchPolicy,
) *FanoutRequestProcessor {
	return &FanoutRequestProcessor{
		upstreamSupervisor: upstreamSupervisor,
		dispatch:           dispatch,
	}
}

func (f *FanoutRequestProcessor) ProcessRequest(
	ctx context.Context,
	upstreamStrategy UpstreamStrategy,
	request protocol.RequestHolder,
) ProcessedResponse {
	upstreamIDs, err := collectFanoutUpstreamIDs(upstreamStrategy, request)
	if err != nil {
		return &UnaryResponse{ResponseWrapper: totalFailureWrapper(request, err)}
	}

	zerolog.Ctx(ctx).Debug().Msgf("fan-out selected %d upstreams for method %s", len(upstreamIDs), request.Method())

	results := make(chan fanoutResult, len(upstreamIDs))
	var wg sync.WaitGroup
	wg.Add(len(upstreamIDs))
	for _, upstreamID := range upstreamIDs {
		go func(upstreamID string) {
			defer wg.Done()
			upstream := f.upstreamSupervisor.GetUpstream(upstreamID)
			if upstream == nil {
				results <- fanoutResult{upstreamID: upstreamID, err: protocol.NoAvailableUpstreamsError()}
				return
			}
			wrapper, err := sendUnaryRequest(ctx, upstream, request)
			results <- fanoutResult{upstreamID: upstreamID, wrapper: wrapper, err: err}
		}(upstreamID)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	collectedResults := make(map[string]fanoutResult, len(upstreamIDs))
	for {
		select {
		case <-ctx.Done():
			return &UnaryResponse{ResponseWrapper: totalFailureWrapper(request, ctx.Err())}
		case result, ok := <-results:
			if !ok {
				aggregator := newFanoutAggregator(f.dispatch)
				for _, upstreamID := range upstreamIDs {
					if result, ok := collectedResults[upstreamID]; ok {
						aggregator.Record(result)
					}
				}
				wrapper := aggregator.Final(request)
				zerolog.Ctx(ctx).Debug().Msgf("fan-out selected final upstream %s for method %s", wrapper.UpstreamId, request.Method())
				return &UnaryResponse{ResponseWrapper: wrapper}
			}
			collectedResults[result.upstreamID] = result
		}
	}
}

var _ RequestProcessor = (*FanoutRequestProcessor)(nil)

type fanoutResult struct {
	upstreamID string
	wrapper    *protocol.ResponseHolderWrapper
	err        error
}

func collectFanoutUpstreamIDs(upstreamStrategy UpstreamStrategy, request protocol.RequestHolder) ([]string, error) {
	var upstreamIDs []string
	seen := make(map[string]struct{})
	for {
		upstreamID, err := upstreamStrategy.SelectUpstream(request)
		if err != nil {
			if len(upstreamIDs) == 0 {
				return nil, err
			}
			return upstreamIDs, nil
		}
		if upstreamID == "" {
			if len(upstreamIDs) == 0 {
				return nil, protocol.NoAvailableUpstreamsError()
			}
			return upstreamIDs, nil
		}
		if _, ok := seen[upstreamID]; ok {
			if len(upstreamIDs) == 0 {
				return nil, protocol.NoAvailableUpstreamsError()
			}
			return upstreamIDs, nil
		}
		seen[upstreamID] = struct{}{}
		upstreamIDs = append(upstreamIDs, upstreamID)
	}
}

type fanoutAggregator interface {
	Record(result fanoutResult)
	Final(request protocol.RequestHolder) *protocol.ResponseHolderWrapper
}

func newFanoutAggregator(dispatch specs.DispatchPolicy) fanoutAggregator {
	switch dispatch {
	case specs.DispatchMaximumValue:
		return &maximumValueAggregator{}
	case specs.DispatchBroadcast:
		fallthrough
	default:
		return &broadcastAggregator{}
	}
}

type broadcastAggregator struct {
	selected *protocol.ResponseHolderWrapper
	fallback *protocol.ResponseHolderWrapper
	lastErr  error
}

func (a *broadcastAggregator) Record(result fanoutResult) {
	if result.err != nil {
		a.lastErr = result.err
		return
	}
	if result.wrapper == nil || result.wrapper.Response == nil {
		a.lastErr = errors.New("fan-out upstream returned empty response")
		return
	}
	if result.wrapper.Response.HasStream() {
		a.lastErr = errors.New("fan-out broadcast received streaming response")
		return
	}
	if result.wrapper.Response.HasError() {
		if a.fallback == nil {
			a.fallback = result.wrapper
		}
		return
	}
	if a.selected == nil {
		a.selected = result.wrapper
	}
}

func (a *broadcastAggregator) Final(request protocol.RequestHolder) *protocol.ResponseHolderWrapper {
	if a.selected != nil {
		return a.selected
	}
	if a.fallback != nil {
		return a.fallback
	}
	if a.lastErr != nil {
		return totalFailureWrapper(request, a.lastErr)
	}
	return totalFailureWrapper(request, protocol.NoAvailableUpstreamsError())
}

type maximumValueAggregator struct {
	selected *protocol.ResponseHolderWrapper
	max      *big.Int
	fallback *protocol.ResponseHolderWrapper
	lastErr  error
}

func (a *maximumValueAggregator) Record(result fanoutResult) {
	if result.err != nil {
		a.lastErr = result.err
		return
	}
	if result.wrapper == nil || result.wrapper.Response == nil {
		a.lastErr = errors.New("fan-out upstream returned empty response")
		return
	}
	if result.wrapper.Response.HasStream() {
		a.lastErr = errors.New("fan-out maximum-value received streaming response")
		return
	}
	if result.wrapper.Response.HasError() {
		if a.fallback == nil {
			a.fallback = result.wrapper
		}
		return
	}

	value, err := parseHexQuantityResult(result.wrapper.Response)
	if err != nil {
		a.lastErr = err
		return
	}
	if a.max == nil || value.Cmp(a.max) > 0 {
		a.max = value
		a.selected = result.wrapper
	}
}

func (a *maximumValueAggregator) Final(request protocol.RequestHolder) *protocol.ResponseHolderWrapper {
	if a.selected != nil {
		return a.selected
	}
	if a.fallback != nil {
		return a.fallback
	}
	if a.lastErr != nil {
		return totalFailureWrapper(request, a.lastErr)
	}
	return totalFailureWrapper(request, protocol.NoAvailableUpstreamsError())
}

func parseHexQuantityResult(response protocol.ResponseHolder) (*big.Int, error) {
	str, err := response.ResponseResultString()
	if err != nil {
		return nil, err
	}
	str = strings.TrimSpace(str)
	if str == "" {
		return nil, errors.New("empty quantity result")
	}
	if !strings.HasPrefix(str, "0x") && !strings.HasPrefix(str, "0X") {
		return nil, fmt.Errorf("invalid hex quantity result %q: missing 0x prefix", str)
	}
	str = str[2:]
	if str == "" {
		return nil, errors.New("empty hex quantity result")
	}
	value, ok := new(big.Int).SetString(str, 16)
	if !ok {
		return nil, fmt.Errorf("invalid hex quantity result %q", str)
	}
	return value, nil
}

func totalFailureWrapper(request protocol.RequestHolder, err error) *protocol.ResponseHolderWrapper {
	return &protocol.ResponseHolderWrapper{
		UpstreamId: NoUpstream,
		RequestId:  request.Id(),
		Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
	}
}
