package detector

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type Event struct {
	TraceID    string `json:"trace_id"`
	SpanID     string `json:"span_id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

type AlertRule struct {
	ID        string
	ProjectID string
	Kind      string
	Threshold map[string]any
}

type loopTracker struct {
	LastKind  string
	LastName  string
	Count     int
	Alerted   bool
	UpdatedAt time.Time
}

type DB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

type Detector struct {
	db     DB
	redis  *redis.Client
	logger *slog.Logger

	mu    sync.RWMutex
	rules map[string][]AlertRule // project_id -> rules

	stateMu sync.Mutex
	loops   map[string]*loopTracker // trace_id + ":" + rule_id -> tracker
}

func New(db DB, rdb *redis.Client, logger *slog.Logger) *Detector {
	return &Detector{
		db:     db,
		redis:  rdb,
		logger: logger,
		rules:  make(map[string][]AlertRule),
		loops:  make(map[string]*loopTracker),
	}
}

// Start runs the periodic rules sync, state cleanup, and pubsub listener.
func (d *Detector) Start(ctx context.Context) error {
	if err := d.syncRules(ctx); err != nil {
		d.logger.Warn("initial rule sync failed, will retry", "err", err)
	}

	go d.ruleSyncLoop(ctx)
	go d.cleanupLoop(ctx)
	return d.listenPubSub(ctx)
}

func (d *Detector) ruleSyncLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.syncRules(ctx); err != nil {
				d.logger.Warn("failed to sync rules", "err", err)
			}
		}
	}
}

func (d *Detector) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.stateMu.Lock()
			now := time.Now()
			for k, v := range d.loops {
				if now.Sub(v.UpdatedAt) > 15*time.Minute {
					delete(d.loops, k)
				}
			}
			d.stateMu.Unlock()
		}
	}
}

func (d *Detector) syncRules(ctx context.Context) error {
	rows, err := d.db.Query(ctx, "SELECT id, project_id, kind, threshold FROM alert_rules WHERE enabled = true")
	if err != nil {
		return err
	}
	defer rows.Close()

	newRules := make(map[string][]AlertRule)
	for rows.Next() {
		var r AlertRule
		var thresholdBytes []byte
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Kind, &thresholdBytes); err != nil {
			return err
		}
		if len(thresholdBytes) > 0 {
			if err := json.Unmarshal(thresholdBytes, &r.Threshold); err != nil {
				d.logger.Warn("invalid threshold JSON", "rule_id", r.ID, "err", err)
				continue
			}
		}
		newRules[r.ProjectID] = append(newRules[r.ProjectID], r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	d.mu.Lock()
	d.rules = newRules
	d.mu.Unlock()
	return nil
}

func (d *Detector) listenPubSub(ctx context.Context) error {
	pubsub := d.redis.PSubscribe(ctx, "agentmesh:spans:*")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-ch:
			d.processMessage(ctx, msg.Channel, msg.Payload)
		}
	}
}

func (d *Detector) processMessage(ctx context.Context, channel, payload string) {
	parts := strings.SplitN(channel, ":", 3)
	if len(parts) != 3 {
		return
	}
	projectID := parts[2]

	var ev Event
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		d.logger.Warn("failed to decode event", "payload", payload, "err", err)
		return
	}

	d.mu.RLock()
	rules := d.rules[projectID]
	d.mu.RUnlock()

	for _, rule := range rules {
		d.evaluateRule(ctx, rule, projectID, ev)
	}
}

func (d *Detector) evaluateRule(ctx context.Context, rule AlertRule, projectID string, ev Event) {
	switch rule.Kind {
	case "guardrail_violation":
		if ev.Status == "denied" && ev.Kind == "mcp.call" {
			d.triggerAlert(ctx, rule.ID, projectID, ev.TraceID, fmt.Sprintf("Guardrail violation on %s", ev.Name))
		}
	case "loop_detected":
		maxRepeats := 10 // default
		if val, ok := rule.Threshold["max_repeats"].(float64); ok {
			maxRepeats = int(val)
		}

		key := ev.TraceID + ":" + rule.ID

		d.stateMu.Lock()
		tracker, ok := d.loops[key]
		if !ok {
			tracker = &loopTracker{}
			d.loops[key] = tracker
		}
		tracker.UpdatedAt = time.Now()

		if ev.Kind == tracker.LastKind && ev.Name == tracker.LastName {
			tracker.Count++
			if tracker.Count > maxRepeats && !tracker.Alerted {
				tracker.Alerted = true
				d.stateMu.Unlock()
				d.triggerAlert(ctx, rule.ID, projectID, ev.TraceID, fmt.Sprintf("Loop detected: %s %s repeated %d times", ev.Kind, ev.Name, tracker.Count))
				return
			}
		} else {
			tracker.LastKind = ev.Kind
			tracker.LastName = ev.Name
			tracker.Count = 1
			tracker.Alerted = false
		}
		d.stateMu.Unlock()
	case "cost_spike":
		// Assume threshold applies to duration for simplicity if missing cost data
		maxDuration := 5000.0 // ms
		if val, ok := rule.Threshold["max_duration_ms"].(float64); ok {
			maxDuration = val
		}
		if float64(ev.DurationMS) > maxDuration {
			d.triggerAlert(ctx, rule.ID, projectID, ev.TraceID, fmt.Sprintf("Cost spike / duration anomaly: %d ms", ev.DurationMS))
		}
	}
}

func (d *Detector) triggerAlert(ctx context.Context, ruleID, projectID, traceID, message string) {
	if d.db == nil {
		return // Ignore for tests where db isn't wired properly
	}
	_, err := d.db.Exec(ctx,
		"INSERT INTO alert_events (project_id, rule_id, trace_id, message, status, created_at) VALUES ($1, $2, $3, $4, 'pending', now())",
		projectID, ruleID, traceID, message,
	)
	if err != nil {
		d.logger.Error("failed to insert alert_event", "rule_id", ruleID, "trace_id", traceID, "err", err)
	} else {
		d.logger.Info("alert triggered", "rule_id", ruleID, "trace_id", traceID, "msg", message)
	}
}
