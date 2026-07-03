package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Worker struct {
	db         *pgxpool.Pool
	logger     *slog.Logger
	httpClient HTTPClient
}

func New(db *pgxpool.Pool, logger *slog.Logger) *Worker {
	return &Worker{
		db:         db,
		logger:     logger,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

type channelConfig struct {
	Type       string `json:"type"`
	WebhookURL string `json:"webhook_url"`
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.processNextBatch(ctx); err != nil {
				w.logger.Error("Failed to process batch", "err", err)
			}
		}
	}
}

func (w *Worker) processNextBatch(ctx context.Context) error {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	query := `
		SELECT e.id, e.message, r.channel_config
		FROM alert_events e
		JOIN alert_rules r ON e.rule_id = r.id
		WHERE e.status = 'pending'
		ORDER BY e.created_at ASC
		FOR UPDATE OF e SKIP LOCKED
		LIMIT 10;
	`

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("query picked events: %w", err)
	}
	defer rows.Close()

	type job struct {
		ID            string
		Message       string
		ChannelConfig []byte
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.ID, &j.Message, &j.ChannelConfig); err != nil {
			return fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows error: %w", err)
	}
	rows.Close() // Ensure closed before updating

	for _, j := range jobs {
		err := w.processJob(ctx, j.Message, j.ChannelConfig)
		
		status := "delivered"
		var errorMsg *string
		if err != nil {
			status = "failed"
			eMsg := err.Error()
			errorMsg = &eMsg
			w.logger.Error("Failed to deliver alert", "event_id", j.ID, "err", err)
		} else {
			w.logger.Info("Alert delivered", "event_id", j.ID)
		}

		_, updateErr := tx.Exec(ctx, `
			UPDATE alert_events 
			SET status = $1, error_message = $2, delivered_at = NOW() 
			WHERE id = $3`,
			status, errorMsg, j.ID,
		)
		if updateErr != nil {
			w.logger.Error("Failed to update event status", "event_id", j.ID, "err", updateErr)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

func (w *Worker) processJob(ctx context.Context, message string, rawConfig []byte) error {
	var cfg channelConfig
	if len(rawConfig) > 0 {
		if err := json.Unmarshal(rawConfig, &cfg); err != nil {
			return fmt.Errorf("invalid channel config JSON: %w", err)
		}
	}

	if cfg.Type == "slack" {
		if cfg.WebhookURL == "" {
			return errors.New("slack webhook_url is missing")
		}
		return w.sendSlack(ctx, cfg.WebhookURL, message)
	}

	return fmt.Errorf("unsupported channel type: %s", cfg.Type)
}

func (w *Worker) sendSlack(ctx context.Context, webhookURL string, message string) error {
	payload := map[string]string{"text": message}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack API returned non-OK status: %d", resp.StatusCode)
	}

	return nil
}
