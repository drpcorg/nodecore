package chains

import (
	_ "embed"
	"fmt"
	"maps"
	"math"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"gopkg.in/yaml.v3"
)

type BlockchainType string

const (
	Algorand            BlockchainType = "avm"
	Bitcoin             BlockchainType = "bitcoin"
	Cosmos              BlockchainType = "cosmos"
	Ethereum            BlockchainType = "eth"
	EthereumBeaconChain BlockchainType = "eth-beacon-chain"
	Near                BlockchainType = "near"
	Polkadot            BlockchainType = "polkadot"
	Ripple              BlockchainType = "ripple"
	Solana              BlockchainType = "solana"
	Starknet            BlockchainType = "starknet"
	Stellar             BlockchainType = "stellar"
	Ton                 BlockchainType = "ton"
	Aztec               BlockchainType = "aztec"
	Aptos               BlockchainType = "aptos"
)

func IsValidBlockchainType(t string) bool {
	switch BlockchainType(t) {
	case Algorand, Bitcoin, Cosmos, Ethereum, EthereumBeaconChain,
		Near, Polkadot, Ripple, Solana, Starknet, Stellar, Ton, Aztec, Aptos:
		return true
	default:
		return false
	}
}

//go:embed public/chains.yaml
var chainsCfg []byte

type ChainConfig struct {
	ChainSettings ChainSettings `yaml:"chain-settings"`
}

type ChainSettings struct {
	Protocols []Protocol             `yaml:"protocols"`
	Default   map[string]interface{} `yaml:"default"`
}

type ChainData struct {
	ShortNames           []string               `yaml:"short-names"`
	ChainId              string                 `yaml:"chain-id"`
	GrpcId               int                    `yaml:"grpcId"`
	MethodSpec           string                 `yaml:"method-spec"`
	Settings             map[string]interface{} `yaml:"settings"`
	NetVersion           string                 `yaml:"net-version"`
	CallValidateContract string                 `yaml:"call-validate-contract"`
	GasPriceCondition    []string               `yaml:"gas-price-condition"`
	LowerBounds          *ChainLowerBounds      `yaml:"lower-bounds"`
}

// GoldLowerBound is a known-oldest data point for a chain, sourced from the
// bundled chains.yaml. It is used to quickly detect whether an upstream is
// fully archival for tx/receipts data without a full binary search.
type GoldLowerBound struct {
	Block uint64 `yaml:"block"`
	Hash  string `yaml:"hash"`
}

// ChainLowerBounds holds the per-chain gold lower bounds. Only tx and receipts
// carry a hash and are consulted during lower-bound detection.
type ChainLowerBounds struct {
	Tx       *GoldLowerBound `yaml:"tx"`
	Receipts *GoldLowerBound `yaml:"receipts"`
}

type Protocol struct {
	Chains   []ChainData            `yaml:"chains"`
	Settings map[string]interface{} `yaml:"settings"`
	Type     BlockchainType         `yaml:"type"`
}

type LagConfig struct {
	Lagging int64 `yaml:"lagging"`
	Syncing int64 `yaml:"syncing"`
}

type Settings struct {
	ExpectedBlockTime        time.Duration `yaml:"expected-block-time"`
	MethodSpec               string        `yaml:"method-spec"`
	Lags                     LagConfig     `yaml:"lags"`
	Options                  *Options      `yaml:"options"`
	SupportFinalizedBlockTag *bool         `yaml:"support-finalized-block-tag"`
	SupportSafeBlockTag      *bool         `yaml:"support-safe-block-tag"`
}

type ConfiguredChain struct {
	GrpcId               int
	ChainId              string
	NetVersion           string
	ShortNames           []string
	Type                 BlockchainType
	Settings             Settings
	Chain                Chain
	MethodSpec           string
	CallValidateContract string
	GasPriceCondition    []string
	LowerBounds          *ChainLowerBounds
}

var UnknownChain = &ConfiguredChain{
	GrpcId:     0,
	ChainId:    "0x0",
	NetVersion: "0",
	ShortNames: []string{},
	Settings:   Settings{},
	Chain:      -1,
}

var chains map[string]*ConfiguredChain
var grpcChains map[int]*ConfiguredChain

