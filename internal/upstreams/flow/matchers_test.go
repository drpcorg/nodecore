package flow_test

import (
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams/flow"
	"github.com/drpcorg/dsheltie/pkg/test_utils"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestMultiMatcher(t *testing.T) {
	method := flow.NewMethodMatcher("eth_getBalance")
	status := flow.NewStatusMatcher()
	multiMatcher := flow.NewMultiMatcher(method, status)
	methods := test_utils.NewMethodsMock()
	methods.On("HasMethod", "eth_getBalance").Return(true)
	state := protocol.UpstreamState{Status: protocol.Available, UpstreamMethods: methods}

	resp := multiMatcher.Match("1", &state)

	assert.Equal(t, flow.SuccessResponse{}, resp)
	assert.Equal(t, flow.SuccessType, resp.Type())
}

func TestMultiMatcherResponseMethodType(t *testing.T) {
	method := flow.NewMethodMatcher("no-method")
	status := flow.NewStatusMatcher()
	multiMatcher := flow.NewMultiMatcher(method, status)
	methods := test_utils.NewMethodsMock()
	methods.On("HasMethod", "no-method").Return(false)
	state := protocol.UpstreamState{Status: protocol.Unavailable, UpstreamMethods: methods}

	resp := multiMatcher.Match("1", &state)

	assert.IsType(t, flow.MethodResponse{}, resp)
	assert.Equal(t, flow.MethodType, resp.Type())
	assert.Equal(t, "method no-method is not supported", resp.Cause())
}

func TestMethodMatcher(t *testing.T) {
	matcher := flow.NewMethodMatcher("eth_getBalance")
	methods := test_utils.NewMethodsMock()
	methods.On("HasMethod", "eth_getBalance").Return(true)
	state := protocol.UpstreamState{UpstreamMethods: methods}

	resp := matcher.Match("1", &state)

	assert.Equal(t, flow.SuccessResponse{}, resp)
	assert.Equal(t, flow.SuccessType, resp.Type())
}

func TestMethodMatcherNoMethod(t *testing.T) {
	matcher := flow.NewMethodMatcher("no-method")
	methods := test_utils.NewMethodsMock()
	methods.On("HasMethod", "no-method").Return(false)
	state := protocol.UpstreamState{UpstreamMethods: methods}

	resp := matcher.Match("1", &state)

	assert.IsType(t, flow.MethodResponse{}, resp)
	assert.Equal(t, flow.MethodType, resp.Type())
	assert.Equal(t, "method no-method is not supported", resp.Cause())
}

func TestStatusMatcher(t *testing.T) {
	matcher := flow.NewStatusMatcher()
	state := protocol.UpstreamState{Status: protocol.Available}

	resp := matcher.Match("1", &state)

	assert.Equal(t, flow.SuccessResponse{}, resp)
	assert.Equal(t, flow.SuccessType, resp.Type())
}

func TestStatusMatcherNotAvailable(t *testing.T) {
	matcher := flow.NewStatusMatcher()
	state := protocol.UpstreamState{Status: protocol.Unavailable}

	resp := matcher.Match("1", &state)

	assert.Equal(t, flow.AvailabilityResponse{}, resp)
	assert.Equal(t, "upstream is not available", resp.Cause())
	assert.Equal(t, flow.AvailabilityType, resp.Type())
}
