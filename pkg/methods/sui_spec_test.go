package specs_test

import (
	"testing"

	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var suiUnaryMethods = []string{
	"/sui.rpc.v2.LedgerService/GetServiceInfo",
	"/sui.rpc.v2.LedgerService/GetObject",
	"/sui.rpc.v2.LedgerService/BatchGetObjects",
	"/sui.rpc.v2.LedgerService/GetTransaction",
	"/sui.rpc.v2.LedgerService/BatchGetTransactions",
	"/sui.rpc.v2.LedgerService/GetCheckpoint",
	"/sui.rpc.v2.LedgerService/GetEpoch",
	"/sui.rpc.v2.StateService/ListDynamicFields",
	"/sui.rpc.v2.StateService/ListOwnedObjects",
	"/sui.rpc.v2.StateService/GetCoinInfo",
	"/sui.rpc.v2.StateService/GetBalance",
	"/sui.rpc.v2.StateService/ListBalances",
	"/sui.rpc.v2.MovePackageService/GetPackage",
	"/sui.rpc.v2.MovePackageService/GetDatatype",
	"/sui.rpc.v2.MovePackageService/GetFunction",
	"/sui.rpc.v2.MovePackageService/ListPackageVersions",
	"/sui.rpc.v2.TransactionExecutionService/ExecuteTransaction",
	"/sui.rpc.v2.TransactionExecutionService/SimulateTransaction",
	"/sui.rpc.v2.SignatureVerificationService/VerifySignature",
	"/sui.rpc.v2.NameService/LookupName",
	"/sui.rpc.v2.NameService/ReverseLookupName",
}

var suiFiniteStreamMethods = []string{
	"/sui.rpc.v2.LedgerService/ListCheckpoints",
	"/sui.rpc.v2.LedgerService/ListTransactions",
	"/sui.rpc.v2.LedgerService/ListEvents",
}

var suiSubscriptionMethods = []string{
	"/sui.rpc.v2.SubscriptionService/SubscribeCheckpoints",
	"/sui.rpc.v2.SubscriptionService/SubscribeTransactions",
	"/sui.rpc.v2.SubscriptionService/SubscribeEvents",
}

func suiServerStreamMethods() []string {
	return append(append([]string{}, suiFiniteStreamMethods...), suiSubscriptionMethods...)
}

func TestSuiBundleIsGrpcOnly(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	assert.Equal(t, []specs.ApiConnectorType{specs.GrpcConnector}, specs.GetSpecConnectors("sui"))

	methods := specs.GetSpecMethodsByConnectors("sui", []specs.ApiConnectorType{specs.GrpcConnector})
	require.NotNil(t, methods)
	assert.Len(t, methods[specs.DefaultMethodGroup], len(suiUnaryMethods)+len(suiServerStreamMethods()))
}

func TestSuiMethodCallTypes(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	for _, name := range suiUnaryMethods {
		method := specs.GetSpecMethod("sui", name)
		require.NotNil(t, method, name)
		assert.Equal(t, specs.GrpcCallTypeUnary, method.GrpcCallType(), name)
	}
	for _, name := range suiFiniteStreamMethods {
		method := specs.GetSpecMethod("sui", name)
		require.NotNil(t, method, name)
		assert.Equal(t, specs.GrpcCallTypeServerStreamFinite, method.GrpcCallType(), name)
		assert.True(t, method.IsSubscribe(), name)
	}
	for _, name := range suiSubscriptionMethods {
		method := specs.GetSpecMethod("sui", name)
		require.NotNil(t, method, name)
		assert.Equal(t, specs.GrpcCallTypeServerStreamSubscription, method.GrpcCallType(), name)
		assert.True(t, method.IsSubscribe(), name)
	}
}

// gRPC caching is a follow-up task, so nothing is cacheable in v1.
func TestSuiMethodsAreNotCacheable(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	for _, name := range append(append([]string{}, suiUnaryMethods...), suiServerStreamMethods()...) {
		method := specs.GetSpecMethod("sui", name)
		require.NotNil(t, method, name)
		assert.False(t, method.IsCacheable(), "%s must not be cacheable", name)
	}
}

func TestGetGrpcServicesListsAllSuiServices(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	assert.Equal(t, []string{
		"sui.rpc.v2.LedgerService",
		"sui.rpc.v2.MovePackageService",
		"sui.rpc.v2.NameService",
		"sui.rpc.v2.SignatureVerificationService",
		"sui.rpc.v2.StateService",
		"sui.rpc.v2.SubscriptionService",
		"sui.rpc.v2.TransactionExecutionService",
	}, specs.GetGrpcServices())
}
