package beacon_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/upstreams/labels/beacon_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBeaconClientVersionAndType(t *testing.T) {
	detector := beacon_labels.NewBeaconChainClientLabelsDetector(chains.GetChain("eth-beacon-chain").Chain)

	cases := []struct {
		body        string
		wantVersion string
		wantType    string
	}{
		{`{"data":{"version":"Lighthouse/v8.2.0-120c3c6/x86_64-linux"}}`, "Lighthouse/v8.2.0-120c3c6/x86_64-linux", "lighthouse"},
		{`{"data":{"version":"Prysm/v5.0.0/abcdef"}}`, "Prysm/v5.0.0/abcdef", "prysm"},
		{`{"data":{"version":"teku"}}`, "teku", "teku"},
		{`{"data":{"version":""}}`, "", ""},
	}
	for _, c := range cases {
		version, clientType, err := detector.ClientVersionAndType([]byte(c.body))
		require.NoError(t, err)
		assert.Equal(t, c.wantVersion, version)
		assert.Equal(t, c.wantType, clientType)
	}
}

func TestBeaconClientVersionUnparseable(t *testing.T) {
	detector := beacon_labels.NewBeaconChainClientLabelsDetector(chains.GetChain("eth-beacon-chain").Chain)
	_, _, err := detector.ClientVersionAndType([]byte(`not json`))
	assert.ErrorContains(t, err, "unparseable")
}

func TestBeaconNodeTypeRequestTemplate(t *testing.T) {
	detector := beacon_labels.NewBeaconChainClientLabelsDetector(chains.GetChain("eth-beacon-chain").Chain)
	req, err := detector.NodeTypeRequest()
	require.NoError(t, err)
	assert.Equal(t, "GET#/eth/v1/node/version", req.Method())
}
