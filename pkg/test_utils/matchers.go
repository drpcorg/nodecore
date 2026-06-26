package test_utils

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var opts = []cmp.Option{
	cmp.AllowUnexported(protocol.UpstreamJsonRpcRequest{}),
	// requestKey is a derived cache key, now computed lazily (see RequestHash),
	// so it's empty until first access; requestKeyOnce holds a sync.Once that
	// go-cmp can't traverse. Both are ignored - identity is already covered by
	// method/params/selectors, which requestKey is purely derived from.
	cmpopts.IgnoreFields(protocol.UpstreamJsonRpcRequest{}, "requestObserver", "mu", "specMethod", "requestKey", "requestKeyOnce"),
}

func UpstreamJsonRpcRequestMatcher(request protocol.RequestHolder) func(protocol.RequestHolder) bool {
	return func(got protocol.RequestHolder) bool {
		r, ok := got.(*protocol.UpstreamJsonRpcRequest)
		if !ok {
			return false
		}
		return cmp.Equal(r, request, opts...)
	}
}
