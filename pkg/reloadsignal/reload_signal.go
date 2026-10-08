// Package reloadsignal takes SIGHUP over for the whole life of the process.
//
// SIGHUP asks nodecore to reload its upstream list, but a process that has not
// subscribed to the signal is terminated by it. Subscribing where the reload is
// served would leave nodecore mortal while it starts and while it shuts down,
// so it is done here, at package initialization - before main, and as early in
// the initialization of the binary as a package with no dependencies gets - and
// is never undone. This package must keep importing nothing but the standard
// library: every import would move its init further from the process start.
//
// What remains is the time the Go runtime needs to reach this init. A parent
// that wants that covered too can start nodecore with SIGHUP ignored; the
// subscription below takes the signal back from there.
package reloadsignal

import (
	"os"
	"os/signal"
	"syscall"
)

var signals = make(chan os.Signal, 1)

func init() {
	signal.Notify(signals, syscall.SIGHUP)
}

// Signals delivers the SIGHUPs sent to the process. One that arrives before
// anybody reads waits in the channel; further ones are dropped until it is read.
func Signals() <-chan os.Signal {
	return signals
}
