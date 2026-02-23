package statsdata_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/stats/statsdata"
	"github.com/stretchr/testify/assert"
)

func TestRequestStatsDataAmount(t *testing.T) {
	data := statsdata.NewRequestStatsData()

	data.AddRequest()
	data.AddRequest()
	data.AddRequest()

	assert.Equal(t, int64(3), data.GetRequestAmount())
}
