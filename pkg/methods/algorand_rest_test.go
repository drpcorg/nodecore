package specs_test

import (
	"testing"

	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAlgorandRestRoutesAreBounded guards the metrics-cardinality fix: the
// Algorand spec must declare REST route templates so a concrete path like
// "GET#/v2/blocks/62206584" collapses to the bounded template "GET#/v2/blocks/*"
// instead of leaking the block number into the `method` metric label.
func TestAlgorandRestRoutesAreBounded(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	tests := []struct {
		name       string
		fullPath   string
		wantTmpl   string
		wantParams []string
	}{
		{"block by round", "GET#/v2/blocks/62206584", "GET#/v2/blocks/*", []string{"62206584"}},
		{"account by address", "GET#/v2/accounts/ABC123XYZ", "GET#/v2/accounts/*", []string{"ABC123XYZ"}},
		{"pending tx by id", "GET#/v2/transactions/pending/TXID9", "GET#/v2/transactions/pending/*", []string{"TXID9"}},
		{"literal status", "GET#/v2/status", "GET#/v2/status", nil},
		{"literal genesis", "GET#/genesis", "GET#/genesis", nil},
		{"submit tx", "POST#/v2/transactions", "POST#/v2/transactions", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, params, ok := specs.MatchRestMethod("algorand", tt.fullPath)
			require.True(t, ok, "path %q should match an algorand REST template", tt.fullPath)
			assert.Equal(t, tt.wantTmpl, tmpl)
			assert.Equal(t, tt.wantParams, params)

			// The bounded template must rebuild back into the concrete upstream path.
			verb, path, err := utils.BuildRestURL(tmpl, params)
			require.NoError(t, err)
			gotVerb, gotPath, _ := splitFullPath(tt.fullPath)
			assert.Equal(t, gotVerb, verb)
			assert.Equal(t, gotPath, path)
		})
	}
}

func splitFullPath(fullPath string) (verb, path string, ok bool) {
	for i := 0; i < len(fullPath); i++ {
		if fullPath[i] == '#' {
			return fullPath[:i], fullPath[i+1:], true
		}
	}
	return fullPath, "", false
}
