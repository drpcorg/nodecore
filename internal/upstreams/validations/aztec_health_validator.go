package validations

import (
	"context"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type aztecHealthValidatorError string

func (e aztecHealthValidatorError) Error() string {
	return string(e)
}

const (
	errAztecNotReady  aztecHealthValidatorError = "aztec node is not ready"
	errAztecEmptyTips aztecHealthValidatorError = "aztec node returned empty L2 tips"
)

type AztecHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewAztecHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain chains.Chain,
	internalTimeout time.Duration,
) *AztecHealthValidator {
	return &AztecHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"node_isReady", []string{}, a.chain,
	)
	if err != nil {
		return err
	}

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return response.GetError()
	}

	var ready bool
	if err := sonic.Unmarshal(response.ResponseResult(), &ready); err != nil {
		// some nodes wrap the answer as a string "true"/"false"
		var s string
		if err2 := sonic.Unmarshal(response.ResponseResult(), &s); err2 != nil {
			// Surface the actual string-decode failure (the bool-decode error
			// above is just the trigger for the fallback path).
			return err2
		}
		ready = s == "true"
	}
	if !ready {
		return errAztecNotReady
	}
	return nil
}

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

func (a *AztecHealthValidator) checkTips() error {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"node_getL2Tips", []string{}, a.chain,
	)
	if err != nil {
		return err
	}

	response := a.connector.SendRequest(ctx, request)
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

var _ HealthValidator = (*AztecHealthValidator)(nil)
