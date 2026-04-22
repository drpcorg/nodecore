package quorum_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/stretchr/testify/assert"
)

func TestParams_Requested(t *testing.T) {
	tests := []struct {
		name string
		p    quorum.Params
		want bool
	}{
		{"both zero", quorum.Params{}, false},
		{"only quorum", quorum.Params{Quorum: 2}, false},
		{"only quorum_of", quorum.Params{QuorumOf: 3}, false},
		{"quorum > quorum_of", quorum.Params{Quorum: 4, QuorumOf: 3}, false},
		{"valid", quorum.Params{Quorum: 2, QuorumOf: 3}, true},
		{"equal", quorum.Params{Quorum: 3, QuorumOf: 3}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.p.Requested())
		})
	}
}

func TestParams_EncodeQuery(t *testing.T) {
	p := quorum.Params{Quorum: 2, QuorumOf: 3}
	got, err := url.ParseQuery(p.EncodeQuery())
	assert.NoError(t, err)
	assert.Equal(t, "3", got.Get("quorum"))
	assert.Equal(t, "2", got.Get("quorum_required"))

	assert.Empty(t, quorum.Params{}.EncodeQuery())
}

func TestParamsFromQuery(t *testing.T) {
	q := url.Values{}
	q.Set("quorum", "5")
	q.Set("quorum_required", "2")
	p := quorum.ParamsFromQuery(q)
	assert.Equal(t, quorum.Params{Quorum: 2, QuorumOf: 5}, p)
	assert.True(t, p.Requested())

	q = url.Values{}
	q.Set("quorum", "3")
	q.Set("quorum_required", "not a number")
	p = quorum.ParamsFromQuery(q)
	assert.Equal(t, quorum.Params{Quorum: 0, QuorumOf: 3}, p)
	assert.False(t, p.Requested())

	assert.False(t, quorum.ParamsFromQuery(url.Values{}).Requested())
}

func TestWithParams_RoundTrip(t *testing.T) {
	ctx := context.Background()

	ctx2 := quorum.WithParams(ctx, quorum.Params{})
	_, ok := quorum.FromContext(ctx2)
	assert.False(t, ok)

	p := quorum.Params{Quorum: 2, QuorumOf: 3}
	ctx3 := quorum.WithParams(ctx, p)
	got, ok := quorum.FromContext(ctx3)
	assert.True(t, ok)
	assert.Equal(t, p, got)
}

func TestFromContext_NilContext(t *testing.T) {
	//nolint:staticcheck // exercising the nil-guard in FromContext.
	var nilCtx context.Context
	_, ok := quorum.FromContext(nilCtx)
	assert.False(t, ok)
}
