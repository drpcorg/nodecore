package aptos_validations_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/aptos_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const ledgerInfoBody = `{"chain_id":1,"epoch":"16331","ledger_version":"5965411071",` +
	`"oldest_ledger_version":"0","ledger_timestamp":"1782581442052319",` +
	`"node_role":"full_node","oldest_block_height":"0","block_height":"860298804",` +
	`"git_hash":"ce732f6fcb5ce034d927a8d3b9c0d0b28d207e63"}`

func isHealthy(r protocol.RequestHolder) bool { return r.Method() == "GET#/v1/-/healthy" }
func isLedger(r protocol.RequestHolder) bool  { return r.Method() == "GET#/v1" }

func TestAptosHealthAvailable(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHealthy)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"message":"aptos-node:ok"}`), 200, protocol.Rest))
	v := aptos_validations.NewAptosHealthValidator("id", conn, chains.GetChain("aptos-mainnet").Chain, time.Second)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestAptosHealthSyncingOn503(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHealthy)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"message":"aptos-node:not ready"}`), 503, protocol.Rest))
	v := aptos_validations.NewAptosHealthValidator("id", conn, chains.GetChain("aptos-mainnet").Chain, time.Second)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

func TestAptosHealthUnavailableOnError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isHealthy)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := aptos_validations.NewAptosHealthValidator("id", conn, chains.GetChain("aptos-mainnet").Chain, time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestAptosChainValidatorValidOnMatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isLedger)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(ledgerInfoBody), 200, protocol.Rest))
	v := aptos_validations.NewAptosChainValidator("id", conn, chains.GetChain("aptos-mainnet"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestAptosChainValidatorFatalOnMismatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isLedger)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(ledgerInfoBody), 200, protocol.Rest))
	// aptos-testnet expects chain_id 2 but the node reports 1.
	v := aptos_validations.NewAptosChainValidator("id", conn, chains.GetChain("aptos-testnet"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestAptosChainValidatorRetriesOnMissingChainId(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// A 200 body without chain_id (e.g. a gateway's JSON error envelope) is a
	// transient fetch problem, not proof of a wrong chain: retry, don't kill.
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isLedger)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{}`), 200, protocol.Rest))
	v := aptos_validations.NewAptosChainValidator("id", conn, chains.GetChain("aptos-mainnet"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}
