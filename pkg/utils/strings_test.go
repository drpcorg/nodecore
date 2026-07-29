package utils_test

import (
	"testing"
	"unicode/utf8"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
)

func TestToValidUTF8KeepsValidInputAsIs(t *testing.T) {
	for _, valid := range []string{"", "eth_call", "GET#/v2/status", "юникод", "🚀_method"} {
		assert.Equal(t, valid, utils.ToValidUTF8(valid))
	}
}

func TestToValidUTF8ReplacesInvalidBytes(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected string
	}{
		"lone continuation byte":  {input: "GET#/status" + string([]byte{0x80}), expected: "GET#/status�"},
		"the production crash":    {input: "GET#/" + string([]byte{0xc0}), expected: "GET#/�"},
		"invalid byte":            {input: string([]byte{0xff}), expected: "�"},
		"truncated two-byte rune": {input: "eth_" + string([]byte{0xc3}), expected: "eth_�"},
		"truncated four-byte":     {input: string([]byte{0xf0, 0x9f, 0x98}), expected: "�"},
		"utf-16 surrogate":        {input: "eth_" + string([]byte{0xed, 0xa0, 0x80}) + "call", expected: "eth_�call"},
		"overlong slash":          {input: "GET#" + string([]byte{0xc0, 0xaf}), expected: "GET#�"},
		"invalid inside valid":    {input: "юни" + string([]byte{0x80}) + "код", expected: "юни�код"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			result := utils.ToValidUTF8(test.input)

			assert.Equal(t, test.expected, result)
			assert.True(t, utf8.ValidString(result))
		})
	}
}

func TestToValidUTF8CollapsesEachInvalidRunIntoOneReplacement(t *testing.T) {
	result := utils.ToValidUTF8("a" + string([]byte{0x80, 0x81, 0xfe, 0xff}) + "b")

	assert.Equal(t, "a�b", result)
	assert.True(t, utf8.ValidString(result))
}
