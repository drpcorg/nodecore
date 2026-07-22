package stellar_validations

import (
	"context"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// StellarHorizonRoot is the subset of Horizon's root document (GET /) that
// nodecore consumes: chain validation (network_passphrase), labels
// (horizon_version) and the history window (history_elder_ledger).
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

func FetchStellarHorizonRoot(connector connectors.ApiConnector, chain chains.Chain, timeout time.Duration) (*StellarHorizonRoot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET#/", nil, chain)

	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var root StellarHorizonRoot
	if err := sonic.Unmarshal(response.ResponseResult(), &root); err != nil {
		return nil, err
	}
	return &root, nil
}

func FetchStellarHorizonHealth(connector connectors.ApiConnector, chain chains.Chain, timeout time.Duration) (*StellarHorizonHealth, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET#/health", nil, chain)

	response := connector.SendRequest(ctx, request)
	// Horizon answers /health with HTTP 503 while unhealthy (e.g. its captive
	// core is still syncing) but the body still carries the health booleans -
	// parse the body regardless of the status so the caller can distinguish
	// "syncing" from "down". Only fall back to the transport error when the
	// body is not the health document.
	var health StellarHorizonHealth
	if err := sonic.Unmarshal(response.ResponseResult(), &health); err != nil {
		if response.HasError() {
			return nil, response.GetError()
		}
		return nil, err
	}
	return &health, nil
}
