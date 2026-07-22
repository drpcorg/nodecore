package starknet_validations_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/starknet_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func isChainId(r protocol.RequestHolder) bool { return r.Method() == "starknet_chainId" }
func isSyncing(r protocol.RequestHolder) bool { return r.Method() == "starknet_syncing" }

func TestStarknetChainValidatorValidOnMatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isChainId)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x534e5f5345504f4c4941"`), protocol.JsonRpc))
	v := starknet_validations.NewStarknetChainValidator("id", conn, chains.GetChain("starknet-sepolia"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestStarknetChainValidatorValidOnCaseInsensitiveMatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isChainId)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x534E5F4D41494E"`), protocol.JsonRpc))
	v := starknet_validations.NewStarknetChainValidator("id", conn, chains.GetChain("starknet"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestStarknetChainValidatorFatalOnMismatch(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// a mainnet node behind a config that says sepolia
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isChainId)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x534e5f4d41494e"`), protocol.JsonRpc))
	v := starknet_validations.NewStarknetChainValidator("id", conn, chains.GetChain("starknet-sepolia"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestStarknetChainValidatorRetriesOnFetchError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isChainId)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := starknet_validations.NewStarknetChainValidator("id", conn, chains.GetChain("starknet-sepolia"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestStarknetChainValidatorFatalOnEmptyChainId(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isChainId)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`""`), protocol.JsonRpc))
	v := starknet_validations.NewStarknetChainValidator("id", conn, chains.GetChain("starknet-sepolia"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestStarknetHealthAvailableOnFalse(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isSyncing)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`false`), protocol.JsonRpc))
	v := starknet_validations.NewStarknetSyncingValidator("id", conn, chains.GetChain("starknet-sepolia"), time.Second)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestStarknetHealthSyncingOnTrue(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isSyncing)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`true`), protocol.JsonRpc))
	v := starknet_validations.NewStarknetSyncingValidator("id", conn, chains.GetChain("starknet-sepolia"), time.Second)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

func TestStarknetHealthAvailableOnSmallLagHexNums(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// spec-shaped sync object: block nums are hex strings; lag 2 <= syncing lag 5
	body := `{"starting_block_hash":"0x4b","starting_block_num":"0x4b","current_block_hash":"0x9c3a4a2e6d7b1f","current_block_num":"0xb8b2fa","highest_block_hash":"0x9c3a4a2e6d7c22","highest_block_num":"0xb8b2fc"}`
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isSyncing)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.JsonRpc))
	v := starknet_validations.NewStarknetSyncingValidator("id", conn, chains.GetChain("starknet-sepolia"), time.Second)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestStarknetHealthSyncingOnBigLagHexNums(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// 0xb8b2fa -> 0xb8b3fa is a 256-block lag, well over the syncing lag of 5
	body := `{"starting_block_hash":"0x4b","starting_block_num":"0x4b","current_block_hash":"0x9c3a4a2e6d7b1f","current_block_num":"0xb8b2fa","highest_block_hash":"0x9c3a4a2e6d7c22","highest_block_num":"0xb8b3fa"}`
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isSyncing)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.JsonRpc))
	v := starknet_validations.NewStarknetSyncingValidator("id", conn, chains.GetChain("starknet-sepolia"), time.Second)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

func TestStarknetHealthAvailableOnSmallLagNumberNums(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// some clients return plain json numbers instead of hex strings
	body := `{"starting_block_num":75,"current_block_num":12107318,"highest_block_num":12107320}`
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isSyncing)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.JsonRpc))
	v := starknet_validations.NewStarknetSyncingValidator("id", conn, chains.GetChain("starknet-sepolia"), time.Second)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestStarknetHealthSyncingOnBigLagNumberNums(t *testing.T) {
	conn := mocks.NewConnectorMock()
	body := `{"starting_block_num":75,"current_block_num":12000000,"highest_block_num":12107320}`
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isSyncing)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.JsonRpc))
	v := starknet_validations.NewStarknetSyncingValidator("id", conn, chains.GetChain("starknet-sepolia"), time.Second)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

func TestStarknetHealthUnavailableOnGarbage(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isSyncing)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"current_block_num":"not-a-number"}`), protocol.JsonRpc))
	v := starknet_validations.NewStarknetSyncingValidator("id", conn, chains.GetChain("starknet-sepolia"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestStarknetHealthUnavailableOnError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isSyncing)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := starknet_validations.NewStarknetSyncingValidator("id", conn, chains.GetChain("starknet-sepolia"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}
