package protocol_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
)

// Constructor helpers keep the selector trees in the tables readable.

func label(name string, values ...string) protocol.RequestLabelSelector {
	return protocol.RequestLabelSelector{Name: name, Values: values}
}

func exists(name string) protocol.RequestExistsSelector {
	return protocol.RequestExistsSelector{Name: name}
}

func and(children ...protocol.RequestSelector) protocol.RequestAndSelector {
	return protocol.RequestAndSelector{Children: children}
}

func or(children ...protocol.RequestSelector) protocol.RequestOrSelector {
	return protocol.RequestOrSelector{Children: children}
}

func not(child protocol.RequestSelector) protocol.RequestNotSelector {
	return protocol.RequestNotSelector{Child: child}
}

func sel(selectors ...protocol.RequestSelector) []protocol.RequestSelector {
	return selectors
}

// TestLabelCacheKey exhaustively pins the node-class reduction for every
// selector type, every combinator, and their nestings. Non-label selectors are
// ⊤ (admit every class) and must drop out; only label/exists leaves and the
// boolean structure connecting them survive.
func TestLabelCacheKey(t *testing.T) {
	tests := []struct {
		name      string
		selectors []protocol.RequestSelector
		expected  string
	}{
		// --- nothing / unconstrained leaves ---
		{
			name:      "no selectors",
			selectors: nil,
			expected:  "",
		},
		{
			name:      "empty slice",
			selectors: sel(),
			expected:  "",
		},
		{
			name:      "any selector",
			selectors: sel(protocol.RequestAnySelector{}),
			expected:  "",
		},
		{
			name:      "height selector",
			selectors: sel(protocol.RequestHeightSelector{Height: 100}),
			expected:  "",
		},
		{
			name:      "slot height selector",
			selectors: sel(protocol.RequestSlotHeightSelector{SlotHeight: 7}),
			expected:  "",
		},
		{
			name:      "block tag latest",
			selectors: sel(protocol.RequestBlockTagSelector{Tag: protocol.BlockTagLatest}),
			expected:  "",
		},
		{
			name:      "block tag finalized",
			selectors: sel(protocol.RequestBlockTagSelector{Tag: protocol.BlockTagFinalized}),
			expected:  "",
		},
		{
			name:      "lower height with explicit height",
			selectors: sel(protocol.RequestLowerHeightSelector{Height: 500, TimeOffset: 1, HeightDelta: 2}),
			expected:  "",
		},
		{
			name:      "predicted lower bound",
			selectors: sel(protocol.RequestLowerHeightSelector{Height: 0, TimeOffset: 42}),
			expected:  "",
		},
		{
			name:      "unsupported selector",
			selectors: sel(protocol.RequestUnsupportedSelector{Reason: "boom"}),
			expected:  "",
		},
		{
			name:      "several non-label selectors stay unconstrained",
			selectors: sel(protocol.RequestHeightSelector{Height: 1}, protocol.RequestBlockTagSelector{Tag: protocol.BlockTagSafe}, protocol.RequestAnySelector{}),
			expected:  "",
		},

		// --- label / exists leaves ---
		{
			name:      "single label single value",
			selectors: sel(label("type", "a")),
			expected:  "label(type=a)",
		},
		{
			name:      "single label multiple values sorted",
			selectors: sel(label("type", "b", "a", "c")),
			expected:  "label(type=a|b|c)",
		},
		{
			name:      "label without values",
			selectors: sel(label("type")),
			expected:  "label(type=)",
		},
		{
			name:      "exists selector",
			selectors: sel(exists("archive")),
			expected:  "exists(archive)",
		},

		// --- implicit top-level AND (multiple selectors) ---
		{
			name:      "label and height drops height",
			selectors: sel(label("type", "a"), protocol.RequestHeightSelector{Height: 100}),
			expected:  "label(type=a)",
		},
		{
			name:      "two labels become an AND sorted",
			selectors: sel(label("type", "a"), label("region", "eu")),
			expected:  "and(label(region=eu)|label(type=a))",
		},
		{
			name:      "label and exists become an AND sorted",
			selectors: sel(label("type", "a"), exists("archive")),
			expected:  "and(exists(archive)|label(type=a))",
		},

		// --- explicit AND ---
		{
			name:      "AND of a single label",
			selectors: sel(and(label("type", "a"))),
			expected:  "label(type=a)",
		},
		{
			name:      "AND with empty children",
			selectors: sel(and()),
			expected:  "",
		},
		{
			name:      "AND of label and lower bound drops the lower bound",
			selectors: sel(and(label("type", "a"), protocol.RequestLowerHeightSelector{Height: 0})),
			expected:  "label(type=a)",
		},
		{
			name:      "AND of only non-label selectors",
			selectors: sel(and(protocol.RequestHeightSelector{Height: 1}, protocol.RequestAnySelector{})),
			expected:  "",
		},
		{
			name:      "AND keeps duplicate labels (no canonicalization)",
			selectors: sel(and(label("type", "a"), label("type", "a"))),
			expected:  "and(label(type=a)|label(type=a))",
		},

		// --- explicit OR ---
		{
			name:      "OR of two labels keeps both",
			selectors: sel(or(label("type", "a"), label("type", "b"))),
			expected:  "or(label(type=a)|label(type=b))",
		},
		{
			name:      "OR of a single label",
			selectors: sel(or(label("type", "a"))),
			expected:  "label(type=a)",
		},
		{
			name:      "OR with a non-label branch collapses to unconstrained",
			selectors: sel(or(label("type", "a"), protocol.RequestHeightSelector{Height: 100})),
			expected:  "",
		},
		{
			name:      "OR with empty children",
			selectors: sel(or()),
			expected:  "",
		},
		{
			name:      "OR of label and exists",
			selectors: sel(or(label("type", "a"), exists("archive"))),
			expected:  "or(exists(archive)|label(type=a))",
		},

		// --- NOT ---
		{
			name:      "NOT of a label",
			selectors: sel(not(label("type", "a"))),
			expected:  "not(label(type=a))",
		},
		{
			name:      "NOT of an exists",
			selectors: sel(not(exists("archive"))),
			expected:  "not(exists(archive))",
		},
		{
			name:      "NOT of a non-label branch is unconstrained",
			selectors: sel(not(protocol.RequestHeightSelector{Height: 100})),
			expected:  "",
		},
		{
			name:      "double NOT of a label",
			selectors: sel(not(not(label("type", "a")))),
			expected:  "not(not(label(type=a)))",
		},
		{
			name:      "NOT of an AND",
			selectors: sel(not(and(label("type", "a"), label("type", "b")))),
			expected:  "not(and(label(type=a)|label(type=b)))",
		},
		{
			name:      "NOT of an OR",
			selectors: sel(not(or(label("type", "a"), label("type", "b")))),
			expected:  "not(or(label(type=a)|label(type=b)))",
		},

		// --- nesting across combinators ---
		{
			name:      "AND with a collapsing OR keeps the surviving label",
			selectors: sel(and(label("type", "c"), or(label("type", "a"), protocol.RequestHeightSelector{Height: 100}))),
			expected:  "label(type=c)",
		},
		{
			name:      "AND of an OR and a label",
			selectors: sel(and(or(label("type", "a"), label("type", "b")), label("type", "c"))),
			expected:  "and(label(type=c)|or(label(type=a)|label(type=b)))",
		},
		{
			name:      "OR of an AND and a label",
			selectors: sel(or(and(label("type", "a"), label("type", "b")), label("type", "c"))),
			expected:  "or(and(label(type=a)|label(type=b))|label(type=c))",
		},
		{
			name: "AND with an inner AND that drops its height",
			// The inner AND keeps a single surviving label, so it collapses to that
			// label (no and() wrapper) before the outer AND folds it in.
			selectors: sel(and(label("region", "eu"), and(label("type", "a"), protocol.RequestHeightSelector{Height: 9}))),
			expected:  "and(label(region=eu)|label(type=a))",
		},
		{
			name:      "NOT inside AND",
			selectors: sel(and(label("type", "a"), not(label("region", "eu")))),
			expected:  "and(label(type=a)|not(label(region=eu)))",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, protocol.LabelCacheKey(tt.selectors))
		})
	}
}

