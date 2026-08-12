package methods

import (
	"context"
	"slices"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

// MethodsDetector reports which methods an upstream does not support. It returns the
// unsupported subset rather than the supported one so that the empty set is the safe
// answer at every level: a detector with no opinion, a failed call and a fully-featured
// node all mean "strip nothing".
type MethodsDetector interface {
	DetectUnsupported(ctx context.Context) mapset.Set[string]
}

// DetectableMethods is the base set a detector may form opinions about: the chain's
// spec methods minus the locally-served ones. A local method is answered by nodecore
// itself, so whether the node implements it is irrelevant - and stripping one would
// remove it from the chain-level supported-method list that dshackle-mode clients read
// to decide what they can route here.
func DetectableMethods(specName string, connectorTypes []specs.ApiConnectorType) mapset.Set[string] {
	detectable := mapset.NewThreadUnsafeSet[string]()

	specMethods := specs.GetSpecMethodsByConnectors(specName, connectorTypes)
	if specMethods == nil {
		return detectable
	}

	for name, method := range specMethods[specs.DefaultMethodGroup] {
		if method.IsLocal() {
			continue
		}
		detectable.Add(name)
	}

	return detectable
}

// IsExplicitlyEnabled reports whether the operator pinned this method on for the
// upstream. Config enable is the last word: it outranks both a detector's verdict and a
// runtime ban, so both paths ask here rather than each re-deriving the rule.
func IsExplicitlyEnabled(methodsConfig *config.MethodsConfig, method string) bool {
	if methodsConfig == nil {
		return false
	}
	return slices.Contains(methodsConfig.EnableMethods, method)
}
