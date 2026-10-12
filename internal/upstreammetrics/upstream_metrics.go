// Package upstreammetrics is the one list of the metrics that carry an
// "upstream" label. An upstream can be removed while nodecore runs, and its
// series must go with it: a gauge would keep reporting its last state forever.
//
// A metric with an "upstream" label is added to the list where it is defined,
// with MustRegister in place of prometheus.MustRegister. Forget then covers it
// without anyone having to remember it at the place where upstreams are removed.
package upstreammetrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Vec is a metric vector that can drop the series of one upstream. Every
// prometheus *Vec type is one.
type Vec interface {
	prometheus.Collector
	DeletePartialMatch(labels prometheus.Labels) int
}

const upstreamLabel = "upstream"

var (
	mu   sync.Mutex
	vecs []Vec
)

// MustRegister registers the vectors with the default Prometheus registry and
// adds them to the list Forget works on.
func MustRegister(perUpstream ...Vec) {
	for _, vec := range perUpstream {
		prometheus.MustRegister(vec)
	}
	Track(perUpstream...)
}

// Track adds vectors to the list without registering them with Prometheus.
func Track(perUpstream ...Vec) {
	mu.Lock()
	defer mu.Unlock()
	vecs = append(vecs, perUpstream...)
}

// Forget drops every series of the upstream from every listed metric. Upstream
// ids are unique in a config, so the id alone identifies the series.
func Forget(upstreamId string) {
	mu.Lock()
	defer mu.Unlock()
	for _, vec := range vecs {
		vec.DeletePartialMatch(prometheus.Labels{upstreamLabel: upstreamId})
	}
}
