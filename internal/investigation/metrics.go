package investigation

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// AlertsReceived counts every webhook alert that passes parsing.
	AlertsReceived = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "oncall_alerts_received_total",
			Help: "Total number of alerts received from all webhook sources.",
		},
		[]string{"source", "severity"},
	)

	// AlertsDeduplicated counts alerts dropped by the dedup window.
	AlertsDeduplicated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "oncall_alerts_deduplicated_total",
		Help: "Total number of duplicate alerts dropped.",
	})

	// AlertsDropped counts alerts dropped because the worker queue was full.
	AlertsDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "oncall_alerts_dropped_total",
		Help: "Total number of alerts dropped because the worker pool was full.",
	})

	// InvestigationsCompleted counts finished investigations by confidence.
	InvestigationsCompleted = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "oncall_investigations_completed_total",
			Help: "Total number of investigations completed.",
		},
		[]string{"confidence"},
	)

	// InvestigationDuration measures how long each investigation took.
	InvestigationDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "oncall_investigation_duration_seconds",
		Help:    "Duration of each investigation in seconds.",
		Buckets: []float64{5, 10, 15, 20, 30, 45, 60},
	})

	// LLMTokensUsed tracks token consumption per investigation.
	LLMTokensUsed = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "oncall_llm_tokens_used",
		Help:    "Number of LLM tokens consumed per investigation.",
		Buckets: []float64{500, 1000, 2000, 3000, 5000, 8000, 12000},
	})

	// WorkerQueueDepth is a gauge tracking how many jobs are waiting.
	WorkerQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "oncall_worker_queue_depth",
		Help: "Number of investigations currently waiting in the worker queue.",
	})

	// WorkerPoolActive tracks how many workers are currently investigating.
	WorkerPoolActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "oncall_worker_pool_active",
		Help: "Number of workers currently running an investigation.",
	})

	// SlackDeliveries counts Slack message delivery outcomes.
	SlackDeliveries = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "oncall_slack_deliveries_total",
			Help: "Total Slack message delivery attempts.",
		},
		[]string{"status"}, // "success" | "error"
	)
)
