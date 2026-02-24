package test_utils

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var opts = []cmp.Option{
	cmp.AllowUnexported(protocol.UpstreamJsonRpcRequest{}),
	cmpopts.IgnoreFields(protocol.UpstreamJsonRpcRequest{}, "requestObserver", "mu"),
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