// nextDynamicChainId tracks the next Chain int to allocate for chains
// registered via LoadExtraChains. It starts at dynamicChainBaseId (defined
// in the generated chains_data.go) and is incremented as extra chains are
// registered.
var nextDynamicChainId = dynamicChainBaseId

// extraChainsMu serializes LoadExtraChains calls and protects mutation of
// the package-level chain registry. extraChainsLoadedFlag flips to true
// only on a *successful* load so that callers can retry with corrected
// input after a validation failure.
var extraChainsMu sync.Mutex
var extraChainsLoadedFlag bool

func init() {
	result, grpcResult, err := configureChainsFromBytes(chainsCfg)
	if err != nil {
		panic(err)
	}
	chains = result
	grpcChains = grpcResult
}

func IsTron(chain Chain) bool {
	return chain == TRON || chain == TRON_SHASTA
}

// LoadExtraChains parses an additional chain registry YAML (same schema as
// the bundled drpcorg/public chains.yaml) and merges its entries into the
// running chain registry. Chains whose short-name isn't already registered
// are allocated a new Chain int (>= dynamicChainBaseId) so they round-trip
// through Chain.String() correctly.
//
// LoadExtraChains must be called once, at startup, before any consumer of
// the chain registry runs. Repeat calls return an error.
//
// Returns an error if the YAML is malformed, if any extra chain re-uses an
// existing short-name, or if an extra chain's grpcId collides with an
// existing one.
func LoadExtraChains(extraYaml []byte) error {
	if len(extraYaml) == 0 {
		return nil
	}
	extraChainsMu.Lock()
	defer extraChainsMu.Unlock()
	if extraChainsLoadedFlag {
		return fmt.Errorf("LoadExtraChains already succeeded once; the chain registry is locked after first successful load")
	}
	if err := loadExtraChainsLocked(extraYaml); err != nil {
		return err
	}
	extraChainsLoadedFlag = true
	return nil
}

func loadExtraChainsLocked(extraYaml []byte) error {
	extraConfigured, extraGrpc, err := configureChainsFromBytes(extraYaml)
	if err != nil {
		return err
	}

	for shortName, configured := range extraConfigured {
		if _, exists := chains[shortName]; exists {
			return fmt.Errorf("extra chain short-name %q already registered", shortName)
		}
		// grpcId is optional for extra chains (e.g. private EVM upstreams that
		// don't speak dRPC's gRPC protocol). 0 is the unset sentinel and every
		// embedded chain sets a non-zero grpcId, so only a non-zero grpcId can
		// genuinely collide with the existing registry.
		if _, exists := grpcChains[configured.GrpcId]; exists && configured.GrpcId != 0 {
			return fmt.Errorf("extra chain grpcId %d (%q) collides with an existing chain", configured.GrpcId, shortName)
		}
	}

	for shortName, configured := range extraConfigured {
		chains[shortName] = configured
	}
	for grpcId, configured := range extraGrpc {
		if grpcId != 0 {
			grpcChains[grpcId] = configured
		}
	}

	return nil
}

func (c *ConfiguredChain) AverageRemoveSpeed() float64 {
	return math.Ceil((1000.0/float64(c.Settings.ExpectedBlockTime.Milliseconds()))*100) / 100
}

func GetAllChains() map[string]*ConfiguredChain {
	return maps.Clone(chains)
}

func IsSupported(chainName string) bool {
	_, ok := chains[chainName]
	return ok
}

func GetChain(chainName string) *ConfiguredChain {
	found, ok := chains[chainName]
	if !ok {
		return UnknownChain
	}
	return found
}

func GetChainByGrpcId(grpcId int) *ConfiguredChain {
	found, ok := grpcChains[grpcId]
	if !ok {
		return UnknownChain
	}
	return found
}

func GetChainByChainIdAndVersion(blockchainType BlockchainType, chainId, netVersion string) *ConfiguredChain {
	for _, chain := range chains {
		if chain.Type == blockchainType && chain.ChainId == chainId && chain.NetVersion == netVersion {
			return chain
		}
	}
	return UnknownChain
}

func GetMethodSpecNameByChain(chain Chain) string {
	configuredChain := GetChain(chain.String())
	return configuredChain.MethodSpec
}

func GetMethodSpecNameByChainName(chainName string) string {
	return GetChain(chainName).MethodSpec
}

