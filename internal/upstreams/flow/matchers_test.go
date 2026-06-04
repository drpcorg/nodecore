package flow

import (
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestMultiMatcher(t *testing.T) {
	method := NewMethodMatcher("eth_getBalance")
	status := NewStatusMatcher()
	multiMatcher := NewMultiMatcher(method, status)
	methods := mocks.NewMethodsMock()
	methods.On("HasMethod", "eth_getBalance").Return(true)
	state := protocol.UpstreamState{Status: protocol.Available, UpstreamMethods: methods}

	resp := multiMatcher.Match("1", &state)

	assert.Equal(t, SuccessResponse{}, resp)
	assert.Equal(t, SuccessType, resp.Type())
}

func TestMultiMatcherResponseMethodType(t *testing.T) {
	method := NewMethodMatcher("no-method")
	status := NewStatusMatcher()
	multiMatcher := NewMultiMatcher(method, status)
	methods := mocks.NewMethodsMock()
	methods.On("HasMethod", "no-method").Return(false)
	state := protocol.UpstreamState{Status: protocol.Unavailable, UpstreamMethods: methods}

	resp := multiMatcher.Match("1", &state)

	assert.IsType(t, MethodResponse{}, resp)
	assert.Equal(t, MethodType, resp.Type())
	assert.Equal(t, "method no-method is not supported", resp.Cause())
}

func TestMethodMatcher(t *testing.T) {
	matcher := NewMethodMatcher("eth_getBalance")
	methods := mocks.NewMethodsMock()
	methods.On("HasMethod", "eth_getBalance").Return(true)
	state := protocol.UpstreamState{UpstreamMethods: methods}

	resp := matcher.Match("1", &state)

	assert.Equal(t, SuccessResponse{}, resp)
	assert.Equal(t, SuccessType, resp.Type())
}

func TestMethodMatcherNoMethod(t *testing.T) {
	matcher := NewMethodMatcher("no-method")
	methods := mocks.NewMethodsMock()
	methods.On("HasMethod", "no-method").Return(false)
	state := protocol.UpstreamState{UpstreamMethods: methods}

	resp := matcher.Match("1", &state)

	assert.IsType(t, MethodResponse{}, resp)
	assert.Equal(t, MethodType, resp.Type())
	assert.Equal(t, "method no-method is not supported", resp.Cause())
}

func TestStatusMatcher(t *testing.T) {
	matcher := NewStatusMatcher()
	state := protocol.UpstreamState{Status: protocol.Available}

	resp := matcher.Match("1", &state)

	assert.Equal(t, SuccessResponse{}, resp)
	assert.Equal(t, SuccessType, resp.Type())
}

func TestStatusMatcherNotAvailable(t *testing.T) {
	matcher := NewStatusMatcher()
	state := protocol.UpstreamState{Status: protocol.Unavailable}

	resp := matcher.Match("1", &state)

	assert.Equal(t, AvailabilityResponse{}, resp)
	assert.Equal(t, "upstream is not available", resp.Cause())
	assert.Equal(t, AvailabilityType, resp.Type())
}

func TestWsCapMatcher(t *testing.T) {
	matcher := NewWsCapMatcher("sub")
	state := protocol.UpstreamState{Caps: mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap)}

	resp := matcher.Match("1", &state)

	assert.Equal(t, SuccessResponse{}, resp)
	assert.Equal(t, SuccessType, resp.Type())
}

func TestWsCapMatcherNotAvailable(t *testing.T) {
	matcher := NewWsCapMatcher("sub")
	state := protocol.UpstreamState{Caps: mapset.NewThreadUnsafeSet[protocol.Cap]()}

	resp := matcher.Match("1", &state)

	assert.IsType(t, MethodResponse{}, resp)
	assert.Equal(t, MethodType, resp.Type())
	assert.Equal(t, "method sub is not supported", resp.Cause())
}

func TestUpstreamIndexMatcher(t *testing.T) {
	matcher := NewUpstreamIndexMatcher("index")
	state := protocol.UpstreamState{UpstreamIndex: "index"}

	resp := matcher.Match("1", &state)

	assert.Equal(t, SuccessResponse{}, resp)
	assert.Equal(t, SuccessType, resp.Type())
}

func TestUpstreamIndexMatcherNotExist(t *testing.T) {
	matcher := NewUpstreamIndexMatcher("not-exist")
	state := protocol.UpstreamState{UpstreamIndex: "index"}

	resp := matcher.Match("1", &state)

	assert.IsType(t, UpstreamIndexResponse{}, resp)
	assert.Equal(t, UpstreamIndexType, resp.Type())
	assert.Equal(t, "no upstream with index not-exist", resp.Cause())
}

func TestSelectorLabelExistsHeightSlotAndLowerMatchers(t *testing.T) {
	methodsMock := mocks.NewMethodsMock()
	state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet[protocol.Cap](), "", nil, nil)
	state.Labels.AddLabel("region", "us")
	state.HeadData = protocol.NewBlockWithHeights(100, 50)

	assert.Equal(t, SuccessType, NewLabelMatcher("region", []string{"us", "eu"}).Match("up", &state).Type())
	assert.Equal(t, SuccessType, NewLabelExistsMatcher("region").Match("up", &state).Type())
	assert.Equal(t, SuccessType, NewHeightMatcher(99).Match("up", &state).Type())
	assert.Equal(t, SuccessType, NewSlotHeightMatcher(50).Match("up", &state).Type())

	lower := NewLowerHeightMatcher(90, protocol.StateBound, 10, 5, func(upstreamId string, boundType protocol.LowerBoundType, timeOffset int64) int64 {
		assert.Equal(t, "up", upstreamId)
		assert.Equal(t, protocol.StateBound, boundType)
		assert.Equal(t, int64(10), timeOffset)
		return 94
	})
	assert.Equal(t, SuccessType, lower.Match("up", &state).Type())
	assert.Contains(t, NewLowerHeightMatcher(90, protocol.StateBound, 0, 0, nil).Match("up", &state).Cause(), "cannot be predicted")
}

func TestSelectorCompositionAndCause(t *testing.T) {
	methodsMock := mocks.NewMethodsMock()
	state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet[protocol.Cap](), "", nil, nil)
	state.Labels.AddLabel("region", "us")

	andMatchers, _ := buildSelectorRouting([]protocol.RequestSelector{protocol.RequestAndSelector{
		Children: []protocol.RequestSelector{
			protocol.RequestLabelSelector{Name: "region", Values: []string{"us"}},
			protocol.RequestExistsSelector{Name: "archive"},
		},
	}}, nil, nil)
	response := andMatchers[0].Match("up", &state)
	assert.Equal(t, SelectorType, response.Type())
	assert.Contains(t, response.Cause(), "Label archive does not exist")

	orMatchers, _ := buildSelectorRouting([]protocol.RequestSelector{protocol.RequestOrSelector{
		Children: []protocol.RequestSelector{
			protocol.RequestExistsSelector{Name: "archive"},
			protocol.RequestLabelSelector{Name: "region", Values: []string{"us"}},
		},
	}}, nil, nil)
	assert.Equal(t, SuccessType, orMatchers[0].Match("up", &state).Type())

	notMatchers, _ := buildSelectorRouting([]protocol.RequestSelector{protocol.RequestNotSelector{Child: protocol.RequestExistsSelector{Name: "archive"}}}, nil, nil)
	assert.Equal(t, SuccessType, notMatchers[0].Match("up", &state).Type())
}
