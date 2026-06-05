package flow

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUpstreamsMatchTraceAddSkipsNilAndSuccess(t *testing.T) {
	trace := &UpstreamsMatchTrace{}

	trace.Add("nil", nil)
	trace.Add("success", SuccessResponse{})
	trace.Add("unavailable", AvailabilityResponse{})

	assert.Len(t, trace.Responses, 1)
	assert.Equal(t, "unavailable", trace.Responses[0].UpstreamID)
	assert.Equal(t, AvailabilityType, trace.Responses[0].Response.Type())
}

func TestUpstreamsMatchTraceCauseEmpty(t *testing.T) {
	assert.Equal(t, "Response is empty", (*UpstreamsMatchTrace)(nil).Cause())
	assert.Equal(t, "Response is empty", (&UpstreamsMatchTrace{}).Cause())
}

func TestUpstreamsMatchTraceCauseFormatsResponses(t *testing.T) {
	trace := &UpstreamsMatchTrace{}
	trace.Add("upstream-a", MethodResponse{method: "eth_blockNumber"})
	trace.Add("upstream-b", AvailabilityResponse{})

	assert.Equal(t, "upstream-a - method eth_blockNumber is not supported; upstream-b - upstream is not available", trace.Cause())
}

func TestUpstreamsMatchTraceCauseIsTruncated(t *testing.T) {
	trace := &UpstreamsMatchTrace{}
	trace.Add("upstream", SelectorUnsupportedResponse{reason: strings.Repeat("x", maxMatchTraceCauseLen)})

	cause := trace.Cause()
	assert.Len(t, cause, maxMatchTraceCauseLen)
	assert.Equal(t, "upstream - "+strings.Repeat("x", maxMatchTraceCauseLen-len("upstream - ")), cause)
}
