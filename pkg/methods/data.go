package specs

import (
	"errors"
	"fmt"
	"slices"

	"github.com/bytedance/sonic"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/samber/lo"
)

type MethodSpec struct {
	SpecData    *SpecData     `json:"spec"`
	SpecImports []string      `json:"spec-imports"`
	Methods     []*MethodData `json:"methods"`
}

type SpecData struct {
	Name          string   `json:"name"`
	ApiConnectors []string `json:"api-connectors"`
	Type          string   `json:"type"`

	apiConnectors []ApiConnectorType
	specType      SpecType
}

func (s *SpecData) UnmarshalJSON(bytes []byte) error {
	type specData SpecData

	var raw specData
	if err := sonic.Unmarshal(bytes, &raw); err != nil {
		return err
	}

	*s = SpecData(raw)
	s.apiConnectors = lo.Map(s.ApiConnectors, func(item string, index int) ApiConnectorType {
		return GetApiConnectorType(item)
	})
	s.specType = GetSpecType(s.Type)

	return nil
}

type MethodData struct {
	Name      string          `json:"name"`
	Group     string          `json:"group"`
	Settings  *MethodSettings `json:"settings"`
	TagParser *TagParser      `json:"tag-parser"`
	Enabled   *bool           `json:"enabled"`
}

type MethodSettings struct {
	Cacheable        *bool          `json:"cacheable"`
	EnforceIntegrity bool           `json:"enforce-integrity"`
	Sticky           *Sticky        `json:"sticky"`
	Subscription     *Subscription  `json:"subscription"`
	Local            bool           `json:"local"`
	Dispatch         DispatchPolicy `json:"dispatch"`
}

type DispatchPolicy string

const (
	DispatchDefault      DispatchPolicy = ""
	DispatchBroadcast    DispatchPolicy = "broadcast"
	DispatchMaximumValue DispatchPolicy = "maximum-value"
	DispatchNotNull      DispatchPolicy = "not-null"
)

type Sticky struct {
	SendSticky   bool `json:"send-sticky"`   // to send to the same node
	CreateSticky bool `json:"create-sticky"` // to add an upstream index to the payload
}

type Subscription struct {
	IsSubscribe bool   `json:"is-subscribe"`
	Method      string `json:"method"`
	UnsubMethod string `json:"unsubscribe-method"`
}

type ParserReturnType string

const (
	BlockNumberType ParserReturnType = "blockNumber" // hex number or tag (latest, earliest, etc)
	BlockRefType    ParserReturnType = "blockRef"    // hash, hex number or tag (latest, earliest, etc)
	ObjectType      ParserReturnType = "object"      // generic object
	StringType      ParserReturnType = "string"      // string values
	BlockRangeType  ParserReturnType = "blockRange"  // block range (from, to)
)

type TagParser struct {
	ReturnType ParserReturnType `json:"type"`
	Path       string           `json:"path"`
}

func (m *MethodData) setDefaults() {
	if m.Group == "" {
		m.Group = "common"
	}
	if m.Enabled == nil {
		m.Enabled = new(true)
	}
	if m.Settings == nil {
		m.Settings = &MethodSettings{Cacheable: new(true)}
	} else {
		if m.Settings.Cacheable == nil {
			m.Settings.Cacheable = new(true)
		}
	}
}

func (m *MethodSpec) validate() error {
	if m.SpecData == nil {
		return errors.New("missing spec data")
	}
	if m.SpecData.Name == "" {
		return errors.New("missing spec name")
	}
	if m.SpecData.specType == UnknownSpec {
		return errors.New("unknown spec type")
	}
	if m.SpecData.specType == BundleSpec {
		if len(m.SpecData.ApiConnectors) != 0 {
			return errors.New("bundle spec api connectors must be empty")
		}
		if len(m.Methods) != 0 {
			return errors.New("bundle spec methods must be empty")
		}
	}
	if m.SpecData.specType == PlainSpec {
		if len(m.SpecData.ApiConnectors) == 0 {
			return errors.New("plain spec api connectors must not be empty")
		}
	}
	return nil
}

