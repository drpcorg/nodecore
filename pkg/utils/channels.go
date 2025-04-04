package utils

func FlatMap[T any, V any](a <-chan T, f func(T) []V) chan V {
	o := make(chan V)
	go func() {
		for v := range a {
			for _, r := range f(v) {
				o <- r
			}
		}
		close(o)
	}()
	return o
}

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
