package beacon_labels

import (
	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
)

// BeaconMappingFunc extracts the consensus-client version string from a beacon
// node's GET /eth/v1/node/version response ({"data":{"version":"Lighthouse/v8.2.0-.../..."}}).
// The extracted value follows the same "Type/Version/..." shape web3_clientVersion
// returns, so it can be fed straight into eth_labels.EthClientLabelsDetector, whose
// clientVersion/clientType parsing then applies unchanged.
var BeaconMappingFunc labels.EthClientLabelMapping = func(data []byte) (string, error) {
	var resp struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := sonic.Unmarshal(data, &resp); err != nil {
		return "", err
	}
	return resp.Data.Version, nil
}
