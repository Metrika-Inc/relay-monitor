package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	GetParentHash = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "relay_monitor",
		Name:      "get_parent",
		Help:      "Histogram for time to get the parent hash",
		Buckets:   prometheus.DefBuckets,
	})

	GetProposerPubKey = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "relay_monitor",
		Name:      "get_proposer_pub_key",
		Help:      "Histogram for time to get the proposer public key",
		Buckets:   prometheus.DefBuckets,
	})

	GetBid = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "relay_monitor",
		Name:      "get_bid",
		Help:      "Histogram for time to get the bid",
		Buckets:   prometheus.DefBuckets,
	})

	IsReady = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "relay_monitor",
		Name:      "ready",
		Help:      "Gauge if the instance is ready",
	})
)
