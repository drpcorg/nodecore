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

func isV3MasterchainInfo(r protocol.RequestHolder) bool {
	return r.Method() == "GET#/api/v3/masterchainInfo"
}

func v3MasterchainInfoBody(lastSeqno uint64, genUtime string, globalId int64, firstSeqno uint64) []byte {
	return []byte(fmt.Sprintf(
		`{"last":{"workchain":-1,"shard":"8000000000000000","seqno":%d,`+
			`"root_hash":"hoPjlUCYxfZMeIjBrxNit+ToEYTj3rtBhG9ZFIUgspM=",`+
			`"file_hash":"SFEPKoSzgQ2mouMgL0wfqBYXG0HA1xUvX76Mw43ykKY=",`+
			`"gen_utime":"%s","global_id":%d},`+
			`"first":{"workchain":-1,"shard":"8000000000000000","seqno":%d,`+
			`"root_hash":"VpWYYOvJx1FBFdGpRjB1WK1IArc8/W9MYbLUBLxOn5I=",`+
			`"file_hash":"pOu2eaWvBw1EGXhFvMKOOnEnLPWB9zSWCF9Nl2xa/ss=",`+
			`"gen_utime":"1780000000","global_id":%d}}`,
		lastSeqno, genUtime, globalId, firstSeqno, globalId,
	))
}

func freshGenUtime() string {
	return fmt.Sprintf("%d", time.Now().Unix())
}

func TestTonV3ChainValidatorValidOnMainnetGlobalId(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", v3MasterchainInfoBody(80354724, freshGenUtime(), -239, 66051993), 200, protocol.Rest))
	v := ton_validations.NewTonV3ChainValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestTonV3ChainValidatorValidOnTestnetGlobalId(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", v3MasterchainInfoBody(72210566, freshGenUtime(), -3, 100), 200, protocol.Rest))
	v := ton_validations.NewTonV3ChainValidator("id", conn, chains.GetChain("ton-testnet"), time.Second)
	assert.Equal(t, validations.Valid, v.Validate())
}

func TestTonV3ChainValidatorFatalOnTestnetGlobalIdBehindMainnetConfig(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// a testnet indexer behind a config that says mainnet
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", v3MasterchainInfoBody(72210566, freshGenUtime(), -3, 100), 200, protocol.Rest))
	v := ton_validations.NewTonV3ChainValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, validations.FatalSettingError, v.Validate())
}

func TestTonV3ChainValidatorRetriesOnMissingGlobalId(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"last":{"seqno":80354724},"first":{"seqno":66051993}}`), 200, protocol.Rest))
	v := ton_validations.NewTonV3ChainValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestTonV3ChainValidatorRetriesOnUnparseableBody(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"last":`), 200, protocol.Rest))
	v := ton_validations.NewTonV3ChainValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestTonV3ChainValidatorRetriesOnFetchError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := ton_validations.NewTonV3ChainValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, validations.SettingsError, v.Validate())
}

func TestTonV3HealthAvailableOnFreshHead(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", v3MasterchainInfoBody(80354724, freshGenUtime(), -239, 66051993), 200, protocol.Rest))
	v := ton_validations.NewTonV3SyncingValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestTonV3HealthSyncingOnStaleGenUtime(t *testing.T) {
	conn := mocks.NewConnectorMock()
	stale := fmt.Sprintf("%d", time.Now().Add(-time.Hour).Unix())
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", v3MasterchainInfoBody(80354724, stale, -239, 66051993), 200, protocol.Rest))
	v := ton_validations.NewTonV3SyncingValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, protocol.Syncing, v.Validate())
}

func TestTonV3HealthAvailableJustUnderTheFreshnessFloor(t *testing.T) {
	conn := mocks.NewConnectorMock()
	// TON's raw ExpectedBlockTime*Lags.Syncing is 40s; the 60s floor must
	// keep a 50s-old head (normal indexer commit cadence) Available
	almostStale := fmt.Sprintf("%d", time.Now().Add(-50*time.Second).Unix())
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", v3MasterchainInfoBody(80354724, almostStale, -239, 66051993), 200, protocol.Rest))
	v := ton_validations.NewTonV3SyncingValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestTonV3HealthAvailableOnUnparseableGenUtime(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", v3MasterchainInfoBody(80354724, "not-a-number", -239, 66051993), 200, protocol.Rest))
	v := ton_validations.NewTonV3SyncingValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, protocol.Available, v.Validate())
}

func TestTonV3HealthUnavailableOnZeroSeqno(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", v3MasterchainInfoBody(0, freshGenUtime(), -239, 66051993), 200, protocol.Rest))
	v := ton_validations.NewTonV3SyncingValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestTonV3HealthUnavailableOnUnparseableBody(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"last":`), 200, protocol.Rest))
	v := ton_validations.NewTonV3SyncingValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}

func TestTonV3HealthUnavailableOnFetchError(t *testing.T) {
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.MatchedBy(isV3MasterchainInfo)).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))
	v := ton_validations.NewTonV3SyncingValidator("id", conn, chains.GetChain("ton"), time.Second)
	assert.Equal(t, protocol.Unavailable, v.Validate())
}
