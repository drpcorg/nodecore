package protocol

import "errors"

const UpstreamIdLabel = "upstream_id"

// UpstreamID returns the single upstream selected by this request. Pins may be
// repeated under AND if they agree; empty, multi-ID, OR and NOT pins are invalid.
func UpstreamID(selectors []RequestSelector) (string, error) {
	var id string
	var visit func([]RequestSelector, bool) error
	visit = func(selectors []RequestSelector, conjunctive bool) error {
		for _, selector := range selectors {
			switch s := selector.(type) {
			case RequestLabelSelector:
				if s.Name != UpstreamIdLabel {
					continue
				}
				if !conjunctive || len(s.Values) != 1 || s.Values[0] == "" {
					return errors.New("upstream_id must select exactly one nonempty ID at the top level or under AND")
				}
				if id != "" && id != s.Values[0] {
					return errors.New("conflicting upstream_id selectors")
				}
				id = s.Values[0]
			case RequestAndSelector:
				if err := visit(s.Children, conjunctive); err != nil {
					return err
				}
			case RequestOrSelector:
				if err := visit(s.Children, false); err != nil {
					return err
				}
			case RequestNotSelector:
				if err := visit([]RequestSelector{s.Child}, false); err != nil {
					return err
				}
			}
		}
		return nil
	}
	err := visit(selectors, true)
	return id, err
}
