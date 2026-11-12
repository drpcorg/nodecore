package labels

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/rs/zerolog/log"
)

type EthClientLabelMapping func(data []byte) (string, error)

type LabelsDetector interface {
	DetectLabels(ctx context.Context) map[string]string
}

type ClientLabelsDetector interface {
	NodeTypeRequest() (protocol.RequestHolder, error)
	ClientVersionAndType(data []byte) (string, string, error)
}

// ClientDetector asks a node which client it runs: as labels for the labels processor,
// and as a typed answer for the client version validator.
type ClientDetector interface {
	LabelsDetector
	DetectClient(ctx context.Context) (clientType, clientVersion string, err error)
}

// ClientLabelsDetectorHandler asks a node which client it runs through one or more
// probes, tried in order: a probe whose request is answered with an error falls through
// to the next one. Every family has a single probe except the ones whose clients each
// answer a method of their own.
type ClientLabelsDetectorHandler struct {
	upstreamId      string
	connector       connectors.ApiConnector
	probes          []ClientLabelsDetector
	internalTimeout time.Duration
}

// DetectClient returns the client type and version reported by the first probe that
// answers, or the last probe's error when none does. Note the (type, version) order,
// the opposite of ClientLabelsDetector.ClientVersionAndType.
func (c *ClientLabelsDetectorHandler) DetectClient(ctx context.Context) (clientType, clientVersion string, err error) {
	for _, probe := range c.probes {
		clientVersion, clientType, err = c.detectWith(ctx, probe)
		if err == nil {
			return clientType, clientVersion, nil
		}
		log.Debug().Err(err).Msgf("client labels probe of '%s' failed", c.upstreamId)
	}
	return "", "", err
}

func (c *ClientLabelsDetectorHandler) DetectLabels(ctx context.Context) map[string]string {
	clientType, clientVersion, err := c.DetectClient(ctx)
	if err != nil {
		log.Error().Err(err).Msgf("unable to detect client labels of '%s'", c.upstreamId)
		return nil
	}

	clientLabels := make(map[string]string, 2)
	if clientVersion != "" {
		clientLabels["client_version"] = clientVersion
	}
	if clientType != "" {
		clientLabels["client_type"] = clientType
	}

	return clientLabels
}

// NewClientLabelDetectorHandler builds a handler over one or more probes, tried in the
// given order.
func NewClientLabelDetectorHandler(
	upstreamId string,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
	probes ...ClientLabelsDetector,
) *ClientLabelsDetectorHandler {
	return &ClientLabelsDetectorHandler{
		upstreamId:      upstreamId,
		connector:       connector,
		probes:          probes,
		internalTimeout: internalTimeout,
	}
}

func (c *ClientLabelsDetectorHandler) detectWith(ctx context.Context, probe ClientLabelsDetector) (clientVersion, clientType string, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.internalTimeout)
	defer cancel()

	request, err := probe.NodeTypeRequest()
	if err != nil {
		return "", "", err
	}

	response := c.connector.SendRequest(ctx, request)
	if response.HasError() {
		return "", "", response.GetError()
	}

	return probe.ClientVersionAndType(response.ResponseResult())
}

var _ ClientDetector = (*ClientLabelsDetectorHandler)(nil)
