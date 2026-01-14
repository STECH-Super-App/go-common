// Package metrics provides Prometheus metrics utilities.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

var (
	// Registry is the default prometheus registry.
	Registry *prometheus.Registry
)

func init() {
	Registry = prometheus.NewRegistry()
	Registry.MustRegister(collectors.NewGoCollector())
	Registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
}

// Register registers a collector with the default registry.
func Register(c prometheus.Collector) error {
	return Registry.Register(c)
}

// MustRegister registers a collector with the default registry and panics on error.
func MustRegister(c prometheus.Collector) {
	Registry.MustRegister(c)
}

// NewCounter creates a new Counter metric.
func NewCounter(name, help string, constLabels map[string]string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{
		Name:        name,
		Help:        help,
		ConstLabels: constLabels,
	})
}

// NewGauge creates a new Gauge metric.
func NewGauge(name, help string, constLabels map[string]string) prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        name,
		Help:        help,
		ConstLabels: constLabels,
	})
}

// NewHistogram creates a new Histogram metric.
func NewHistogram(name, help string, buckets []float64, constLabels map[string]string) prometheus.Histogram {
	return prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:        name,
		Help:        help,
		Buckets:     buckets,
		ConstLabels: constLabels,
	})
}
