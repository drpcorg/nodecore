package specs

import (
	"fmt"
	"slices"
	"strings"

	"github.com/drpcorg/nodecore/pkg/utils"
)

type resolvedSpec struct {
	// methods is the connector-agnostic view used by the current helpers.
	// connectors keeps the same spec projected per connector type.
	methods    *methodGroups
	connectors *connectorMethods
	// restMatcher resolves a literal "VERB#/path" against every REST-style
	// method name in this spec. Nil when the spec defines no REST methods.
	restMatcher *utils.PathMatcher
}

type importedSpecData struct {
	specsByName    map[string]*resolvedSpec
	connectorTypes map[ApiConnectorType]struct{}
}

var resolvedSpecs map[string]*resolvedSpec

func enrichSpecs(specs map[string]*MethodSpec) error {
	for specName := range specs {
		if _, specExisted := resolvedSpecs[specName]; specExisted {
			continue
		}
		if err := enrichSpec(specName, specs, map[string]bool{}); err != nil {
			return err
		}
	}

	return nil
}

func enrichSpec(specName string, specs map[string]*MethodSpec, visiting map[string]bool) error {
	if _, specExisted := resolvedSpecs[specName]; specExisted {
		return nil
	}

	spec, ok := specs[specName]
	if !ok {
		return fmt.Errorf("spec '%s' not found", specName)
	}
	if visiting[specName] {
		return fmt.Errorf("spec '%s', error 'circular spec import detected'", specName)
	}

	visiting[specName] = true
	defer delete(visiting, specName)

	// Leaf specs can drop disabled methods immediately. Importing specs must keep
	// them around because a disabled local method is also used as a delete/override
	// marker during merge.
	removeDisabled := len(spec.SpecImports) == 0

	currentMethods, err := newMethodGroupsFromSpec(spec, spec.SpecData.apiConnectors, removeDisabled)
	if err != nil {
		return wrapSpecError(specName, err)
	}

	importedSpecs, err := resolveImportedSpecs(spec, spec.SpecImports, specs, visiting, currentMethods)
	if err != nil {
		return wrapSpecError(specName, err)
	}

	// Bundle-like specs may not declare connectors themselves, so inherit the
	// effective connector set from their imports before building per-connector views.
	currentConnectorTypes := resolveConnectorTypes(spec.SpecData.apiConnectors, importedSpecs.connectorTypes)
	currentConnectors, err := newConnectorMethodsFromSpec(spec, currentConnectorTypes, removeDisabled)
	if err != nil {
		return wrapSpecError(specName, err)
	}
	if err := currentConnectors.mergeImports(importedSpecs, currentConnectorTypes, spec.SpecImports); err != nil {
		return wrapSpecError(specName, err)
	}

	currentConnectors.initConnectors()
	resolvedSpecs[specName] = newResolvedSpec(currentMethods, currentConnectors)
	return nil
}

// buildRestMatcher collects every method name that looks like a REST template
// ("VERB#/...") and turns them into a single PathMatcher. Returns nil if the
// spec has no REST methods, so the helper can short-circuit on JSON-RPC-only
// specs without paying for a trie walk.
func buildRestMatcher(methods *methodGroups) *utils.PathMatcher {
	if methods == nil {
		return nil
	}
	var templates []string
	for name := range methods.defaultMethods() {
		if strings.Contains(name, "#") {
			templates = append(templates, name)
		}
	}
	if len(templates) == 0 {
		return nil
	}
	return utils.NewPathMatcher(templates)
}

func resolveImportedSpecs(currentSpec *MethodSpec, importedSpecNames []string, specs map[string]*MethodSpec, visiting map[string]bool, currentMethods *methodGroups) (*importedSpecData, error) {
	importedSpecs := newImportedSpecData()
	importedMethodOwners := map[string]string{}

	for _, importedSpecName := range importedSpecNames {
		importedSpecData, ok := specs[importedSpecName]
		if !ok {
			return nil, fmt.Errorf("imported spec %s not found", importedSpecName)
		}
		if err := enrichSpec(importedSpecName, specs, visiting); err != nil {
			return nil, err
		}

		importedSpec := resolvedSpecs[importedSpecName]
		if err := validateImportedSpecCompatibility(currentSpec, importedSpecData, importedSpec); err != nil {
			return nil, err
		}
		if err := mergeImportedSpecMethods(currentMethods, importedMethodOwners, importedSpecName, importedSpec); err != nil {
			return nil, err
		}

		// Reuse the already-enriched imported spec both for connector-type discovery
		// and for the later connector-specific merge pass.
		importedSpecs.add(importedSpecName, importedSpec)
	}

	return importedSpecs, nil
}

