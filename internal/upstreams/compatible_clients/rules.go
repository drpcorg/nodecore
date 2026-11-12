// Package compatible_clients holds the client-version rules that decide which node
// clients may serve traffic: the YAML file shipped in github.com/drpcorg/public, indexed
// per client and network so that a version is judged with one lookup.
package compatible_clients

import (
	"fmt"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public"
	"gopkg.in/yaml.v3"
)

// DefaultUrl is where the rules are refreshed from unless configured otherwise.
const DefaultUrl = "https://raw.githubusercontent.com/drpcorg/public/main/compatible-clients.yaml"

// Rules is the decoded compatible-clients file. Unmarshalling builds the index, so any
// decoded value is ready to check against.
type Rules struct {
	Rules []Rule `yaml:"rules"`

	byClient map[string]*clientRules
}

// Rule is one entry of the file. An entry without networks is the client's default
// rule; an entry with networks extends that default on those networks.
type Rule struct {
	Client    string   `yaml:"client"`
	Networks  []string `yaml:"networks"`
	Blacklist []string `yaml:"blacklist"`
	Whitelist []string `yaml:"whitelist"`
}

// Embedded decodes the copy of the file shipped in github.com/drpcorg/public.
func Embedded() (*Rules, error) {
	var rules Rules
	if err := yaml.Unmarshal(public.GetCompatibleClients(), &rules); err != nil {
		return nil, fmt.Errorf("unable to decode the embedded compatible clients: %w", err)
	}
	return &rules, nil
}

// Check judges a client version on a chain. The rule applied is the one for the first
// of the chain's names (its name, then its short names) that has an entry, already
// extended by the client's default rule at decode time; a chain without an entry
// falls back to the default rule; a client without either has NoRule.
func (r *Rules) Check(clientType, clientVersion string, chain *chains.ConfiguredChain) Verdict {
	client, ok := r.byClient[normalizeName(clientType)]
	if !ok {
		return NoRule
	}
	for _, name := range chainNames(chain) {
		if networkRule, found := client.byNetwork[name]; found {
			return networkRule.check(clientVersion)
		}
	}
	if client.defaultRule == nil {
		return NoRule
	}
	return client.defaultRule.check(clientVersion)
}

func (r *Rules) UnmarshalYAML(node *yaml.Node) error {
	// a new named type carries the fields and tags but not this method, so the decoder
	// takes its plain struct path instead of calling UnmarshalYAML again
	type plain Rules
	if err := node.Decode((*plain)(r)); err != nil {
		return err
	}
	r.buildIndex()
	return nil
}

// rule is one effective rule: both sets hold normalized versions.
type rule struct {
	blacklist mapset.Set[string]
	whitelist mapset.Set[string]
}

func newRule() *rule {
	return &rule{
		blacklist: mapset.NewThreadUnsafeSet[string](),
		whitelist: mapset.NewThreadUnsafeSet[string](),
	}
}

type clientRules struct {
	defaultRule *rule
	byNetwork   map[string]*rule
}

// buildIndex groups the entries by client, unions same-key duplicates, then extends
// every network rule with the client's default: blacklist = default ∪ network,
// whitelist = the network's when it has one, else the default's. After it, a lookup
// never merges.
func (r *Rules) buildIndex() {
	r.byClient = make(map[string]*clientRules, len(r.Rules))
	for _, entry := range r.Rules {
		client := r.clientRules(normalizeName(entry.Client))
		if len(entry.Networks) == 0 {
			if client.defaultRule == nil {
				client.defaultRule = newRule()
			}
			client.defaultRule.add(entry)
			continue
		}
		for _, network := range entry.Networks {
			name := normalizeName(network)
			if _, ok := client.byNetwork[name]; !ok {
				client.byNetwork[name] = newRule()
			}
			client.byNetwork[name].add(entry)
		}
	}

	for _, client := range r.byClient {
		if client.defaultRule == nil {
			continue
		}
		for _, network := range client.byNetwork {
			network.blacklist = client.defaultRule.blacklist.Union(network.blacklist)
			if network.whitelist.IsEmpty() {
				network.whitelist = client.defaultRule.whitelist.Clone()
			}
		}
	}
}

func (r *Rules) clientRules(client string) *clientRules {
	if existing, ok := r.byClient[client]; ok {
		return existing
	}
	created := &clientRules{byNetwork: make(map[string]*rule)}
	r.byClient[client] = created
	return created
}

func (e *rule) add(entry Rule) {
	e.blacklist.Append(normalizeVersions(entry.Blacklist)...)
	e.whitelist.Append(normalizeVersions(entry.Whitelist)...)
}

func chainNames(chain *chains.ConfiguredChain) []string {
	names := make([]string, 0, len(chain.ShortNames)+1)
	names = append(names, normalizeName(chain.Chain.String()))
	for _, shortName := range chain.ShortNames {
		names = append(names, normalizeName(shortName))
	}
	return names
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func normalizeVersions(versions []string) []string {
	normalized := make([]string, 0, len(versions))
	for _, version := range versions {
		normalized = append(normalized, NormalizeVersion(version))
	}
	return normalized
}
