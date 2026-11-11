package validations

import (
	"context"

	"github.com/drpcorg/nodecore/internal/upstreams/compatible_clients"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// ClientVersionValidator probes the node's client type and version through the
// family's client detector and checks them against the compatible-clients rules on
// every tick, so a rule change is applied without a restart. A forbidden version is a
// fatal settings error: the upstream is refused at startup and removed at runtime
// until a later check is clean. A node that does not answer the probe cannot be
// checked: by default it passes (no client, no rule), and when the registry does not
// allow undetected clients it is a non-fatal settings error, like with every other
// settings validator.
type ClientVersionValidator struct {
	upstreamId string
	chain      *chains.ConfiguredChain
	detector   labels.ClientDetector
	registry   *compatible_clients.Registry
}

func NewClientVersionValidator(
	upstreamId string,
	chain *chains.ConfiguredChain,
	detector labels.ClientDetector,
	registry *compatible_clients.Registry,
) *ClientVersionValidator {
	return &ClientVersionValidator{
		upstreamId: upstreamId,
		chain:      chain,
		detector:   detector,
		registry:   registry,
	}
}

func (v *ClientVersionValidator) Validate(ctx context.Context) ValidationSettingResult {
	clientType, clientVersion, err := v.detector.DetectClient(ctx)
	if err != nil {
		if v.registry.AllowsUndetected() {
			log.Debug().Err(err).Msgf("unable to detect the client of upstream '%s', skipping the client version validation", v.upstreamId)
			return Valid
		}
		log.Warn().Err(err).Msgf("unable to detect the client of upstream '%s' for the client version validation", v.upstreamId)
		return SettingsError
	}
	if clientType == "" || clientVersion == "" {
		return Valid
	}

	switch v.registry.Check(clientType, clientVersion, v.chain) {
	case compatible_clients.NotWhitelisted:
		log.Warn().Msgf("upstream '%s' runs client '%s' version '%s' on chain '%s', which is not in the whitelist of compatible clients", v.upstreamId, clientType, clientVersion, v.chain.Chain.String())
		return FatalSettingError
	case compatible_clients.Blacklisted:
		log.Warn().Msgf("upstream '%s' runs client '%s' version '%s' on chain '%s', which is in the blacklist of compatible clients", v.upstreamId, clientType, clientVersion, v.chain.Chain.String())
		return FatalSettingError
	default:
		return Valid
	}
}
