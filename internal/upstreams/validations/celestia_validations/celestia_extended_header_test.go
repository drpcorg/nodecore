package celestia_validations_test

import (
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/upstreams/validations/celestia_validations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExtendedHeader(t *testing.T) {
	header, err := celestia_validations.ParseExtendedHeader([]byte(`{"header":{"chain_id":"celestia","height":"42","last_block_id":{"hash":"AB"}},"commit":{"block_id":{"hash":"CD"}}}`))
	require.NoError(t, err)

	height, err := header.Height()
	require.NoError(t, err)
	assert.Equal(t, uint64(42), height)
	assert.Equal(t, "celestia", header.Header.ChainId)
	assert.Equal(t, "CD", header.Commit.BlockId.Hash)
	assert.Equal(t, "AB", header.Header.LastBlockId.Hash)
}

func TestParseExtendedHeaderNonNumericHeight(t *testing.T) {
	header, err := celestia_validations.ParseExtendedHeader([]byte(`{"header":{"height":"abc"}}`))
	require.NoError(t, err)
	_, err = header.Height()
	assert.Error(t, err)
}

func TestParseExtendedHeaderTruncatesPayloadInError(t *testing.T) {
	// a real header is tens of KB; the error must not carry it whole into the logs
	payload := `{"header":{"chain_id":"celestia"},"validator_set":"` + strings.Repeat("x", 4000) + `"}`
	_, err := celestia_validations.ParseExtendedHeader([]byte(payload))
	require.Error(t, err)
	assert.Less(t, len(err.Error()), 400)
}
