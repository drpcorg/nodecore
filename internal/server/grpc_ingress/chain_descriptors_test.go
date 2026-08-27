package grpc_ingress

import (
	"testing"

	specs "github.com/drpcorg/method-specs/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// The specs give reflection the service NAMES, the linked generated packages
// give it the CONTENT; this test guarantees the two never diverge.
func TestChainDescriptorsCoverSpecServices(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

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
