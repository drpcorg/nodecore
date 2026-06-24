package protocol_test

import (
	"fmt"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/blake2b"
)

func TestNewUpstreamRestRequestStoresTemplateVerbatim(t *testing.T) {
	req := protocol.NewUpstreamRestRequest("test-id", "GET#/v2/status", nil, nil, "")

	assert.Equal(t, "GET#/v2/status", req.Method())
	assert.Equal(t, protocol.Rest, req.RequestType())
	assert.NotEmpty(t, req.Id(), "id must be initialised so concurrent observers don't collide")
	assert.NotNil(t, req.RequestObserver(), "observer must be non-nil so ObserverConnector doesn't panic")
}

func TestNewUpstreamRestRequestForwardsBody(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	req := protocol.NewUpstreamRestRequest("test-id", "POST#/v2/transactions", nil, body, "")

	got, err := req.Body()
	assert.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestNewUpstreamRestRequestEmptyBodyIsAllowed(t *testing.T) {
	req := protocol.NewUpstreamRestRequest("test-id", "GET#/v2/status", nil, nil, "")

	got, err := req.Body()
	assert.NoError(t, err)
	assert.Empty(t, got, "GET requests forward an empty body")
}

func TestNewUpstreamRestRequestCarriesTemplateWithWildcard(t *testing.T) {
	rp := &protocol.RequestParams{PathParams: []string{"X1Y2"}}
	req := protocol.NewUpstreamRestRequest("test-id", "GET#/v2/accounts/*", rp, nil, "")

	assert.Equal(t, "GET#/v2/accounts/*", req.Method(),
		"the template is the canonical method - it's what spec lookup, stats, and caching key on")
	assert.Equal(t, []string{"X1Y2"}, req.RequestParams().PathParams,
		"path captures live on RequestParams so the connector can rebuild the literal URL at send time")
}

func TestNewUpstreamRestRequestNotStreamingNotSubscribe(t *testing.T) {
	req := protocol.NewUpstreamRestRequest("test-id", "GET#/v2/status", nil, nil, "")

	assert.False(t, req.IsStream())
	assert.False(t, req.IsSubscribe())
}

func TestNewInternalUpstreamRestRequestStoresTemplateVerbatim(t *testing.T) {
	// Internal requests now carry a spec method template as Method() - the same
	// contract as NewUpstreamRestRequest - not a per-resource literal path. This
	// keeps the `method` identity bounded and lets the request resolve a real
	// spec method.
	req := protocol.NewInternalUpstreamRestRequest("GET#/v2/status", nil, chains.ALGORAND)

	assert.Equal(t, "GET#/v2/status", req.Method())
	assert.Equal(t, protocol.Rest, req.RequestType())
	assert.Equal(t, "1", req.Id())
	assert.NotNil(t, req.RequestObserver())
	assert.Nil(t, req.RequestParams(), "a fixed-path request carries no RequestParams")
}

func TestNewInternalUpstreamRestRequestCarriesWildcardCaptures(t *testing.T) {
	// algorand_chain_specific.go fetches "/v2/blocks/{round}?header-only=true":
	// the round is a wildcard capture on PathParams and the flag lives on
	// QueryParams, so Method() stays the bounded template the connector expands
	// via BuildRestURL.
	rp := &protocol.RequestParams{
		PathParams:  []string{"42"},
		QueryParams: map[string][]string{"header-only": {"true"}},
	}
	req := protocol.NewInternalUpstreamRestRequest("GET#/v2/blocks/*", rp, chains.ALGORAND)

	assert.Equal(t, "GET#/v2/blocks/*", req.Method(),
		"the template is the canonical method - the block number must not leak into it")
	assert.Equal(t, []string{"42"}, req.RequestParams().PathParams)
	assert.Equal(t, []string{"true"}, req.RequestParams().QueryParams["header-only"])
}

func TestNewInternalUpstreamRestRequestWithBodyForwardsBody(t *testing.T) {
	body := []byte(`{"num":42}`)
	req := protocol.NewInternalUpstreamRestRequestWithBody("POST#/wallet/getblock", nil, body, chains.TRON)

	assert.Equal(t, "POST#/wallet/getblock", req.Method())
	got, err := req.Body()
	assert.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestRestRequestHashPinsEncodingForBareTemplate(t *testing.T) {
	// With no params or body, the hash is just blake2b(method) - pins the exact
	// encoding so a future change to calculateRestHash is caught.
	req := protocol.NewUpstreamRestRequest("id", "GET#/v2/status", nil, nil, "")

	expected := fmt.Sprintf("%x", blake2b.Sum256([]byte("GET#/v2/status")))
	assert.Equal(t, expected, req.RequestHash())
}

func TestRestRequestHashDistinguishesPathParams(t *testing.T) {
	// The whole point of the fix: same template, different wildcard capture must
	// not collide (GET#/v2/blocks/* for two different rounds).
	one := protocol.NewUpstreamRestRequest("id", "GET#/v2/blocks/*",
		&protocol.RequestParams{PathParams: []string{"1"}}, nil, "")
	two := protocol.NewUpstreamRestRequest("id", "GET#/v2/blocks/*",
		&protocol.RequestParams{PathParams: []string{"2"}}, nil, "")

	assert.NotEqual(t, one.RequestHash(), two.RequestHash())
}

func TestRestRequestHashDistinguishesBody(t *testing.T) {
	// POST routes carry the resource in the body (e.g. tron getblock by number).
	one := protocol.NewUpstreamRestRequest("id", "POST#/wallet/getblock", nil, []byte(`{"num":1}`), "")
	two := protocol.NewUpstreamRestRequest("id", "POST#/wallet/getblock", nil, []byte(`{"num":2}`), "")

	assert.NotEqual(t, one.RequestHash(), two.RequestHash())
}

func TestRestRequestHashDistinguishesQueryParams(t *testing.T) {
	one := protocol.NewUpstreamRestRequest("id", "GET#/v2/accounts/*",
		&protocol.RequestParams{PathParams: []string{"a"}, QueryParams: map[string][]string{"format": {"json"}}}, nil, "")
	two := protocol.NewUpstreamRestRequest("id", "GET#/v2/accounts/*",
		&protocol.RequestParams{PathParams: []string{"a"}, QueryParams: map[string][]string{"format": {"msgpack"}}}, nil, "")

	assert.NotEqual(t, one.RequestHash(), two.RequestHash())
}

func TestRestRequestHashIsStableAndIgnoresHeaders(t *testing.T) {
	// Identical method/params/body -> identical hash, regardless of map order or
	// headers. Headers are excluded so cache entries stay shareable across
	// clients with differing auth/tracing headers.
	mk := func(headers map[string][]string) string {
		return protocol.NewUpstreamRestRequest("id", "GET#/v2/accounts/*", &protocol.RequestParams{
			PathParams:  []string{"a"},
			QueryParams: map[string][]string{"b": {"2"}, "a": {"1"}},
			Headers:     headers,
		}, []byte("body"), "").RequestHash()
	}

	noHeaders := mk(nil)
	withHeaders := mk(map[string][]string{"Authorization": {"Bearer x"}, "X-Trace": {"abc"}})

	assert.Equal(t, noHeaders, withHeaders, "headers must not affect the hash")
	assert.Equal(t, noHeaders, mk(nil), "identical inputs must hash identically")
}

func TestRestRequestHashEmptyForInternalRequest(t *testing.T) {
	// Internal requests bypass the cache and aren't subscribable, so - like
	// internal JSON-RPC requests - they carry no hash.
	req := protocol.NewInternalUpstreamRestRequest("GET#/v2/blocks/*",
		&protocol.RequestParams{PathParams: []string{"42"}}, chains.ALGORAND)

	assert.Empty(t, req.RequestHash())
}

func restHash(method string, rp *protocol.RequestParams, body []byte) string {
	return protocol.NewUpstreamRestRequest("id", method, rp, body, "").RequestHash()
}

func TestRestRequestHashDistinguishesMethodTemplate(t *testing.T) {
	rp := &protocol.RequestParams{PathParams: []string{"a"}}

	assert.NotEqual(t,
		restHash("GET#/v2/accounts/*", rp, nil),
		restHash("GET#/v2/assets/*", rp, nil),
		"different templates with the same captures must not collide")
}

func TestRestRequestHashEqualForIdenticalInputs(t *testing.T) {
	mk := func() string {
		return restHash("GET#/v2/blocks/*",
			&protocol.RequestParams{
				PathParams:  []string{"42"},
				QueryParams: map[string][]string{"header-only": {"true"}},
			}, []byte("body"))
	}

	assert.Equal(t, mk(), mk(), "two independent constructions with identical inputs must match")
}

func TestRestRequestHashEqualAcrossStreamAndUnary(t *testing.T) {
	rp := &protocol.RequestParams{PathParams: []string{"42"}}
	unary := protocol.NewUpstreamRestRequest("id", "GET#/v2/blocks/*", rp, nil, "")
	stream := protocol.NewStreamUpstreamRestRequest("id", "GET#/v2/blocks/*", rp, nil, "")

	assert.Equal(t, unary.RequestHash(), stream.RequestHash(),
		"streaming is a transport flag, not part of request identity")
}

func TestRestRequestHashQueryKeyOrderIndependent(t *testing.T) {
	// The map literal order differs but the canonical (sorted) encoding must not.
	a := restHash("GET#/v2/accounts/*", &protocol.RequestParams{
		QueryParams: map[string][]string{"a": {"1"}, "b": {"2"}, "c": {"3"}},
	}, nil)
	b := restHash("GET#/v2/accounts/*", &protocol.RequestParams{
		QueryParams: map[string][]string{"c": {"3"}, "b": {"2"}, "a": {"1"}},
	}, nil)

	assert.Equal(t, a, b)
}

func TestRestRequestHashPathParamOrderMatters(t *testing.T) {
	// Two wildcards: the capture order is semantically meaningful, so swapping
	// them must change the hash.
	assert.NotEqual(t,
		restHash("GET#/a/*/b/*", &protocol.RequestParams{PathParams: []string{"1", "2"}}, nil),
		restHash("GET#/a/*/b/*", &protocol.RequestParams{PathParams: []string{"2", "1"}}, nil))
}

func TestRestRequestHashNoSegmentBoundaryCollision(t *testing.T) {
	// The '\n' delimiter between method and captures prevents ambiguous merges:
	// method "GET#/a" + capture "b" must not hash like method "GET#/ab" with no
	// captures (or "GET#/a"+"" etc.).
	withCapture := restHash("GET#/a", &protocol.RequestParams{PathParams: []string{"b"}}, nil)
	merged := restHash("GET#/ab", nil, nil)
	concatenated := restHash("GET#/ab", &protocol.RequestParams{PathParams: []string{""}}, nil)

	assert.NotEqual(t, withCapture, merged)
	assert.NotEqual(t, withCapture, concatenated)
}

func TestRestRequestHashNilAndEmptyBodyEqual(t *testing.T) {
	// A nil body and an empty body carry the same (absent) payload identity.
	assert.Equal(t,
		restHash("POST#/wallet/getnodeinfo", nil, nil),
		restHash("POST#/wallet/getnodeinfo", nil, []byte{}))
}

func TestRestRequestHashBodyPresenceMatters(t *testing.T) {
	assert.NotEqual(t,
		restHash("POST#/wallet/getblock", nil, nil),
		restHash("POST#/wallet/getblock", nil, []byte(`{"num":1}`)))
}

func TestRestRequestHashQueryPresenceMatters(t *testing.T) {
	assert.NotEqual(t,
		restHash("GET#/v2/accounts/*", &protocol.RequestParams{PathParams: []string{"a"}}, nil),
		restHash("GET#/v2/accounts/*", &protocol.RequestParams{
			PathParams:  []string{"a"},
			QueryParams: map[string][]string{"format": {"json"}},
		}, nil))
}

func TestRestRequestHashNilParamsEqualsEmptyParams(t *testing.T) {
	// nil RequestParams and a zero-valued RequestParams describe the same
	// (no captures, no query) request.
	assert.Equal(t,
		restHash("GET#/v2/status", nil, nil),
		restHash("GET#/v2/status", &protocol.RequestParams{}, nil))
}
