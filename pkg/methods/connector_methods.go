package specs

import (
	"slices"

	"github.com/samber/lo"
)

type connectorMethods struct {
	byConnector   map[ApiConnectorType]*methodGroups
	apiConnectors []ApiConnectorType
}

func newConnectorMethodsFromSpec(spec *MethodSpec, connectorTypes []ApiConnectorType, removeDisabled bool) (*connectorMethods, error) {
	methodsByConnector := newConnectorMethods()

	for _, connectorType := range connectorTypes {
		methods, err := newMethodGroupsFromSpec(spec, connectorTypes, removeDisabled)
		if err != nil {
			return nil, err
		}

		methodsByConnector.byConnector[connectorType] = methods
	}

	return methodsByConnector, nil
}

func newConnectorMethods() *connectorMethods {
	return &connectorMethods{
		byConnector: map[ApiConnectorType]*methodGroups{},
	}
}

func (c *connectorMethods) mergeImports(importedSpecs *importedSpecData, connectorTypes []ApiConnectorType, importedSpecNames []string) error {
	allowedConnectorTypes := connectorTypeSet(connectorTypes)

	for _, importedSpecName := range importedSpecNames {
		importedSpec := importedSpecs.specsByName[importedSpecName]
		if importedSpec == nil || importedSpec.connectors == nil {
			continue
		}
		// Imports are merged only into connector buckets that exist on the current
		// spec, so a plain spec does not accidentally inherit methods for unrelated
		// connector types.
		if err := c.mergeFrom(importedSpec.connectors, allowedConnectorTypes); err != nil {
			return err
		}
	}

	return nil
}

func (c *connectorMethods) mergeFrom(importedConnectorMethods *connectorMethods, allowedConnectorTypes map[ApiConnectorType]struct{}) error {
	if importedConnectorMethods == nil {
		return nil
	}

	for connectorType, importedMethods := range importedConnectorMethods.byConnector {
		if len(allowedConnectorTypes) > 0 {
			if _, ok := allowedConnectorTypes[connectorType]; !ok {
				continue
			}
		}

		currentMethods, ok := c.byConnector[connectorType]
		if !ok {
			currentMethods = newMethodGroups()
			c.byConnector[connectorType] = currentMethods
		}
		if err := currentMethods.mergeFrom(importedMethods); err != nil {
			return err
		}
	}

	return nil
}

func (c *connectorMethods) collectTypes(connectorTypes map[ApiConnectorType]struct{}) {
	for connectorType := range c.byConnector {
		connectorTypes[connectorType] = struct{}{}
	}
}

func connectorTypeSet(connectorTypes []ApiConnectorType) map[ApiConnectorType]struct{} {
	allowedConnectorTypes := make(map[ApiConnectorType]struct{}, len(connectorTypes))
	for _, connectorType := range connectorTypes {
		allowedConnectorTypes[connectorType] = struct{}{}
	}

	return allowedConnectorTypes
}

func (c *connectorMethods) initConnectors() {
	if len(c.byConnector) == 0 {
		return
	}
	connectors := lo.Keys(c.byConnector)
	slices.Sort(connectors)

	c.apiConnectors = connectors
}
