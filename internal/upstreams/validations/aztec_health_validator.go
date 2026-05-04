package validations

import (
	"context"
	"errors"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errAztecNotReady = errors.New("aztec node is not ready")
var errAztecEmptyTips = errors.New("aztec node returned empty L2 tips")

const (
	aztecMaxRetries     = 2
	aztecRetryBackoff   = 200 * time.Millisecond
	aztecMaxRetryBackoff = time.Second
)

// transientHttpCodes are 5xx statuses the public Aztec endpoint occasionally
// emits during deploys / sequencer failovers. They almost always recover on
// retry, so the health validator should not flap an upstream into Unavailable
// on a single hit.
var transientHttpCodes = map[int]struct{}{
	502: {},
	503: {},
	504: {},
}

type AztecHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func (a *AztecHealthValidator) Validate() protocol.AvailabilityStatus {
	if err := a.checkReady(); err != nil {
		log.Error().Err(err).Msgf("aztec upstream '%s' health validation failed", a.upstreamId)
		return protocol.Unavailable
	}
	if err := a.checkTips(); err != nil {
		log.Error().Err(err).Msgf("aztec upstream '%s' tips validation failed", a.upstreamId)
		return protocol.Unavailable
	}
	return protocol.Available
}

func (a *AztecHealthValidator) checkReady() error {
	response, err := a.sendWithRetry("node_isReady")
	if err != nil {
		return err
	}
	if response.HasError() {
		return response.GetError()
	}

	var ready bool
	if err := sonic.Unmarshal(response.ResponseResult(), &ready); err != nil {
		// some nodes wrap the answer as a string "true"/"false"
		var s string
		if err2 := sonic.Unmarshal(response.ResponseResult(), &s); err2 != nil {
			return err
		}
		ready = s == "true"
	}
	if !ready {
		return errAztecNotReady
	}
	return nil
}

func (a *AztecHealthValidator) checkTips() error {
	response, err := a.sendWithRetry("node_getL2Tips")
	if err != nil {
		return err
	}
	if response.HasError() {
		return response.GetError()
	}

	tips := AztecL2Tips{}
	if err := sonic.Unmarshal(response.ResponseResult(), &tips); err != nil {
		return err
	}
	if tips.Proposed.Number == 0 {
		return errAztecEmptyTips
	}
	if tips.Proven.Number > tips.Proposed.Number {
		return errAztecEmptyTips
	}
	return nil
}

// sendWithRetry sends the JSON-RPC method up to aztecMaxRetries+1 times when
// the upstream answers with a transient HTTP 5xx (502/503/504). The final
// response is returned regardless of success - callers handle non-retryable
// errors as before.
func (a *AztecHealthValidator) sendWithRetry(method string) (protocol.ResponseHolder, error) {
	var response protocol.ResponseHolder
	backoff := aztecRetryBackoff
	for attempt := 0; attempt <= aztecMaxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
		request, err := protocol.NewInternalUpstreamJsonRpcRequest(method, nil, chains.AZTEC_MAINNET)
		if err != nil {
			cancel()
			return nil, err
		}
		response = a.connector.SendRequest(ctx, request)
		cancel()

		if !isTransientHttpStatus(response) {
			return response, nil
		}
		if attempt == aztecMaxRetries {
			break
		}
		log.Warn().Msgf(
			"aztec upstream '%s' answered HTTP %d for %s; retry %d/%d after %s",
			a.upstreamId, response.ResponseCode(), method, attempt+1, aztecMaxRetries, backoff,
		)
		time.Sleep(backoff)
		backoff *= 2
		if backoff > aztecMaxRetryBackoff {
			backoff = aztecMaxRetryBackoff
		}
	}
	return response, nil
}

func isTransientHttpStatus(response protocol.ResponseHolder) bool {
	if response == nil || !response.HasError() {
		return false
	}
	_, ok := transientHttpCodes[response.ResponseCode()]
	return ok
}

func NewAztecHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
) *AztecHealthValidator {
	return &AztecHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

var _ HealthValidator = (*AztecHealthValidator)(nil)

// AztecL2Tips models node_getL2Tips. Aztec reshaped the payload between v3 and v4:
// v3 had every tip flat ({number, hash}); v4 nested proven/finalized/checkpointed
// as {block: {number, hash}, checkpoint: {...}} and kept proposed flat.
// AztecVersionedTip transparently handles both shapes.
type AztecL2Tips struct {
	Proposed     AztecTip          `json:"proposed"`
	Proven       AztecVersionedTip `json:"proven"`
	Finalized    AztecVersionedTip `json:"finalized"`
	Checkpointed AztecVersionedTip `json:"checkpointed"`
}

type AztecTip struct {
	Number uint64 `json:"number"`
	Hash   string `json:"hash"`
}

type AztecVersionedTip struct {
	Number uint64
	Hash   string
}

func (a *AztecVersionedTip) UnmarshalJSON(data []byte) error {
	var nested struct {
		Block *AztecTip `json:"block"`
	}
	if err := sonic.Unmarshal(data, &nested); err == nil && nested.Block != nil {
		a.Number = nested.Block.Number
		a.Hash = nested.Block.Hash
		return nil
	}
	var flat AztecTip
	if err := sonic.Unmarshal(data, &flat); err != nil {
		return err
	}
	a.Number = flat.Number
	a.Hash = flat.Hash
	return nil
}
