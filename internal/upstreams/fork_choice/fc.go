package fork_choice

import "github.com/drpcorg/nodecore/internal/protocol"

type ForkChoice interface {
	Choose(upstreamId string, event *protocol.StateUpstreamEvent) (bool, uint64)
}
