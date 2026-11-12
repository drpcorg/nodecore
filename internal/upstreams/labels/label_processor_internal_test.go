package labels

import (
	"context"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
)

type staticLabelsDetector map[string]string

func (s staticLabelsDetector) DetectLabels(context.Context) map[string]string {
	return s
}

func TestDetectLabelsAndSendDropsVerdictOfCancelledRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	labelsChan := make(chan lo.Tuple2[string, string], 1)

	// a detector that maps a failed request to a label reports a cancellation as one
	detectLabelsAndSend(ctx, staticLabelsDetector{"archive": "false"}, labelsChan)

	assert.Empty(t, labelsChan)
}

func TestDetectLabelsAndSendPublishesVerdictOfLiveRun(t *testing.T) {
	labelsChan := make(chan lo.Tuple2[string, string], 1)

	detectLabelsAndSend(context.Background(), staticLabelsDetector{"archive": "false"}, labelsChan)

	assert.Equal(t, lo.T2("archive", "false"), <-labelsChan)
}
