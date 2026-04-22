package quorum

import (
	"context"
	"net/url"
	"strconv"
)

type Params struct {
	Quorum   int
	QuorumOf int
}

func (p Params) Requested() bool {
	return p.Quorum > 0 && p.QuorumOf > 0 && p.Quorum <= p.QuorumOf
}

func (p Params) EncodeQuery() string {
	if !p.Requested() {
		return ""
	}
	v := url.Values{}
	v.Set("quorum", strconv.Itoa(p.QuorumOf))
	v.Set("quorum_required", strconv.Itoa(p.Quorum))
	return v.Encode()
}

func ParamsFromQuery(q url.Values) Params {
	p := Params{}
	if raw := q.Get("quorum"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			p.QuorumOf = n
		}
	}
	if raw := q.Get("quorum_required"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			p.Quorum = n
		}
	}
	return p
}

type ctxKey struct{}

func WithParams(ctx context.Context, p Params) context.Context {
	if !p.Requested() {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, p)
}

func FromContext(ctx context.Context) (Params, bool) {
	if ctx == nil {
		return Params{}, false
	}
	p, ok := ctx.Value(ctxKey{}).(Params)
	return p, ok
}
