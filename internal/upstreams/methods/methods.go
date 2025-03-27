package methods

import (
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dshaltie/internal/config"
)

var (
	TraceGroup   = "trace"
	DebugGroup   = "debug"
	FilterGroup  = "filter"
	DefaultGroup = "default"
)

var methodGroups = mapset.NewThreadUnsafeSet[string](TraceGroup, DefaultGroup, FilterGroup, DebugGroup)

func isMethodGroup(value string) bool {
	return methodGroups.ContainsOne(value)
}

type Methods interface {
	GetSupportedMethods() mapset.Set[string]
	HasMethod(string) bool
	GetGroupMethods(string) mapset.Set[string]
}

type UpstreamMethods struct {
	delegate         Methods
	availableMethods mapset.Set[string]
}

func NewUpstreamMethods(delegate Methods, methodsConfig *config.MethodsConfig) *UpstreamMethods {
	enableMethodGroupMethods, enableMethods := getMethodGroupMethods(delegate, methodsConfig.EnableMethods)
	disableMethodGroupMethods, disableMethods := getMethodGroupMethods(delegate, methodsConfig.DisableMethods)

	availableMethods := delegate.GetSupportedMethods().Union(enableMethodGroupMethods)
	availableMethods.RemoveAll(disableMethodGroupMethods.ToSlice()...)
	availableMethods = availableMethods.Union(enableMethods)
	availableMethods.RemoveAll(disableMethods.ToSlice()...)

	return &UpstreamMethods{
		availableMethods: availableMethods,
		delegate:         delegate,
	}
}

func (u *UpstreamMethods) GetSupportedMethods() mapset.Set[string] {
	return u.availableMethods.Clone()
}

func (u *UpstreamMethods) HasMethod(method string) bool {
	return u.availableMethods.ContainsOne(method)
}

func (u *UpstreamMethods) GetGroupMethods(group string) mapset.Set[string] {
	return u.delegate.GetGroupMethods(group)
}

func getMethodGroupMethods(delegate Methods, values []string) (mapset.Set[string], mapset.Set[string]) {
	groupMethods := mapset.NewThreadUnsafeSet[string]()
	plainMethods := mapset.NewThreadUnsafeSet[string]()

	for _, value := range values {
		if isMethodGroup(value) {
			groupMethods = groupMethods.Union(delegate.GetGroupMethods(value))
		} else {
			plainMethods.Add(value)
		}
	}

	return groupMethods, plainMethods
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

func (c *ChainMethods) GetGroupMethods(group string) mapset.Set[string] {
	for _, delegateMethods := range c.delegates {
		groupMethods := delegateMethods.GetGroupMethods(group)
		if !groupMethods.IsEmpty() {
			return groupMethods
		}
	}
	return mapset.NewThreadUnsafeSet[string]()
}

var _ Methods = (*ChainMethods)(nil)
