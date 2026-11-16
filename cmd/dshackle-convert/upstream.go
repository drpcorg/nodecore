package main

import (
	"encoding/base64"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
)

const (
	authorizationHeader = "Authorization"
	originHeader        = "Origin"
	websocketType       = "websocket"
	restIndexerType     = "rest-indexer"
	restAdditionalType  = "rest-additional"
	tonIndexerTag       = "ton_v3"
	tonIndexerSuffix    = "-v3"

	modeRpcOnly   = "RPC_ONLY"
	modeWsOnly    = "WS_ONLY"
	modeMixedHead = "RPC_REQUESTS_WITH_MIXED_HEAD"
	modeWsHead    = "RPC_REQUESTS_WITH_WS_HEAD"

	placeholderMessage = "contains a ${VAR} placeholder; nodecore does not substitute environment variables, replace it with the value"
)

// placeholderPattern is the `${NAME}` form dshackle substitutes from a JVM property or an
// environment variable in every string value. nodecore has no such substitution.
var placeholderPattern = regexp.MustCompile(`\$\{(\w+?)}`)

// endpoint is one dshackle `rpc` or `ws` block, read into what a nodecore connector needs.
type endpoint struct {
	url     string
	headers map[string]string // Authorization and Origin, when the block sets them
	ca      string
	ownAuth bool // the block has its own basic-auth or bearer-auth, written as a header or not
}

func hasPlaceholder(value string) bool {
	return placeholderPattern.MatchString(value)
}

// convertUpstream converts entry `index` of cluster.upstreams. It returns one upstream, or
// two for a TON upstream with a ton_v3 endpoint (the v2 one first). It returns nil after
// calling r.skip when the entry is skipped. The scope used for the report is the upstream's
// id, or "#<index>" when it has none.
func convertUpstream(up *section, index int, r *report) []*config.Upstream {
	id, _ := up.str("id")
	if id == "" {
		return skipUpstream(up, r, fmt.Sprintf("#%d", index), "no id")
	}
	if enabled, ok := up.boolean("enabled"); ok && !enabled {
		return skipUpstream(up, r, id, "disabled in the dshackle config (enabled: false)")
	}
	connection := up.sub("connection")
	// dshackle looks for a node connection first; grpc counts only when there is none
	if connection.has("grpc") && !hasNodeConnection(connection) {
		return skipUpstream(up, r, id, "dshackle-to-dshackle upstreams (connection.grpc) are not supported")
	}
	chainName, _ := up.str("chain")
	canonical, chain, ok := resolveChain(chainName)
	if !ok {
		return skipUpstream(up, r, id, fmt.Sprintf("chain '%s' is not served by nodecore", chainName))
	}
	block, blockField, bitcoinBlock := connectionBlock(connection, id, r)
	rpc := block.sub("rpc")
	hasRpc := rpc.has("url")
	// dshackle never reads `ws` or `connector-mode` from a bitcoin connection; they stay
	// unread and are reported by the catch-all
	hasWs := !bitcoinBlock && block.sub("ws").has("url")
	if !hasRpc && !hasWs {
		return skipUpstream(up, r, id, "no rpc.url or ws.url")
	}

	custom := customHeaders(up, id, r)
	primary := &config.Upstream{
		Id:          id,
		ChainName:   canonical,
		Labels:      convertLabels(up, id, r),
		Options:     convertOptions(up.sub("options"), id, "options.", r),
		Methods:     convertMethods(up, id, r),
		LowerBounds: convertLowerBounds(up, id, r),
	}
	reportUnsupportedFields(up, id, r)

	emitWs := hasWs
	if !bitcoinBlock {
		emitWs = applyConnectorMode(block, blockField, hasRpc, hasWs, id, r)
	}
	if hasRpc {
		if node, found := readRpc(rpc, id, blockField+".rpc", r); found {
			primary.Connectors = append(primary.Connectors, newConnector(primaryConnectorType(chain.Type), node, custom, id, r))
		}
	}
	if emitWs {
		if socket, found := readWs(block.sub("ws"), id, blockField+".ws", r); found {
			primary.Connectors = append(primary.Connectors, newConnector(websocketType, socket, custom, id, r))
		}
	}
	if len(primary.Connectors) == 0 {
		return skipUpstream(up, r, id, "no rpc.url or ws.url")
	}
	if bitcoinBlock {
		if esplora := esploraConnector(block, blockField, chain, primary, r); esplora != nil {
			primary.Connectors = append(primary.Connectors, esplora)
		}
	}

	applyMethodGroups(up, primary, chain, r)

	upstreams := []*config.Upstream{primary}
	if !bitcoinBlock {
		if indexer := tonIndexer(block, blockField, chain, primary, custom, r); indexer != nil {
			upstreams = append(upstreams, indexer)
		}
	}
	return upstreams
}

