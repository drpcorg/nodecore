package main

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const mergeKey = "<<"

// pair is one scalar entry of a mapping, with the value's literal text.
type pair struct{ key, value string }

type mappingEntry struct {
	key  string
	node *yaml.Node
}

type listItem struct {
	index int
	item  *section // nil when the item is not a mapping
}

// section is a YAML mapping whose key reads are recorded, so that everything the conversion
// rules never looked at can be listed afterwards. It is built on yaml.Node rather than on
// decoded values because scalars must keep their literal text: a label `version: 1.10` is the
// string "1.10", not the float 1.1.
//
// A getter marks its key read only when it returns a usable value. A key holding a value of
// the wrong shape therefore stays unread and surfaces through unread, instead of being lost.
type section struct {
	path    string
	fields  []mappingEntry
	index   map[string]*yaml.Node
	read    map[string]bool
	closed  map[string]bool // read together with everything under it
	subs    map[string]*section
	lists   map[string][]listItem
	present bool
}

// parseInput fails when data is not YAML or its top level is not a mapping.
func parseInput(data []byte) (*section, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New("the config is empty")
	}
	root := resolveAlias(doc.Content[0])
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("the top level of the config must be a mapping")
	}
	return newSection("", root), nil
}

// exists is false for the empty section sub returns on a missing or non-mapping key.
func (s *section) exists() bool {
	return s.present
}

// has reports presence only; it does not mark the key read.
func (s *section) has(key string) bool {
	_, ok := s.index[key]
	return ok
}

// str returns the literal text of a scalar.
func (s *section) str(key string) (string, bool) {
	node, ok := s.scalar(key)
	if !ok {
		return "", false
	}
	s.read[key] = true
	return node.Value, true
}

// boolean reads a flag the way dshackle does: a plain scalar is true when its text is "true"
// in any letter case and false otherwise (`enabled: no` is false), while a quoted scalar is
// not a flag at all.
func (s *section) boolean(key string) (bool, bool) {
	node, ok := s.scalar(key)
	if !ok || !isPlain(node) {
		return false, false
	}
	s.read[key] = true
	return strings.EqualFold(node.Value, "true"), true
}

// integer reads a number from a plain scalar; dshackle ignores a quoted one.
func (s *section) integer(key string) (int64, bool) {
	node, ok := s.scalar(key)
	if !ok || !isPlain(node) {
		return 0, false
	}
	value, err := strconv.ParseInt(strings.TrimSpace(node.Value), 10, 64)
	if err != nil {
		return 0, false
	}
	s.read[key] = true
	return value, true
}

// sub returns the mapping under key. It never returns nil: a missing key or a value that is
// not a mapping gives an empty section, on which every getter reports "absent".
func (s *section) sub(key string) *section {
	if existing, ok := s.subs[key]; ok {
		return existing
	}
	node, ok := s.index[key]
	if !ok || node.Kind != yaml.MappingNode {
		return newSection(childPath(s.path, key), nil)
	}
	s.read[key] = true
	child := newSection(childPath(s.path, key), node)
	s.subs[key] = child
	return child
}

// list returns the mapping items of a sequence. Items of another shape are skipped here and
// reported by unread.
func (s *section) list(key string) []*section {
	items, ok := s.lists[key]
	if !ok {
		node, found := s.index[key]
		if !found || node.Kind != yaml.SequenceNode {
			return nil
		}
		s.read[key] = true
		base := childPath(s.path, key)
		for i, raw := range node.Content {
			item := listItem{index: i}
			if resolved := resolveAlias(raw); resolved.Kind == yaml.MappingNode {
				item.item = newSection(fmt.Sprintf("%s[%d]", base, i), resolved)
			}
			items = append(items, item)
		}
		s.lists[key] = items
	}
	sections := make([]*section, 0, len(items))
	for _, item := range items {
		if item.item != nil {
			sections = append(sections, item.item)
		}
	}
	return sections
}

// strings returns the scalar items of a sequence; a lone scalar gives one item.
func (s *section) strings(key string) []string {
	node, ok := s.index[key]
	if !ok {
		return nil
	}
	switch {
	case isValueScalar(node):
		s.close(key)
		return []string{node.Value}
	case node.Kind == yaml.SequenceNode:
		s.read[key] = true
		values := make([]string, 0, len(node.Content))
		skipped := make([]listItem, 0)
		for i, raw := range node.Content {
			if item := resolveAlias(raw); isValueScalar(item) {
				values = append(values, item.Value)
				continue
			}
			// left for unread to report: an item that is not a scalar is not carried over
			skipped = append(skipped, listItem{index: i})
		}
		s.lists[key] = skipped
		return values
	}
	return nil
}

