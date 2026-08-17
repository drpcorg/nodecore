package specific_helpers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// StellarHealth is the getHealth result. stellar-rpc reports the head, the
// retention window and its own staleness verdict in this one document, so head
// polling, health validation and lower-bound detection all read it.
type StellarHealth struct {
	Status                string `json:"status"`
	LatestLedger          uint64 `json:"latestLedger"`
	OldestLedger          uint64 `json:"oldestLedger"`
	LatestLedgerCloseTime string `json:"latestLedgerCloseTime"`
	OldestLedgerCloseTime string `json:"oldestLedgerCloseTime"`
	LedgerRetentionWindow uint64 `json:"ledgerRetentionWindow"`
}

// StellarHorizonRoot is the subset of Horizon's root document (GET /) that
// nodecore consumes: head (history_latest_ledger), chain validation
// (network_passphrase), labels (horizon_version) and the history window
// (history_elder_ledger).
type StellarHorizonRoot struct {
	HorizonVersion              string `json:"horizon_version"`
	NetworkPassphrase           string `json:"network_passphrase"`
	HistoryLatestLedger         uint64 `json:"history_latest_ledger"`
	HistoryLatestLedgerClosedAt string `json:"history_latest_ledger_closed_at"`
	HistoryElderLedger          uint64 `json:"history_elder_ledger"`
	CoreLatestLedger            uint64 `json:"core_latest_ledger"`
}

// StellarHorizonHealth is the response of Horizon's GET /health. The endpoint
// serves a JSON body with a text/plain Content-Type; that is fine here because
// ResponseResult() hands over raw bytes regardless of the declared type.
type StellarHorizonHealth struct {
	DatabaseConnected bool `json:"database_connected"`
	CoreUp            bool `json:"core_up"`
	CoreSynced        bool `json:"core_synced"`
}

var (
	ErrStellarEmptyHealth              = errors.New("stellar node returned an empty getHealth body")
	ErrStellarHorizonEmptyRoot         = errors.New("horizon returned an empty root document")
	ErrStellarHorizonNotHealthDocument = errors.New("horizon body is not a health document")
)

// FetchStellarHealth calls getHealth. An unhealthy node answers with a JSON-RPC
// error rather than a degraded result, so the error is returned verbatim and
// callers classify it themselves. The caller owns the timeout.
func FetchStellarHealth(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) (*StellarHealth, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getHealth", map[string]any{}, chain)
	if err != nil {
		return nil, err
	}
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	return ParseStellarHealth(response.ResponseResult())
}

func ParseStellarHealth(raw []byte) (*StellarHealth, error) {
	if len(raw) == 0 {
		return nil, ErrStellarEmptyHealth
	}
	var health StellarHealth
	if err := sonic.Unmarshal(raw, &health); err != nil {
		return nil, fmt.Errorf("stellar getHealth payload unparseable: %w", err)
	}
	return &health, nil
}

// FetchStellarHorizonRoot reads Horizon's root document. The caller owns the
// timeout.
func FetchStellarHorizonRoot(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) (*StellarHorizonRoot, error) {
	request := protocol.NewInternalUpstreamRestRequest("GET#/", nil, chain)
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	return ParseStellarHorizonRoot(response.ResponseResult())
}

func ParseStellarHorizonRoot(raw []byte) (*StellarHorizonRoot, error) {
	if len(raw) == 0 {
		return nil, ErrStellarHorizonEmptyRoot
	}
	var root StellarHorizonRoot
	if err := sonic.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("horizon root document payload unparseable: %w", err)
	}
	return &root, nil
}

// FetchStellarHorizonHealth reads Horizon's GET /health.
//
// Horizon answers 503 while unhealthy but still sends the health booleans, so
// that body IS the answer rather than an error - it is what keeps "captive core
// still syncing" distinguishable from "horizon is down". Every other failure is
// a transport error: a rate-limit 429 or a proxy's 502 carries RFC-7807
// problem+json, which unmarshals into this struct without complaint and with
// every boolean absent, i.e. false - reporting a node whose database is down and
// swallowing the real cause. Hence both gates: the status must be 503, and the
// body must actually carry the health booleans.
func FetchStellarHorizonHealth(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) (*StellarHorizonHealth, error) {
	request := protocol.NewInternalUpstreamRestRequest("GET#/health", nil, chain)
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		if response.ResponseCode() == http.StatusServiceUnavailable {
			if health, err := ParseStellarHorizonHealth(response.ResponseResult()); err == nil {
				return health, nil
			}
		}
		return nil, response.GetError()
	}
	return ParseStellarHorizonHealth(response.ResponseResult())
}

// ParseStellarHorizonHealth requires all three booleans to be present. Horizon
// always sends all three; without the presence check any JSON object parses into
// an all-false struct and reads as a maximally unhealthy node.
func ParseStellarHorizonHealth(raw []byte) (*StellarHorizonHealth, error) {
	var probe struct {
		DatabaseConnected *bool `json:"database_connected"`
		CoreUp            *bool `json:"core_up"`
		CoreSynced        *bool `json:"core_synced"`
	}
	if err := sonic.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("horizon health document payload unparseable: %w", err)
	}
	if probe.DatabaseConnected == nil || probe.CoreUp == nil || probe.CoreSynced == nil {
		return nil, ErrStellarHorizonNotHealthDocument
	}
	return &StellarHorizonHealth{
		DatabaseConnected: *probe.DatabaseConnected,
		CoreUp:            *probe.CoreUp,
		CoreSynced:        *probe.CoreSynced,
	}, nil
}
