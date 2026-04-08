//go:build ignore

package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

type runbook struct {
	title   string
	content string
}

var runbooks = []runbook{
	{
		title: "High error rate after deployment",
		content: `Symptom: 5xx error rate spikes within 30 minutes of a new deployment.

Diagnosis steps:
1. Confirm a recent deploy with: kubectl rollout history deployment/<service>
2. Check pod startup logs: kubectl logs -l app=<service> --previous
3. Look for database migration errors or missing environment variables
4. Check if any new dependencies fail to connect at startup
5. Compare error patterns in Loki between old and new pod versions

Remediation:
- Rollback immediately: kubectl rollout undo deployment/<service>
- Verify rollback success: kubectl rollout status deployment/<service>
- Check that error rate returns to baseline within 2 minutes of rollback
- File a post-mortem and hold the deploy until root cause is confirmed`,
	},
	{
		title: "OOMKilled pods — memory limit exceeded",
		content: `Symptom: Pods are repeatedly restarting with OOMKilled in container status.

Diagnosis steps:
1. Confirm OOMKill: kubectl describe pod <pod> | grep -A5 "Last State"
2. Check current memory limits: kubectl get pod <pod> -o jsonpath='{.spec.containers[*].resources}'
3. Look for memory leak patterns in logs: query Loki for "OOM", "heap", "memory"
4. Check if memory usage was trending up before the kill: query Prometheus memory_usage metric
5. Identify if a recent code change touched memory allocation

Remediation:
- Immediate: increase memory limit in the deployment manifest
- kubectl set resources deployment/<service> --limits=memory=512Mi
- Long-term: profile the application with pprof to find the leak
- Add memory usage alerts at 80% of limit to get early warning next time`,
	},
	{
		title: "Database connection pool exhaustion",
		content: `Symptom: Services return 500s with "too many connections" or connection timeout errors.

Diagnosis steps:
1. Check current connection count: SELECT count(*) FROM pg_stat_activity;
2. Identify which services hold most connections: SELECT application_name, count(*) FROM pg_stat_activity GROUP BY 1 ORDER BY 2 DESC;
3. Look for long-running queries blocking connections: SELECT pid, now()-query_start, query FROM pg_stat_activity WHERE state='active';
4. Check if a recent deployment changed connection pool settings
5. Look for connection leak patterns — connections that open but never close

Remediation:
- Kill long-running blocking queries: SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE now()-query_start > interval '5 minutes';
- Restart the affected service to clear leaked connections
- Reduce max_connections in the service's database pool config
- Add PgBouncer as a connection pooler if this recurs`,
	},
	{
		title: "Kafka consumer lag — message backlog growing",
		content: `Symptom: Consumer group lag is growing, events are processed late or not at all.

Diagnosis steps:
1. Check consumer group lag: kafka-consumer-groups.sh --describe --group <group>
2. Check if consumers are alive: look for consumer heartbeat logs in Loki
3. Check partition assignment — consumers may be stuck on rebalancing
4. Look for deserialization errors in logs — bad messages can stall a partition
5. Check if producer is sending faster than consumers can process

Remediation:
- Restart consumer pods: kubectl rollout restart deployment/<consumer>
- If a bad message is stuck, skip it: set offset to current+1 for the stuck partition
- Scale up consumers: kubectl scale deployment/<consumer> --replicas=<n>
- Add dead letter queue for deserialization failures to prevent single message blocking`,
	},
	{
		title: "DNS resolution failures — service discovery broken",
		content: `Symptom: Services cannot connect to each other, logs show "no such host" or DNS timeout errors.

Diagnosis steps:
1. Test DNS from inside a pod: kubectl exec -it <pod> -- nslookup kubernetes.default
2. Check CoreDNS pods are running: kubectl get pods -n kube-system -l k8s-app=kube-dns
3. Look for CoreDNS errors: kubectl logs -n kube-system -l k8s-app=kube-dns --tail=50
4. Check if the issue is cluster-wide or specific to a namespace
5. Verify the service exists and has endpoints: kubectl get svc,endpoints <service>

Remediation:
- Restart CoreDNS: kubectl rollout restart deployment/coredns -n kube-system
- If a specific service is missing: check if the deployment and service are in the same namespace
- Check network policies are not blocking DNS (UDP port 53)
- Increase CoreDNS replicas if load is high: kubectl scale deployment/coredns --replicas=3 -n kube-system`,
	},
	{
		title: "Certificate expiry — TLS handshake failures",
		content: `Symptom: TLS handshake errors, "certificate expired" in logs, HTTPS endpoints returning 502/503.

Diagnosis steps:
1. Check certificate expiry: echo | openssl s_client -connect <host>:443 2>/dev/null | openssl x509 -noout -dates
2. Check cert-manager status: kubectl get certificates,certificaterequests -A
3. Look for cert-manager errors: kubectl logs -n cert-manager deployment/cert-manager --tail=50
4. Check if the Let's Encrypt rate limit has been hit (50 certs per domain per week)
5. Verify the Ingress resource references the correct TLS secret

Remediation:
- Force renewal: kubectl annotate certificate <name> cert-manager.io/issue-temporary-certificate="true"
- If cert-manager is broken, manually renew: certbot renew --force-renewal
- Short-term workaround: extend expiry with a self-signed cert to restore traffic
- Add a monitoring alert for certificates expiring within 14 days`,
	},
	{
		title: "Disk I/O saturation — slow reads and writes",
		content: `Symptom: Services are slow, Prometheus shows high disk I/O wait, logs mention slow queries or timeouts.

Diagnosis steps:
1. Check I/O wait: query node_cpu_seconds_total{mode="iowait"} in Prometheus
2. Identify which process is causing I/O: run iostat -x 1 10 on the affected node
3. Check Postgres: look for slow queries with pg_stat_activity and autovacuum running
4. Check if a log rotation or backup job is running and saturating the disk
5. Look for disk space: df -h — a nearly full disk causes write slowdowns

Remediation:
- Stop non-critical batch jobs competing for I/O
- If Postgres: run VACUUM ANALYZE manually and check autovacuum settings
- Consider migrating to faster storage class (SSD vs HDD)
- Add disk I/O and disk space alerts to catch saturation early`,
	},
	{
		title: "HTTP 429 — downstream rate limit hit",
		content: `Symptom: Service is receiving 429 Too Many Requests from a downstream dependency.

Diagnosis steps:
1. Identify which downstream is rate-limiting from error logs in Loki
2. Check the rate limit headers in the 429 response: Retry-After, X-RateLimit-Remaining
3. Look for a traffic spike in Prometheus that caused the downstream to throttle
4. Check if a retry loop is amplifying requests — retries on 5xx should not apply to 429
5. Check if a recent deployment increased the call rate to the downstream

Remediation:
- Immediately add exponential backoff with jitter on 429 responses
- Implement a token bucket or client-side rate limiter for the downstream call
- Contact the downstream provider to increase limits if request volume is legitimate
- Add a circuit breaker to stop hammering the downstream when it is rate limiting`,
	},
}

func main() {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://oncall:oncall@localhost:5432/oncall?sslmode=disable"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("cannot connect to database: %v", err)
	}

	ctx := context.Background()
	inserted := 0

	for _, rb := range runbooks {
		result, err := db.ExecContext(ctx, `
			INSERT INTO runbooks (title, content)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, rb.title, rb.content)
		if err != nil {
			log.Printf("failed to insert %q: %v", rb.title, err)
			continue
		}
		rows, _ := result.RowsAffected()
		if rows > 0 {
			inserted++
			fmt.Printf("✓ inserted: %s\n", rb.title)
		} else {
			fmt.Printf("- skipped (already exists): %s\n", rb.title)
		}
	}

	fmt.Printf("\nDone. Inserted %d/%d runbooks.\n", inserted, len(runbooks))

	// Show current runbook count
	var count int
	db.QueryRowContext(ctx, "SELECT count(*) FROM runbooks").Scan(&count)
	fmt.Printf("Total runbooks in database: %d\n", count)
}
