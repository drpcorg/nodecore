package validations

import (
	"context"

	"github.com/drpcorg/nodecore/internal/upstreams/compatible_clients"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

// ClientVersionValidator probes the node's client type and version through the
// family's client detector and checks them against the compatible-clients rules on
// every tick, so a rule change is applied without a restart. A forbidden version is a
// fatal settings error: the upstream is refused at startup and removed at runtime
// until a later check is clean. A node that does not answer the probe cannot be
// checked and is a non-fatal settings error, like with every other settings validator.
type ClientVersionValidator struct {
	upstreamId string
	chain      *chains.ConfiguredChain
	detector   labels.ClientDetector
	rules      *utils.Reloadable[compatible_clients.Rules]
}

func NewClientVersionValidator(
	upstreamId string,
	chain *chains.ConfiguredChain,
	detector labels.ClientDetector,
	rules *utils.Reloadable[compatible_clients.Rules],
) *ClientVersionValidator {
	return &ClientVersionValidator{
		upstreamId: upstreamId,
		chain:      chain,
		detector:   detector,
		rules:      rules,
	}
}

func (v *ClientVersionValidator) Validate(ctx context.Context) ValidationSettingResult {
	clientType, clientVersion, err := v.detector.DetectClient(ctx)
	if err != nil {
		log.Warn().Err(err).Msgf("unable to detect the client of upstream '%s' for the client version validation", v.upstreamId)
		return SettingsError
	}
	if clientType == "" || clientVersion == "" {
		return Valid
	}

	switch v.rules.Get().Check(clientType, clientVersion, v.chain) {
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
