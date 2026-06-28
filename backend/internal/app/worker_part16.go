package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

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