func hasNodeConnection(connection *section) bool {
	return connection.has("generic") || connection.has("ethereum") || connection.has("ethereum-pos") || connection.has("bitcoin")
}

func skipUpstream(up *section, r *report, scope, reason string) []*config.Upstream {
	r.skip(scope, reason)
	up.ignoreAll()
	return nil
}

// connectionBlock returns the block that holds `rpc` and `ws`, and its report field. dshackle
// accepts four spellings; the first present one wins, as in its reader. bitcoinBlock is true
// for a bitcoin connection, which has `esplora` and `zeromq` next to `rpc` and no `ws`.
func connectionBlock(connection *section, scope string, r *report) (block *section, blockField string, bitcoinBlock bool) {
	switch {
	case connection.has("generic"):
		return connection.sub("generic"), "connection.generic", false
	case connection.has("ethereum"):
		return connection.sub("ethereum"), "connection.ethereum", false
	case connection.has("ethereum-pos"):
		pos := connection.sub("ethereum-pos")
		dropField(pos, "upstream-rating", scope, "connection.ethereum-pos.upstream-rating", noEquivalentMessage, r)
		return pos.sub("execution"), "connection.ethereum-pos.execution", false
	case connection.has("bitcoin"):
		bitcoin := connection.sub("bitcoin")
		dropField(bitcoin, "zeromq", scope, "connection.bitcoin.zeromq", "nodecore has no ZeroMQ head source; the head is polled over rpc", r)
		return bitcoin, "connection.bitcoin", true
	}
	return connection.sub("generic"), "connection.generic", false
}

// applyConnectorMode reports what dshackle's connector mode cannot express in nodecore and
// tells whether the websocket connector is emitted.
func applyConnectorMode(block *section, blockField string, hasRpc, hasWs bool, scope string, r *report) bool {
	mode := modeRpcOnly
	switch {
	case hasRpc && hasWs:
		mode = modeWsHead
	case hasWs:
		mode = modeWsOnly
	}
	if configured, ok := block.str("connector-mode"); ok {
		upper := strings.ToUpper(strings.TrimSpace(configured))
		if slices.Contains([]string{modeRpcOnly, modeWsOnly, modeMixedHead, modeWsHead}, upper) {
			mode = upper
		} else {
			r.drop(scope, "connector-mode", "unknown mode; the default was used")
		}
	}
	if !hasRpc || !hasWs {
		// a mode that names a url the upstream does not have: dshackle refuses such an
		// upstream, here the mode is dropped and the one url is used
		fits := (mode == modeRpcOnly && hasRpc) || (mode == modeWsOnly && hasWs)
		if !fits {
			r.drop(scope, "connector-mode", "the mode does not match the configured urls and was ignored")
		}
		return hasWs
	}
	switch mode {
	case modeRpcOnly:
		block.ignore("ws")
		r.drop(scope, blockField+".ws", "not carried over: dshackle does not use ws.url in RPC_ONLY mode")
		return false
	case modeWsOnly:
		r.drop(scope, "connector-mode", "WS_ONLY is not supported when rpc.url is set; nodecore sends requests over HTTP")
	case modeMixedHead:
		r.drop(scope, "connector-mode", "RPC_REQUESTS_WITH_MIXED_HEAD is not supported; the head comes from websocket")
	}
	return true
}

