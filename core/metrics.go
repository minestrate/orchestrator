package core

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricServersActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "minestrate_servers_active",
		Help: "Number of servers currently in non-stopped state.",
	})

	metricPoolUtilization = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "minestrate_pool_utilization",
		Help: "Worker pool saturation (current jobs / max workers). 0.0–1.0.",
	})

	metricSpawnDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "minestrate_spawn_duration_seconds",
		Help:    "Container start time distribution (job start to container running).",
		Buckets: []float64{0.5, 1, 2, 5, 10, 15, 30},
	})

	metricPortPoolFree = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "minestrate_port_pool_free",
		Help: "Number of free ports in the pool.",
	})
)

// updateMetrics refreshes all gauge metrics from the orchestrator state.
func (o *Orchestrator) updateMetrics() {
	_, active, free, _ := o.Metrics()
	metricServersActive.Set(float64(active))
	metricPortPoolFree.Set(float64(free))

	workers := o.cfg.Orchestrator.Workers
	if workers > 0 {
		queueDepth := len(o.jobQueue)
		metricPoolUtilization.Set(float64(queueDepth) / float64(workers))
	}
}

// observeSpawn records a container spawn duration observation.
func observeSpawn(durationSeconds float64) {
	metricSpawnDuration.Observe(durationSeconds)
}
