package specific_helpers

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/sui"
	"google.golang.org/protobuf/proto"
)

// SuiGetServiceInfoMethod is the single probe source for the Sui family: head
// polling, health and chain validation, labels and lower-bound detection all
// read its response.
const SuiGetServiceInfoMethod = "/sui.rpc.v2.LedgerService/GetServiceInfo"

// NewSuiServiceInfoRequest builds the internal GetServiceInfo probe request.
// GetServiceInfoRequest is an empty message, so the body is zero bytes on the
// wire - probes cross the schema boundary as bytes.
func NewSuiServiceInfoRequest(chain chains.Chain) protocol.RequestHolder {
	return protocol.NewInternalUpstreamGrpcRequest(SuiGetServiceInfoMethod, nil, chain)
}

// FetchSuiServiceInfo calls GetServiceInfo and returns both the typed response
// and the raw response bytes. The caller owns the timeout.
func FetchSuiServiceInfo(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) (*sui.GetServiceInfoResponse, []byte, error) {
	response := connector.SendRequest(ctx, NewSuiServiceInfoRequest(chain))
	if response.HasError() {
		return nil, nil, response.GetError()
	}
	serviceInfo, err := ParseSuiServiceInfo(response.ResponseResult())
	if err != nil {
		return nil, nil, err
	}
	return serviceInfo, response.ResponseResult(), nil
}

// ParseSuiServiceInfo unmarshals a GetServiceInfoResponse. Zero bytes are a
// valid serialization of the message, so emptiness alone is not an error -
// callers validate the fields they need.
func ParseSuiServiceInfo(data []byte) (*sui.GetServiceInfoResponse, error) {
	var serviceInfo sui.GetServiceInfoResponse
	if err := proto.Unmarshal(data, &serviceInfo); err != nil {
		return nil, err
	}
	return &serviceInfo, nil
}
