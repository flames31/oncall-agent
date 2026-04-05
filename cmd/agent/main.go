package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/flames31/oncall-agent/internal/config"
	"github.com/flames31/oncall-agent/internal/llm"
	"github.com/flames31/oncall-agent/internal/store"
)

func main() {
	// Structured JSON logging — readable by both humans and log aggregators
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// ── Config ────────────────────────────────────────────────────────────
	cfg, err := config.Load("config.yaml")
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}
	slog.Info("config loaded",
		"model", cfg.GroqModel,
		"worker_count", cfg.WorkerCount,
		"dedup_window_seconds", cfg.DedupWindowSeconds,
		"max_llm_iterations", cfg.MaxLLMIterations,
	)

	// ── Database ──────────────────────────────────────────────────────────
	db, err := store.New(cfg.PostgresDSN)
	if err != nil {
		slog.Error("db connect failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	slog.Info("database connected")

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

	// ── HTTP server ───────────────────────────────────────────────────────
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Stub routes — implemented in Phase 2 (webhook) and Phase 5 (slack)
	mux.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /slack/actions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Real Prometheus metrics wired in Phase 6
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

	// ── Graceful shutdown ─────────────────────────────────────────────────
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
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}
