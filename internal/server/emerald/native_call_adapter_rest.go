package emerald

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
)

type restNativeCallAdapter struct{}

func (a restNativeCallAdapter) BuildRequest(
	chain *chains.ConfiguredChain,
	item *dshackle.NativeCallItem,
	requestSelector *dshackle.Selector,
	chunkSize uint32,
) (protocol.RequestHolder, *dshackle.NativeCallReplyItem) {
	restData := item.GetRestData()
	if restData == nil {
		return nil, a.ErrorItem(item.GetId(), protocol.ClientError(fmt.Errorf("rest_data is missing")))
	}

	if err := validateRestMethodTemplate(item.GetMethod()); err != nil {
		return nil, a.ErrorItem(item.GetId(), protocol.ClientError(err))
	}

	// gRPC clients already deliver path/headers/query params pre-structured,
	// so we plumb them straight into RequestParams instead of recomputing
	// anything. item.GetMethod() is taken as authoritative for the canonical
	// method template; the HTTP connector will expand it at send time using
	// PathParams to fill in any "*" wildcards.
	requestParams := &protocol.RequestParams{
		PathParams:  append([]string(nil), restData.GetPathParams()...),
		Headers:     keyValueListToMap(restData.GetHeaders()),
		QueryParams: keyValueListToMap(restData.GetQueryParams()),
	}

	requestID := strconv.FormatUint(uint64(item.GetId()), 10)
	selectors, err := mapNativeCallSelectors(requestSelector, item.GetSelectors())
	if err != nil {
		return nil, a.ErrorItem(item.GetId(), protocol.ClientError(err))
	}
	if chunkSize > 0 {
		return protocol.NewStreamUpstreamRestRequest(requestID, item.GetMethod(), requestParams, restData.GetPayload(), chain.MethodSpec, selectors...), nil
	}
	return protocol.NewUpstreamRestRequest(requestID, item.GetMethod(), requestParams, restData.GetPayload(), chain.MethodSpec, selectors...), nil
}

func (restNativeCallAdapter) SendReply(
	stream dshackle.Blockchain_NativeCallServer,
	wrapper *protocol.ResponseHolderWrapper,
	nonce uint64,
	signer signature.ResponseSigner,
) error {
	return sendReply(stream, wrapper, nonce, signer, passThroughStream, nativeCallErrorItem)
}

func (restNativeCallAdapter) ErrorItem(requestID uint32, responseError *protocol.ResponseError) *dshackle.NativeCallReplyItem {
	return noUpstreamErrorItem(requestID, responseError)
}

// validateRestMethodTemplate checks that a gRPC-supplied method string is
// well-formed: "VERB#/path", both halves non-empty. The actual verb/path
// split happens inside the HTTP connector when the request is sent.
func validateRestMethodTemplate(method string) error {
	parts := strings.SplitN(method, protocol.MethodSeparator, 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("rest method must be in form VERB%spath, got %q", protocol.MethodSeparator, method)
	}
	return nil
}
