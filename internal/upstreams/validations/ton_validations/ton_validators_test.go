package ton_validations_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/ton_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const (
	mainnetZerostateRoot = "F6OpKZKqvqeFp6CQmFomXNMfMj2EnaUSOXN+Mh+wVWk="
	mainnetZerostateFile = "XplPz01CXAps5qeSWUtxcyBfdAo5zVb1N979KLSKD24="
	testnetZerostateRoot = "gj+B8wb/AmlPk1z1AhVI484rhrUpgSr2oSFIh56VoSg="
	testnetZerostateFile = "Z+IKwYS54DmmJmesw/nAD5DzWadnOCMzee+kdgSYDOg="
)

func isMasterchainInfo(r protocol.RequestHolder) bool {
	return r.Method() == "GET#/getMasterchainInfo"
}

func masterchainInfoBody(seqno uint64, initRoot, initFile string) []byte {
	return []byte(fmt.Sprintf(
		`{"ok":true,"result":{"@type":"blocks.masterchainInfo",`+
			`"last":{"workchain":-1,"shard":"-9223372036854775808","seqno":%d,`+
			`"root_hash":"hoPjlUCYxfZMeIjBrxNit+ToEYTj3rtBhG9ZFIUgspM=",`+
			`"file_hash":"SFEPKoSzgQ2mouMgL0wfqBYXG0HA1xUvX76Mw43ykKY="},`+
			`"state_root_hash":"R1+4UAlRJfzBKsQPdvT6fycXd8RoCSB/pXekL/Jmm6s=",`+
			`"init":{"workchain":-1,"shard":"0","seqno":0,`+
			`"root_hash":"%s","file_hash":"%s"}},"@extra":"1784332062:_:0.004:c"}`,
		seqno, initRoot, initFile,
	))
}

const notOkBody = `{"ok":false,"error":"failed to parse request","code":422,"@extra":"1784332062:_:0.004:c"}`

func TestTonChainValidatorValidOnMainnetZerostate(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isMasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", masterchainInfoBody(80352632, mainnetZerostateRoot, mainnetZerostateFile), 200, protocol.Rest))
	v := ton_validations.NewTonChainValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestTonChainValidatorValidOnTestnetZerostate(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isMasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", masterchainInfoBody(72210566, testnetZerostateRoot, testnetZerostateFile), 200, protocol.Rest))
	v := ton_validations.NewTonChainValidator("id", conn, chains.GetChain("ton-testnet"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestTonChainValidatorFatalOnTestnetZerostateBehindMainnetConfig(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// a testnet node behind a config that says mainnet
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isMasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", masterchainInfoBody(72210566, testnetZerostateRoot, testnetZerostateFile), 200, protocol.Rest))
	v := ton_validations.NewTonChainValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestTonChainValidatorFatalOnUnmappedChain(t *testing.T) {
	conn := mocks.NewConnectorMock()
	v := ton_validations.NewTonChainValidator("id", conn, chains.GetChain("aptos-mainnet"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
	conn.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

func TestTonChainValidatorRetriesOnNotOk(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isMasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(notOkBody), 200, protocol.Rest))
	v := ton_validations.NewTonChainValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestTonChainValidatorRetriesOnFetchError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isMasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := ton_validations.NewTonChainValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestTonHealthAvailable(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isMasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", masterchainInfoBody(80352632, mainnetZerostateRoot, mainnetZerostateFile), 200, protocol.Rest))
	v := ton_validations.NewTonHealthValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestTonHealthUnavailableOnNotOk(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isMasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(notOkBody), 200, protocol.Rest))
	v := ton_validations.NewTonHealthValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestTonHealthUnavailableOnZeroSeqno(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isMasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", masterchainInfoBody(0, mainnetZerostateRoot, mainnetZerostateFile), 200, protocol.Rest))
	v := ton_validations.NewTonHealthValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestTonHealthUnavailableOnFetchError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isMasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := ton_validations.NewTonHealthValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}
