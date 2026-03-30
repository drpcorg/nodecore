package fork_choice

import "github.com/drpcorg/nodecore/internal/protocol"

type ForkChoice interface {
	Choose(upstreamId string, event *protocol.HeadUpstreamEvent) (bool, protocol.Block)
}
