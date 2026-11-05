package protocol_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestUpstreamIDIsSingleAndConjunctive(t *testing.T) {
	pin := func(ids ...string) protocol.RequestSelector {
		return protocol.RequestLabelSelector{Name: protocol.UpstreamIdLabel, Values: ids}
	}
	for _, tc := range []struct {
		name      string
		selectors []protocol.RequestSelector
		id        string
		invalid   bool
	}{
		{"none", nil, "", false},
		{"one", []protocol.RequestSelector{pin("a")}, "a", false},
		{"repeated", []protocol.RequestSelector{pin("a"), protocol.RequestAndSelector{Children: []protocol.RequestSelector{pin("a")}}}, "a", false},
		{"empty", []protocol.RequestSelector{pin()}, "", true},
		{"empty ID", []protocol.RequestSelector{pin("")}, "", true},
		{"multiple", []protocol.RequestSelector{pin("a", "b")}, "", true},
		{"conflicting", []protocol.RequestSelector{pin("a"), protocol.RequestAndSelector{Children: []protocol.RequestSelector{pin("b")}}}, "", true},
		{"OR", []protocol.RequestSelector{protocol.RequestOrSelector{Children: []protocol.RequestSelector{pin("a")}}}, "", true},
		{"NOT", []protocol.RequestSelector{protocol.RequestNotSelector{Child: pin("a")}}, "", true},
		{"AND inside OR", []protocol.RequestSelector{protocol.RequestOrSelector{Children: []protocol.RequestSelector{protocol.RequestAndSelector{Children: []protocol.RequestSelector{pin("a")}}}}}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := protocol.UpstreamID(tc.selectors)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.id, id)
			}
		})
	}
}
