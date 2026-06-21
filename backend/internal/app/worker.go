package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type ControlWorker struct {
	store    *Store
	interval time.Duration
	log      *slog.Logger
}

func NewControlWorker(store *Store, interval time.Duration, log *slog.Logger) *ControlWorker {
	return &ControlWorker{store: store, interval: interval, log: log}
}

func (w *ControlWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.processOne(ctx); err != nil && err != ErrNotFound {
			w.log.Error("worker process failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *ControlWorker) processOne(ctx context.Context) error {
	job, err := queryOne(ctx, w.store.DB, `
		UPDATE worker_jobs
		SET status='running', started_at=now(), updated_at=now()
		WHERE id = (
			SELECT id FROM worker_jobs
			WHERE status='queued' AND preferred_node_kind=''
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING *`)
	if err != nil {
		return err
	}
	opID := fmt.Sprint(job["operation_run_id"])
	_, _ = w.store.DB.ExecContext(ctx, "UPDATE operation_runs SET status='running', started_at=COALESCE(started_at, now()), updated_at=now() WHERE id=$1", opID)
	_, _ = w.store.DB.ExecContext(ctx, `
		INSERT INTO operation_logs(operation_run_id, worker_job_id, level, message)
		VALUES ($1, $2, 'info', $3)`, opID, job["id"], "dispatching "+fmt.Sprint(job["tool_name"]))

	result := map[string]any{
		"adapter": true,
		"tool":    job["tool_name"],
		"message": "MVP adapter completed without external side effects",
	}
	resultJSON, _ := jsonParam(result)
	_, err = w.store.DB.ExecContext(ctx, `
		UPDATE worker_jobs SET status='completed', result=$2::jsonb, finished_at=now(), updated_at=now()
		WHERE id=$1`, job["id"], resultJSON)
	if err != nil {
		return err
	}
	_, err = w.store.DB.ExecContext(ctx, `
		UPDATE operation_runs SET status='completed', result=$2::jsonb, finished_at=now(), updated_at=now()
		WHERE id=$1`, opID, resultJSON)
	return err
}

type NodeWorker struct {
	cfg          Config
	name         string
	kind         string
	capabilities []string
	log          *slog.Logger
	client       *http.Client
	token        string
}

func NewNodeWorker(cfg Config, name, kind string, capabilities []string, log *slog.Logger) *NodeWorker {
	return &NodeWorker{
		cfg:          cfg,
		name:         name,
		kind:         kind,
		capabilities: capabilities,
		log:          log,
		client:       &http.Client{Timeout: 10 * time.Second},
	}
}

func (n *NodeWorker) Run(ctx context.Context) error {
	if err := n.register(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(n.cfg.WorkerInterval)
	defer ticker.Stop()
	for {
		if err := n.heartbeat(ctx); err != nil {
			n.log.Error("heartbeat failed", "error", err)
		}
		if err := n.claimAndRun(ctx); err != nil {
			n.log.Error("claim failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (n *NodeWorker) register(ctx context.Context) error {
	var resp map[string]any
	err := n.post(ctx, "/api/worker-nodes/register", map[string]any{
		"name":         n.name,
		"kind":         n.kind,
		"capabilities": n.capabilities,
	}, &resp, false)
	if err != nil {
		return err
	}
	token, ok := resp["token"].(string)
	if !ok || token == "" {
		return fmt.Errorf("register response missing token")
	}
	n.token = token
	n.log.Info("node registered", "name", n.name)
	return nil
}

func (n *NodeWorker) heartbeat(ctx context.Context) error {
	var resp map[string]any
	return n.post(ctx, "/api/worker-nodes/heartbeat", map[string]any{}, &resp, true)
}

func (n *NodeWorker) claimAndRun(ctx context.Context) error {
	var resp struct {
		Job map[string]any `json:"job"`
	}
	if err := n.post(ctx, "/api/worker-nodes/jobs/claim", map[string]any{}, &resp, true); err != nil {
		return err
	}
	if resp.Job == nil {
		return nil
	}
	jobID := fmt.Sprint(resp.Job["id"])
	_ = n.post(ctx, "/api/worker-nodes/jobs/"+jobID+"/logs", map[string]any{"level": "info", "message": "node-worker executing echo adapter"}, nil, true)
	result := map[string]any{"echo": resp.Job["payload"], "node": n.name}
	return n.post(ctx, "/api/worker-nodes/jobs/"+jobID+"/complete", map[string]any{"result": result}, nil, true)
}

func (n *NodeWorker) post(ctx context.Context, path string, body any, dst any, auth bool) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.cfg.GatewayURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}
	res, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("gateway returned %s for %s", res.Status, path)
	}
	if dst != nil {
		return json.NewDecoder(res.Body).Decode(dst)
	}
	return nil
}
