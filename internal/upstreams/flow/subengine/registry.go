package subengine

import (
	"context"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
)

// Registry holds one Engine per chain, created lazily on first use. It is
// process-wide and shared by both the HTTP/WS and gRPC entry points so that a
// subscription opened over either transport aggregates into the same source.
type Registry struct {
	ctx     context.Context
	engines *utils.CMap[chains.Chain, Engine]
}

func NewRegistry(ctx context.Context) *Registry {
	return &Registry{
		ctx:     ctx,
		engines: utils.NewCMap[chains.Chain, Engine](),
	}
}

// Get returns the engine for chain, creating it on first access. The Load fast
// path avoids constructing a throwaway engine on the common hit; LoadOrStore
// keeps creation atomic on the first-time race (a discarded engine holds no
// resources - sources are only started on Subscribe).
func (r *Registry) Get(chain chains.Chain) Engine {
	if engine, ok := r.engines.Load(chain); ok {
		return engine
	}
	engine, _ := r.engines.LoadOrStore(chain, NewEngine(r.ctx, chain))
	return engine
}
