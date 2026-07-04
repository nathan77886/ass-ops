package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"time"
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
	body := map[string]any{}
	if metrics, err := collectLocalWorkerMetrics("/"); err == nil {
		body["metrics"] = metrics
	}
	return n.post(ctx, "/api/worker-nodes/heartbeat", body, &resp, true)
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
	result, err := n.runClaimedJob(ctx, resp.Job)
	if err != nil {
		_ = n.post(ctx, "/api/worker-nodes/jobs/"+jobID+"/logs", map[string]any{"level": "error", "message": err.Error()}, nil, true)
		return n.post(ctx, "/api/worker-nodes/jobs/"+jobID+"/fail", map[string]any{"error": err.Error()}, nil, true)
	}
	return n.post(ctx, "/api/worker-nodes/jobs/"+jobID+"/complete", map[string]any{"result": result}, nil, true)
}

func (n *NodeWorker) runClaimedJob(ctx context.Context, job map[string]any) (map[string]any, error) {
	toolName := fmt.Sprint(job["tool_name"])
	payload := mapFromAny(job["payload"])
	switch toolName {
	case "node.echo":
		_ = n.post(ctx, "/api/worker-nodes/jobs/"+fmt.Sprint(job["id"])+"/logs", map[string]any{"level": "info", "message": "node-worker executing echo adapter"}, nil, true)
		return map[string]any{"echo": payload, "node": n.name}, nil
	case "node.exec":
		return n.runCommandTool(ctx, "sh", []string{"-lc", stringFromMap(payload, "command")}, payload)
	case "node.docker":
		return n.runCommandTool(ctx, "docker", stringSliceFromAny(payload["args"]), payload)
	case "node.k8s", "node.kubectl":
		return n.runCommandTool(ctx, "kubectl", stringSliceFromAny(payload["args"]), payload)
	case "node.argo", "node.argocd":
		return n.runCommandTool(ctx, "argocd", stringSliceFromAny(payload["args"]), payload)
	default:
		return nil, fmt.Errorf("unsupported node-worker tool %q", toolName)
	}
}

func (n *NodeWorker) runCommandTool(ctx context.Context, binary string, args []string, payload map[string]any) (map[string]any, error) {
	if binary == "sh" && (len(args) < 2 || args[1] == "") {
		return nil, fmt.Errorf("command is required")
	}
	if binary != "sh" && len(args) == 0 {
		return nil, fmt.Errorf("args are required")
	}
	timeout := intFromAny(payload["timeout_seconds"], 60)
	if timeout <= 0 || timeout > 300 {
		timeout = 60
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, binary, args...)
	stdout, stderr, exitCode, err := runCommandCapture(cmd)
	result := map[string]any{
		"node":            n.name,
		"tool_binary":     binary,
		"args":            args,
		"stdout":          truncateOutput(sanitizeSSHOutput(stdout), 64*1024),
		"stderr":          truncateOutput(sanitizeSSHOutput(stderr), 64*1024),
		"exit_code":       exitCode,
		"timeout_seconds": timeout,
	}
	if err != nil {
		return result, fmt.Errorf("%s failed with exit code %d: %w", binary, exitCode, err)
	}
	return result, nil
}

func runCommandCapture(cmd *exec.Cmd) (string, string, int, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode(), err
	}
	return stdout.String(), stderr.String(), -1, err
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