// tonIndexer builds the second upstream of a TON node that also has a v3 indexer. dshackle
// describes both APIs as one upstream; nodecore recommends one upstream per API so that each
// gets its own head, health, chain validation and lower bounds. Enabled methods and manual
// lower bounds stay with the node upstream: they describe the v2 API.
func tonIndexer(block *section, blockField string, chain *chains.ConfiguredChain, primary *config.Upstream, custom []pair, r *report) *config.Upstream {
	var indexer *config.Upstream
	indexerId := primary.Id + tonIndexerSuffix
	for _, entry := range block.list("additional") {
		tag, _ := entry.str("tag")
		if indexer == nil && chain.Type == chains.Ton && tag == tonIndexerTag && entry.sub("rpc").has("url") {
			read, found := readRpc(entry.sub("rpc"), indexerId, blockField+".additional["+tonIndexerTag+"].rpc", r)
			if found {
				indexer = &config.Upstream{
					Id:         indexerId,
					ChainName:  primary.ChainName,
					Labels:     maps.Clone(primary.Labels),
					Connectors: []*config.ApiConnectorConfig{newConnector(restIndexerType, read, custom, indexerId, r)},
				}
				if primary.Options != nil {
					indexer.Options = cloneOptions(primary.Options)
				}
				if primary.Methods != nil && len(primary.Methods.DisableMethods) > 0 {
					indexer.Methods = &config.MethodsConfig{DisableMethods: slices.Clone(primary.Methods.DisableMethods)}
				}
				// whatever else the entry holds stays unread, for the catch-all
				continue
			}
		}
		entry.ignoreAll()
		r.drop(primary.Id, blockField+".additional", "only the ton_v3 endpoint of a TON upstream is carried over")
	}
	if indexer != nil {
		r.manual(primary.Id, blockField+".additional", fmt.Sprintf(
			"split into two upstreams as nodecore recommends for TON: %s (v2 API) and %s (v3 indexer); %s is a new upstream id",
			primary.Id, indexerId, indexerId))
	}
	return indexer
}

// esploraConnector turns the esplora endpoint of a bitcoin connection into a rest-additional
// connector, which serves `listunspent` in nodecore as esplora does in dshackle. The
// upstream's custom headers are left out: dshackle sends them to the node only. It returns
// nil when the block has no esplora url, or the chain's method spec has no esplora methods.
func esploraConnector(block *section, blockField string, chain *chains.ConfiguredChain, primary *config.Upstream, r *report) *config.ApiConnectorConfig {
	esplora := block.sub("esplora")
	if !esplora.has("url") {
		// dshackle ignores such a block; what it holds stays unread, for the catch-all
		return nil
	}
	field := blockField + ".esplora"
	if !slices.Contains(specs.GetSpecConnectors(chain.MethodSpec), specs.RestAdditional) {
		block.ignore("esplora")
		r.drop(primary.Id, field, fmt.Sprintf("chain '%s' has no esplora methods in nodecore", primary.ChainName))
		return nil
	}
	read, found := readRpc(esplora, primary.Id, field, r)
	if !found {
		return nil
	}
	return newConnector(restAdditionalType, read, nil, primary.Id, r)
}

