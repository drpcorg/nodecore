package utils

func Map[T any, V any](a <-chan T, f func(T) V) chan V {
	o := make(chan V)
	go func() {
		for v := range a {
			o <- f(v)
		}
		close(o)
	}()
	return o
}
