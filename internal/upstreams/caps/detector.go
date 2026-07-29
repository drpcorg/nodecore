package caps

import (
	"context"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
)

// CapDetector streams the set of capabilities it currently asserts. Unlike the
// poll-only LowerBoundDetector, a cap detector owns its own cadence: it may be
// push-driven (watching a connector's state stream) or poll-driven (a ticker), and
// re-emits a fresh snapshot - restricted to Domain() - whenever its inputs change.
// The channel is closed when ctx is done.
type CapDetector interface {
	DetectCaps(ctx context.Context) <-chan mapset.Set[protocol.Cap]
	// Domain is the set of caps this detector governs. The processor attributes only
	// these caps to the detector when merging, so detectors with disjoint domains
	// compose into the upstream's full cap set.
	Domain() []protocol.Cap
}

// DetectorInput bundles everything a chain needs to build its cap detectors. It is
// assembled at the upstream level (where the connector set and methods are known)
// and handed to ChainSpecific.CapDetectors.
type DetectorInput struct {
	// WsConnector is the upstream's websocket connector, or nil if it has none.
	WsConnector connectors.ApiConnector
	// HeadConnector is the connector driving the head; for ws-derived caps like
	// NewHeads it must be a websocket connector.
	HeadConnector connectors.ApiConnector
	Methods       methods.Methods
	// Head is the shared head processor, used to gate WsCap on head liveness for
	// ws-driven heads. Nil when the upstream has no head processor.
	Head HeadSource
}

// subscribeStates returns the connector's state stream, or nil when the connector is
// absent or doesn't expose states (e.g. an http connector). Detectors treat a nil
// result as "this cap can never be asserted".
func subscribeStates(conn connectors.ApiConnector, name string) *utils.Subscription[protocol.SubscribeConnectorState] {
	if conn == nil {
		return nil
	}
	return conn.SubscribeStates(name)
}
