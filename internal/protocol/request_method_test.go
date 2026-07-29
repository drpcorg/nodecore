package protocol_test

import (
	"testing"
	"unicode/utf8"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestMethodKeepsValidNameInBothForms(t *testing.T) {
	for _, name := range []string{"eth_call", "GET#/v2/status", "юникод"} {
		method := protocol.NewRequestMethod(name)

		assert.Equal(t, name, method.Name())
		assert.Equal(t, name, method.ValidUTF8Name())
	}
}

func TestRequestMethodKeepsInvalidNameIntactAndSanitizesTheLabelForm(t *testing.T) {
	name := "GET#/status" + string([]byte{0x80})

	method := protocol.NewRequestMethod(name)

	assert.Equal(t, name, method.Name())
	assert.False(t, utf8.ValidString(method.Name()))
	assert.Equal(t, "GET#/status�", method.ValidUTF8Name())
	assert.True(t, utf8.ValidString(method.ValidUTF8Name()))
}

// A client-supplied method name with invalid UTF-8 used to take the process down:
// prometheus panics on such a label value and there is no recover() on the request
// path. These two tests pin the fix at the sinks that actually break - the metric
// label and the observer's value, which is what ends up in the proto3 stats key.
func TestRequestMethodLabelFormIsAcceptedByPrometheus(t *testing.T) {
	metric := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total"}, []string{"chain", "method"})

	restRequest := protocol.NewUpstreamRestRequest("1", "GET#/status"+string([]byte{0xc0}), &protocol.RequestParams{}, nil, "cosmos")
	jsonRpcRequest := protocol.NewUpstreamJsonRpcRequest(
		"1",
		protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_" + string([]byte{0x80})},
		false,
		"eth",
	)

	for _, request := range []protocol.RequestHolder{restRequest, jsonRpcRequest} {
		assert.NotPanics(t, func() {
			metric.WithLabelValues("cosmos", request.Method().ValidUTF8Name()).Inc()
		})
		// The raw form is what used to reach the label and crash the process.
		// Asserting it still panics keeps the reason for ValidUTF8Name visible.
		assert.Panics(t, func() {
			metric.WithLabelValues("cosmos", request.Method().Name()).Inc()
		})
	}
}

func TestRequestObserverExposesTheValidMethodFormToStats(t *testing.T) {
	name := "eth_" + string([]byte{0x80})
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: name}, false, "eth")

	request.RequestObserver().AddResult(protocol.NewUnaryRequestResult().WithUpstreamId("upId"), true)

	results := request.RequestObserver().GetResults()
	require.Len(t, results, 1)
	result, ok := results[0].(*protocol.UnaryRequestResult)
	require.True(t, ok)

	// The result carries the whole RequestMethod, so the stats key builder picks
	// the form it needs: proto3's "string method" field requires valid UTF-8 -
	// an invalid one fails proto.Marshal and drops the whole stats batch.
	assert.Equal(t, name, result.GetMethod().Name())
	assert.Equal(t, "eth_�", result.GetMethod().ValidUTF8Name())
	assert.True(t, utf8.ValidString(result.GetMethod().ValidUTF8Name()))
}

func TestRequestMethodIsComparable(t *testing.T) {
	methods := map[protocol.RequestMethod]int{protocol.NewRequestMethod("eth_call"): 1}

	assert.Equal(t, 1, methods[protocol.NewRequestMethod("eth_call")])
	assert.NotEqual(t, protocol.NewRequestMethod("eth_call"), protocol.NewRequestMethod("eth_chainId"))
}