func configureChainsFromBytes(rawYaml []byte) (map[string]*ConfiguredChain, map[int]*ConfiguredChain, error) {
	configuredChains := make(map[string]*ConfiguredChain)
	configuredGrpcChains := make(map[int]*ConfiguredChain)

	var config ChainConfig
	if err := yaml.Unmarshal(rawYaml, &config); err != nil {
		return nil, nil, err
	}

	for _, protocol := range config.ChainSettings.Protocols {
		defaultSettings := deepMerge(config.ChainSettings.Default, protocol.Settings)
		for _, chain := range protocol.Chains {
			chainSettings := deepMerge(defaultSettings, chain.Settings)
			out, err := yaml.Marshal(chainSettings)
			if err != nil {
				return nil, nil, err
			}
			settings := Settings{}
			err = yaml.Unmarshal(out, &settings)
			if err != nil {
				return nil, nil, err
			}
			if settings.ExpectedBlockTime == 0 {
				log.Panic().Msgf("expected block time of chain %s is zero", chain.ShortNames[0])
			}

			if len(chain.ShortNames) == 0 {
				return nil, nil, fmt.Errorf("chain entry has no short-names")
			}

			// For chains not present in the build-time chainsMap, allocate a
			// dynamic Chain id and register the primary short-name so that
			// Chain.String() round-trips correctly.
			network, ok := chainsMap[chain.ShortNames[0]]
			if !ok {
				network = nextDynamicChainId
				nextDynamicChainId++
				chainsMap[chain.ShortNames[0]] = network
				dynamicChainNames[network] = chain.ShortNames[0]
			}

			netVersion := lo.Ternary(chain.NetVersion != "", chain.NetVersion, getNetVersion(chain.ChainId))
			methodSpec := lo.Ternary(
				chain.MethodSpec != "",
				getMethodSpecName(protocol.Type, chain.MethodSpec),
				getMethodSpecName(protocol.Type, settings.MethodSpec),
			)

			configuredChain := &ConfiguredChain{
				GrpcId:               chain.GrpcId,
				ChainId:              strings.ToLower(chain.ChainId),
				ShortNames:           chain.ShortNames,
				NetVersion:           strings.ToLower(netVersion),
				Type:                 protocol.Type,
				Settings:             settings,
				Chain:                network,
				MethodSpec:           methodSpec,
				CallValidateContract: chain.CallValidateContract,
				GasPriceCondition:    append([]string(nil), chain.GasPriceCondition...),
				LowerBounds:          chain.LowerBounds,
			}

			for _, shortName := range chain.ShortNames {
				configuredChains[shortName] = configuredChain
			}
			configuredGrpcChains[configuredChain.GrpcId] = configuredChain
		}
	}

	return configuredChains, configuredGrpcChains, nil
}

func getNetVersion(chainId string) string {
	n := new(big.Int)
	n.SetString(chainId, 0)

	return n.String()
}

func getMethodSpecName(blockchainType BlockchainType, methodSpecName string) string {
	if methodSpecName != "" {
		return methodSpecName
	}
	switch blockchainType {
	case Ethereum:
		return "eth"
	case Solana:
		return "solana"
	case Aztec:
		return "aztec"
	case Algorand:
		return "algorand"
	case EthereumBeaconChain:
		return "eth-beacon-chain"
	case Aptos:
		return "aptos"
	case Bitcoin:
		return "bitcoin"
	case Near:
		return "near"
	case Ripple:
		return "ripple"
	case Starknet:
		return "starknet"
	case Ton:
		return "ton"
	case Stellar:
		return "stellar"
	}

	return ""
}

func deepMerge(dst, src map[string]interface{}) map[string]interface{} {
	newMap := make(map[string]interface{})

	for key, value := range dst {
		newMap[key] = value
	}

	for key, srcVal := range src {
		if dstVal, ok := dst[key]; ok {
			if srcMap, srcMapOk := srcVal.(map[string]interface{}); srcMapOk {
				if dstMap, dstMapOk := dstVal.(map[string]interface{}); dstMapOk {
					newMap[key] = deepMerge(dstMap, srcMap)
					continue
				}
			}
		}
		newMap[key] = srcVal
	}

	return newMap
}
