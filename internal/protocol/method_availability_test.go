package protocol_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
)

func TestClassifyMethodAvailability(t *testing.T) {
	tests := []struct {
		name     string
		err      *protocol.ResponseError
		expected protocol.MethodAvailability
	}{
		{
			name:     "nil error tells us nothing",
			err:      nil,
			expected: protocol.MethodAvailabilityUnknown,
		},
		{
			name:     "geth does not exist",
			err:      protocol.ResponseErrorWithMessage("the method trace_block does not exist/is not available"),
			expected: protocol.MethodNotAvailable,
		},
		{
			name:     "geth module disabled",
			err:      protocol.ResponseErrorWithMessage("trace_block found but the containing module is disabled"),
			expected: protocol.MethodNotAvailable,
		},
		{
			name:     "lowercase method not found",
			err:      protocol.ResponseErrorWithMessage("method not found"),
			expected: protocol.MethodNotAvailable,
		},
		{
			name:     "capitalised Method not found",
			err:      protocol.ResponseErrorWithMessage("Method not found"),
			expected: protocol.MethodNotAvailable,
		},
		{
			name:     "method is not available",
			err:      protocol.ResponseErrorWithMessage("Method is not available"),
			expected: protocol.MethodNotAvailable,
		},
		{
			name:     "the method X is not available",
			err:      protocol.ResponseErrorWithMessage("The method trace_block is not available"),
			expected: protocol.MethodNotAvailable,
		},
		{
			name:     "jsonrpc code wins without a matching message",
			err:      protocol.ResponseErrorWithData(protocol.NoSupportedMethod, "nope", nil),
			expected: protocol.MethodNotAvailable,
		},
		{
			name:     "missing required argument means the method exists",
			err:      protocol.ResponseErrorWithMessage("missing value for required argument 0"),
			expected: protocol.MethodAvailable,
		},
		{
			name:     "invalid params means the method exists",
			err:      protocol.ResponseErrorWithMessage("Invalid params"),
			expected: protocol.MethodAvailable,
		},
		{
			name:     "a public gateway rejecting the method by name",
			err:      protocol.ResponseErrorWithMessage("method error; invalid method: eth_callMany"),
			expected: protocol.MethodNotAvailable,
		},
		{
			name:     "lotus quotes the method name",
			err:      protocol.ResponseErrorWithMessage("method 'eth_callMany' not found"),
			expected: protocol.MethodNotAvailable,
		},
		{
			name:     "a proxy complaining about the http verb is not about the method",
			err:      protocol.ResponseErrorWithMessage("invalid method: GET"),
			expected: protocol.MethodAvailabilityUnknown,
		},
		{
			name:     "-32602 means the method exists whatever the wording",
			err:      protocol.ResponseErrorWithData(protocol.InvalidParams, "wrong param count", nil),
			expected: protocol.MethodAvailable,
		},
		{
			name:     "unrelated error tells us nothing",
			err:      protocol.ResponseErrorWithMessage("execution reverted"),
			expected: protocol.MethodAvailabilityUnknown,
		},
		{
			name:     "transient transport error tells us nothing",
			err:      protocol.ResponseErrorWithMessage("connection reset by peer"),
			expected: protocol.MethodAvailabilityUnknown,
		},
		{
			name:     "not-available is checked before the params patterns",
			err:      protocol.ResponseErrorWithMessage("Method not found, Invalid params"),
			expected: protocol.MethodNotAvailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, protocol.ClassifyMethodAvailability(test.err))
		})
	}
}
