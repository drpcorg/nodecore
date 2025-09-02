package specs

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"reflect"

	"github.com/bytedance/sonic"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/imdario/mergo"
	"github.com/samber/lo"
)

//go:embed specs/*.json
var specsFS embed.FS

const (
	DefaultMethodGroup = "default"
	SpecPathVar        = "DSHELTIE_SPECS_PATH"
)

type MethodSpec struct {
	SpecData    *SpecData     `json:"spec"`
	SpecImports []string      `json:"spec-imports"`
	Methods     []*MethodData `json:"methods"`
}

type SpecData struct {
	Name string `json:"name"`
}

type MethodData struct {
	Name      string          `json:"name"`
	Group     string          `json:"group"`
	Settings  *MethodSettings `json:"settings"`
	TagParser *TagParser      `json:"tag-parser"`
	Enabled   *bool           `json:"enabled"`
}

type MethodSettings struct {
	Cacheable    *bool         `json:"cacheable"`
	Sticky       *Sticky       `json:"sticky"`
	Subscription *Subscription `json:"subscription"`
}

type Sticky struct {
	SendSticky   bool `json:"send-sticky"`   // to send to the same node
	CreateSticky bool `json:"create-sticky"` // to add an upstream index to the payload
}

type Subscription struct {
	IsSubscribe   bool   `json:"is-subscribe"`
	IsUnsubscribe bool   `json:"is-unsubscribe"`
	UnsubMethod   string `json:"unsubscribe-method"`
}

type ParserReturnType string

const (
	BlockNumberType ParserReturnType = "blockNumber" // hex number or tag (latest, earliest, etc)
	BlockRefType    ParserReturnType = "blockRef"    // hash, hex number or tag (latest, earliest, etc)
	StringType      ParserReturnType = "string"      // string values
)

type TagParser struct {
	ReturnType ParserReturnType `json:"type"`
	Path       string           `json:"path"`
}

type groupMethods map[string]map[string]*Method

var specMethods map[string]groupMethods

func GetSpecMethods(specName string) map[string]map[string]*Method {
	methods, ok := specMethods[specName]
	if !ok {
		return nil
	}
	return maps.Clone(methods)
}

func GetSpecMethod(specName, methodName string) *Method {
	methodSpec := GetSpecMethods(specName)
	if methodSpec == nil {
		return nil
	}
	method, ok := methodSpec[DefaultMethodGroup][methodName]
	if !ok {
		return nil
	}
	return method
}

func IsSubscribeMethod(specName, methodName string) bool {
	subSettings := subscribeSettings(specName, methodName)
	if subSettings == nil {
		return false
	}
	return subSettings.IsSubscribe
}

func IsUnsubscribeMethod(specName, methodName string) bool {
	subSettings := subscribeSettings(specName, methodName)
	if subSettings == nil {
		return false
	}
	return subSettings.IsUnsubscribe
}

func IsStickySendMethod(specMethod *Method) bool {
	if specMethod == nil {
		return false
	}
	sticky := specMethod.Sticky
	if sticky == nil {
		return false
	}
	return sticky.SendSticky
}

func IsStickyCreateMethod(specMethod *Method) bool {
	if specMethod == nil {
		return false
	}
	sticky := specMethod.Sticky
	if sticky == nil {
		return false
	}
	return sticky.CreateSticky
}

func GetUnsubscribeMethod(specName, methodName string) (string, bool) {
	subSettings := subscribeSettings(specName, methodName)
	if subSettings == nil {
		return "", false
	}
	return subSettings.UnsubMethod, true
}

func getMethod(specName, methodName string) *Method {
	methods, ok := specMethods[specName]
	if !ok {
		return nil
	}
	method, ok := methods[DefaultMethodGroup][methodName]
	if !ok {
		return nil
	}
	return method
}

func subscribeSettings(specName, methodName string) *Subscription {
	method := getMethod(specName, methodName)
	if method == nil {
		return nil
	}
	return method.Subscription
}

type MethodSpecLoader struct {
	specsFS fs.ReadFileFS
}

func NewMethodSpecLoader() MethodSpecLoader {
	return MethodSpecLoader{
		specsFS: specsFS,
	}
}

func NewMethodSpecLoaderWithFs(specsFS fs.FS) MethodSpecLoader {
	readFileFs, ok := specsFS.(fs.ReadFileFS)
	if !ok {
		panic("not ReadFileFS")
	}
	return MethodSpecLoader{
		specsFS: readFileFs,
	}
}

