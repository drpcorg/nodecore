package evm_caps

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bytedance/sonic"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

const (
	// BaseTxLimit is the combined pending+queued tx count at or above which a node's
	// mempool is considered usable for pending-tx subscriptions. Mirrors dshackle's
	// BASE_TX_LIMIT.
	BaseTxLimit = 1000
	// pendingTxValidationInterval is how often txpool_content is polled while the ws
	// connector is up. Mempool size changes slowly relative to this; dshackle polls
	// on a similar minutes-scale cadence.
	pendingTxValidationInterval = 5 * time.Minute
)

// EvmPendingTxCapDetector asserts PendingTxCap only while the websocket connector is
// connected AND the node's txpool_content reports at least BaseTxLimit combined
// pending+queued transactions. It is built only for chains that actually need the
// check (BASE mainnet); other chains use the generic ws-presence detector. Mirrors
// dshackle's PendingTransactionValidatorImpl.
type EvmPendingTxCapDetector struct {
	name            string
	wsConn          connectors.ApiConnector
	internalConn    connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
	// txLimit is the combined pending+queued tx count at or above which the mempool
	// is considered usable. Passed in per chain so different chains can use different
	// thresholds.
	txLimit int
}

func NewEvmPendingTxCapDetector(
	name string,
	wsConn connectors.ApiConnector,
	internalConn connectors.ApiConnector,
	chain chains.Chain,
	internalTimeout time.Duration,
	txLimit int,
) *EvmPendingTxCapDetector {
	return &EvmPendingTxCapDetector{
		name:            name,
		wsConn:          wsConn,
		internalConn:    internalConn,
		chain:           chain,
		internalTimeout: internalTimeout,
		txLimit:         txLimit,
	}
}

func (e *EvmPendingTxCapDetector) Domain() []protocol.Cap {
	return []protocol.Cap{protocol.PendingTxCap}
}

func (e *EvmPendingTxCapDetector) DetectCaps(ctx context.Context) <-chan mapset.Set[protocol.Cap] {
	out := make(chan mapset.Set[protocol.Cap], 1)

	var stateSub *utils.Subscription[protocol.SubscribeConnectorState]
	if e.wsConn != nil {
		stateSub = e.wsConn.SubscribeStates(e.name)
	}
	if stateSub == nil {
		close(out)
		return out
	}

	go func() {
		defer close(out)
		defer stateSub.Unsubscribe()

		ticker := time.NewTicker(pendingTxValidationInterval)
		defer ticker.Stop()

		wsConnected := false
		txpoolFull := false
		var emitted mapset.Set[protocol.Cap]

		emit := func() {
			caps := mapset.NewThreadUnsafeSet[protocol.Cap]()
			if wsConnected && txpoolFull {
				caps.Add(protocol.PendingTxCap)
			}
			if emitted != nil && emitted.Equal(caps) {
				return
			}
			emitted = caps
			select {
			case out <- caps:
			case <-ctx.Done():
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case state, ok := <-stateSub.Events:
				if !ok {
					return
				}
				wsConnected = state == protocol.WsConnected
				// Poll immediately on connect so the cap appears without waiting a full
				// interval; drop it at once on disconnect.
				if wsConnected {
					txpoolFull = e.txpoolAboveLimit(ctx)
				} else {
					txpoolFull = false
				}
				emit()
			case <-ticker.C:
				if wsConnected {
					txpoolFull = e.txpoolAboveLimit(ctx)
					emit()
				}
			}
		}
	}()

	return out
}

// txpoolAboveLimit reports whether txpool_content shows at least txLimit combined
// pending+queued transactions. Any error (method unsupported, timeout, malformed
// response) is treated as below the limit, matching dshackle's onErrorResume(false).
func (e *EvmPendingTxCapDetector) txpoolAboveLimit(ctx context.Context) bool {
	if e.internalConn == nil {
		return false
	}

	reqCtx, cancel := context.WithTimeout(ctx, e.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("txpool_content", nil, e.chain)
	if err != nil {
		return false
	}

	response := e.internalConn.SendRequest(reqCtx, request)
	if response.HasError() {
		log.Error().Err(response.GetError()).Msgf("unable to read txpool_content of upstream cap detector '%s'", e.name)
		return false
	}

	// pending/queued are maps keyed by sender address; their sizes are the per-sender
	// tx-group counts. Decoding the values as raw messages skips parsing each tx,
	// matching dshackle counting fieldNames() of each node.
	var content struct {
		Pending map[string]json.RawMessage `json:"pending"`
		Queued  map[string]json.RawMessage `json:"queued"`
	}
	if err := sonic.Unmarshal(response.ResponseResult(), &content); err != nil {
		log.Error().Err(err).Msgf("unable to parse txpool_content of upstream cap detector '%s'", e.name)
		return false
	}

	return len(content.Pending)+len(content.Queued) >= e.txLimit
}
