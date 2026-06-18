package flow

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
)

// SubFilter decides, per client, whether a fanned-out event should be delivered
// to that client. A shared local source (e.g. the chain's single all-logs
// stream) carries every event for all of its subscribers; each client's
// SubFilter drops the ones it did not subscribe to. resolveSource returns nil
// for sources that need no per-client filtering (newHeads, generic passthrough).
//
// It receives the source-attached protocol.ParsedEvent (parsed once per event in
// the source) so it can match without re-parsing the raw JSON for every
// subscriber.
type SubFilter interface {
	Matches(event protocol.ParsedEvent) bool
}

type parsedLog struct {
	raw     json.RawMessage
	address string
	topics  []string
}

var _ protocol.ParsedEvent = (*parsedLog)(nil)

func (p *parsedLog) Raw() []byte { return p.raw }

func parseLogEvent(raw json.RawMessage) *parsedLog {
	var rl struct {
		Address string   `json:"address"`
		Topics  []string `json:"topics"`
	}
	_ = sonic.Unmarshal(raw, &rl) // best-effort; missing fields stay zero
	pl := &parsedLog{raw: raw, address: strings.ToLower(rl.Address), topics: make([]string, len(rl.Topics))}
	for i, t := range rl.Topics {
		pl.topics[i] = strings.ToLower(t)
	}
	return pl
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

	// A present-but-malformed address would otherwise leave the address set empty
	// and silently match EVERY address (the firehose). Reject it so the caller
	// falls back to the generic node-backed path, where the node validates the
	// filter itself.
	addrs, ok := parseStringOrArray(rf.Address)
	if !ok {
		return nil, errors.New("malformed address in logs filter")
	}
	for _, addr := range addrs {
		filter.addresses[strings.ToLower(addr)] = struct{}{}
	}
	for _, topic := range rf.Topics {
		tf := topicFilter{set: map[string]struct{}{}}
		trimmed := strings.TrimSpace(string(topic))
		if len(topic) == 0 || trimmed == "null" {
			tf.any = true
		} else if vals, ok := parseStringOrArray(topic); ok && len(vals) > 0 {
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
// array of strings into a slice. The bool distinguishes a usable value (absent,
// or a well-formed string/array - ok=true) from a malformed one (present but
// neither a string nor an array of strings - ok=false), so the caller can fall
// back rather than silently widening the match.
func parseStringOrArray(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 {
		return nil, true // absent
	}
	var single string
	if err := sonic.Unmarshal(raw, &single); err == nil {
		return []string{single}, true
	}
	var many []string
	if err := sonic.Unmarshal(raw, &many); err == nil {
		return many, true
	}
	return nil, false // malformed
}

// Matches reports whether the parsed eth log satisfies the filter. It reads the
// pre-parsed view (parsed once in the source) instead of re-walking the raw JSON
// per subscriber. A log with fewer topics than the filter requires does not match
// (standard eth semantics).
func (f *logFilter) Matches(event protocol.ParsedEvent) bool {
	if f == nil {
		return true
	}
	if event == nil {
		return false // contract violation: a logs source must attach a ParsedEvent
	}
	pl, ok := event.(*parsedLog)
	if !ok {
		pl = parseLogEvent(event.Raw()) // defensive: unknown parsed type; never hit for logs
	}
	if len(f.addresses) > 0 {
		if _, ok := f.addresses[pl.address]; !ok {
			return false
		}
	}
	for i, tf := range f.topics {
		if tf.any {
			continue
		}
		if i >= len(pl.topics) {
			return false // log has fewer topics than the filter requires
		}
		if _, ok := tf.set[pl.topics[i]]; !ok {
			return false
		}
	}
	return true
}