// pairs returns the scalar entries of a mapping in input order.
func (s *section) pairs(key string) []pair {
	child := s.sub(key)
	if !child.exists() {
		return nil
	}
	entries := make([]pair, 0, len(child.fields))
	for _, entry := range child.fields {
		// an entry that is not a scalar stays unread and is reported by unread
		if isValueScalar(entry.node) {
			child.read[entry.key] = true
			entries = append(entries, pair{key: entry.key, value: entry.node.Value})
		}
	}
	return entries
}

// keys lists the keys of the section in input order; it does not mark them read.
func (s *section) keys() []string {
	names := make([]string, 0, len(s.fields))
	for _, entry := range s.fields {
		names = append(names, entry.key)
	}
	return names
}

// ignore marks the key and everything under it read.
func (s *section) ignore(key string) {
	if s.has(key) {
		s.close(key)
	}
}

// ignoreAll marks every key of the section read.
func (s *section) ignoreAll() {
	for _, entry := range s.fields {
		s.close(entry.key)
	}
}

// unread lists the full paths of the keys no getter consumed, depth-first in input order. An
// unread key is reported once, by its own path, without descending into it.
func (s *section) unread() []string {
	paths := make([]string, 0)
	for _, entry := range s.fields {
		keyPath := childPath(s.path, entry.key)
		switch {
		case !s.read[entry.key]:
			paths = append(paths, keyPath)
		case s.closed[entry.key]:
		case s.subs[entry.key] != nil:
			paths = append(paths, s.subs[entry.key].unread()...)
		default:
			for _, item := range s.lists[entry.key] {
				if item.item == nil {
					paths = append(paths, fmt.Sprintf("%s[%d]", keyPath, item.index))
					continue
				}
				paths = append(paths, item.item.unread()...)
			}
		}
	}
	return paths
}

func (s *section) scalar(key string) (*yaml.Node, bool) {
	node, ok := s.index[key]
	if !ok || !isValueScalar(node) {
		return nil, false
	}
	return node, true
}

func (s *section) close(key string) {
	s.read[key] = true
	s.closed[key] = true
}

func newSection(path string, mapping *yaml.Node) *section {
	created := &section{
		path:    path,
		index:   make(map[string]*yaml.Node),
		read:    make(map[string]bool),
		closed:  make(map[string]bool),
		subs:    make(map[string]*section),
		lists:   make(map[string][]listItem),
		present: mapping != nil,
	}
	if mapping != nil {
		created.fields = mappingFields(mapping, nil)
		for _, entry := range created.fields {
			created.index[entry.key] = entry.node
		}
	}
	return created
}

// mappingFields returns the entries of a mapping with aliases followed and merge keys
// expanded. A key written directly in the mapping wins over a merged one, and among several
// merged mappings the first one wins, as the YAML merge type defines.
//
// visiting holds the mappings being expanded further up the call chain: a mapping that merges
// itself, directly or through another one, is expanded once instead of forever.
func mappingFields(mapping *yaml.Node, visiting []*yaml.Node) []mappingEntry {
	if slices.Contains(visiting, mapping) {
		return nil
	}
	visiting = append(visiting, mapping)
	direct := make(map[string]bool)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if key := mapping.Content[i]; !isMergeKey(key) {
			direct[key.Value] = true
		}
	}

	entries := make([]mappingEntry, 0, len(mapping.Content)/2)
	seen := make(map[string]bool)
	add := func(entry mappingEntry) {
		if !seen[entry.key] {
			seen[entry.key] = true
			entries = append(entries, entry)
		}
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], resolveAlias(mapping.Content[i+1])
		if !isMergeKey(key) {
			add(mappingEntry{key: key.Value, node: value})
			continue
		}
		for _, merged := range mergedMappings(value) {
			for _, entry := range mappingFields(merged, visiting) {
				if !direct[entry.key] {
					add(entry)
				}
			}
		}
	}
	return entries
}

func mergedMappings(value *yaml.Node) []*yaml.Node {
	switch value.Kind {
	case yaml.MappingNode:
		return []*yaml.Node{value}
	case yaml.SequenceNode:
		mappings := make([]*yaml.Node, 0, len(value.Content))
		for _, raw := range value.Content {
			if item := resolveAlias(raw); item.Kind == yaml.MappingNode {
				mappings = append(mappings, item)
			}
		}
		return mappings
	}
	return nil
}

func isMergeKey(key *yaml.Node) bool {
	return key.Tag == "!!merge" || (key.Value == mergeKey && key.Style == 0 && key.Tag == "")
}

// isValueScalar is true for a scalar that carries a value; a YAML null (`key:` or `key: ~`)
// does not.
func isValueScalar(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.Tag != "!!null"
}

// isPlain is true for an unquoted scalar; dshackle takes flags and numbers from those only.
func isPlain(node *yaml.Node) bool {
	return node.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) == 0
}

func resolveAlias(node *yaml.Node) *yaml.Node {
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	return node
}

func childPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}
