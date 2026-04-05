package tools

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// DeploymentClient queries the deployments table.
type DeploymentClient struct {
	db *sql.DB
}

func NewDeploymentClient(db *sql.DB) *DeploymentClient {
	return &DeploymentClient{db: db}
}

// Deployment is a single deploy event from the store.
type Deployment struct {
	ID            int64
	Service       string
	Version       string
	DeployedAt    time.Time
	CommitSHA     string
	CommitMessage string
	// RecentDeploy is true if this deploy happened within 30 min of the alert.
	RecentDeploy bool
}

// GetRecentDeployments returns deployments for the given service in the
// last lookbackHours. Deploys within 30 min of alertTime are flagged.
func (c *DeploymentClient) GetRecentDeployments(
	ctx context.Context,
	service string,
	alertTime time.Time,
	lookbackHours int,
) (string, error) {
	since := alertTime.Add(-time.Duration(lookbackHours) * time.Hour)

	const q = `
		SELECT id, service, version, deployed_at, 
		       COALESCE(commit_sha, ''), COALESCE(commit_message, '')
		FROM   deployments
		WHERE  service = $1
		  AND  deployed_at >= $2
		ORDER  BY deployed_at DESC
		LIMIT  20
	`

	rows, err := c.db.QueryContext(ctx, q, service, since)
	if err != nil {
		return "", fmt.Errorf("querying deployments: %w", err)
	}
	defer rows.Close()

	var deploys []Deployment
	for rows.Next() {
		var d Deployment
		if err := rows.Scan(
			&d.ID, &d.Service, &d.Version, &d.DeployedAt,
			&d.CommitSHA, &d.CommitMessage,
		); err != nil {
			return "", fmt.Errorf("scanning deployment row: %w", err)
		}
		// Flag deploys within 30 minutes of the alert as high-signal
		d.RecentDeploy = alertTime.Sub(d.DeployedAt) <= 30*time.Minute &&
			d.DeployedAt.Before(alertTime)
		deploys = append(deploys, d)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterating deployments: %w", err)
	}

	return formatDeployments(service, deploys, lookbackHours, alertTime), nil
}

func formatDeployments(service string, deploys []Deployment, hours int, alertTime time.Time) string {
	if len(deploys) == 0 {
		return fmt.Sprintf(
			"No deployments found for service %q in the last %d hours. "+
				"Deployment is unlikely to be the root cause.",
			service, hours,
		)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Deployments for service %q in the last %d hours (%d found):\n\n",
		service, hours, len(deploys))

	for _, d := range deploys {
		age := alertTime.Sub(d.DeployedAt).Round(time.Minute)
		flag := ""
		if d.RecentDeploy {
			flag = " *** HIGH SIGNAL: deployed within 30 min of alert ***"
		}

		fmt.Fprintf(&b, "  - Version %s deployed %s ago (at %s)%s\n",
			d.Version,
			age,
			d.DeployedAt.UTC().Format("15:04 UTC"),
			flag,
		)
		if d.CommitMessage != "" {
			fmt.Fprintf(&b, "    Commit: %s\n", d.CommitMessage)
		}
		if d.CommitSHA != "" {
			short := d.CommitSHA
			if len(short) > 8 {
				short = short[:8]
			}
			fmt.Fprintf(&b, "    SHA: %s\n", short)
		}
		b.WriteString("\n")
	}

	return b.String()
}
