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
	// extraFS, when non-nil, is walked after specsFS and its specs are merged
	// on top of the embedded ones. Extra specs may add new method specs but
	// must not reuse an embedded spec name (same strict add-only semantics as
	// extra chains via chains.LoadExtraChains).
	extraFS fs.ReadFileFS
}

func NewMethodSpecLoader() MethodSpecLoader {
	return MethodSpecLoader{
		specsFS: specsFS,
	}
}

func NewMethodSpecLoaderWithFs(specsFS fs.FS) MethodSpecLoader {
	return MethodSpecLoader{
		specsFS: asReadFileFS(specsFS),
	}
}

// NewMethodSpecLoaderWithExtraFs returns a loader that serves the embedded
// specs plus any *.json specs found in extra. Extra specs extend the embedded
// registry; a spec whose name already exists in the embedded set is rejected.
func NewMethodSpecLoaderWithExtraFs(extra fs.FS) MethodSpecLoader {
	return MethodSpecLoader{
		specsFS: specsFS,
		extraFS: asReadFileFS(extra),
	}
}

func asReadFileFS(fsys fs.FS) fs.ReadFileFS {
	readFileFs, ok := fsys.(fs.ReadFileFS)
	if !ok {
		panic("not ReadFileFS")
	}
	return readFileFs
}

func (m MethodSpecLoader) Load() error {
	specs := map[string]*MethodSpec{}
	if err := m.walkSpecs(m.specsFS, specs); err != nil {
		return fmt.Errorf("couldn't read method specs: %s", err.Error())
	}
	if m.extraFS != nil {
		if err := m.walkSpecs(m.extraFS, specs); err != nil {
			return fmt.Errorf("couldn't read extra method specs: %s", err.Error())
		}
	}
	if len(specs) == 0 {
		return fmt.Errorf("no method specs")
	}

	// Build into a local map and publish it atomically so concurrent readers
	// never observe a half-built map or race with a reload.
	building := map[string]*resolvedSpec{}
	if err := enrichSpecs(specs, building); err != nil {
		return fmt.Errorf("couldn't merge method specs: %s", err.Error())
	}
	resolvedSpecs.Store(&building)

	return nil
}

func (m MethodSpecLoader) walkSpecs(fsys fs.ReadFileFS, specs map[string]*MethodSpec) error {
	return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		fileInfo, err := d.Info()
		if err != nil {
			return err
		}

		spec, err := loadSpec(fsys, path, fileInfo)
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
}

func loadSpec(fsys fs.ReadFileFS, path string, file fs.FileInfo) (*MethodSpec, error) {
	if !file.IsDir() && filepath.Ext(path) == ".json" {
		specBytes, err := fsys.ReadFile(path)
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
