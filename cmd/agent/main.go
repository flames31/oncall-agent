package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/flames31/oncall-agent/internal/config"
	"github.com/flames31/oncall-agent/internal/investigation"
	"github.com/flames31/oncall-agent/internal/llm"
	"github.com/flames31/oncall-agent/internal/store"
	"github.com/flames31/oncall-agent/internal/tools"
	"github.com/flames31/oncall-agent/internal/webhook"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// ── Config ────────────────────────────────────────────────────────────
	cfg, err := config.Load("config.yaml")
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}
	slog.Info("config loaded", "model", cfg.GroqModel, "worker_count", cfg.WorkerCount)

	// ── Database ──────────────────────────────────────────────────────────
	db, err := store.New(cfg.PostgresDSN)
	if err != nil {
		slog.Error("db connect failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	migCtx, migCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer migCancel()
	if err := db.Migrate(migCtx); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// ── Groq client ───────────────────────────────────────────────────────
	groqClient := llm.NewClient(cfg.GroqAPIKey, cfg.GroqModel)
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()
	if err := groqClient.Ping(pingCtx); err != nil {
		slog.Error("groq ping failed", "error", err)
		os.Exit(1)
	}
	slog.Info("groq connected", "model", groqClient.Model())

	// ── Tool clients ──────────────────────────────────────────────────────
	toolSet := buildToolSet(cfg, db.DB)

	// ── Investigator ──────────────────────────────────────────────────────
	investigator := llm.NewInvestigator(groqClient, toolSet, cfg.MaxLLMIterations)

	// ── Deduplicator ─────────────────────────────────────────────────────
	dedup := investigation.NewDeduplicator(cfg.DedupWindowSeconds)

	// ── Webhook handler ───────────────────────────────────────────────────
	webhookHandler := webhook.NewHandler(
		webhook.Config{
			PagerDutySecret: cfg.PagerDutySecret,
			OnAlert: func(a webhook.Alert) {
				// Run the investigation with a 45-second deadline.
				// Slack delivery is wired in Phase 5 — for now we just log the result.
				go func() {
					ctx, cancel := context.WithTimeout(
						context.Background(),
						time.Duration(cfg.InvestigationTimeout)*time.Second,
					)
					defer cancel()

					result, err := investigator.RunInvestigation(ctx, a)
					if err != nil {
						slog.Error("investigation failed",
							"fingerprint", a.Fingerprint,
							"error", err,
						)
						return
					}

					slog.Info("investigation result",
						"fingerprint", a.Fingerprint,
						"service", a.ServiceName,
						"root_cause", result.RootCause,
						"confidence", result.Confidence,
						"iterations", result.IterationsUsed,
						"tokens", result.TokensUsed,
						"evidence_count", len(result.Evidence),
						"actions_count", len(result.RecommendedActions),
					)
				}()
			},
		},
		dedup,
	)

	// ── HTTP server ───────────────────────────────────────────────────────
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.Handle("POST /webhook", webhookHandler)
	mux.HandleFunc("POST /slack/actions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("# metrics endpoint — wired in Phase 6\n"))
	})

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-stop
	slog.Info("shutting down")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
}

// buildToolSet constructs all Phase 3 tool clients.
// Kubernetes is optional — if no cluster is available it is set to nil
// and the dispatcher returns a graceful "unavailable" message.
func buildToolSet(cfg *config.Config, db *sql.DB) *llm.ToolSet {
	var k8sClient *tools.KubernetesClient
	k8s, err := tools.NewKubernetesClient()
	if err != nil {
		slog.Warn("kubernetes client unavailable — pod status tool disabled", "error", err)
	} else {
		k8sClient = k8s
	}

	return &llm.ToolSet{
		Prometheus:  tools.NewPrometheusClient(cfg.PrometheusURL),
		Loki:        tools.NewLokiClient(cfg.LokiURL),
		Deployments: tools.NewDeploymentClient(db),
		Kubernetes:  k8sClient,
		Runbooks:    tools.NewRunbookClient(db),
	}
}
