package chains

import (
	_ "embed"
	"maps"
	"math/big"
	"time"

	"github.com/samber/lo"
	"gopkg.in/yaml.v3"
)

//go:embed public/chains.yaml
var chainsCfg []byte

type BlockchainType string

const (
	Bitcoin             BlockchainType = "bitcoin"
	Cosmos              BlockchainType = "cosmos"
	Ethereum            BlockchainType = "eth"
	EthereumBeaconChain BlockchainType = "eth-beacon-chain"
	Near                BlockchainType = "near"
	Polkadot            BlockchainType = "polkadot"
	Solana              BlockchainType = "solana"
	Starknet            BlockchainType = "starknet"
	Ton                 BlockchainType = "ton"
	Aztec               BlockchainType = "aztec"
)

type ChainConfig struct {
	ChainSettings ChainSettings `yaml:"chain-settings"`
}

type ChainSettings struct {
	Protocols []Protocol             `yaml:"protocols"`
	Default   map[string]interface{} `yaml:"default"`
}

type ChainData struct {
	ShortNames []string               `yaml:"short-names"`
	ChainId    string                 `yaml:"chain-id"`
	MethodSpec string                 `yaml:"method-spec"`
	Settings   map[string]interface{} `yaml:"settings"`
	NetVersion string                 `yaml:"net-version"`
}

type Protocol struct {
	Chains   []ChainData            `yaml:"chains"`
	Settings map[string]interface{} `yaml:"settings"`
	Type     BlockchainType         `yaml:"type"`
}

type Settings struct {
	ExpectedBlockTime time.Duration `yaml:"expected-block-time"`
}

type ConfiguredChain struct {
	ChainId    string
	NetVersion string
	ShortNames []string
	Type       BlockchainType
	Settings   Settings
	Chain      Chain
	MethodSpec string
}

var UnknownChain = &ConfiguredChain{
	ChainId:    "0x0",
	NetVersion: "0",
	ShortNames: []string{},
	Settings:   Settings{},
	Chain:      -1,
}

var chains map[string]*ConfiguredChain

func init() {
	result, err := configureChains()
	if err != nil {
		panic(err)
	}
	chains = result
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

func GetChainByChainIdAndVersion(chainId, netVersion string) *ConfiguredChain {
	for _, chain := range chains {
		if chain.ChainId == chainId && chain.NetVersion == netVersion {
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

func configureChains() (map[string]*ConfiguredChain, error) {
	configuredChains := make(map[string]*ConfiguredChain)

	var config ChainConfig
	if err := yaml.Unmarshal(chainsCfg, &config); err != nil {
		return nil, err
	}

	for _, protocol := range config.ChainSettings.Protocols {
		defaultSettings := deepMerge(config.ChainSettings.Default, protocol.Settings)
		for _, chain := range protocol.Chains {
			chainSettings := deepMerge(defaultSettings, chain.Settings)
			out, err := yaml.Marshal(chainSettings)
			if err != nil {
				return nil, err
			}
			settings := Settings{}
			err = yaml.Unmarshal(out, &settings)
			if err != nil {
				return nil, err
			}

			if network, ok := chainsMap[chain.ShortNames[0]]; ok {
				netVersion := lo.Ternary(chain.NetVersion != "", chain.NetVersion, getNetVersion(chain.ChainId))

				configuredChain := &ConfiguredChain{
					ChainId:    chain.ChainId,
					ShortNames: chain.ShortNames,
					NetVersion: netVersion,
					Type:       protocol.Type,
					Chain:      network,
					MethodSpec: getMethodSpecName(protocol.Type, chain.MethodSpec),
				}

				for _, shortName := range chain.ShortNames {
					configuredChains[shortName] = configuredChain
				}
			}
		}
	}

	return configuredChains, nil
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
