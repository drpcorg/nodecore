package utils_test

import (
	"testing"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
)

type testInterface interface {
	test()
}

type testStruct struct {
	id string
}

func (t *testStruct) test() {}

func TestCmapSimpleValue(t *testing.T) {
	cmap := utils.NewCMap[string, testStruct]()

	val, ok := cmap.Load("key")
	assert.False(t, ok)
	assert.Equal(t, testStruct{}, val)

	newVal, ok := cmap.LoadOrStore("key", testStruct{id: "name"})
	assert.False(t, ok)
	assert.Equal(t, testStruct{id: "name"}, newVal)

	newVal, ok = cmap.LoadOrStore("key", testStruct{id: "name"})
	assert.True(t, ok)
	assert.Equal(t, testStruct{id: "name"}, newVal)
}

func TestCmapInterface(t *testing.T) {
	cmap := utils.NewCMap[string, testInterface]()

	val, ok := cmap.Load("key")
	assert.False(t, ok)
	assert.Nil(t, val)

	newVal, ok := cmap.LoadOrStore("key", &testStruct{id: "name"})
	assert.False(t, ok)
	assert.Equal(t, &testStruct{id: "name"}, newVal)

	newVal, ok = cmap.LoadOrStore("key", &testStruct{id: "name"})
	assert.True(t, ok)
	assert.Equal(t, &testStruct{id: "name"}, newVal)
}

func TestCmapSimplePointer(t *testing.T) {
	cmap := utils.NewCMap[string, *testStruct]()

	val, ok := cmap.Load("key")
	assert.False(t, ok)
	assert.Nil(t, val)

	newVal, ok := cmap.LoadOrStore("key", &testStruct{id: "name"})
	assert.False(t, ok)
	assert.Equal(t, &testStruct{id: "name"}, newVal)

	newVal, ok = cmap.LoadOrStore("key", &testStruct{id: "name"})
	assert.True(t, ok)
	assert.Equal(t, &testStruct{id: "name"}, newVal)
}
