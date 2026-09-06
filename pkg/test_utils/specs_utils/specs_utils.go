package specs_utils

import (
	"sync"

	specs "github.com/drpcorg/public/pkg/methods"
)

var loadMethodSpecsOnce sync.Once

// LoadMethodSpecs loads the method specs once per test binary, the way main loads
// them once per process. Re-running the loader per test rewrites the spec registry
// while goroutines started by earlier tests (chain supervisors, upstreams) may
// still be reading it.
func LoadMethodSpecs() {
	loadMethodSpecsOnce.Do(func() {
		if err := specs.NewMethodSpecLoader().Load(); err != nil {
			panic(err)
		}
	})
}
