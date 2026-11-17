package main

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

type skippedUpstream struct {
	id     string
	reason string
}

// note is one report line: a setting, what happened to it, and the upstreams it applies to.
type note struct {
	field   string
	message string
	scoped  bool // false for a file-level setting, which is never merged with scoped ones
	ids     []string
}

// report collects everything the conversion did not carry over as is. Nothing the converter
// leaves out may go unreported, so every rule that drops or alters a setting writes here.
type report struct {
	skipped  []skippedUpstream
	manuals  []*note
	dropped  []*note
	sections map[string]*note
}

// skip records an upstream that is left out of the output.
func (r *report) skip(upstreamId, reason string) {
	r.skipped = append(r.skipped, skippedUpstream{id: upstreamId, reason: reason})
}

// manual records a setting the operator has to act on. scope is an upstream id, or "" for a
// file-level setting.
func (r *report) manual(scope, field, message string) {
	r.manuals = r.add(r.manuals, "manual", scope, field, message)
}

// drop records a setting that has no place in the nodecore config.
func (r *report) drop(scope, field, message string) {
	r.dropped = r.add(r.dropped, "drop", scope, field, message)
}

// write prints the summary line and the non-empty groups, separated by one empty line.
func (r *report) write(w io.Writer, converted, total int, outPath string) {
	blocks := []string{fmt.Sprintf("converted %d of %d upstreams -> %s\n", converted, total, outPath)}

	if len(r.skipped) > 0 {
		var block strings.Builder
		block.WriteString("skipped upstreams:\n")
		for _, skipped := range r.skipped {
			fmt.Fprintf(&block, "  %s: %s\n", skipped.id, skipped.reason)
		}
		blocks = append(blocks, block.String())
	}
	if block := renderNotes("needs manual action:", r.manuals); block != "" {
		blocks = append(blocks, block)
	}
	if block := renderNotes("not carried over:", r.dropped); block != "" {
		blocks = append(blocks, block)
	}

	_, _ = io.WriteString(w, strings.Join(blocks, "\n"))
}

// add appends a line, or adds the upstream to an existing line with the same field and
// message, so a setting present on every upstream is reported once.
func (r *report) add(notes []*note, group, scope, field, message string) []*note {
	if r.sections == nil {
		r.sections = make(map[string]*note)
	}
	scoped := scope != ""
	key := fmt.Sprintf("%s\x00%t\x00%s\x00%s", group, scoped, field, message)
	if existing, ok := r.sections[key]; ok {
		if scoped && !slices.Contains(existing.ids, scope) {
			existing.ids = append(existing.ids, scope)
		}
		return notes
	}
	created := &note{field: field, message: message, scoped: scoped}
	if scoped {
		created.ids = []string{scope}
	}
	r.sections[key] = created
	return append(notes, created)
}

func renderNotes(heading string, notes []*note) string {
	if len(notes) == 0 {
		return ""
	}
	var block strings.Builder
	block.WriteString(heading + "\n")
	for _, item := range notes {
		if item.scoped {
			fmt.Fprintf(&block, "  %s (%s): %s\n", item.field, strings.Join(item.ids, ", "), item.message)
			continue
		}
		fmt.Fprintf(&block, "  %s: %s\n", item.field, item.message)
	}
	return block.String()
}
