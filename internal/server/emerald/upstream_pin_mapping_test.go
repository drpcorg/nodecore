package emerald

import (
	"errors"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/stretchr/testify/require"
)

func TestNativeSelectorMappingPreservesUpstreamID(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	chain := &chains.ConfiguredChain{MethodSpec: "eth"}
	for name, build := range map[string]func(*dshackle.Selector) ([]protocol.RequestSelector, error){
		"call": func(selector *dshackle.Selector) ([]protocol.RequestSelector, error) {
			request, failure := (jsonRpcNativeCallAdapter{}).BuildRequest(chain, &dshackle.NativeCallItem{Id: 1, Method: "eth_chainId"}, selector, 0)
			if failure != nil {
				return nil, errors.New(failure.GetErrorMessage())
			}
			return request.Selectors(), nil
		},
		"jsonrpc subscribe": func(selector *dshackle.Selector) ([]protocol.RequestSelector, error) {
			request, err := (jsonRpcNativeSubscribeAdapter{}).BuildRequest(chain, nil, &dshackle.NativeSubscribeRequest{Method: "eth_subscribe", Selector: selector})
			if err != nil {
				return nil, err
			}
			return request.Selectors(), nil
		},
		"grpc subscribe": func(selector *dshackle.Selector) ([]protocol.RequestSelector, error) {
			request, err := (grpcNativeSubscribeAdapter{}).BuildRequest(suiChain(), nil, &dshackle.NativeSubscribeRequest{
				Method: "/sui.rpc.v2.SubscriptionService/SubscribeCheckpoints", Selector: selector,
			})
			if err != nil {
				return nil, err
			}
			return request.Selectors(), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				values  []string
				invalid bool
			}{
				{name: "plain", values: []string{"a"}},
				{name: "spaces", values: []string{" a "}},
				{name: "tabs", values: []string{"\ta\t"}},
				{name: "nonbreaking spaces", values: []string{"\u00a0a\u00a0"}},
				{name: "whitespace ID", values: []string{" "}},
				{name: "no values", invalid: true},
				{name: "empty ID", values: []string{""}, invalid: true},
				{name: "trailing empty", values: []string{"a", ""}, invalid: true},
				{name: "leading empty", values: []string{"", "a"}, invalid: true},
				{name: "extra whitespace", values: []string{"a", " "}, invalid: true},
				{name: "duplicate ID", values: []string{"a", "a"}, invalid: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					selectors, err := build(upstreamPinSelector(tc.values...))
					var id string
					if err == nil {
						require.Equal(t, []protocol.RequestSelector{protocol.RequestLabelSelector{Name: protocol.UpstreamIdLabel, Values: tc.values}}, selectors)
						id, err = protocol.UpstreamID(selectors)
					}
					if tc.invalid {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
						require.Equal(t, tc.values[0], id)
					}
				})
			}
			t.Run("ordinary label normalization", func(t *testing.T) {
				selectors, err := build(&dshackle.Selector{SelectorType: &dshackle.Selector_LabelSelector{LabelSelector: &dshackle.LabelSelector{
					Name: "region", Value: []string{" us ", "", "\t", "\u00a0eu\u00a0"},
				}}})
				require.NoError(t, err)
				require.Equal(t, []protocol.RequestSelector{protocol.RequestLabelSelector{Name: "region", Values: []string{"us", "eu"}}}, selectors)
			})
		})
	}
}

func TestNativeCallRejectsRawRequestAndItemPinConflict(t *testing.T) {
	request, failure := (jsonRpcNativeCallAdapter{}).BuildRequest(&chains.ConfiguredChain{MethodSpec: "eth"}, &dshackle.NativeCallItem{
		Id: 1, Method: "eth_chainId", Selectors: []*dshackle.Selector{upstreamPinSelector("a")},
	}, upstreamPinSelector(" a "), 0)
	require.Nil(t, request)
	require.NotNil(t, failure)
	require.Equal(t, int32(400), failure.GetItemErrorCode())
	require.Contains(t, failure.GetErrorMessage(), "conflicting upstream_id")
}

func upstreamPinSelector(values ...string) *dshackle.Selector {
	return &dshackle.Selector{SelectorType: &dshackle.Selector_LabelSelector{LabelSelector: &dshackle.LabelSelector{
		Name: protocol.UpstreamIdLabel, Value: values,
	}}}
}
