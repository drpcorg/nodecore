package specs

import (
	"errors"
	"fmt"
	"regexp"
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
	Grpc             *GrpcSettings  `json:"grpc"`
}

type GrpcSettings struct {
	CallType GrpcCallType `json:"call-type"`
}

type GrpcCallType string

const (
	GrpcCallTypeUnary GrpcCallType = "unary"
	// GrpcCallTypeServerStreamSubscription is an unbounded live stream; a clean
	// end from the node is a failure.
	GrpcCallTypeServerStreamSubscription GrpcCallType = "server-stream-subscription"
	// GrpcCallTypeServerStreamFinite is a bounded stream (a result delivered as
	// several messages); a clean end from the node is normal completion.
	GrpcCallTypeServerStreamFinite GrpcCallType = "server-stream-finite"
)

// IsServerStream reports whether the call type is one of the two
// server-streaming shapes.
func (g GrpcCallType) IsServerStream() bool {
	return g == GrpcCallTypeServerStreamSubscription || g == GrpcCallTypeServerStreamFinite
}

func (m *MethodSettings) isGrpcServerStream() bool {
	return m.Grpc != nil && m.Grpc.CallType.IsServerStream()
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
		m.Settings = &MethodSettings{}
	}
	if m.Settings.Grpc != nil && m.Settings.Grpc.CallType == "" {
		m.Settings.Grpc.CallType = GrpcCallTypeUnary
	}
	if m.Settings.Cacheable == nil {
		m.Settings.Cacheable = new(!m.Settings.isGrpcServerStream())
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
	return m.validateGrpcMethods()
}

var grpcMethodNamePattern = regexp.MustCompile(`^/(?:[A-Za-z_][A-Za-z0-9_]*\.)+[A-Za-z_][A-Za-z0-9_]*/[A-Za-z_][A-Za-z0-9_]*$`)

func (m *MethodSpec) validateGrpcMethods() error {
	hasGrpcConnector := slices.Contains(m.SpecData.apiConnectors, GrpcConnector)
	for _, method := range m.Methods {
		if !hasGrpcConnector {
			if method.Settings != nil && method.Settings.Grpc != nil {
				return fmt.Errorf("method '%s' has grpc settings but the spec has no grpc api connector", method.Name)
			}
			continue
		}
		if !grpcMethodNamePattern.MatchString(method.Name) {
			return fmt.Errorf("invalid grpc method name '%s', expected the '/package.Service/Method' shape", method.Name)
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
	if m.Grpc != nil {
		if err := m.Grpc.CallType.validate(); err != nil {
			return err
		}
		if m.Subscription != nil {
			return errors.New("subscription settings cannot be used with grpc methods, use grpc.call-type instead")
		}
		if m.Grpc.CallType.IsServerStream() {
			if m.Cacheable != nil && *m.Cacheable {
				return errors.New("server-stream methods cannot be cacheable")
			}
			if m.Dispatch != DispatchDefault {
				return errors.New("dispatch cannot be used with server-stream methods")
			}
			if m.Sticky != nil && (m.Sticky.SendSticky || m.Sticky.CreateSticky) {
				return errors.New("sticky cannot be used with server-stream methods")
			}
		}
	}
	return nil
}

func (g GrpcCallType) validate() error {
	switch g {
	case "", GrpcCallTypeUnary, GrpcCallTypeServerStreamSubscription, GrpcCallTypeServerStreamFinite:
		return nil
	default:
		return fmt.Errorf("unknown grpc call-type - %s", g)
	}
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

// The declaration order is significant: GetBestConnector picks the lowest
// value in default mode ("simplest") and the highest in strict mode ("most
// capable"), and that choice becomes both the head connector and the
// internal-request connector.
const (
	UnknownType ApiConnectorType = iota
	JsonRpcConnector
	TendermintConnector // Tendermint/CometBFT RPC - the same methods over JSON-RPC (POST /) and URI calls (GET /<method>)
	RestConnector
	RestIndexer // a self-contained indexer REST API next to the node API (e.g. the TON v3 indexer); a plain type - may be an upstream's only connector
	GrpcConnector
	WebsocketConnector
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
	case TendermintConnector:
		return "tendermint"
	}
	return ""
}

var apiConnectors = map[string]ApiConnectorType{
	"json-rpc":        JsonRpcConnector,
	"tendermint":      TendermintConnector,
	"rest":            RestConnector,
	"grpc":            GrpcConnector,
	"websocket":       WebsocketConnector,
	"rest-indexer":    RestIndexer,
	"rest-additional": RestAdditional,
}
var plainApiConnectorTypes = []ApiConnectorType{
	JsonRpcConnector,
	TendermintConnector,
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
