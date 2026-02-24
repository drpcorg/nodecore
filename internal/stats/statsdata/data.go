package statsdata

import (
	"sync/atomic"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type StatsDims int

const (
	UpstreamId StatsDims = iota
	ReqKind
	RespKind
	ApiKey
	Chain
	Method
)

type StatsKey struct {
	UpstreamId string
	ReqKind    protocol.RequestKind
	RespKind   protocol.ResponseKind
	ApiKey     string
	Chain      chains.Chain
	Method     string
	Timestamp  int64
}

type StatsData interface {
	stats()
}

type RequestStatsData struct {
	requestAmount atomic.Int64
}

func NewRequestStatsData() *RequestStatsData {
	return &RequestStatsData{}
}

func (r *RequestStatsData) GetRequestAmount() int64 {
	return r.requestAmount.Load()
}

func (r *RequestStatsData) AddRequest() {
	r.requestAmount.Add(1)
}

func (r *RequestStatsData) stats() {
	// noop
}
