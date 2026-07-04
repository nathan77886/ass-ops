package app

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

var workerInstallTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func (s *Server) installWorkerOnSSHMachine(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	var machineModel GormSSHMachine
	if err := s.store.Gorm.WithContext(r.Context()).First(&machineModel, &GormSSHMachine{GormBase: GormBase{ID: machineID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	var req struct {
		NodeName       string   `json:"node_name"`
		Kind           string   `json:"kind"`
		Capabilities   []string `json:"capabilities"`
		GatewayURL     string   `json:"gateway_url"`
		NodeWorkerPath string   `json:"node_worker_path"`
		TimeoutSeconds int      `json:"timeout_seconds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.NodeName == "" {
		req.NodeName = defaultWorkerNodeName(machineModel.Name)
	}
	if req.Kind == "" {
		req.Kind = "remote"
	}
	if len(req.Capabilities) == 0 {
		req.Capabilities = []string{"exec", "docker", "k8s", "argo"}
	}
	if req.GatewayURL == "" {
		req.GatewayURL = s.cfg.GatewayURL
	}
	if req.NodeWorkerPath == "" {
		req.NodeWorkerPath = "/usr/local/bin/node-worker"
	}
	command, err := buildWorkerInstallCommand(workerInstallRequest{
		NodeName:       req.NodeName,
		Kind:           req.Kind,
		Capabilities:   req.Capabilities,
		GatewayURL:     req.GatewayURL,
		NodeWorkerPath: req.NodeWorkerPath,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 120
	}
	if req.TimeoutSeconds > 300 {
		writeError(w, http.StatusBadRequest, "timeout_seconds must be <= 300")
		return
	}
	input := map[string]any{
		"ssh_machine_id":   machineID,
		"command":          command,
		"timeout_seconds":  req.TimeoutSeconds,
		"install_worker":   true,
		"worker_node_name": req.NodeName,
		"worker_kind":      req.Kind,
	}
	payload := map[string]any{"kind": "worker_install", "machine_id": machineID, "input": input}
	machine := sshMachineMap(machineModel, nil)
	if !s.requireProjectPolicyOrApproval(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: fmt.Sprint(machine["project_id"])}, "ssh.exec", "install worker "+fmt.Sprint(machine["name"]), payload) {
		return
	}
	s.createSSHRun(w, r, machineID, input, "ssh.exec", "install worker "+fmt.Sprint(machine["name"]), "worker_install.enqueue")
}

type workerInstallRequest struct {
	NodeName       string
	Kind           string
	Capabilities   []string
	GatewayURL     string
	NodeWorkerPath string
}

func buildWorkerInstallCommand(req workerInstallRequest) (string, error) {
	if !workerInstallTokenPattern.MatchString(req.NodeName) {
		return "", fmt.Errorf("node_name contains unsupported characters")
	}
	if !workerInstallTokenPattern.MatchString(req.Kind) || req.Kind == "control-worker" || req.Kind == "local" {
		return "", fmt.Errorf("kind must be a remote worker kind")
	}
	capabilities := cleanWorkerInstallCapabilities(req.Capabilities)
	if len(capabilities) == 0 {
		return "", fmt.Errorf("capabilities are required")
	}
	if err := validateWorkerGatewayURL(req.GatewayURL); err != nil {
		return "", err
	}
	if err := validateWorkerBinaryPath(req.NodeWorkerPath); err != nil {
		return "", err
	}
	caps := strings.Join(capabilities, ",")
	unit := fmt.Sprintf(`[Unit]
Description=ASSOPS remote worker
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=ASSOPS_GATEWAY_URL=%s
Environment=ASSOPS_NODE_WORKER_HEALTH_ADDR=:8082
ExecStart=%s -name %s -kind %s -capabilities %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, req.GatewayURL, req.NodeWorkerPath, req.NodeName, req.Kind, caps)
	unitBase64 := base64.StdEncoding.EncodeToString([]byte(unit))
	return strings.Join([]string{
		"set -eu",
		"command -v systemctl >/dev/null",
		"test -x " + shellQuote(req.NodeWorkerPath),
		"tmp=$(mktemp)",
		"printf '%s' " + shellQuote(unitBase64) + " | base64 -d > \"$tmp\"",
		"sudo install -m 0644 \"$tmp\" /etc/systemd/system/assops-node-worker.service",
		"rm -f \"$tmp\"",
		"sudo systemctl daemon-reload",
		"sudo systemctl enable --now assops-node-worker.service",
		"sudo systemctl --no-pager --full status assops-node-worker.service",
	}, "\n"), nil
}

func cleanWorkerInstallCapabilities(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if workerInstallTokenPattern.MatchString(value) {
			seen[value] = true
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func validateWorkerGatewayURL(value string) error {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("gateway_url must be an absolute URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("gateway_url must be http or https")
	}
	return nil
}

func validateWorkerBinaryPath(value string) error {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, " \t\r\n'\"`$\\") {
		return fmt.Errorf("node_worker_path must be an absolute path without shell metacharacters")
	}
	return nil
}

func defaultWorkerNodeName(machineName string) string {
	parts := strings.FieldsFunc(strings.ToLower(machineName), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	name := strings.Join(parts, "-")
	if name == "" {
		name = "worker"
	}
	if len(name) > 50 {
		name = name[:50]
	}
	return "remote-" + strings.Trim(name, "-")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
