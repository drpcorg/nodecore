package utils_test

import (
	"sync"
	"sync/atomic"
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

func TestCmapLoadOrStoreLazyBuildsOnMiss(t *testing.T) {
	cmap := utils.NewCMap[string, *testStruct]()

	var calls int
	built := &testStruct{id: "name"}
	val, loaded := cmap.LoadOrStoreLazy("key", func() *testStruct {
		calls++
		return built
	})

	assert.False(t, loaded)
	assert.Same(t, built, val)
	assert.Equal(t, 1, calls)
}

func TestCmapLoadOrStoreLazySkipsBuildOnHit(t *testing.T) {
	cmap := utils.NewCMap[string, *testStruct]()

	existing := &testStruct{id: "name"}
	cmap.Store("key", existing)

	var calls int
	val, loaded := cmap.LoadOrStoreLazy("key", func() *testStruct {
		calls++
		return &testStruct{id: "other"}
	})

	assert.True(t, loaded)
	assert.Same(t, existing, val)
	assert.Equal(t, 0, calls)
}

func TestCmapLoadOrStoreLazyConcurrent(t *testing.T) {
	cmap := utils.NewCMap[string, *testStruct]()

	const goroutines = 64
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(goroutines)

	var builds atomic.Int32
	results := make([]*testStruct, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer done.Done()
			start.Wait()
			results[idx], _ = cmap.LoadOrStoreLazy("key", func() *testStruct {
				builds.Add(1)
				return &testStruct{id: "name"}
			})
		}(i)
	}

	start.Done()
	done.Wait()

	// Every goroutine observes the same single stored value.
	stored, ok := cmap.Load("key")
	assert.True(t, ok)
	for _, r := range results {
		assert.Same(t, stored, r)
	}
	// valueFn may run more than once during the first-time race, but at least once.
	assert.GreaterOrEqual(t, builds.Load(), int32(1))
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
