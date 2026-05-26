package specs

import (
	"reflect"

	"github.com/imdario/mergo"
)

type methodGroups struct {
	byGroup map[string]map[string]*Method
}

func newMethodGroupsFromSpec(spec *MethodSpec, connectorTypes []ApiConnectorType, removeDisabled bool) (*methodGroups, error) {
	methods := newMethodGroups()

	for _, methodData := range spec.Methods {
		if removeDisabled && !*methodData.Enabled {
			continue
		}

		method, err := fromMethodData(methodData, connectorTypes)
		if err != nil {
			return nil, err
		}

		methods.add(methodData.Group, method)
	}

	return methods, nil
}

func newMethodGroups() *methodGroups {
	return &methodGroups{
		byGroup: map[string]map[string]*Method{
			DefaultMethodGroup: {},
			SubMethodGroup:     {},
		},
	}
}

func (m *methodGroups) add(group string, method *Method) {
	m.ensureGroup(group)
	m.byGroup[group][method.Name] = method
	// The synthetic default/sub groups are lookup indexes over the same methods,
	// not independent copies.
	m.byGroup[DefaultMethodGroup][method.Name] = method
	if method.IsSubscribe() {
		m.byGroup[SubMethodGroup][method.Name] = method
	}
}

func (m *methodGroups) ensureGroup(group string) {
	if _, ok := m.byGroup[group]; ok {
		return
	}

	m.byGroup[group] = map[string]*Method{}
}

func (m *methodGroups) mergeFrom(importedMethods *methodGroups) error {
	if importedMethods == nil {
		return nil
	}

	// Each resolved view owns its merged methods, so imports are cloned when a
	// group or method is adopted from another spec.
	for importedGroup, importedMethodsMap := range importedMethods.byGroup {
		currentGroup, existedInCurrent := m.byGroup[importedGroup]
		if !existedInCurrent {
			m.byGroup[importedGroup] = cloneMethodMap(importedMethodsMap)
			continue
		}

		for importedMethodName, importedMethod := range importedMethodsMap {
			method, existed := currentGroup[importedMethodName]
			if existed {
				// A disabled local method means "remove the inherited method" rather
				// than "merge settings into it".
				if !method.Enabled() {
					delete(currentGroup, importedMethodName)
					continue
				}
				if err := mergo.Merge(method, importedMethod, mergo.WithTransformers(boolTransformer{})); err != nil {
					return err
				}
			} else {
				method = cloneMethod(importedMethod)
			}

			currentGroup[importedMethodName] = method
		}
	}

	return nil
}

func (m *methodGroups) defaultMethods() map[string]*Method {
	return m.byGroup[DefaultMethodGroup]
}

func (m *methodGroups) method(methodName string) *Method {
	method, ok := m.defaultMethods()[methodName]
	if !ok {
		return nil
	}

	return method
}

func (m *methodGroups) subMethods() map[string]*Method {
	return m.byGroup[SubMethodGroup]
}

func cloneMethodMap(methods map[string]*Method) map[string]*Method {
	clonedMethods := make(map[string]*Method, len(methods))
	for methodName, method := range methods {
		clonedMethods[methodName] = cloneMethod(method)
	}

	return clonedMethods
}

func cloneMethod(method *Method) *Method {
	if method == nil {
		return nil
	}

	return new(*method)
}

type boolTransformer struct {
}

func (t boolTransformer) Transformer(typ reflect.Type) func(dst, src reflect.Value) error {
	switch typ.Kind() {
	case reflect.Bool:
		return func(dst, src reflect.Value) error {
			if dst.CanSet() {
				dst.Set(dst) // always prefer its own value
			}
			return nil
		}
	default:
		return nil
	}
}
