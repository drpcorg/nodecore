package upstreams

import "strings"

type UpstreamVendor int

const (
	Unknown UpstreamVendor = iota
	QuickNode
	DRPC
	Alchemy
	Infura
)

func DetectUpstreamVendor(urls []string) UpstreamVendor {
	if len(urls) == 0 {
		return Unknown
	}
	first := upstreamVendor(urls[0])
	if first == Unknown {
		return Unknown
	}

	for _, url := range urls[1:] {
		vendor := upstreamVendor(url)
		if vendor != first {
			return Unknown
		}
	}

	return first
}

func upstreamVendor(url string) UpstreamVendor {
	if strings.Contains(url, "infura.io") {
		return Infura
	} else if strings.Contains(url, "quiknode.pro") {
		return QuickNode
	} else if strings.Contains(url, "alchemy.com") || strings.Contains(url, ".alchemyapi.io") {
		return Alchemy
	} else if strings.Contains(url, "drpc.live") || strings.Contains(url, "drpc.org") {
		return DRPC
	}
	return Unknown
}