func TestLabelCacheKeyDistinguishesClasses(t *testing.T) {
	a := protocol.LabelCacheKey(sel(label("type", "a")))
	b := protocol.LabelCacheKey(sel(label("type", "b")))

	assert.NotEqual(t, a, b)
	assert.NotEmpty(t, a)
	assert.NotEmpty(t, b)
}

func TestLabelCacheKeyDistinguishesAndOrNot(t *testing.T) {
	children := sel(label("type", "a"), label("type", "b"))
	andKey := protocol.LabelCacheKey(sel(and(children...)))
	orKey := protocol.LabelCacheKey(sel(or(children...)))
	notAndKey := protocol.LabelCacheKey(sel(not(and(children...))))

	keys := []string{andKey, orKey, notAndKey}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			assert.NotEqualf(t, keys[i], keys[j], "keys %d and %d must differ", i, j)
		}
	}
}

func TestLabelCacheKeyExistsDiffersFromLabel(t *testing.T) {
	// exists(name) and label(name=...) are different constraints on the same key.
	assert.NotEqual(t,
		protocol.LabelCacheKey(sel(exists("type"))),
		protocol.LabelCacheKey(sel(label("type", "a"))),
	)
}

func TestLabelCacheKeyOrderIndependent(t *testing.T) {
	t.Run("top-level selectors", func(t *testing.T) {
		forward := protocol.LabelCacheKey(sel(label("type", "a"), label("region", "eu")))
		reversed := protocol.LabelCacheKey(sel(label("region", "eu"), label("type", "a")))
		assert.Equal(t, forward, reversed)
	})

	t.Run("AND children", func(t *testing.T) {
		forward := protocol.LabelCacheKey(sel(and(label("type", "a"), label("region", "eu"))))
		reversed := protocol.LabelCacheKey(sel(and(label("region", "eu"), label("type", "a"))))
		assert.Equal(t, forward, reversed)
	})

	t.Run("OR children", func(t *testing.T) {
		forward := protocol.LabelCacheKey(sel(or(label("type", "a"), label("type", "b"))))
		reversed := protocol.LabelCacheKey(sel(or(label("type", "b"), label("type", "a"))))
		assert.Equal(t, forward, reversed)
	})

	t.Run("label values", func(t *testing.T) {
		forward := protocol.LabelCacheKey(sel(label("type", "a", "b", "c")))
		reversed := protocol.LabelCacheKey(sel(label("type", "c", "b", "a")))
		assert.Equal(t, forward, reversed)
	})

	t.Run("deeply nested children", func(t *testing.T) {
		// Reorder children of an AND that sits two levels down (inside an OR,
		// inside the top-level AND) along with the values of a nested label, and
		// the key must be unchanged - sorting applies at every level, not just
		// the top.
		forward := protocol.LabelCacheKey(sel(and(
			or(
				and(label("type", "a"), label("region", "eu")),
				label("tier", "x", "y"),
			),
			not(label("zone", "z")),
		)))
		reordered := protocol.LabelCacheKey(sel(and(
			not(label("zone", "z")),
			or(
				label("tier", "y", "x"),
				and(label("region", "eu"), label("type", "a")),
			),
		)))
		assert.Equal(t, forward, reordered)
		assert.NotEmpty(t, forward)
	})
}

