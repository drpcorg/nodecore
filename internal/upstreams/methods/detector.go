package methods

import (
	"context"

	mapset "github.com/deckarep/golang-set/v2"
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
