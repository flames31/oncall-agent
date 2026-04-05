//go:build ignore

package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"

	"github.com/flames31/oncall-agent/internal/tools"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prometheusURL := envOrDefault("PROMETHEUS_URL", "http://localhost:9090")
	lokiURL := envOrDefault("LOKI_URL", "http://localhost:3100")
	postgresDSN := envOrDefault("POSTGRES_DSN",
		"postgres://oncall:oncall@localhost:5432/oncall?sslmode=disable")

	// ── Prometheus ────────────────────────────────────────────────────────
	fmt.Println("=== Prometheus ===")
	prom := tools.NewPrometheusClient(prometheusURL)
	result, err := prom.QueryNamed(ctx, "error_rate", "demo-service", 30)
	printResult("error_rate", result, err)

	result, err = prom.QueryNamed(ctx, "latency_p99", "demo-service", 30)
	printResult("latency_p99", result, err)

	// ── Loki ──────────────────────────────────────────────────────────────
	fmt.Println("\n=== Loki ===")
	loki := tools.NewLokiClient(lokiURL)
	result, err = loki.QueryLogs(ctx, "demo-service", time.Now(), 5)
	printResult("loki logs", result, err)

	// ── Deployments ───────────────────────────────────────────────────────
	fmt.Println("\n=== Deployments ===")
	db, err := sql.Open("postgres", postgresDSN)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Seed a test deployment so the query returns something
	_, _ = db.ExecContext(ctx, `
		INSERT INTO deployments (service, version, deployed_at, commit_sha, commit_message)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT DO NOTHING`,
		"demo-service", "v1.2.3", time.Now().Add(-15*time.Minute),
		"abc12345", "fix: reduce memory allocation in request handler",
	)

	depClient := tools.NewDeploymentClient(db)
	result, err = depClient.GetRecentDeployments(ctx, "demo-service", time.Now(), 2)
	printResult("deployments", result, err)

	// ── Runbooks ──────────────────────────────────────────────────────────
	fmt.Println("\n=== Runbook Search ===")
	// Seed a test runbook
	_, _ = db.ExecContext(ctx, `
		INSERT INTO runbooks (title, content)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING`,
		"High error rate after deployment",
		"When error rate spikes after a deploy, immediately check the deployment logs. "+
			"Roll back with: kubectl rollout undo deployment/<service>. "+
			"Check for database migration issues or misconfigured environment variables.",
	)

	rbClient := tools.NewRunbookClient(db)
	result, err = rbClient.Search(ctx, "high error rate deploy rollback", 3)
	printResult("runbook search", result, err)

	fmt.Println("\n=== All tool tests complete ===")
	fmt.Println("Note: Kubernetes tool requires a running cluster — test manually.")
}

func printResult(name, result string, err error) {
	if err != nil {
		fmt.Printf("[%s] ERROR: %v\n", name, err)
		return
	}
	fmt.Printf("[%s]\n%s\n", name, result)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
