package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSectionLiteralScalars(t *testing.T) {
	s := mustSection(t, "labels:\n  version: 1.10\n  archive: true\nport: 2449\n")
	assert.Equal(t, []pair{{"version", "1.10"}, {"archive", "true"}}, s.pairs("labels"))
	port, ok := s.integer("port")
	assert.True(t, ok)
	assert.Equal(t, int64(2449), port)
}

func TestSectionMergeKeysAndAliases(t *testing.T) {
	s := mustSection(t, `
common: &common
  chain: ethereum
  role: fallback
url: &u http://localhost:8545
cluster:
  upstreams:
    - <<: *common
      id: eth-1
      role: primary
      link: *u
`)
	up := s.sub("cluster").list("upstreams")[0]
	chain, _ := up.str("chain")
	role, _ := up.str("role")
	link, _ := up.str("link")
	assert.Equal(t, "ethereum", chain)
	assert.Equal(t, "primary", role) // the direct key wins over the merged one
	assert.Equal(t, "http://localhost:8545", link)
}

func TestSectionUnread(t *testing.T) {
	s := mustSection(t, `
port: 2449
proxy:
  port: 8080
cluster:
  upstreams:
    - id: a
      node-id: 1
      options:
        min-peers: ten
    - just-a-string
`)
	s.integer("port")
	ups := s.sub("cluster").list("upstreams")
	require.Len(t, ups, 1)
	ups[0].str("id")
	_, ok := ups[0].sub("options").integer("min-peers")
	assert.False(t, ok)
	assert.Equal(t, []string{
		"proxy",
		"cluster.upstreams[0].node-id",
		"cluster.upstreams[0].options.min-peers",
		"cluster.upstreams[1]",
	}, s.unread())
}

func TestSectionMissingAndWrongShape(t *testing.T) {
	s := mustSection(t, "host: 0.0.0.0\nenabled: \"yes\"\ninclude: extra.yaml\n")
	assert.False(t, s.sub("tls").exists())
	assert.Empty(t, s.sub("tls").list("x"))
	_, ok := s.sub("host").str("x") // sub on a scalar
	assert.False(t, ok)
	_, ok = s.boolean("enabled")
	assert.False(t, ok)
	assert.Equal(t, []string{"extra.yaml"}, s.strings("include"))
	s.ignore("host")
	assert.Equal(t, []string{"enabled"}, s.unread())
}

func TestParseInputErrors(t *testing.T) {
	_, err := parseInput([]byte("- a\n- b\n"))
	assert.Error(t, err)
	_, err = parseInput([]byte("key: [unclosed"))
	assert.Error(t, err)
}

// dshackle reads a plain scalar other than "true" as false and ignores a quoted one; numbers
// are taken from plain scalars only.
func TestSectionTypedScalarsFollowDshackle(t *testing.T) {
	s := mustSection(t, "a: yes\nb: TRUE\nc: \"true\"\nd: 0\nport: \"2450\"\nsize: 7\n")
	a, ok := s.boolean("a")
	assert.True(t, ok)
	assert.False(t, a)
	b, ok := s.boolean("b")
	assert.True(t, ok)
	assert.True(t, b)
	_, ok = s.boolean("c")
	assert.False(t, ok)
	d, ok := s.boolean("d")
	assert.True(t, ok)
	assert.False(t, d)
	_, ok = s.integer("port")
	assert.False(t, ok)
	size, ok := s.integer("size")
	assert.True(t, ok)
	assert.Equal(t, int64(7), size)
	assert.Equal(t, []string{"c", "port"}, s.unread())
}

func TestSectionReportsNonScalarItems(t *testing.T) {
	s := mustSection(t, "labels:\n  a: [1, 2]\n  b: x\n  c: {d: e}\nchains:\n  - eth\n  - {a: b}\n")
	assert.Equal(t, []pair{{"b", "x"}}, s.pairs("labels"))
	assert.Equal(t, []string{"eth"}, s.strings("chains"))
	assert.Equal(t, []string{"labels.a", "labels.c", "chains[1]"}, s.unread())
}

func TestSectionSelfReferencingMerge(t *testing.T) {
	s := mustSection(t, "a: &a\n  <<: *a\n  id: x\n")
	id, ok := s.sub("a").str("id")
	assert.True(t, ok)
	assert.Equal(t, "x", id)
}
