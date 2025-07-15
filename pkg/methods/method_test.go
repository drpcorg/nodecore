package specs_test

import (
	"context"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestDefaultMethod(t *testing.T) {
	method := specs.DefaultMethod("methodName")

	assert.Equal(t, "methodName", method.Name)
	assert.True(t, method.Enabled())
	assert.True(t, method.IsCacheable())
	assert.Nil(t, method.Parse(context.Background(), ""))
}

func TestParseBlockNumberArray(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/parsers")
	err := specs.Load()
	assert.NoError(t, err)

	spec := specs.GetSpecMethods("test")

	tests := []struct {
		name     string
		data     any
		expected rpc.BlockNumber
	}{
		{
			data:     []any{"hello", false, "0x5"},
			name:     "real number - 0x5",
			expected: rpc.BlockNumber(5),
		},
		{
			data:     []any{"hello", false, "earliest"},
			name:     "earliest",
			expected: rpc.EarliestBlockNumber,
		},
		{
			data:     []any{"hello", false, "latest"},
			name:     "latest",
			expected: rpc.LatestBlockNumber,
		},
		{
			data:     []any{"hello", false, "pending"},
			name:     "pending",
			expected: rpc.PendingBlockNumber,
		},
		{
			data:     []any{"hello", false, "finalized"},
			name:     "finalized",
			expected: rpc.FinalizedBlockNumber,
		},
		{
			data:     []any{"hello", false, "safe"},
			name:     "safe",
			expected: rpc.SafeBlockNumber,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			method := spec[specs.DefaultMethodGroup]["test"]

			result := method.Parse(context.Background(), test.data)

			assert.IsType(te, &specs.BlockNumberParam{}, result)
			assert.Equal(te, test.expected, result.(*specs.BlockNumberParam).BlockNumber)
		})
	}
}

func TestParseBlockNumberObject(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/parsers")
	err := specs.Load()
	assert.NoError(t, err)

	spec := specs.GetSpecMethods("test")

	method := spec[specs.DefaultMethodGroup]["call"]

	result := method.Parse(context.Background(), []any{112, map[string]interface{}{"from": "0x2"}})

	assert.IsType(t, &specs.BlockNumberParam{}, result)
	assert.Equal(t, rpc.BlockNumber(2), result.(*specs.BlockNumberParam).BlockNumber)
}

func TestParseBlockRef(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/parsers")
	err := specs.Load()
	assert.NoError(t, err)

	spec := specs.GetSpecMethods("test")

	tests := []struct {
		name         string
		data         any
		expected     any
		paramType    specs.MethodParam
		actualResult func(m specs.MethodParam) any
	}{
		{
			data:      []any{"0x5"},
			name:      "real number - 0x5",
			expected:  rpc.BlockNumber(5),
			paramType: &specs.BlockNumberParam{},
			actualResult: func(m specs.MethodParam) any {
				return m.(*specs.BlockNumberParam).BlockNumber
			},
		},
		{
			data:      []any{"earliest"},
			name:      "earliest",
			expected:  rpc.EarliestBlockNumber,
			paramType: &specs.BlockNumberParam{},
			actualResult: func(m specs.MethodParam) any {
				return m.(*specs.BlockNumberParam).BlockNumber
			},
		},
		{
			data:      []any{"latest"},
			name:      "latest",
			expected:  rpc.LatestBlockNumber,
			paramType: &specs.BlockNumberParam{},
			actualResult: func(m specs.MethodParam) any {
				return m.(*specs.BlockNumberParam).BlockNumber
			},
		},
		{
			data:      []any{"pending"},
			name:      "pending",
			expected:  rpc.PendingBlockNumber,
			paramType: &specs.BlockNumberParam{},
			actualResult: func(m specs.MethodParam) any {
				return m.(*specs.BlockNumberParam).BlockNumber
			},
		},
		{
			data:      []any{"finalized"},
			name:      "finalized",
			expected:  rpc.FinalizedBlockNumber,
			paramType: &specs.BlockNumberParam{},
			actualResult: func(m specs.MethodParam) any {
				return m.(*specs.BlockNumberParam).BlockNumber
			},
		},
		{
			data:      []any{"safe"},
			name:      "safe",
			expected:  rpc.SafeBlockNumber,
			paramType: &specs.BlockNumberParam{},
			actualResult: func(m specs.MethodParam) any {
				return m.(*specs.BlockNumberParam).BlockNumber
			},
		},
		{
			data:      []any{"0xe0594250efac73640aeff78ec40aaaaa87f91edb54e5af926ee71a32ef32da34"},
			name:      "hash",
			expected:  "0xe0594250efac73640aeff78ec40aaaaa87f91edb54e5af926ee71a32ef32da34",
			paramType: &specs.HashTagParam{},
			actualResult: func(m specs.MethodParam) any {
				return m.(*specs.HashTagParam).Hash
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			method := spec[specs.DefaultMethodGroup]["call_1"]

			result := method.Parse(context.Background(), test.data)

			assert.IsType(te, test.paramType, result)
			assert.Equal(te, test.expected, test.actualResult(result))
		})
	}
}

func TestParseStringValue(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/parsers")
	err := specs.Load()
	assert.NoError(t, err)

	spec := specs.GetSpecMethods("test")
	method := spec[specs.DefaultMethodGroup]["eth_uninstallFilter"]

	result := method.Parse(context.Background(), []any{"testValue"})

	assert.IsType(t, &specs.StringParam{}, result)
	assert.Equal(t, "testValue", result.(*specs.StringParam).Value)
}

func TestModifyValue(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/parsers")
	err := specs.Load()
	assert.NoError(t, err)

	spec := specs.GetSpecMethods("test")
	method := spec[specs.DefaultMethodGroup]["eth_uninstallFilter"]
	input := []any{"oldVal"}
	newVal := "newVal"

	result := method.Modify(context.Background(), input, newVal)

	assert.Equal(t, `["newVal"]`, string(result))
}

func TestUnableParseBlockNumberThenNil(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/parsers")
	err := specs.Load()
	assert.NoError(t, err)

	spec := specs.GetSpecMethods("test")
	data := []any{"hello", false, "wrongNumber"}
	method := spec[specs.DefaultMethodGroup]["test"]

	result := method.Parse(context.Background(), data)

	assert.Nil(t, result)
}

func TestUnableParseBlockRefThenNil(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/parsers")
	err := specs.Load()
	assert.NoError(t, err)

	spec := specs.GetSpecMethods("test")
	data := []any{"wrongNumber"}
	method := spec[specs.DefaultMethodGroup]["call_1"]

	result := method.Parse(context.Background(), data)

	assert.Nil(t, result)
}

func TestBlockTag(t *testing.T) {
	tests := []struct {
		name     string
		num      rpc.BlockNumber
		expected bool
	}{
		{
			"latest",
			rpc.LatestBlockNumber,
			true,
		},
		{
			"finalized",
			rpc.FinalizedBlockNumber,
			true,
		},
		{
			"safe",
			rpc.SafeBlockNumber,
			true,
		},
		{
			"earliest",
			rpc.EarliestBlockNumber,
			true,
		},
		{
			"pending",
			rpc.PendingBlockNumber,
			true,
		},
		{
			"common num",
			rpc.BlockNumber(12334),
			false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			assert.Equal(te, test.expected, specs.IsBlockTagNumber(test.num))
		})
	}
}
