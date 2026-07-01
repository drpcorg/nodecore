package utils_test

import (
	"testing"
	"unicode/utf8"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeMetricLabel_ValidUnchanged(t *testing.T) {
	for _, s := range []string{"", "GET#/v2/status", "POST#/wallet/getblock", "юникод"} {
		assert.Equal(t, s, utils.SanitizeMetricLabel(s))
	}
}

func TestSanitizeMetricLabel_InvalidBecomesValid(t *testing.T) {
	// The exact byte sequence that crashed nodecore in production:
	// a REST request method "GET#/" followed by the invalid byte 0xC0.
	bad := "GET#/" + string([]byte{0xc0})
	require.False(t, utf8.ValidString(bad))

	got := utils.SanitizeMetricLabel(bad)
	assert.True(t, utf8.ValidString(got))
	assert.Equal(t, "GET#/�", got)
}

// TestSanitizeMetricLabel_PrometheusDoesNotPanic is the regression test for the
// crash: client_golang panics on invalid-UTF-8 label values, so the sanitized
// value must be accepted by a CounterVec.
func TestSanitizeMetricLabel_PrometheusDoesNotPanic(t *testing.T) {
	counter := prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "sanitize_metric_label_test_total"},
		[]string{"method"},
	)
	bad := "GET#/" + string([]byte{0xc0})

	assert.NotPanics(t, func() {
		counter.WithLabelValues(utils.SanitizeMetricLabel(bad)).Inc()
	})
}
