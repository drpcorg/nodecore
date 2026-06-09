package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLabelBalancingValidate(t *testing.T) {
	tests := []struct {
		name      string
		order     []string
		errSubstr string
	}{
		{name: "empty order", order: nil, errSubstr: "at least one label"},
		{name: "empty label", order: []string{"full", ""}, errSubstr: "must not contain an empty label"},
		{name: "duplicate label", order: []string{"full", "archive", "full"}, errSubstr: "duplicate label 'full'"},
		{name: "valid", order: []string{"full", "archive", "fast"}, errSubstr: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			err := (&LabelBalancingConfig{Order: test.order}).validate()
			if test.errSubstr == "" {
				assert.NoError(te, err)
			} else {
				assert.ErrorContains(te, err, test.errSubstr)
			}
		})
	}
}

func TestLabelBalancingSetDefaultsIncludeDefault(t *testing.T) {
	l := &LabelBalancingConfig{Order: []string{"full"}}
	l.setDefaults()
	require.NotNil(t, l.IncludeDefault)
	assert.True(t, *l.IncludeDefault, "include-default should default to true")

	l2 := &LabelBalancingConfig{Order: []string{"full"}, IncludeDefault: new(false)}
	l2.setDefaults()
	assert.False(t, *l2.IncludeDefault, "explicit include-default=false must be preserved")

	var l3 *LabelBalancingConfig
	assert.NotPanics(t, func() { l3.setDefaults() }, "nil receiver must be safe")
}

func TestLabelBalancingFor(t *testing.T) {
	global := &LabelBalancingConfig{Order: []string{"full"}}
	perChain := &LabelBalancingConfig{Order: []string{"archive"}}
	u := &UpstreamConfig{
		LabelBalancing: global,
		ChainDefaults: map[string]*ChainDefaults{
			"polygon":  {LabelBalancing: perChain},
			"optimism": {},
		},
	}

	assert.Same(t, perChain, u.LabelBalancingFor("polygon"), "per-chain override wins")
	assert.Same(t, global, u.LabelBalancingFor("optimism"), "chain without override falls back to global")
	assert.Same(t, global, u.LabelBalancingFor("ethereum"), "chain absent from chain-defaults falls back to global")

	noGlobal := &UpstreamConfig{}
	assert.Nil(t, noGlobal.LabelBalancingFor("ethereum"), "no config anywhere -> nil")
}
