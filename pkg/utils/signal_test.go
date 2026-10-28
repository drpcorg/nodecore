package utils_test

import (
	"testing"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
)

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestSignalWakesEveryWaiterOfTheNextNotify(t *testing.T) {
	var signal utils.Signal
	first, second := signal.C(), signal.C()
	assert.False(t, closed(first))

	signal.Notify()
	assert.True(t, closed(first))
	assert.True(t, closed(second))

	// a later waiter waits for the next one
	next := signal.C()
	assert.False(t, closed(next))
	signal.Notify()
	signal.Notify()
	assert.True(t, closed(next))
}
