package server_ctx

import (
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/auth"
)

// reservedClientHeaders is nodecore's own credential surface: consumed by the
// ingresses for authentication and NEVER forwarded to an upstream - a node
// operator must not receive credentials usable against nodecore. Lowercase;
// header and metadata keys are case-insensitive. Ingress-specific reserved
// keys (e.g. the gRPC chain-routing metadata) are passed as extras by the
// respective ingress.
var reservedClientHeaders = mapset.NewThreadUnsafeSet(
	strings.ToLower(auth.XNodecoreKey),
	strings.ToLower(auth.XNodecoreToken),
	"authorization",
)

// SanitizeForwardedHeaders deep-copies client headers/metadata (http.Header,
// grpc metadata.MD and the protocol-level map are all map[string][]string)
// without the reserved credential headers and any extra reserved keys, so
// what enters a request holder is already safe to forward to an upstream.
func SanitizeForwardedHeaders[M ~map[string][]string](src M, extraReserved ...string) M {
	if len(src) == 0 {
		return nil
	}
	extras := mapset.NewThreadUnsafeSet[string]()
	for _, key := range extraReserved {
		extras.Add(strings.ToLower(key))
	}
	out := make(M, len(src))
	for key, values := range src {
		lowered := strings.ToLower(key)
		if reservedClientHeaders.Contains(lowered) || extras.Contains(lowered) {
			continue
		}
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
