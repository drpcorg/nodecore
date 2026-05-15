package specs

import "fmt"

type resolvedSpec struct {
	// methods is the connector-agnostic view used by the current helpers.
	// connectors keeps the same spec projected per connector type.
	methods    *methodGroups
	connectors *connectorMethods
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

	importedSpecs, err := resolveImportedSpecs(spec.SpecImports, specs, visiting, currentMethods)
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

func resolveImportedSpecs(importedSpecNames []string, specs map[string]*MethodSpec, visiting map[string]bool, currentMethods *methodGroups) (*importedSpecData, error) {
	importedSpecs := newImportedSpecData()
	importedMethodOwners := map[string]string{}

	for _, importedSpecName := range importedSpecNames {
		if _, ok := specs[importedSpecName]; !ok {
			return nil, fmt.Errorf("imported spec %s not found", importedSpecName)
		}
		if err := enrichSpec(importedSpecName, specs, visiting); err != nil {
			return nil, err
		}

		importedSpec := resolvedSpecs[importedSpecName]
		if err := mergeImportedSpecMethods(currentMethods, importedMethodOwners, importedSpecName, importedSpec); err != nil {
			return nil, err
		}

		// Reuse the already-enriched imported spec both for connector-type discovery
		// and for the later connector-specific merge pass.
		importedSpecs.add(importedSpecName, importedSpec)
	}

	return importedSpecs, nil
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
		methods:    methods,
		connectors: connectors,
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
