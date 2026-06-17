package flow

import (
	"encoding/json"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
)

// SubFilter decides, per client, whether a fanned-out event should be delivered
// to that client. A shared local source (e.g. the chain's single all-logs
// stream) carries every event for all of its subscribers; each client's
// SubFilter drops the ones it did not subscribe to. resolveSource returns nil
// for sources that need no per-client filtering (newHeads, generic passthrough).
type SubFilter interface {
	Matches(message []byte) bool
}

// topicFilter is one position of an eth logs topic filter: any (the request had
// null at this position) matches anything; otherwise the log's topic at this
// position must be one of set.
type topicFilter struct {
	any bool
	set map[string]struct{}
}

// logFilter is a parsed eth_subscribe("logs", {...}) filter. The shared all-logs
// source carries every log of every block; each client applies its own logFilter
// in the processor so it sees only the logs it subscribed to. All hex values are
// compared lowercased.
type logFilter struct {
	addresses map[string]struct{} // empty => match any address
	topics    []topicFilter       // positional; empty => match any topics
}

var _ SubFilter = (*logFilter)(nil)

// rawLogFilter mirrors the eth logs filter object. address is a string or an
// array of strings; each topics entry is null, a string, or an array of strings.
type rawLogFilter struct {
	Address json.RawMessage   `json:"address"`
	Topics  []json.RawMessage `json:"topics"`
}

// parseLogFilter extracts the filter object (params[1]) from an
// eth_subscribe("logs", {...}) request. A missing object (`["logs"]`) yields a
// match-everything filter; a malformed object returns an error so the caller can
// fall back to the generic node-backed path.
func parseLogFilter(request protocol.RequestHolder) (*logFilter, error) {
	body, err := request.Body()
	if err != nil {
		return nil, err
	}
	filter := &logFilter{addresses: map[string]struct{}{}}

	node, err := sonic.Get(body, "params", 1)
	if err != nil {
		return filter, nil // no filter object - match everything
	}
	raw, err := node.Raw()
	if err != nil {
		return filter, nil
	}
	var rf rawLogFilter
	if err := sonic.UnmarshalString(raw, &rf); err != nil {
		return nil, err
	}

	for _, addr := range parseStringOrArray(rf.Address) {
		filter.addresses[strings.ToLower(addr)] = struct{}{}
	}
	for _, topic := range rf.Topics {
		tf := topicFilter{set: map[string]struct{}{}}
		trimmed := strings.TrimSpace(string(topic))
		if len(topic) == 0 || trimmed == "null" {
			tf.any = true
		} else if vals := parseStringOrArray(topic); len(vals) > 0 {
			for _, v := range vals {
				tf.set[strings.ToLower(v)] = struct{}{}
			}
		} else {
			tf.any = true
		}
		filter.topics = append(filter.topics, tf)
	}
	return filter, nil
}

// parseStringOrArray decodes a JSON value that is either a single string or an
// array of strings into a slice (nil for null/empty/other).
func parseStringOrArray(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if err := sonic.Unmarshal(raw, &single); err == nil {
		return []string{single}
	}
	var many []string
	if err := sonic.Unmarshal(raw, &many); err == nil {
		return many
	}
	return nil
}

// Matches reports whether a raw eth log object satisfies the filter. A log with
// fewer topics than the filter requires does not match (standard eth semantics).
func (f *logFilter) Matches(logRaw []byte) bool {
	if f == nil {
		return true
	}
	if len(f.addresses) > 0 {
		addrNode, err := sonic.Get(logRaw, "address")
		if err != nil {
			return false
		}
		addr, err := addrNode.String()
		if err != nil {
			return false
		}
		if _, ok := f.addresses[strings.ToLower(addr)]; !ok {
			return false
		}
	}
	for i, tf := range f.topics {
		if tf.any {
			continue
		}
		topicNode, err := sonic.Get(logRaw, "topics", i)
		if err != nil {
			return false // log has fewer topics than the filter requires
		}
		topic, err := topicNode.String()
		if err != nil {
			return false
		}
		if _, ok := tf.set[strings.ToLower(topic)]; !ok {
			return false
		}
	}
	return true
}
