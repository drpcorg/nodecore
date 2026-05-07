package labels

import (
	"regexp"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const (
	UnknownClientVersion = "unknown"
	DefaultClient        = "default client"
)

var semverLikeRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+`)

type EthClientLabelMapping func(data []byte) (string, error)

var EthMappingFunc EthClientLabelMapping = func(data []byte) (string, error) {
	var raw string
	err := sonic.Unmarshal(data, &raw)
	return raw, err
}

type EthClientLabelsDetector struct {
	upstreamId  string
	chain       chains.Chain
	mappingFunc EthClientLabelMapping
}

func (e *EthClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamJsonRpcRequest("web3_clientVersion", nil, e.chain)
}

func (e *EthClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	raw, err := e.mappingFunc(data)
	if err != nil {
		return "", "", err
	}

	clientVersion := e.clientVersion(raw)
	clientType := e.clientType(raw)
	return clientVersion, clientType, nil
}

func NewEthClientLabelsDetector(upstreamId string, chain chains.Chain, mappingFunc EthClientLabelMapping) *EthClientLabelsDetector {
	return &EthClientLabelsDetector{
		upstreamId:  upstreamId,
		chain:       chain,
		mappingFunc: mappingFunc,
	}
}

func (e *EthClientLabelsDetector) clientVersion(client string) string {
	if i := strings.IndexByte(client, '/'); i != -1 {
		client = client[i+1:]

		// Full standard format: "geth/1.9.0/linux/go1.15" — version sits between the two slashes
		if j := strings.IndexByte(client, '/'); j != -1 {
			return client[:j]
		}

		// "Type/Version" format
		if client == "" {
			log.Warn().Msgf("unable to detect client version of upstream '%s', empty version after slash in: %s", e.upstreamId, client)
			return UnknownClientVersion
		}
		return client
	}

	// Semver-like string without a client type prefix
	if isSemverLike(client) {
		return client
	}

	// No slashes and no dots — definitely not a version
	if !strings.Contains(client, ".") {
		log.Warn().Msgf("unable to detect client version of upstream '%s', single word without version info: %s", e.upstreamId, client)
		return UnknownClientVersion
	}

	// Fallback: hand back whatever we got
	log.Warn().Msgf("unable to detect client version of upstream '%s', raw version: %s", e.upstreamId, client)
	return client
}

func (e *EthClientLabelsDetector) clientType(client string) string {
	if client == "" {
		return DefaultClient
	}

	// Has a slash — type is everything before it
	if i := strings.IndexByte(client, '/'); i != -1 {
		clientPart := client[:i]
		if clientPart == "" {
			return DefaultClient
		}
		return strings.ToLower(clientPart)
	}

	// Bare semver: no client type embedded
	if isSemverLike(client) {
		return DefaultClient
	}

	// Single word with no dots — treat the whole thing as the type
	if !strings.Contains(client, ".") {
		return strings.ToLower(client)
	}

	// Anything else is unrecognized
	return DefaultClient
}

func isSemverLike(version string) bool {
	return semverLikeRe.MatchString(version)
}

var _ ClientLabelsDetector = (*EthClientLabelsDetector)(nil)