func TestLabelCacheKeyDeterministic(t *testing.T) {
	selectors := sel(and(or(label("type", "a"), label("type", "b")), not(label("region", "eu")), exists("archive")))

	first := protocol.LabelCacheKey(selectors)
	for i := 0; i < 10; i++ {
		assert.Equal(t, first, protocol.LabelCacheKey(selectors))
	}
}

func TestRequestHashCarriesLabelKey(t *testing.T) {
	body := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getBalance", Params: []byte(`["0x1","0x2"]`)}

	noLabel := protocol.NewUpstreamJsonRpcRequest("1", body, false, "eth")
	classA := protocol.NewUpstreamJsonRpcRequest("1", body, false, "eth", label("type", "a"))
	classB := protocol.NewUpstreamJsonRpcRequest("1", body, false, "eth", label("type", "b"))

	// The same method+params resolves to a distinct hash per class, so a response
	// cached for one class can never be served to a request that pinned another.
	assert.NotEqual(t, noLabel.RequestHash(), classA.RequestHash())
	assert.NotEqual(t, noLabel.RequestHash(), classB.RequestHash())
	assert.NotEqual(t, classA.RequestHash(), classB.RequestHash())

	// A height-only selector carries no class constraint, so the hash is unchanged.
	heightOnly := protocol.NewUpstreamJsonRpcRequest("1", body, false, "eth", protocol.RequestHeightSelector{Height: 100})
	assert.Equal(t, noLabel.RequestHash(), heightOnly.RequestHash())
}
