package grpc_ingress

import (
	"testing"

	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// The specs give reflection the service NAMES, the linked generated packages
// give it the CONTENT; this test guarantees the two never diverge.
func TestChainDescriptorsCoverSpecServices(t *testing.T) {
	specs_utils.LoadMethodSpecs()

	services := specs.GetGrpcServices()
	require.NotEmpty(t, services)

	for _, service := range services {
		_, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service))
		assert.NoError(t, err,
			"service %s is declared in a method spec but its descriptors are not linked into the binary - add its generated package to chain_descriptors.go",
			service,
		)
	}
}

// Reflection clients (Postman among them) resolve imports one at a time via
// file_by_filename, which the server answers from GlobalFiles.FindFileByPath.
// file_containing_symbol silently skips unresolvable imports, so only this
// walk catches a dependency whose descriptors are not linked - the client
// then fails the whole reflection load with "proto: not found".
func TestChainDescriptorsResolveEveryImportByFilename(t *testing.T) {
	specs_utils.LoadMethodSpecs()

	services := specs.GetGrpcServices()
	require.NotEmpty(t, services)

	checked := map[string]bool{}
	var walk func(path, importer string)
	walk = func(path, importer string) {
		if checked[path] {
			return
		}
		checked[path] = true
		file, err := protoregistry.GlobalFiles.FindFileByPath(path)
		if !assert.NoError(t, err, "%s (imported by %s) is not resolvable by filename - reflection clients fail the whole load on it", path, importer) {
			return
		}
		imports := file.Imports()
		for i := 0; i < imports.Len(); i++ {
			walk(imports.Get(i).Path(), path)
		}
	}

	for _, service := range services {
		descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service))
		require.NoError(t, err, service)
		walk(descriptor.ParentFile().Path(), service)
	}
}
