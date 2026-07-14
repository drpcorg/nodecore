package aptos_specific_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestAptosSubscribeHeadRequestUnsupported(t *testing.T) {
	req, err := test_utils.NewAptosChainSpecific(context.Background(), nil).SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.EqualError(t, err, "aptos does not support websocket subscriptions")
}

func TestAptosParseBlock(t *testing.T) {
	body := []byte(`{"chain_id":1,"block_height":"860298804","ledger_version":"5965411071"}`)
	block, err := test_utils.NewAptosChainSpecific(context.Background(), nil).ParseBlock(body)
	assert.NoError(t, err)
	assert.Equal(t, protocol.NewBlock(860298804, 0, blockchain.EmptyHash, blockchain.EmptyHash), block)
}

func aptosLedgerResponse(height, version string) protocol.ResponseHolder {
	body := `{"chain_id":1,"block_height":"` + height + `","ledger_version":"` + version + `",` +
		`"oldest_block_height":"0","oldest_ledger_version":"0"}`
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.Rest)
}

func syntheticId(height uint64) blockchain.HashId {
	out := make([]byte, 32)
	for i := range 8 {
		out[31-i] = byte(height & 0xff)
		height >>= 8
	}
	return blockchain.NewHashIdFromBytes(out)
}

// The Aptos REST API exposes no parent hash on the head-poll path, so both the
// hash and the parent hash are synthetic height encodings (the Solana pattern):
// consumers checking parent linkage must see block(N).ParentHash == block(N-1).Hash.
func TestAptosGetLatestBlockUsesConsistentSyntheticHashes(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/v1"
	})).Return(aptosLedgerResponse("100", "5000")).Once()

	block, err := test_utils.NewAptosChainSpecific(ctx, conn).GetLatestBlock(ctx)
	assert.NoError(t, err)
	// exactly one upstream request per poll: no by_height hash fetch
	conn.AssertExpectations(t)

	assert.Equal(t, uint64(100), block.Height)
	assert.Equal(t, uint64(5000), block.Slot)
	assert.Equal(t, syntheticId(100), block.Hash)
	assert.Equal(t, syntheticId(99), block.ParentHash)
}

func TestAptosHeadLinkageIsConsistentAcrossPolls(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", ctx, mock.Anything).Return(aptosLedgerResponse("100", "5000")).Once()
	conn.On("SendRequest", ctx, mock.Anything).Return(aptosLedgerResponse("101", "5007")).Once()

	specific := test_utils.NewAptosChainSpecific(ctx, conn)
	first, err := specific.GetLatestBlock(ctx)
	assert.NoError(t, err)
	second, err := specific.GetLatestBlock(ctx)
	assert.NoError(t, err)

	assert.Equal(t, first.Hash, second.ParentHash)
}
