package protocol

import (
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
)

type RequestResult interface {
	withReqKind(reqKind RequestKind) RequestResult
	withTimestamp(timestamp time.Time) RequestResult
	withApiKey(apiKey string) RequestResult
	withChain(chain chains.Chain) RequestResult
	withMethod(method string) RequestResult
}

type UnaryRequestResult struct {
	upstreamId         string
	reqKind            RequestKind
	respKind           ResponseKind
	duration           float64
	apiKey             string
	chain              chains.Chain
	method             string
	timestamp          time.Time
	hasSuccessfulRetry bool
}

func (u *UnaryRequestResult) GetUpstreamId() string {
	return u.upstreamId
}

func (u *UnaryRequestResult) GetReqKind() RequestKind {
	return u.reqKind
}

func (u *UnaryRequestResult) GetRespKind() ResponseKind {
	return u.respKind
}

func (u *UnaryRequestResult) GetTimestamp() time.Time {
	return u.timestamp
}

func (u *UnaryRequestResult) GetApiKey() string {
	return u.apiKey
}

func (u *UnaryRequestResult) GetChain() chains.Chain {
	return u.chain
}

func (u *UnaryRequestResult) GetMethod() string {
	return u.method
}

func (u *UnaryRequestResult) IsSuccessfulRetry() bool {
	return u.hasSuccessfulRetry
}

func (u *UnaryRequestResult) GetDuration() float64 {
	return u.duration
}

func NewUnaryRequestResult() *UnaryRequestResult {
	return &UnaryRequestResult{}
}

func (u *UnaryRequestResult) WithSuccessfulRetry() *UnaryRequestResult {
	u.hasSuccessfulRetry = true
	return u
}

func (u *UnaryRequestResult) WithUpstreamId(upstreamId string) *UnaryRequestResult {
	u.upstreamId = upstreamId
	return u
}

func (u *UnaryRequestResult) WithRespKindFromResponse(response ResponseHolder) *UnaryRequestResult {
	u.respKind = GetRespKindFromResponse(response)
	return u
}

func (u *UnaryRequestResult) WithDuration(duration float64) *UnaryRequestResult {
	u.duration = duration
	return u
}

func (u *UnaryRequestResult) withReqKind(reqKind RequestKind) RequestResult {
	u.reqKind = reqKind
	return u
}

func (u *UnaryRequestResult) withChain(chain chains.Chain) RequestResult {
	u.chain = chain
	return u
}

func (u *UnaryRequestResult) withMethod(method string) RequestResult {
	u.method = method
	return u
}

func (u *UnaryRequestResult) withApiKey(apiKey string) RequestResult {
	u.apiKey = apiKey
	return u
}

func (u *UnaryRequestResult) withTimestamp(timestamp time.Time) RequestResult {
	u.timestamp = timestamp
	return u
}

var _ RequestResult = (*UnaryRequestResult)(nil)
