package fork_choice

import "github.com/drpcorg/dshaltie/internal/protocol"

type ForkChoice interface {
	Choose(protocol.UpstreamEvent) (bool, uint64)
}
