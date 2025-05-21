package fork_choice

import "github.com/drpcorg/dsheltie/internal/protocol"

type ForkChoice interface {
	Choose(protocol.UpstreamEvent) (bool, uint64)
}
