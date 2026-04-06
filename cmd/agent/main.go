package main

import (
	"context"
	"database/sql"
	"fmt"
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
	"github.com/flames31/oncall-agent/internal/report"
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
	slog.Info("config loaded", "model", cfg.GroqModel, "channel", cfg.SlackChannel)

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

	// ── Groq ──────────────────────────────────────────────────────────────
	groqClient := llm.NewClient(cfg.GroqAPIKey, cfg.GroqModel)
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()
	if err := groqClient.Ping(pingCtx); err != nil {
		slog.Error("groq ping failed", "error", err)
		os.Exit(1)
	}
	slog.Info("groq connected", "model", groqClient.Model())

	// ── Slack ─────────────────────────────────────────────────────────────
	slackClient := report.NewSlackClient(cfg.SlackBotToken, cfg.SlackChannel)

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
				go func() {
					start := time.Now()

					ctx, cancel := context.WithTimeout(
						context.Background(),
						time.Duration(cfg.InvestigationTimeout)*time.Second,
					)
					defer cancel()

					// Run the investigation
					result, err := investigator.RunInvestigation(ctx, a)
					if err != nil {
						slog.Error("investigation failed",
							"fingerprint", a.Fingerprint,
							"error", err,
						)
						return
					}

					// Format and post to Slack
					r := report.Build(result, a, start)

					postCtx, postCancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer postCancel()

					ts, err := slackClient.PostReport(postCtx, r)
					if err != nil {
						slog.Error("slack post failed",
							"fingerprint", a.Fingerprint,
							"error", err,
						)
						return
					}

					slog.Info("report posted to slack",
						"fingerprint", a.Fingerprint,
						"service", a.ServiceName,
						"confidence", result.Confidence,
						"ts", ts,
						"duration_ms", time.Since(start).Milliseconds(),
					)

					// Store feedback record (correct = nil = not yet reviewed)
					fbCtx, fbCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer fbCancel()
					if err := db.WriteFeedback(fbCtx, store.FeedbackEntry{
						AlertFingerprint: a.Fingerprint,
						ReportJSON:       result,
						Correct:          nil,
					}); err != nil {
						slog.Warn("feedback write failed", "error", err)
					}
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

	// Feedback handler — Slack button clicks
	mux.HandleFunc("POST /slack/actions", func(w http.ResponseWriter, r *http.Request) {
		// Acknowledge immediately — Slack requires a response within 3 seconds
		w.WriteHeader(http.StatusOK)

		actionID, fingerprint, err := report.ParseFeedback(r)
		if err != nil {
			slog.Warn("feedback parse failed", "error", err)
			return
		}

		slog.Info("feedback received",
			"action", actionID,
			"fingerprint", fingerprint,
		)

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			correct := actionID == "feedback_correct"
			if err := db.WriteFeedback(ctx, store.FeedbackEntry{
				AlertFingerprint: fingerprint,
				ReportJSON:       map[string]string{"action": actionID},
				Correct:          &correct,
			}); err != nil {
				slog.Error("feedback write failed", "error", err)
				return
			}

			// On confirmation, upsert the root cause as a new runbook
			// so future similar alerts benefit from this finding
			if correct {
				title := fmt.Sprintf("Confirmed: %s on %s", fingerprint, time.Now().Format("2006-01-02"))
				content := fmt.Sprintf("Alert fingerprint: %s was confirmed correct by on-call engineer on %s.",
					fingerprint, time.Now().UTC().Format(time.RFC3339))

				if err := db.UpsertRunbook(ctx, title, content); err != nil {
					slog.Warn("runbook upsert failed", "error", err)
				} else {
					slog.Info("runbook upserted from confirmed feedback", "fingerprint", fingerprint)
				}
			}
		}()
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

func buildToolSet(cfg *config.Config, db *sql.DB) *llm.ToolSet {
	var k8sClient *tools.KubernetesClient
	k8s, err := tools.NewKubernetesClient()
	if err != nil {
		slog.Warn("kubernetes unavailable", "error", err)
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
