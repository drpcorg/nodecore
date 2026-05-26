package specs

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/bytedance/sonic"
	mapset "github.com/deckarep/golang-set/v2"
)

//go:embed specs/*.json
var specsFS embed.FS

const (
	DefaultMethodGroup = "default"
	SubMethodGroup     = "sub"
	SpecPathVar        = "NODECORE_SPECS_PATH"
)

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
	resolvedSpecs = map[string]*resolvedSpec{}

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

	if err = enrichSpecs(specs); err != nil {
		return fmt.Errorf("couldn't merge method specs: %s", err.Error())
	}

	return nil
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

		if err := spec.validate(); err != nil {
			return nil, fmt.Errorf("file - '%s', spec validation error: %s", path, err.Error())
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
