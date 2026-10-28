package utils

import "sync"

// Signal wakes everyone waiting on it: the channel C returns is closed by the
// next Notify. It carries no state, so nothing is lost when a waiter is slow;
// with no waiter a Notify allocates nothing.
type Signal struct {
	mu sync.Mutex
	ch chan struct{}
}

func (s *Signal) C() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch == nil {
		s.ch = make(chan struct{})
	}
	return s.ch
}

func (s *Signal) Notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch != nil {
		close(s.ch)
		s.ch = nil
	}
}
