package specs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/itchyny/gojq"
	"github.com/rs/zerolog"
	"github.com/samber/lo"
)

const newValue = "$newValue"

type Method struct {
	enabled          bool
	cacheable        bool
	enforceIntegrity bool
	local            bool
	parser           *jqParser
	modifyParser     *modifyJqParser
	Subscription     *Subscription
	Sticky           *Sticky
	Name             string
}

type jqParser struct {
	returnType ParserReturnType
	query      *gojq.Query
}

type modifyJqParser struct {
	code *gojq.Code
}

func DefaultMethod(name string) *Method {
	return &Method{
		Name:      name,
		enabled:   true,
		cacheable: true,
	}
}

func MethodWithSettings(name string, settings *MethodSettings, tagParser *TagParser) *Method {
	methodData := &MethodData{
		Name:      name,
		Enabled:   lo.ToPtr(true),
		Settings:  settings,
		TagParser: tagParser,
	}

	method, err := fromMethodData(methodData)
	if err != nil {
		return nil
	}
	return method
}

func (m *Method) IsCacheable() bool {
	return m.cacheable
}

func (m *Method) ShouldEnforceIntegrity() bool {
	return m.enforceIntegrity
}

func (m *Method) Enabled() bool {
	return m.enabled
}

func (m *Method) IsLocal() bool {
	return m.local
}

func fromMethodData(methodData *MethodData) (*Method, error) {
	var parser *jqParser
	if methodData.TagParser != nil {
		jqQuery, err := gojq.Parse(methodData.TagParser.Path)
		if err != nil {
			return nil, fmt.Errorf("couldn't parse a jq path of method %s - %s", methodData.Name, err.Error())
		}
		parser = &jqParser{
			returnType: methodData.TagParser.ReturnType,
			query:      jqQuery,
		}
	}

	var sub *Subscription
	var sticky *Sticky
	var modifyParser *modifyJqParser
	cacheable := true
	local := false
	enforceIntegrity := false
	if methodData.Settings != nil {
		if methodData.Settings.Cacheable != nil {
			cacheable = *methodData.Settings.Cacheable
		}
		if methodData.Settings.Subscription != nil {
			sub = methodData.Settings.Subscription
		}
		enforceIntegrity = methodData.Settings.EnforceIntegrity
		local = methodData.Settings.Local
		if methodData.Settings.Sticky != nil {
			sticky = methodData.Settings.Sticky
			if methodData.Settings.Sticky.SendSticky && methodData.TagParser != nil {
				query := fmt.Sprintf("%s = %s", methodData.TagParser.Path, newValue)
				jqQuery, err := gojq.Parse(query)
				if err != nil {
					return nil, fmt.Errorf("cound't create a modify parser query for method %s, error - %s", methodData.Name, err.Error())
				}
				code, err := gojq.Compile(jqQuery, gojq.WithVariables([]string{newValue}))
				if err != nil {
					return nil, fmt.Errorf("cound't create a modify parser query for method %s, error - %s", methodData.Name, err.Error())
				}
				modifyParser = &modifyJqParser{
					code: code,
				}
			}
		}
	}

	return &Method{
		enabled:          lo.Ternary(methodData.Enabled == nil, true, *methodData.Enabled),
		cacheable:        cacheable,
		local:            local,
		enforceIntegrity: enforceIntegrity,
		Name:             methodData.Name,
		parser:           parser,
		modifyParser:     modifyParser,
		Sticky:           sticky,
		Subscription:     sub,
	}, nil
}

type MethodParam interface {
	param()
}

type BlockNumberParam struct { // hex number or tag
	BlockNumber rpc.BlockNumber
}

func (b *BlockNumberParam) param() {
}

type BlockRangeParam struct { // hex number or tag
	From *rpc.BlockNumber
	To   *rpc.BlockNumber
}

func (b *BlockRangeParam) param() {
}

type HashTagParam struct { // hash
	Hash string
}

func (b *HashTagParam) param() {
}

type StringParam struct { // any string value
	Value string
}

func (s *StringParam) param() {}

func (m *Method) Modify(ctx context.Context, data any, newV any) []byte {
	if m.modifyParser == nil {
		return nil
	}
	log := zerolog.Ctx(ctx)
	iter := m.modifyParser.code.Run(data, newV)
	modifiedValue, err := m.jqParse(iter)
	if err != nil {
		log.Warn().Err(err).Msgf("couldn't parse tag of method %s", m.Name)
		return nil
	}
	modifiedData, err := sonic.Marshal(modifiedValue)
	if err != nil {
		log.Warn().Err(err).Msgf("couldn't marshall a modified body %v of method %s", modifiedValue, m.Name)
		return nil
	}
	return modifiedData
}