func validateImportedSpecCompatibility(currentSpec, importedSpecData *MethodSpec, importedSpec *resolvedSpec) error {
	if currentSpec == nil || currentSpec.SpecData == nil || currentSpec.SpecData.specType == BundleSpec {
		return nil
	}

	importedConnectorTypes := effectiveConnectorTypes(importedSpecData, importedSpec)
	if sameConnectorTypes(currentSpec.SpecData.apiConnectors, importedConnectorTypes) {
		return nil
	}

	return fmt.Errorf(
		"plain spec %s cannot import spec %s because api connectors differ: current=%v imported=%v",
		currentSpec.SpecData.Name,
		importedSpecData.SpecData.Name,
		connectorTypeNames(currentSpec.SpecData.apiConnectors),
		connectorTypeNames(importedConnectorTypes),
	)
}

func mergeImportedSpecMethods(currentMethods *methodGroups, importedMethodOwners map[string]string, importedSpecName string, importedSpec *resolvedSpec) error {
	if importedSpec == nil || importedSpec.methods == nil {
		return nil
	}
	if err := validateSameLevelImportedMethods(importedMethodOwners, importedSpecName, importedSpec.methods); err != nil {
		return err
	}

	return currentMethods.mergeFrom(importedSpec.methods)
}

func resolveConnectorTypes(currentConnectorTypes []ApiConnectorType, importedConnectorTypes map[ApiConnectorType]struct{}) []ApiConnectorType {
	if len(currentConnectorTypes) > 0 {
		return currentConnectorTypes
	}

	resolvedConnectorTypes := make([]ApiConnectorType, 0, len(importedConnectorTypes))
	for connectorType := range importedConnectorTypes {
		resolvedConnectorTypes = append(resolvedConnectorTypes, connectorType)
	}

	return resolvedConnectorTypes
}

func effectiveConnectorTypes(importedSpecData *MethodSpec, importedSpec *resolvedSpec) []ApiConnectorType {
	if importedSpecData == nil || importedSpecData.SpecData == nil {
		return nil
	}
	if importedSpecData.SpecData.specType == PlainSpec {
		return slices.Clone(importedSpecData.SpecData.apiConnectors)
	}
	if importedSpec == nil || importedSpec.connectors == nil {
		return nil
	}

	return slices.Clone(importedSpec.connectors.apiConnectors)
}

func sameConnectorTypes(left, right []ApiConnectorType) bool {
	leftSet := connectorTypeSet(left)
	rightSet := connectorTypeSet(right)
	if len(leftSet) != len(rightSet) {
		return false
	}

	for connectorType := range leftSet {
		if _, ok := rightSet[connectorType]; !ok {
			return false
		}
	}

	return true
}

func connectorTypeNames(connectorTypes []ApiConnectorType) []string {
	names := make([]string, 0, len(connectorTypes))
	for _, connectorType := range connectorTypes {
		names = append(names, connectorType.String())
	}
	slices.Sort(names)

	return names
}

func validateSameLevelImportedMethods(importedMethodOwners map[string]string, importedSpecName string, importedMethods *methodGroups) error {
	for methodName := range importedMethods.defaultMethods() {
		previousImportedSpecName, exists := importedMethodOwners[methodName]
		if exists {
			return fmt.Errorf(
				"same-level imported specs %s and %s define method %s",
				previousImportedSpecName,
				importedSpecName,
				methodName,
			)
		}
		importedMethodOwners[methodName] = importedSpecName
	}

	return nil
}

func newResolvedSpec(methods *methodGroups, connectors *connectorMethods) *resolvedSpec {
	return &resolvedSpec{
		methods:     methods,
		connectors:  connectors,
		restMatcher: buildRestMatcher(methods),
	}
}

func newImportedSpecData() *importedSpecData {
	return &importedSpecData{
		specsByName:    map[string]*resolvedSpec{},
		connectorTypes: map[ApiConnectorType]struct{}{},
	}
}

func (i *importedSpecData) add(specName string, spec *resolvedSpec) {
	if spec == nil {
		return
	}

	i.specsByName[specName] = spec
	if spec.connectors != nil {
		spec.connectors.collectTypes(i.connectorTypes)
	}
}

func wrapSpecError(specName string, err error) error {
	return fmt.Errorf("spec '%s', error '%s'", specName, err.Error())
}
