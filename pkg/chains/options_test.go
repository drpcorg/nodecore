package chains

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// http-response-timeout: negative is a configuration error; 0 is legal and means "no client-side
// timeout" (the caller's context is the only thing that ends a stuck exchange).
func TestOptionsValidateHttpResponseTimeout(t *testing.T) {
	negative := &Options{HttpResponseTimeout: new(-time.Second)}
	assert.ErrorContains(t, negative.Validate(), "http response timeout can't be less than 0")

	zero := &Options{HttpResponseTimeout: new(time.Duration(0))}
	assert.NoError(t, zero.Validate())

	unset := &Options{}
	assert.NoError(t, unset.Validate())
}
