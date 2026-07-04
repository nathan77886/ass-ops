package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var workerNodeMetricsMu sync.Mutex

func collectLocalWorkerMetrics(diskPath string) (map[string]any, error) {
	hostname, _ := os.Hostname()
	metrics := map[string]any{
		"os":           runtime.GOOS,
		"arch":         runtime.GOARCH,
		"hostname":     hostname,
		"collected_at": time.Now().UTC().Format(time.RFC3339),
	}
	readLoadAvg(metrics)
	readMemInfo(metrics)
	readUptime(metrics)
	readDiskStats(metrics, diskPath)
	return metrics, nil
}

func readLoadAvg(metrics map[string]any) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	keys := []string{"cpu_load_1m", "cpu_load_5m", "cpu_load_15m"}
	for i, key := range keys {
		if i >= len(fields) {
			return
		}
		if value, err := strconv.ParseFloat(fields[i], 64); err == nil {
			metrics[key] = value
		}
	}
}

func readMemInfo(metrics map[string]any) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			metrics["memory_total_bytes"] = value * 1024
		case "MemAvailable":
			metrics["memory_available_bytes"] = value * 1024
		}
	}
}

func readUptime(metrics map[string]any) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err == nil {
		metrics["uptime_seconds"] = int64(value)
	}
}

func readDiskStats(metrics map[string]any, diskPath string) {
	if strings.TrimSpace(diskPath) == "" {
		diskPath = "/"
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(diskPath, &stat); err != nil {
		return
	}
	metrics["disk_total_bytes"] = int64(stat.Blocks) * int64(stat.Bsize)
	metrics["disk_free_bytes"] = int64(stat.Bavail) * int64(stat.Bsize)
}

func writeWorkerNodeMetrics(path, nodeID string, metrics map[string]any) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || len(metrics) == 0 {
		return nil
	}
	metrics = sanitizeWorkerNodeMetrics(metrics)
	if len(metrics) == 0 {
		return nil
	}
	path = workerMetricsPath(path)
	workerNodeMetricsMu.Lock()
	defer workerNodeMetricsMu.Unlock()
	all := readWorkerNodeMetricsLocked(path)
	all[nodeID] = metrics
	return writeWorkerNodeMetricsLocked(path, all)
}

func readWorkerNodeMetrics(path string) map[string]map[string]any {
	path = workerMetricsPath(path)
	workerNodeMetricsMu.Lock()
	defer workerNodeMetricsMu.Unlock()
	return readWorkerNodeMetricsLocked(path)
}

func readWorkerNodeMetricsLocked(path string) map[string]map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]map[string]any{}
	}
	var out map[string]map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]map[string]any{}
	}
	if out == nil {
		return map[string]map[string]any{}
	}
	return out
}

func writeWorkerNodeMetricsLocked(path string, data map[string]map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func workerMetricsPath(path string) string {
	if path = strings.TrimSpace(path); path != "" {
		return path
	}
	return filepath.Join(os.TempDir(), "assops-worker-node-metrics.json")
}

func sanitizeWorkerNodeMetrics(input map[string]any) map[string]any {
	allowed := map[string]bool{
		"cpu_load_1m":            true,
		"cpu_load_5m":            true,
		"cpu_load_15m":           true,
		"memory_total_bytes":     true,
		"memory_available_bytes": true,
		"disk_total_bytes":       true,
		"disk_free_bytes":        true,
		"uptime_seconds":         true,
		"os":                     true,
		"arch":                   true,
		"hostname":               true,
		"collected_at":           true,
	}
	out := map[string]any{}
	for key, value := range input {
		if !allowed[key] {
			continue
		}
		switch v := value.(type) {
		case string:
			if len(v) <= 256 {
				out[key] = v
			}
		case float64, int, int64, uint64:
			out[key] = v
		}
	}
	return out
}

func workerNodeSummaryItems(nodes []GormWorkerNode, metrics map[string]map[string]any) []map[string]any {
	items := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		item := workerNodeMap(node)
		if metric := metrics[node.ID]; len(metric) > 0 {
			item["metrics"] = metric
		}
		items = append(items, item)
	}
	return items
}

func localGatewayWorkerNodeItem() map[string]any {
	item := map[string]any{
		"id":                "gateway-local",
		"name":              "gateway-local",
		"kind":              "local",
		"capabilities":      []string{"exec", "git", "ssh", "ai", "k8s", "argo", "docker"},
		"status":            "online",
		"last_heartbeat_at": time.Now().UTC().Format(time.RFC3339),
		"metadata":          map[string]any{"source": "gateway"},
	}
	if metrics, err := collectLocalWorkerMetrics("/"); err == nil {
		item["metrics"] = metrics
	}
	return item
}

func incrementWorkerNodeKind(items []map[string]any, kind string) []map[string]any {
	for _, item := range items {
		if strings.TrimSpace(stringFromMap(item, "kind")) == kind {
			item["count"] = intFromAny(item["count"], 0) + 1
			return items
		}
	}
	return append(items, map[string]any{"kind": kind, "count": 1})
}
