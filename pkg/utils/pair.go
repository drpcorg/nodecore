package utils

type Pair[F any, S any] struct {
	F F
	S S
}

func NewPair[F any, S any](f F, s S) Pair[F, S] {
	return Pair[F, S]{
		F: f,
		S: s,
	}
}

func NewPairP[F any, S any](f F, s S) *Pair[F, S] {
	r := NewPair(f, s)
	return &r
}
