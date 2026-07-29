package protocol

import (
	"github.com/drpcorg/nodecore/pkg/utils"
)

// RequestMethod is a request's method name in two forms, both derived once when
// the request is created.
//
// Name is the authoritative form, byte-for-byte as the client sent it: routing,
// spec lookups, method matching, cache keys, rate-limit keys and log output all
// use it.
//
// ValidUTF8Name is the only form safe to hand to code that requires valid UTF-8.
// A REST path or a JSON-RPC method name may carry arbitrary bytes, and two kinds
// of sink break on them: Prometheus label values, where client_golang panics and
// takes the process down, and the stats key marshalled into proto3's
// "string method" field, where proto.Marshal fails and drops the whole batch.
type RequestMethod struct {
	name          string
	validUTF8Name string
}

func NewRequestMethod(name string) RequestMethod {
	return RequestMethod{
		name:          name,
		validUTF8Name: utils.ToValidUTF8(name),
	}
}

func (m RequestMethod) Name() string {
	return m.name
}

func (m RequestMethod) ValidUTF8Name() string {
	return m.validUTF8Name
}
