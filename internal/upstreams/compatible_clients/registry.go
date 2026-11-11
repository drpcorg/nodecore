package compatible_clients

import (
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
)

// Registry is what the client version validator reads: the live rules and the policy
// for a node whose client cannot be probed.
type Registry struct {
	rules           *utils.Reloadable[Rules]
	allowUndetected bool
}

func NewRegistry(rules *utils.Reloadable[Rules], allowUndetected bool) *Registry {
	return &Registry{rules: rules, allowUndetected: allowUndetected}
}

// AllowsUndetected reports whether a node whose client type and version cannot be
// detected passes the check (true) or is held out until it answers (false).
func (r *Registry) AllowsUndetected() bool {
	return r.allowUndetected
}

// Check judges a client version on a chain against the current rules.
func (r *Registry) Check(clientType, clientVersion string, chain *chains.ConfiguredChain) Verdict {
	return r.rules.Get().Check(clientType, clientVersion, chain)
}
