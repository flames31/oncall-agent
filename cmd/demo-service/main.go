package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	broken int32 // 1 = returning 500s, 0 = healthy

	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests by status code.",
	}, []string{"service", "status"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency.",
		Buckets: prometheus.DefBuckets,
	}, []string{"service"})

	// Memory held by /leak endpoint — grows until OOM
	leakedMemory [][]byte
)

const serviceName = "demo-service"

func main() {
	mux := http.NewServeMux()

	// Healthy endpoint
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() {
			requestDuration.WithLabelValues(serviceName).Observe(
				time.Since(start).Seconds())
		}()

		if atomic.LoadInt32(&broken) == 1 {
			// When broken: 80% of requests return 500
			if rand.Float32() < 0.80 {
				requestsTotal.WithLabelValues(serviceName, "500").Inc()
				http.Error(w, "internal server error", http.StatusInternalServerError)
				log.Printf("ERROR request failed: simulated application error")
				return
			}
		}

		// Add artificial latency when broken to also trigger HighLatency rule
		if atomic.LoadInt32(&broken) == 1 {
			time.Sleep(time.Duration(rand.Intn(600)+400) * time.Millisecond)
		}

		requestsTotal.WithLabelValues(serviceName, "200").Inc()
		fmt.Fprintln(w, "ok")
	})

	// Break the service — starts returning 500s
	mux.HandleFunc("/break", func(w http.ResponseWriter, r *http.Request) {
		atomic.StoreInt32(&broken, 1)
		log.Printf("WARN service is now broken — returning 500s")
		fmt.Fprintln(w, "service broken")
	})

	// Fix the service
	mux.HandleFunc("/fix", func(w http.ResponseWriter, r *http.Request) {
		atomic.StoreInt32(&broken, 0)
		log.Printf("INFO service restored to healthy state")
		fmt.Fprintln(w, "service fixed")
	})

	// Leak memory to simulate OOM
	mux.HandleFunc("/leak", func(w http.ResponseWriter, r *http.Request) {
		// Allocate 50MB each call
		chunk := make([]byte, 50*1024*1024)
		for i := range chunk {
			chunk[i] = byte(i)
		}
		leakedMemory = append(leakedMemory, chunk)
		log.Printf("WARN memory leak: allocated 50MB, total leaked chunks: %d", len(leakedMemory))
		fmt.Fprintf(w, "leaked 50MB — total chunks: %d\n", len(leakedMemory))
	})

	// Background traffic generator — keeps metrics flowing
	// so Prometheus always has data to evaluate rules against
	go generateTraffic()

	mux.Handle("/metrics", promhttp.Handler())

	log.Println("demo-service listening on :9001")
	log.Fatal(http.ListenAndServe(":9001", mux))
}

// generateTraffic sends a request to itself every 500ms.
// This keeps the rate metrics populated so alert rules fire quickly.
func generateTraffic() {
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		resp, err := client.Get("http://localhost:9001/")
		if err == nil {
			resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}
}