func (m *MethodData) validate() error {
	if m.TagParser != nil {
		if err := m.TagParser.validate(); err != nil {
			return err
		}
	}
	if m.Settings != nil {
		if err := m.Settings.validate(); err != nil {
			return err
		}
	}

	return nil
}

func (m *MethodSettings) validate() error {
	if err := m.Dispatch.validate(); err != nil {
		return err
	}
	if m.Dispatch != DispatchDefault {
		if m.Local {
			return errors.New("dispatch cannot be used with local methods")
		}
		if m.Subscription != nil && m.Subscription.IsSubscribe {
			return errors.New("dispatch cannot be used with subscription methods")
		}
		if m.Sticky != nil && (m.Sticky.SendSticky || m.Sticky.CreateSticky) {
			return errors.New("dispatch cannot be used with sticky methods")
		}
	}
	if m.Sticky != nil {
		if m.Sticky.CreateSticky && m.Sticky.SendSticky {
			return errors.New("both 'create-sticky' and 'send-sticky' are enabled")
		}
	}
	return nil
}

func (d DispatchPolicy) validate() error {
	switch d {
	case DispatchDefault, DispatchBroadcast, DispatchMaximumValue, DispatchNotNull:
		return nil
	default:
		return fmt.Errorf("unknown dispatch policy - %s", d)
	}
}

func (p *TagParser) validate() error {
	if p.Path == "" {
		return errors.New("empty tag-parser path")
	}
	if err := p.ReturnType.validate(); err != nil {
		return err
	}

	return nil
}

func (p ParserReturnType) validate() error {
	switch p {
	case BlockRefType, BlockNumberType, StringType, ObjectType, BlockRangeType:
	default:
		return fmt.Errorf("wrong return type of tag-parser - %s", p)
	}
	return nil
}

type SpecType int

const (
	UnknownSpec SpecType = iota
	BundleSpec
	PlainSpec
)

var specTypes = map[string]SpecType{
	"bundle": BundleSpec,
	"plain":  PlainSpec,
}

func GetSpecType(name string) SpecType {
	specType, ok := specTypes[name]
	if !ok {
		return UnknownSpec
	}
	return specType
}

type ApiConnectorType int

const (
	UnknownType ApiConnectorType = iota
	JsonRpcConnector
	RestConnector
	GrpcConnector
	WebsocketConnector
	RestIndexer    // a self-contained indexer REST API next to the node API (e.g. the TON v3 indexer); a plain type - may be an upstream's only connector
	RestAdditional // is used for connectors that provide extra REST methods, but they can't be used for chain-specific
)

func (a ApiConnectorType) String() string {
	switch a {
	case JsonRpcConnector:
		return "json-rpc"
	case RestConnector:
		return "rest"
	case GrpcConnector:
		return "grpc"
	case WebsocketConnector:
		return "websocket"
	case UnknownType:
		return "unknown"
	case RestIndexer:
		return "rest-indexer"
	case RestAdditional:
		return "rest-additional"
	}
	return ""
}

var apiConnectors = map[string]ApiConnectorType{
	"json-rpc":        JsonRpcConnector,
	"rest":            RestConnector,
	"grpc":            GrpcConnector,
	"websocket":       WebsocketConnector,
	"rest-indexer":    RestIndexer,
	"rest-additional": RestAdditional,
}
var plainApiConnectorTypes = []ApiConnectorType{
	JsonRpcConnector,
	RestConnector,
	GrpcConnector,
	WebsocketConnector,
	RestIndexer,
}

var additionalApiConnectors = mapset.NewThreadUnsafeSet[ApiConnectorType](RestAdditional)

func IsAdditionalApiConnectorType(apiConnectorType ApiConnectorType) bool {
	return additionalApiConnectors.Contains(apiConnectorType)
}

func GetPlainApiConnectorType() []ApiConnectorType {
	return slices.Clone(plainApiConnectorTypes)
}

func GetApiConnectorType(name string) ApiConnectorType {
	connector, ok := apiConnectors[name]
	if !ok {
		return UnknownType
	}
	return connector
}

func ValidateApiConnectorType(connectorName string) error {
	_, ok := apiConnectors[connectorName]
	if !ok {
		return fmt.Errorf("invalid connector type - '%s'", connectorName)
	}
	return nil
}
