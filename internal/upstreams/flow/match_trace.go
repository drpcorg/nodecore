package flow

import (
	"fmt"
	"strings"
)

const maxMatchTraceCauseLen = 200

type UpstreamMatchTrace struct {
	UpstreamID string
	Response   MatchResponse
}

type UpstreamsMatchTrace struct {
	Responses []UpstreamMatchTrace
}

func (t *UpstreamsMatchTrace) Add(upstreamId string, response MatchResponse) {
	if t == nil || response == nil || response.Type() == SuccessType {
		return
	}
	t.Responses = append(t.Responses, UpstreamMatchTrace{UpstreamID: upstreamId, Response: response})
}

func (t *UpstreamsMatchTrace) Cause() string {
	if t == nil || len(t.Responses) == 0 {
		return "Response is empty"
	}
	parts := make([]string, 0, len(t.Responses))
	for _, item := range t.Responses {
		parts = append(parts, fmt.Sprintf("%s - %s", item.UpstreamID, item.Response.Cause()))
	}
	cause := strings.Join(parts, "; ")
	if len(cause) > maxMatchTraceCauseLen {
		return cause[:maxMatchTraceCauseLen]
	}
	return cause
}
