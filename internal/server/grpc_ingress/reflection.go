package grpc_ingress

import (
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	v1reflectiongrpc "google.golang.org/grpc/reflection/grpc_reflection_v1"
	v1alphareflectiongrpc "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// chainServiceInfoProvider makes reflection's ListServices advertise the
// chain services the catch-all ingress routes (from the loaded method specs)
// on top of whatever is registered on the server itself. Only services whose
// descriptors are compiled into the binary are advertised, so ListServices
// never claims a symbol that FileContainingSymbol cannot serve; the
// descriptors themselves come from the default resolver (the global protobuf
// registry, populated by the generated chain packages, e.g. pkg/sui).
type chainServiceInfoProvider struct {
	server *grpc.Server
}

func (p *chainServiceInfoProvider) GetServiceInfo() map[string]grpc.ServiceInfo {
	services := p.server.GetServiceInfo()
	for _, name := range specs.GetGrpcServices() {
		if _, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(name)); err == nil {
			services[name] = grpc.ServiceInfo{}
		}
	}
	return services
}

// registerChainAwareReflection registers both reflection protocol versions
// (grpcurl still probes v1alpha against older servers) with the chain-aware
// service list.
func registerChainAwareReflection(server *grpc.Server) {
	opts := reflection.ServerOptions{Services: &chainServiceInfoProvider{server: server}}
	v1reflectiongrpc.RegisterServerReflectionServer(server, reflection.NewServerV1(opts))
	v1alphareflectiongrpc.RegisterServerReflectionServer(server, reflection.NewServer(opts))
}
