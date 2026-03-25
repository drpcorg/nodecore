package labels

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/rs/zerolog/log"
)

type LabelsDetector interface {
	DetectLabels() map[string]string
}

type ClientLabelsDetector interface {
	NodeTypeRequest() (protocol.RequestHolder, error)
	ClientVersionAndType(data []byte) (string, string, error)
}

type ClientLabelsDetectorHandler struct {
	upstreamId          string
	connector           connectors.ApiConnector
	clientLabelDetector ClientLabelsDetector
	internalTimeout     time.Duration
}

func (c *ClientLabelsDetectorHandler) DetectLabels() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), c.internalTimeout)
	defer cancel()

	request, err := c.clientLabelDetector.NodeTypeRequest()
	if err != nil {
		log.Error().Err(err).Msgf("failed to get a client labels request of '%s'", c.upstreamId)
		return nil
	}

	response := c.connector.SendRequest(ctx, request)
	if response.HasError() {
		log.Error().Err(response.GetError()).Msgf("unable to perform a client labels request of '%s'", c.upstreamId)
		return nil
	}
	clientLabels := make(map[string]string, 2)

	clientVersion, clientType, err := c.clientLabelDetector.ClientVersionAndType(response.ResponseResult())
	if err != nil {
		log.Error().Err(err).Msgf("unable to parse client labels of '%s'", c.upstreamId)
		return nil
	}

	if clientVersion != "" {
		clientLabels["client_version"] = clientVersion
	}
	if clientType != "" {
		clientLabels["client_type"] = clientType
	}

	return clientLabels
}

func NewClientLabelDetectorHandler(
	upstreamId string,
	connector connectors.ApiConnector,
	clientLabelDetector ClientLabelsDetector,
	internalTimeout time.Duration,
) *ClientLabelsDetectorHandler {
	return &ClientLabelsDetectorHandler{
		upstreamId:          upstreamId,
		connector:           connector,
		clientLabelDetector: clientLabelDetector,
		internalTimeout:     internalTimeout,
	}
}

var _ LabelsDetector = (*ClientLabelsDetectorHandler)(nil)
