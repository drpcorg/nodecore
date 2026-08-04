package specs_test

import (
	"testing"

	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonRpcMethods / wsMethods return the default-group method names a spec
// exposes on one connector type. Both specs put the regular calls on json-rpc
// AND websocket, so the websocket view is a superset of the json-rpc one.
func jsonRpcMethods(t *testing.T, specName string) map[string]*specs.Method {
	t.Helper()
	groups := specs.GetSpecMethodsByConnectors(specName, []specs.ApiConnectorType{specs.JsonRpcConnector})
	require.NotNil(t, groups, "spec %s has no json-rpc methods", specName)
	return groups[specs.DefaultMethodGroup]
}

func wsMethods(t *testing.T, specName string) map[string]*specs.Method {
	t.Helper()
	groups := specs.GetSpecMethodsByConnectors(specName, []specs.ApiConnectorType{specs.WebsocketConnector})
	require.NotNil(t, groups, "spec %s has no websocket methods", specName)
	return groups[specs.DefaultMethodGroup]
}

func TestPolkadotSpecResolvedCounts(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	assert.Len(t, jsonRpcMethods(t, "polkadot"), 81)
	assert.Len(t, wsMethods(t, "polkadot"), 92)
	assert.Len(t, jsonRpcMethods(t, "avail"), 92)
	assert.Len(t, wsMethods(t, "avail"), 103)
}

// Substrate serves every RPC method over ws as well as http, so a ws-only
// upstream must keep the regular calls and not collapse to subscriptions only.
func TestPolkadotRegularCallsAreAvailableOverWebsocket(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	assert.ElementsMatch(t,
		[]specs.ApiConnectorType{specs.JsonRpcConnector, specs.WebsocketConnector},
		specs.GetSpecConnectors("polkadot"),
	)
	assert.Contains(t, wsMethods(t, "polkadot"), "state_getStorage")
	assert.Contains(t, wsMethods(t, "polkadot"), "chain_getHeader")
}

func TestAvailIsStrictSupersetOfPolkadot(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	base := jsonRpcMethods(t, "polkadot")
	avail := jsonRpcMethods(t, "avail")
	for name := range base {
		assert.Contains(t, avail, name, "avail lost polkadot method %s", name)
	}
	for _, extra := range []string{
		"kate_blockLength", "kate_queryDataProof", "kate_queryProof", "kate_queryRows",
		"mmr_generateProof", "mmr_root", "mmr_verifyProof", "mmr_verifyProofStateless",
		"chainSpec_v1_chainName", "chainSpec_v1_genesisHash", "chainSpec_v1_properties",
	} {
		assert.Contains(t, avail, extra, "avail is missing %s", extra)
		assert.NotContains(t, base, extra, "polkadot should not carry the avail method %s", extra)
	}
}

// Nothing is cacheable: Polkadot's block ref is an optional hash argument, so an
// omitted ref means "latest" and no tag parser detects that.
func TestPolkadotMethodsAreNotCacheableAndHaveNoDispatch(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	for _, specName := range []string{"polkadot", "avail"} {
		for name, method := range wsMethods(t, specName) {
			assert.False(t, method.IsCacheable(), "%s.%s must not be cacheable", specName, name)
			assert.Equal(t, specs.DispatchDefault, method.DispatchPolicy(), "%s.%s must not set dispatch", specName, name)
		}
	}
}

func TestPolkadotNoUnstableMethods(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	for _, specName := range []string{"polkadot", "avail"} {
		for name := range wsMethods(t, specName) {
			assert.NotContains(t, name, "_unstable_", "%s must not declare %s", specName, name)
		}
	}
}

// The notification name is echoed to the client and the unsubscribe name is sent
// upstream, so both are pinned here: a typo cannot be caught anywhere else -
// upstream notifications are matched on params.subscription, never on method.
func TestPolkadotSubscriptionTriples(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	expected := []struct {
		subscribe    string
		notification string
		unsubscribe  string
	}{
		{"subscribe_newHead", "chain_newHead", "unsubscribe_newHead"},
		{"chain_subscribeNewHead", "chain_newHead", "chain_unsubscribeNewHead"},
		{"chain_subscribeNewHeads", "chain_newHead", "chain_unsubscribeNewHeads"},
		{"chain_subscribeAllHeads", "chain_allHead", "chain_unsubscribeAllHeads"},
		{"chain_subscribeFinalizedHeads", "chain_finalizedHead", "chain_unsubscribeFinalizedHeads"},
		{"chain_subscribeFinalisedHeads", "chain_finalizedHead", "chain_unsubscribeFinalisedHeads"},
		{"chain_subscribeRuntimeVersion", "state_runtimeVersion", "chain_unsubscribeRuntimeVersion"},
		{"state_subscribeRuntimeVersion", "state_runtimeVersion", "state_unsubscribeRuntimeVersion"},
		{"state_subscribeStorage", "state_storage", "state_unsubscribeStorage"},
		{"author_submitAndWatchExtrinsic", "author_watchExtrinsic", "author_unwatchExtrinsic"},
		{"grandpa_subscribeJustifications", "grandpa_justifications", "grandpa_unsubscribeJustifications"},
	}

	for _, specName := range []string{"polkadot", "avail"} {
		for _, want := range expected {
			assert.True(t, specs.IsSubscribeMethod(specName, want.subscribe), "%s: %s is not a subscribe method", specName, want.subscribe)

			method := specs.GetSpecMethod(specName, want.subscribe)
			require.NotNil(t, method, "%s: no spec method %s", specName, want.subscribe)
			require.NotNil(t, method.Subscription, "%s: %s has no subscription settings", specName, want.subscribe)
			assert.Equal(t, want.notification, method.Subscription.Method, "%s: wrong notification name for %s", specName, want.subscribe)

			unsub, ok := specs.GetUnsubscribeMethod(specName, want.subscribe)
			require.True(t, ok, "%s: no unsubscribe method for %s", specName, want.subscribe)
			assert.Equal(t, want.unsubscribe, unsub, "%s: wrong unsubscribe name for %s", specName, want.subscribe)
		}
	}
}