func readRpc(rpc *section, scope, field string, r *report) (endpoint, bool) {
	address, ok := rpc.str("url")
	if !ok {
		return endpoint{}, false
	}
	reportUrlPlaceholder(address, scope, field+".url", r)
	read := endpoint{url: address, headers: make(map[string]string)}
	read.ownAuth = setAuthorization(rpc, read.headers, scope, field, r)

	tls := rpc.sub("tls")
	if ca, found := tls.str("ca"); found {
		reportPlaceholder(ca, scope, field+".tls.ca", r)
		read.ca = ca
	}
	if tls.has("certificate") || tls.has("key") {
		tls.ignore("certificate")
		tls.ignore("key")
		r.drop(scope, field+".tls", "nodecore does not present a client certificate to the node")
	}
	for _, name := range []string{"max-connections", "queue-size"} {
		dropField(rpc, name, scope, field+"."+name, noEquivalentMessage, r)
	}
	return read, true
}

func readWs(ws *section, scope, field string, r *report) (endpoint, bool) {
	address, ok := ws.str("url")
	if !ok {
		return endpoint{}, false
	}
	reportUrlPlaceholder(address, scope, field+".url", r)
	read := endpoint{url: address, headers: make(map[string]string)}
	read.ownAuth = setAuthorization(ws, read.headers, scope, field, r)
	if origin, found := ws.str("origin"); found {
		reportPlaceholder(origin, scope, field+".origin", r)
		read.headers[originHeader] = origin
	}
	for _, name := range []string{"frameSize", "msgSize", "connections"} {
		dropField(ws, name, scope, field+"."+name, noEquivalentMessage, r)
	}
	return read, true
}

// setAuthorization turns basic-auth or bearer-auth into an Authorization header and reports
// whether the block has such a section at all. When both are set basic wins, as in dshackle.
func setAuthorization(block *section, headers map[string]string, scope, field string, r *report) bool {
	value, written, own := authorization(block, scope, field, r)
	if written {
		headers[authorizationHeader] = value
	}
	return own
}

func authorization(block *section, scope, field string, r *report) (value string, written, own bool) {
	basic, bearer := block.sub("basic-auth"), block.sub("bearer-auth")
	if basic.has("username") && basic.has("password") {
		username, userOk := basic.str("username")
		password, passwordOk := basic.str("password")
		if userOk && passwordOk {
			if bearer.exists() {
				block.ignore("bearer-auth")
				r.drop(scope, field+".bearer-auth", "basic-auth is also set and wins, as in dshackle")
			}
			if hasPlaceholder(username) || hasPlaceholder(password) {
				// encoding would hide the literal placeholder inside base64
				r.manual(scope, field+".basic-auth", "contains a ${VAR} placeholder; the Authorization header was not written, "+
					"add `Authorization: Basic <base64(username:password)>` by hand")
				return "", false, true
			}
			return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password)), true, true
		}
	}
	if token, ok := bearer.str("token"); ok {
		reportPlaceholder(token, scope, field+".bearer-auth.token", r)
		return "Bearer " + token, true, true
	}
	return "", false, false
}

// newConnector combines the upstream's custom headers with the endpoint's own. The endpoint's
// Authorization replaces a custom one, whatever its letter case.
func newConnector(connectorType string, read endpoint, custom []pair, scope string, r *report) *config.ApiConnectorConfig {
	headers := make(map[string]string)
	for _, header := range custom {
		if read.ownAuth && strings.EqualFold(header.key, authorizationHeader) {
			r.drop(scope, "custom-headers."+authorizationHeader, "replaced by the connector's basic-auth or bearer-auth")
			continue
		}
		headers[header.key] = header.value
	}
	maps.Copy(headers, read.headers)
	if len(headers) == 0 {
		headers = nil
	}
	return &config.ApiConnectorConfig{Type: connectorType, Url: read.url, Headers: headers, Ca: read.ca}
}

func customHeaders(up *section, scope string, r *report) []pair {
	headers := make([]pair, 0)
	for _, header := range up.pairs("custom-headers") {
		name, value := strings.TrimSpace(header.key), strings.TrimSpace(header.value)
		if name == "" || value == "" {
			continue
		}
		reportPlaceholder(value, scope, "custom-headers."+name, r)
		headers = append(headers, pair{key: name, value: value})
	}
	return headers
}

