package methods

import (
	"context"

	mapset "github.com/deckarep/golang-set/v2"
	specs "github.com/drpcorg/method-specs/pkg/methods"
)

// MethodsDetector reports which methods an upstream does not support. It returns the
// unsupported subset rather than the supported one, so that adding a detector can only
// ever narrow an upstream and never widen it.
//
// The return value is three-valued, and the distinction carries the whole design:
//
//   - a non-empty set - "these methods are missing";
//   - an empty, non-nil set - "I asked, and nothing is missing";
//   - nil - "I have never managed to find out".
//
// Collapsing the last two would make a node that is briefly unreachable indistinguishable
// from a fully-featured one, and republish an empty verdict that restores every method the
// previous round stripped. GenericMethodsProcessor keeps each detector's last non-nil
// verdict, so a detector that returns nil contributes what it last established rather than
// dropping out of the merge.
//
// A detector that can answer for only part of its subject is expected to remember the rest
// itself, at whatever granularity it owns - see MethodProbeDetector, which retains per
// probe so that one timed-out call does not discard what it knows about the others.
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
	if len(connectorTypes) == 0 {
		// No connector, nothing to form an opinion about. Passing the empty list on would
		// mean the opposite: GetSpecMethodsByConnectors reads it as "don't filter" and
		// returns every method of every connector.
		return detectable
	}

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