func (m MethodSpecLoader) Load() error {
	specMethods = map[string]groupMethods{}

	specs := map[string]*MethodSpec{}
	err := fs.WalkDir(m.specsFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		fileInfo, err := d.Info()
		if err != nil {
			return err
		}

		spec, err := m.loadSpec(path, fileInfo)
		if err != nil {
			return err
		}
		if spec != nil {
			_, exist := specs[spec.SpecData.Name]
			if exist {
				return fmt.Errorf("spec with name '%s' already exists", spec.SpecData.Name)
			}

			specs[spec.SpecData.Name] = spec
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("couldn't read method specs: %s", err.Error())
	}

	if len(specs) == 0 {
		return fmt.Errorf("no method specs")
	}

	err = enrichSpecs(specs)
	if err != nil {
		return fmt.Errorf("couldn't merge method specs: %s", err.Error())
	}

	return nil
}

func enrichSpecs(specs map[string]*MethodSpec) error {
	for specName, spec := range specs {
		_, specExisted := specMethods[specName]
		if specExisted {
			continue
		}

		if len(spec.SpecImports) == 0 {
			currentGroupMethods, err := getGroupMethods(spec, true)
			if err != nil {
				return fmt.Errorf("spec '%s', error '%s'", specName, err.Error())
			}
			specMethods[specName] = currentGroupMethods
		} else {
			importedSpecMap := map[string]*MethodSpec{}
			for _, importedSpecName := range spec.SpecImports {
				importedMethodsSpec, ok := specs[importedSpecName]
				if ok {
					importedSpecMap[importedSpecName] = importedMethodsSpec
				}
			}
			err := enrichSpecs(importedSpecMap)
			if err != nil {
				return fmt.Errorf("spec '%s', error '%s'", specName, err.Error())
			}

			currentGroupMethods, err := getGroupMethods(spec, false)
			if err != nil {
				return fmt.Errorf("spec '%s', error '%s'", specName, err.Error())
			}

			for _, importedSpecName := range spec.SpecImports {
				importedMethodsSpec, ok := specMethods[importedSpecName]
				if ok {
					err = mergeMethods(currentGroupMethods, importedMethodsSpec)
					if err != nil {
						return fmt.Errorf("spec '%s', error '%s'", specName, err.Error())
					}
				}
			}

			specMethods[specName] = currentGroupMethods
		}
	}
	return nil
}

func mergeMethods(currentGroupMethods, importedGroupMethods groupMethods) error {
	for importedGroup, importedMethodsMap := range importedGroupMethods {
		_, existedInCurrent := currentGroupMethods[importedGroup]
		if !existedInCurrent {
			currentGroupMethods[importedGroup] = importedMethodsMap
		} else {
			currentGroup := currentGroupMethods[importedGroup]
			for importedMethodName, importedMethod := range importedMethodsMap {
				method, existed := currentGroup[importedMethodName]
				if existed {
					if !method.Enabled() {
						delete(currentGroup, importedMethodName)
						continue
					}
					err := mergo.Merge(method, importedMethod, mergo.WithTransformers(boolTransformer{}))
					if err != nil {
						return err
					}
				} else {
					method = importedMethod
				}
				currentGroup[importedMethodName] = method
			}
		}
	}

	return nil
}

func getGroupMethods(spec *MethodSpec, removeDisabled bool) (groupMethods, error) {
	specGroupMethodsByName := groupMethods{}
	specGroupMethodsByName[DefaultMethodGroup] = map[string]*Method{}
	specGroupMethods := lo.GroupBy(spec.Methods, func(item *MethodData) string {
		return item.Group
	})

	for group, methodDataArray := range specGroupMethods {
		for _, methodData := range methodDataArray {
			if removeDisabled && !*methodData.Enabled {
				continue
			}

			_, existed := specGroupMethodsByName[group]
			if !existed {
				specGroupMethodsByName[group] = make(map[string]*Method)
			}
			method, err := fromMethodData(methodData)
			if err != nil {
				return nil, err
			}
			specGroupMethodsByName[group][methodData.Name] = method
			specGroupMethodsByName[DefaultMethodGroup][methodData.Name] = method
		}
	}

	return specGroupMethodsByName, nil
}

func (m MethodSpecLoader) loadSpec(path string, file fs.FileInfo) (*MethodSpec, error) {
	if !file.IsDir() && filepath.Ext(path) == ".json" {
		specBytes, err := m.specsFS.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var spec MethodSpec
		err = sonic.Unmarshal(specBytes, &spec)
		if err != nil {
			return nil, err
		}

		if spec.SpecData == nil || spec.SpecData.Name == "" {
			return nil, fmt.Errorf("empty spec name, file - '%s'", path)
		}

		methodNames := mapset.NewThreadUnsafeSet[string]()

		for i, method := range spec.Methods {
			if method.Name == "" {
				return nil, fmt.Errorf("empty method name, file - '%s', index - %d", path, i)
			}
			if err = method.validate(); err != nil {
				return nil, fmt.Errorf("error during method '%s' of '%s' validation, cause: %s", method.Name, path, err.Error())
			}
			if methodNames.ContainsOne(method.Name) {
				return nil, fmt.Errorf("method '%s' already exists, file - '%s'", method.Name, path)
			}

			method.setDefaults()
			methodNames.Add(method.Name)
		}

		return &spec, nil
	}
	return nil, nil
}

func (m *MethodData) setDefaults() {
	if m.Group == "" {
		m.Group = "common"
	}
	if m.Enabled == nil {
		m.Enabled = lo.ToPtr(true)
	}
	if m.Settings == nil {
		m.Settings = &MethodSettings{Cacheable: lo.ToPtr(true)}
	} else {
		if m.Settings.Cacheable == nil {
			m.Settings.Cacheable = lo.ToPtr(true)
		}
	}
}

func (m *MethodData) validate() error {
	if m.TagParser != nil {
		if err := m.TagParser.validate(); err != nil {
			return err
		}
	}
	if m.Settings != nil {
		if err := m.Settings.validate(); err != nil {
			return err
		}
	}

	return nil
}

func (m *MethodSettings) validate() error {
	if m.Sticky != nil {
		if m.Sticky.CreateSticky && m.Sticky.SendSticky {
			return errors.New("both 'create-sticky' and 'send-sticky' are enabled")
		}
	}
	if m.Subscription != nil {
		if m.Subscription.IsSubscribe && m.Subscription.IsUnsubscribe {
			return errors.New("both 'is-subscribe' and 'is-unsubscribe' are enabled")
		}
	}
	return nil
}

func (p *TagParser) validate() error {
	if p.Path == "" {
		return errors.New("empty tag-parser path")
	}
	if err := p.ReturnType.validate(); err != nil {
		return err
	}

	return nil
}

func (p ParserReturnType) validate() error {
	switch p {
	case BlockRefType, BlockNumberType, StringType:
	default:
		return fmt.Errorf("wrong return type of tag-parser - %s", p)
	}
	return nil
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
