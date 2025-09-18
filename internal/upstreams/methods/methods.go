package methods

import (
	"fmt"
	"maps"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/internal/config"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/samber/lo"
)

type Methods interface {
	GetSupportedMethods() mapset.Set[string]
	HasMethod(method string) bool
	GetMethod(methodName string) *specs.Method
}

type UpstreamMethods struct {
	availableMethods map[string]*specs.Method
	methodNames      mapset.Set[string]
}

func NewUpstreamMethods(methodSpecName string, methodsConfig *config.MethodsConfig) (*UpstreamMethods, error) {
	specMethods := specs.GetSpecMethods(methodSpecName)
	if specMethods == nil {
		return nil, fmt.Errorf("no method spec with name '%s'", methodSpecName)
	}
	specMethodGroups := mapset.NewThreadUnsafeSet[string](lo.Keys(specMethods)...)
	availableMethods := maps.Clone(specMethods[specs.DefaultMethodGroup])

	// remove disabled methods
	for _, disabled := range methodsConfig.DisableMethods {
		if specMethodGroups.ContainsOne(disabled) {
			methodGroup := specMethods[disabled]
			for _, method := range lo.Keys(methodGroup) {
				delete(availableMethods, method)
			}
		} else {
			delete(availableMethods, disabled)
		}
	}

	// add enabled methods
	for _, enabled := range methodsConfig.EnableMethods {
		if specMethodGroups.ContainsOne(enabled) {
			methodGroup := specMethods[enabled]
			for methodName, method := range methodGroup {
				availableMethods[methodName] = method
			}
		} else {
			method, ok := specMethods[specs.DefaultMethodGroup][enabled]
			if ok {
				availableMethods[enabled] = method
			} else {
				// if there is no such method in the chain spec then add it as a default one
				availableMethods[enabled] = specs.DefaultMethod(enabled)
			}
		}
	}

	return &UpstreamMethods{
		availableMethods: availableMethods,
		methodNames:      mapset.NewThreadUnsafeSet[string](lo.Keys(availableMethods)...),
	}, nil
}

func (u *UpstreamMethods) GetSupportedMethods() mapset.Set[string] {
	return u.methodNames.Clone()
}

func (u *UpstreamMethods) HasMethod(method string) bool {
	return u.methodNames.ContainsOne(method)
}

func (u *UpstreamMethods) GetMethod(methodName string) *specs.Method {
	if !u.HasMethod(methodName) {
		return nil
	}
	return u.availableMethods[methodName]
}

var _ Methods = (*UpstreamMethods)(nil)

type ChainMethods struct {
	delegates        []Methods
	availableMethods mapset.Set[string]
}

func NewChainMethods(delegates []Methods) *ChainMethods {
	availableMethods := mapset.NewThreadUnsafeSet[string]()
	for _, delegateMethods := range delegates {
		availableMethods = availableMethods.Union(delegateMethods.GetSupportedMethods())
	}

	return &ChainMethods{
		availableMethods: availableMethods,
		delegates:        delegates,
	}
}

func (c *ChainMethods) GetSupportedMethods() mapset.Set[string] {
	return c.availableMethods.Clone()
}

func (c *ChainMethods) HasMethod(method string) bool {
	return c.availableMethods.ContainsOne(method)
}

func (c *ChainMethods) GetMethod(methodName string) *specs.Method {
	for _, delegate := range c.delegates {
		if delegate.HasMethod(methodName) {
			return delegate.GetMethod(methodName)
		}
	}
	return nil
}

var _ Methods = (*ChainMethods)(nil)
