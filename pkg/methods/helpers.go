package specs

import (
	"maps"

	mapset "github.com/deckarep/golang-set/v2"
)

func GetSpecMethodsByConnectors(specName string, connectorTypes []ApiConnectorType) map[string]map[string]*Method {
	spec := getResolvedSpec(specName)
	if spec == nil || spec.methods == nil {
		return nil
	}
	if len(connectorTypes) == 0 {
		return spec.methods.byGroup
	}

	mergedMethods := newMethodGroups()
	if spec.connectors == nil {
		return maps.Clone(mergedMethods.byGroup)
	}

	selectedMethods := map[string]struct{}{}
	for _, connectorType := range connectorTypes {
		currentConnectorMethods := spec.connectors.byConnector[connectorType]
		if currentConnectorMethods == nil {
			continue
		}

		overlayMethodGroups(mergedMethods, currentConnectorMethods, selectedMethods)
	}

	return maps.Clone(mergedMethods.byGroup)
}

func GetSpecConnectors(specName string) []ApiConnectorType {
	spec := getResolvedSpec(specName)
	if spec == nil || spec.connectors == nil {
		return nil
	}
	return spec.connectors.apiConnectors
}

func GetSpecMethod(specName, methodName string) *Method {
	methods := getMethodGroups(specName)
	if methods == nil {
		return nil
	}
	return methods.method(methodName)
}

func GetSpecMethodWithFallback(specName, methodName string) *Method {
	method := GetSpecMethod(specName, methodName)
	if method != nil {
		return method
	}
	return DefaultMethodWithConnectorTypes(methodName, GetSpecConnectors(specName))
}

// MatchRestMethod resolves a concrete REST path ("VERB#/v2/accounts/abc")
// against the spec's registered templates and returns the matched template
// name, the wildcard captures in path order, and ok. The template name is
// what callers should then pass to GetSpecMethod / use as the canonical
// method identifier.
//
// Returns ok=false when the spec is unknown, has no REST methods, or the
// given path matches none of them. Callers decide what to do on a miss -
// the helper itself doesn't fall back.
func MatchRestMethod(specName, fullPath string) (template string, pathParams []string, ok bool) {
	spec := getResolvedSpec(specName)
	if spec == nil || spec.restMatcher == nil {
		return "", nil, false
	}
	return spec.restMatcher.Match(fullPath)
}

func GetSubMethods(specName string) mapset.Set[string] {
	subMethods := mapset.NewThreadUnsafeSet[string]()
	methods := getMethodGroups(specName)
	if methods == nil {
		return subMethods
	}
	for name := range methods.subMethods() {
		subMethods.Add(name)
	}
	return subMethods
}

func IsSubscribeMethod(specName, methodName string) bool {
	subSettings := subscribeSettings(specName, methodName)
	if subSettings == nil {
		return false
	}
	return subSettings.IsSubscribe
}

func GetUnsubscribeMethod(specName, methodName string) (string, bool) {
	subSettings := subscribeSettings(specName, methodName)
	if subSettings == nil {
		return "", false
	}
	return subSettings.UnsubMethod, true
}

func getMethod(specName, methodName string) *Method {
	methods := getMethodGroups(specName)
	if methods == nil {
		return nil
	}
	return methods.method(methodName)
}

func subscribeSettings(specName, methodName string) *Subscription {
	method := getMethod(specName, methodName)
	if method == nil {
		return nil
	}
	return method.Subscription
}

func getResolvedSpec(specName string) *resolvedSpec {
	m := resolvedSpecs.Load()
	if m == nil {
		return nil
	}
	spec, ok := (*m)[specName]
	if !ok {
		return nil
	}

	return spec
}

func getMethodGroups(specName string) *methodGroups {
	spec := getResolvedSpec(specName)
	if spec == nil {
		return nil
	}

	return spec.methods
}

func overlayMethodGroups(dst, src *methodGroups, selectedMethods map[string]struct{}) {
	if dst == nil || src == nil || selectedMethods == nil {
		return
	}

	for groupName, groupMethods := range src.byGroup {
		if groupName == DefaultMethodGroup || groupName == SubMethodGroup {
			continue
		}

		dst.ensureGroup(groupName)
		for methodName, method := range groupMethods {
			if _, exists := selectedMethods[methodName]; exists {
				continue
			}

			clonedMethod := cloneMethod(method)
			dst.byGroup[groupName][methodName] = clonedMethod
			dst.byGroup[DefaultMethodGroup][methodName] = clonedMethod
			selectedMethods[methodName] = struct{}{}
			if clonedMethod.IsSubscribe() {
				dst.byGroup[SubMethodGroup][methodName] = clonedMethod
			}
		}
	}
}
