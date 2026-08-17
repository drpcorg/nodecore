package stellar_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/upstreams/labels/stellar_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStellarClientLabelsDetectorRequestsVersionInfo(t *testing.T) {
	d := stellar_labels.NewStellarClientLabelsDetector(chains.GetChain("stellar").Chain)

	req, err := d.NodeTypeRequest()
	require.NoError(t, err)
	assert.Equal(t, "getVersionInfo", req.Method())
}

func TestStellarClientLabelsDetectorTruncatesTheCommitSuffix(t *testing.T) {
	d := stellar_labels.NewStellarClientLabelsDetector(chains.GetChain("stellar").Chain)

	version, clientType, err := d.ClientVersionAndType(
		[]byte(`{"version":"27.1.1-7e71a2c","commitHash":"7e71a2c","protocolVersion":27}`))
	require.NoError(t, err)
	assert.Equal(t, "27.1.1", version)
	assert.Equal(t, "stellar-rpc", clientType)
}

func TestStellarClientLabelsDetectorKeepsABareSemver(t *testing.T) {
	d := stellar_labels.NewStellarClientLabelsDetector(chains.GetChain("stellar").Chain)

	version, clientType, err := d.ClientVersionAndType([]byte(`{"version":"27.1.1"}`))
	require.NoError(t, err)
	assert.Equal(t, "27.1.1", version)
	assert.Equal(t, "stellar-rpc", clientType)
}

func TestStellarClientLabelsDetectorErrorsOnUnparseablePayload(t *testing.T) {
	d := stellar_labels.NewStellarClientLabelsDetector(chains.GetChain("stellar").Chain)

	_, _, err := d.ClientVersionAndType([]byte(`not json`))
	assert.Error(t, err)
}

func TestStellarHorizonClientLabelsDetectorRequestsTheRoot(t *testing.T) {
	d := stellar_labels.NewStellarHorizonClientLabelsDetector(chains.GetChain("stellar").Chain)

	req, err := d.NodeTypeRequest()
	require.NoError(t, err)
	assert.Equal(t, "GET#/", req.Method())
}

func TestStellarHorizonClientLabelsDetectorTruncatesTheCommitSuffix(t *testing.T) {
	d := stellar_labels.NewStellarHorizonClientLabelsDetector(chains.GetChain("stellar").Chain)

	version, clientType, err := d.ClientVersionAndType(
		[]byte(`{"horizon_version":"27.0.0-9f3c1d2","network_passphrase":"x"}`))
	require.NoError(t, err)
	assert.Equal(t, "27.0.0", version)
	assert.Equal(t, "horizon", clientType)
}

func TestStellarHorizonClientLabelsDetectorErrorsOnUnparseablePayload(t *testing.T) {
	d := stellar_labels.NewStellarHorizonClientLabelsDetector(chains.GetChain("stellar").Chain)

	_, _, err := d.ClientVersionAndType([]byte(`<html>`))
	assert.Error(t, err)
}