func convertLabels(up *section, scope string, r *report) map[string]string {
	labels := make(map[string]string)
	for _, label := range up.pairs("labels") {
		name, value := strings.TrimSpace(label.key), strings.TrimSpace(label.value)
		if name == "" || value == "" {
			continue
		}
		reportPlaceholder(value, scope, "labels."+name, r)
		labels[name] = value
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

func convertMethods(up *section, scope string, r *report) *config.MethodsConfig {
	methods := up.sub("methods")
	converted := new(config.MethodsConfig)
	for _, entry := range methods.list("enabled") {
		name, ok := entry.str("name")
		if !ok {
			continue
		}
		field := "methods.enabled[" + name + "]"
		dropField(entry, "quorum", scope, field+".quorum", "per-method quorum is not supported", r)
		if entry.has("static") {
			// nodecore would forward the method to the node instead of answering with the value
			dropField(entry, "static", scope, field+".static", "static responses are not supported; the method was not enabled", r)
			continue
		}
		converted.EnableMethods = append(converted.EnableMethods, name)
	}
	for _, entry := range methods.list("disabled") {
		if name, ok := entry.str("name"); ok {
			converted.DisableMethods = append(converted.DisableMethods, name)
		}
	}
	if len(converted.EnableMethods) == 0 && len(converted.DisableMethods) == 0 {
		return nil
	}
	return converted
}

// applyMethodGroups carries dshackle's method-groups over as group names: nodecore expands an
// entry of methods.enable or methods.disable that names a group of the chain's method spec to
// the whole group, and applies disable before enable. Three things differ from dshackle and
// are handled here:
//
//   - a name that is not a group of this chain's spec would be taken by nodecore for a method
//     of that name, so it is reported instead of written;
//   - a group listed as both enabled and disabled is refused by nodecore's validation; the
//     disabled entry is kept, which is what dshackle does;
//   - a disabled method that belongs to an enabled group stays on in nodecore, because enable
//     is applied last, so the method is taken out of the disable list and reported.
//
// dshackle switches trace and debug on only through their groups, while nodecore starts from
// every method of the spec and lets method detection remove what the node does not serve.
// That default is deliberately left alone: nothing is disabled to imitate dshackle.
func applyMethodGroups(up *section, upstream *config.Upstream, chain *chains.ConfiguredChain, r *report) {
	groups := up.sub("method-groups")
	if !groups.exists() {
		return
	}
	connectorTypes := make([]specs.ApiConnectorType, 0, len(upstream.Connectors))
	for _, connector := range upstream.Connectors {
		connectorTypes = append(connectorTypes, specs.GetApiConnectorType(connector.Type))
	}
	specGroups := specs.GetSpecMethodsByConnectors(chain.MethodSpec, connectorTypes)
	known := func(names []string, list string) []string {
		kept := make([]string, 0, len(names))
		for _, name := range names {
			if _, ok := specGroups[name]; !ok {
				r.drop(upstream.Id, fmt.Sprintf("method-groups.%s[%s]", list, name),
					fmt.Sprintf("chain '%s' has no method group '%s'; not carried over", upstream.ChainName, name))
				continue
			}
			kept = append(kept, name)
		}
		return kept
	}
	disabledGroups := known(groups.strings("disabled"), "disabled")
	enabledGroups := make([]string, 0)
	for _, name := range known(groups.strings("enabled"), "enabled") {
		if slices.Contains(disabledGroups, name) {
			r.drop(upstream.Id, "method-groups.enabled["+name+"]", "also listed in disabled; disabled wins, as in dshackle")
			continue
		}
		enabledGroups = append(enabledGroups, name)
	}

	methods := upstream.Methods
	if methods == nil {
		methods = new(config.MethodsConfig)
	}
	disabledMethods := make([]string, 0, len(methods.DisableMethods))
	for _, name := range methods.DisableMethods {
		if group, found := enabledGroupOf(name, enabledGroups, specGroups); found {
			r.drop(upstream.Id, "methods.disabled["+name+"]",
				fmt.Sprintf("part of the enabled group '%s'; nodecore's enable wins, so the method stays on", group))
			continue
		}
		disabledMethods = append(disabledMethods, name)
	}
	methods.EnableMethods = append(enabledGroups, methods.EnableMethods...)
	methods.DisableMethods = append(disabledGroups, disabledMethods...)
	if len(methods.EnableMethods) == 0 {
		methods.EnableMethods = nil
	}
	if len(methods.DisableMethods) == 0 {
		methods.DisableMethods = nil
	}
	upstream.Methods = methods
	if methods.EnableMethods == nil && methods.DisableMethods == nil {
		upstream.Methods = nil
	}
}

func enabledGroupOf(method string, enabledGroups []string, specGroups map[string]map[string]*specs.Method) (string, bool) {
	for _, group := range enabledGroups {
		if _, ok := specGroups[group][method]; ok {
			return group, true
		}
	}
	return "", false
}

// convertLowerBounds carries over the fixed manual bounds. nodecore bounds are static block
// numbers, so a bound counted back from the head has no equivalent.
func convertLowerBounds(up *section, scope string, r *report) map[string]int64 {
	bounds := up.sub("additional-settings").sub("manual-lower-bounds")
	converted := make(map[string]int64)
	for _, key := range bounds.keys() {
		name := strings.ToLower(key)
		field := "additional-settings.manual-lower-bounds." + name
		if !slices.Contains(config.LowerBoundTypeNames, name) {
			dropField(bounds, key, scope, field, "unknown bound type", r)
			continue
		}
		entry := bounds.sub(key)
		kind, _ := entry.str("type")
		switch strings.ToLower(kind) {
		case "head":
			dropField(bounds, key, scope, field, "head-relative bounds are not supported; nodecore bounds are static", r)
		case "fixed":
			value, ok := entry.integer("value")
			switch {
			case !ok && !entry.has("value"):
				r.drop(scope, field, "no value")
			case !ok: // an unusable value stays unread, for the catch-all
			case value < 1:
				r.drop(scope, field, "the value must be 1 or greater")
			default:
				converted[name] = value
			}
		default:
			dropField(bounds, key, scope, field, fmt.Sprintf("unknown bound kind '%s'", kind), r)
		}
	}
	if len(converted) == 0 {
		return nil
	}
	return converted
}

// reportUnsupportedFields covers the upstream-level settings that have no place in nodecore.
func reportUnsupportedFields(up *section, scope string, r *report) {
	dropField(up, "node-id", scope, "node-id", "not used by nodecore", r)
	if role, ok := up.str("role"); ok {
		switch strings.ToLower(strings.TrimSpace(role)) {
		case "primary", "standard": // nodecore's behaviour is the same, nothing to say
		case "secondary", "fallback":
			r.drop(scope, "role", "roles are not supported; the upstream is treated as primary")
		default:
			r.drop(scope, "role", fmt.Sprintf("unknown role '%s'; the upstream is treated as primary", role))
		}
	}
}

func reportPlaceholder(value, scope, field string, r *report) {
	if hasPlaceholder(value) {
		r.manual(scope, field, placeholderMessage)
	}
}

// reportUrlPlaceholder tells apart a placeholder nodecore can still parse around (in the path
// or query) from one that breaks the URL (host, port, or the whole value): with the second
// kind nodecore refuses the config until the placeholder is replaced.
func reportUrlPlaceholder(address, scope, field string, r *report) {
	if !hasPlaceholder(address) {
		return
	}
	message := placeholderMessage
	if parsed, err := url.Parse(address); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		message += " (nodecore will not start until it is replaced)"
	}
	r.manual(scope, field, message)
}

func dropField(owner *section, key, scope, field, message string, r *report) {
	if owner.has(key) {
		owner.ignore(key)
		r.drop(scope, field, message)
	}
}
