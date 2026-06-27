package aptos_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/upstreams/labels/aptos_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

func TestAptosNodeTypeRequest(t *testing.T) {
	req, err := aptos_labels.NewAptosClientLabelsDetector(chains.GetChain("aptos-mainnet").Chain).NodeTypeRequest()
	assert.NoError(t, err)
	assert.Equal(t, "GET#/v1", req.Method())
}

func TestAptosClientVersionAndType(t *testing.T) {
	body := []byte(`{"node_role":"full_node","git_hash":"ce732f6fcb5ce034d927a8d3b9c0d0b28d207e63"}`)
	version, clientType, err := aptos_labels.NewAptosClientLabelsDetector(chains.GetChain("aptos-mainnet").Chain).
		ClientVersionAndType(body)
	assert.NoError(t, err)
	assert.Equal(t, "ce732f6fcb5c", version) // first 12 chars of git_hash
	assert.Equal(t, "aptos-node", clientType)
}
