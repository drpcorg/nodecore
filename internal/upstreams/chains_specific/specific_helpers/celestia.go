package specific_helpers

import (
	"fmt"
	"strconv"

	"github.com/bytedance/sonic"
)

// CelestiaExtendedHeader models the celestia-node header.* responses (CometBFT
// encoding: height is a decimal string, hashes are bare hex strings). Head polling,
// chain validation and lower-bound detection all read it.
type CelestiaExtendedHeader struct {
	Header struct {
		ChainId     string `json:"chain_id"`
		Height      string `json:"height"`
		LastBlockId struct {
			Hash string `json:"hash"`
		} `json:"last_block_id"`
	} `json:"header"`
	Commit struct {
		BlockId struct {
			Hash string `json:"hash"`
		} `json:"block_id"`
	} `json:"commit"`
}

func (e *CelestiaExtendedHeader) Height() (uint64, error) {
	return strconv.ParseUint(e.Header.Height, 10, 64)
}

func ParseCelestiaExtendedHeader(data []byte) (*CelestiaExtendedHeader, error) {
	header := CelestiaExtendedHeader{}
	if err := sonic.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("couldn't parse the celestia extended header, reason - %s", err.Error())
	}
	if header.Header.Height == "" {
		// a real ExtendedHeader is tens of KB (validator set + signatures), don't log it whole
		if len(data) > 256 {
			data = data[:256]
		}
		return nil, fmt.Errorf("couldn't parse the celestia extended header, got '%s'", string(data))
	}
	return &header, nil
}
