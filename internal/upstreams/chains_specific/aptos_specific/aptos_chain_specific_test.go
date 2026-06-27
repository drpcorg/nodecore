package aptos_specific_test

import (
	"context"
	"encoding/hex"
	"strings"
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

func TestAptosGetLatestBlock(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()

	ledger := []byte(`{"chain_id":1,"block_height":"100","ledger_version":"5000",` +
		`"oldest_block_height":"0","oldest_ledger_version":"0"}`)
	conn.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/v1"
	})).Return(protocol.NewHttpUpstreamResponse("1", ledger, 200, protocol.Rest)).Once()

	hashHex := "0x" + strings.Repeat("ab", 32)
	blockBody := []byte(`{"block_height":"100","block_hash":"` + hashHex + `","first_version":"4900","last_version":"5000"}`)
	conn.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		rp := r.RequestParams()
		return r.Method() == "GET#/v1/blocks/by_height/*" && rp != nil && len(rp.PathParams) == 1 && rp.PathParams[0] == "100"
	})).Return(protocol.NewHttpUpstreamResponse("1", blockBody, 200, protocol.Rest)).Once()

	block, err := test_utils.NewAptosChainSpecific(ctx, conn).GetLatestBlock(ctx)
	assert.NoError(t, err)
	conn.AssertExpectations(t)

	rawHash, _ := hex.DecodeString(strings.Repeat("ab", 32))
	assert.Equal(t, uint64(100), block.Height)
	assert.Equal(t, uint64(5000), block.Slot)
	assert.Equal(t, blockchain.NewHashIdFromBytes(rawHash), block.Hash)
}