func parseTag(ctx context.Context, name string, returnType ParserReturnType, paramAny any) MethodParam {
	log := zerolog.Ctx(ctx)
	switch param := paramAny.(type) {
	case string:
		if returnType == BlockNumberType && isHexNumberOrTag(param) {
			var num rpc.BlockNumber
			err := sonic.Unmarshal([]byte(fmt.Sprintf(`"%s"`, param)), &num)
			if err != nil {
				log.Warn().Err(err).Msgf("couldn't parse tag of method to BlockNumber %s", name)
				return nil
			}
			return &BlockNumberParam{BlockNumber: num}
		} else if returnType == BlockRefType {
			var blockNumberOrHash rpc.BlockNumberOrHash
			err := sonic.Unmarshal([]byte(fmt.Sprintf(`"%s"`, param)), &blockNumberOrHash)
			if err != nil {
				log.Warn().Err(err).Msgf("couldn't parse tag of method to BlockNumberOrHash %s", name)
				return nil
			}
			if blockNumberOrHash.BlockHash != nil {
				return &HashTagParam{Hash: blockNumberOrHash.BlockHash.String()}
			} else if blockNumberOrHash.BlockNumber != nil {
				return &BlockNumberParam{BlockNumber: *blockNumberOrHash.BlockNumber}
			}
		} else if returnType == StringType {
			return &StringParam{Value: param}
		}
		return nil
	case map[string]any:
		if returnType != BlockRangeType {
			log.Warn().Msgf("wrong return type of tag-parser - %s, expected - %s", returnType, BlockRangeType)
			return nil
		}
		var from *rpc.BlockNumber
		var to *rpc.BlockNumber
		if param["from"] != nil {
			err := sonic.Unmarshal([]byte(fmt.Sprintf(`"%s"`, param["from"])), &from)
			if err != nil {
				log.Warn().Err(err).Msgf("couldn't parse tag of method to BlockNumber %s", name)
				return nil
			}
		}
		if param["to"] != nil {
			err := sonic.Unmarshal([]byte(fmt.Sprintf(`"%s"`, param["to"])), &to)
			if err != nil {
				log.Warn().Err(err).Msgf("couldn't parse tag of method to BlockNumber %s", name)
				return nil
			}
		}
		return &BlockRangeParam{From: from, To: to}
	}
	return nil
}

func (m *Method) Parse(ctx context.Context, data any) MethodParam {
	if m.parser == nil {
		return nil
	}
	log := zerolog.Ctx(ctx)
	iter := m.parser.query.Run(data)
	methodParam, err := m.jqParse(iter)
	if err != nil {
		log.Warn().Err(err).Msgf("couldn't parse tag of method %s", m.Name)
		return nil
	}
	switch param := methodParam.(type) {
	case string:
		return parseTag(ctx, m.Name, m.parser.returnType, param)
	case map[string]any:
		if m.parser.returnType != ObjectType {
			return nil
		}
		for key, value := range param {
			tp := ParserReturnType(key)
			if tp.validate() != nil {
				panic(fmt.Sprintf("wrong return type of tag-parser - %s", tp))
			}
			return parseTag(ctx, m.Name, tp, value)
		}
	}

	return nil
}

func (m *Method) jqParse(iter gojq.Iter) (any, error) {
	for {
		param, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := param.(error); ok {
			if err != nil {
				return nil, err
			}
		} else {
			return param, nil
		}
	}
	return nil, errors.New("no parsed value")
}

func isHexNumberOrTag(param string) bool {
	return strings.HasPrefix(param, "0x") || isBlockTag(param)
}

func isBlockTag(param string) bool {
	switch param {
	case "latest", "earliest", "pending", "finalized", "safe":
		return true
	default:
		return false
	}
}

func IsBlockTagNumber(num rpc.BlockNumber) bool {
	switch num {
	case rpc.SafeBlockNumber, rpc.LatestBlockNumber, rpc.PendingBlockNumber, rpc.FinalizedBlockNumber, rpc.EarliestBlockNumber:
		return true
	default:
		return false
	}
}
