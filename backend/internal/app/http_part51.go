package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"sort"
	"time"
)

func (s *Server) enqueueRemoteOperationRunGorm(ctx context.Context, tx *gorm.DB, remoteID, tool string, input map[string]any, actorID string) (map[string]any, error) {
	var remoteModel GormGitRemote
	if err := tx.WithContext(ctx).First(&remoteModel, &GormGitRemote{GormBase: GormBase{ID: remoteID}}).Error; err != nil {
		return nil, gormNotFoundAsErrNotFound(err)
	}
	var repo GormProjectGitRepository
	if err := tx.WithContext(ctx).First(&repo, &GormProjectGitRepository{GormBase: GormBase{ID: remoteModel.ProjectGitRepositoryID}}).Error; err != nil {
		return nil, gormNotFoundAsErrNotFound(err)
	}
	remote := gitRemoteMap(remoteModel, nil, repo.ProjectID)
	op, err := enqueueOperationGorm(ctx, tx, repo.ProjectID, remoteID, tool, tool+" "+remoteModel.Name, input, []string{"git"}, "")
	if err != nil {
		return nil, fmt.Errorf("could not enqueue operation")
	}
	if err := createRemoteOperationRunGorm(ctx, tx, op, remote, input, actorID, tool); err != nil {
		return nil, fmt.Errorf("could not create operation run")
	}
	return op, nil
}

func (s *Server) enqueueOperation(ctx context.Context, projectID, remoteID, tool, title string, input map[string]any, capabilities []string, preferredKind string) (map[string]any, error) {
	var op map[string]any
	if err := s.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		op, err = enqueueOperationGorm(ctx, tx, projectID, remoteID, tool, title, input, capabilities, preferredKind)
		if err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(ctx, tx)
		if err != nil {
			return fmt.Errorf("syncing canonical assets for operation enqueue: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return op, nil
}

func (s *Server) listOperations(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation"}, "read") {
		return
	}
	user := currentUser(r)
	items, err := s.operationListGorm(r.Context(), user)
	writeQueryResult(w, items, err)
}

func (s *Server) operationListGorm(ctx context.Context, user *User) ([]map[string]any, error) {
	var ops []GormOperationRun
	if err := s.store.Gorm.WithContext(ctx).Order(gormOrderDesc("created_at")).Limit(250).Find(&ops).Error; err != nil {
		return nil, err
	}
	if !userCanReadAllProjects(user) {
		allowed, err := s.projectMembershipSetGorm(ctx, user)
		if err != nil {
			return nil, err
		}
		filtered := ops[:0]
		for _, op := range ops {
			projectID := cleanOptionalID(op.ProjectID.String)
			if projectID == "" || allowed[projectID] {
				filtered = append(filtered, op)
			}
		}
		ops = filtered
	}
	if len(ops) > 100 {
		ops = ops[:100]
	}
	opIDs := make([]string, 0, len(ops))
	for _, op := range ops {
		opIDs = append(opIDs, op.ID)
	}
	logCounts := map[string]int{}
	if len(opIDs) > 0 {
		var logs []GormOperationLog
		if err := s.store.Gorm.WithContext(ctx).Where(gormField("operation_run_id", opIDs)).Find(&logs).Error; err != nil {
			return nil, err
		}
		for _, log := range logs {
			logCounts[cleanOptionalID(log.OperationRunID.String)]++
		}
	}
	items := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		item := operationRunGormMap(op)
		item["log_count"] = logCounts[op.ID]
		items = append(items, item)
	}
	return items, nil
}

func (s *Server) getWorkerQueueSummary(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation"}, "read") {
		return
	}
	if !s.requirePolicy(w, r, PolicyResource{Type: "worker_node"}, "read") {
		return
	}
	user := currentUser(r)
	summary, err := s.workerQueueSummaryGorm(r.Context(), user)
	writeQueryOne(w, summary, err)
}

func workerQueueBackendSummary() map[string]any {
	return map[string]any{
		"backend":           "postgres",
		"claiming":          "select_for_update_skip_locked",
		"pubsub":            "disabled",
		"pubsub_enabled":    false,
		"log_fanout":        "sse_polling",
		"websocket_fanout":  "deferred",
		"active_components": []string{"postgres_polling", "row_lock_claiming", "sse_polling_log_fanout"},
		"deferred_backends": []string{"websocket_fanout"},
		"message":           "Worker jobs use PostgreSQL polling and row locks; log fanout uses SSE polling.",
	}
}

func (s *Server) workerQueueSummaryGorm(ctx context.Context, user *User) (map[string]any, error) {
	var jobs []GormWorkerJob
	if err := s.store.Gorm.WithContext(ctx).Find(&jobs).Error; err != nil {
		return nil, err
	}
	if !userCanReadAllProjects(user) {
		allowed, err := s.projectMembershipSetGorm(ctx, user)
		if err != nil {
			return nil, err
		}
		opProjects, err := s.operationProjectMapForJobsGorm(ctx, jobs)
		if err != nil {
			return nil, err
		}
		filtered := jobs[:0]
		for _, job := range jobs {
			projectID := opProjects[cleanOptionalID(job.OperationRunID.String)]
			if projectID == "" || allowed[projectID] {
				filtered = append(filtered, job)
			}
		}
		jobs = filtered
	}
	var nodes []GormWorkerNode
	if err := s.store.Gorm.WithContext(ctx).Find(&nodes).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	jobsByStatus := map[string]int{}
	queueByToolCount := map[string]int{}
	recentFailures := []map[string]any{}
	summary := map[string]any{
		"total_nodes":        len(nodes),
		"online_nodes":       0,
		"stale_nodes":        0,
		"total_jobs":         len(jobs),
		"queued_jobs":        0,
		"running_jobs":       0,
		"failed_jobs":        0,
		"completed_24h":      0,
		"failed_24h":         0,
		"aged_queued_jobs":   0,
		"stale_running_jobs": 0,
	}
	for _, node := range nodes {
		if node.Status == "online" && !node.LastHeartbeatAt.Before(now.Add(-2*time.Minute)) {
			summary["online_nodes"] = intFromAny(summary["online_nodes"], 0) + 1
		}
		if node.LastHeartbeatAt.Before(now.Add(-2 * time.Minute)) {
			summary["stale_nodes"] = intFromAny(summary["stale_nodes"], 0) + 1
		}
	}
	nodesByKind := workerNodeKindCounts(nodes)
	for _, job := range jobs {
		jobsByStatus[job.Status]++
		switch job.Status {
		case "queued":
			summary["queued_jobs"] = intFromAny(summary["queued_jobs"], 0) + 1
			queueByToolCount[job.ToolName]++
			if job.CreatedAt.Before(now.Add(-15 * time.Minute)) {
				summary["aged_queued_jobs"] = intFromAny(summary["aged_queued_jobs"], 0) + 1
			}
		case "running":
			summary["running_jobs"] = intFromAny(summary["running_jobs"], 0) + 1
			if job.StartedAt.Valid && job.StartedAt.Time.Before(now.Add(-15*time.Minute)) {
				summary["stale_running_jobs"] = intFromAny(summary["stale_running_jobs"], 0) + 1
			}
		case "failed":
			summary["failed_jobs"] = intFromAny(summary["failed_jobs"], 0) + 1
			if !job.UpdatedAt.Before(now.Add(-24 * time.Hour)) {
				summary["failed_24h"] = intFromAny(summary["failed_24h"], 0) + 1
			}
			recentFailures = append(recentFailures, map[string]any{"id": job.ID, "tool_name": job.ToolName, "error": job.Error, "updated_at": job.UpdatedAt})
		case "completed":
			if !job.UpdatedAt.Before(now.Add(-24 * time.Hour)) {
				summary["completed_24h"] = intFromAny(summary["completed_24h"], 0) + 1
			}
		}
	}
	sort.Slice(recentFailures, func(i, j int) bool {
		return fmt.Sprint(recentFailures[i]["updated_at"]) > fmt.Sprint(recentFailures[j]["updated_at"])
	})
	if len(recentFailures) > 5 {
		recentFailures = recentFailures[:5]
	}
	summary["jobs_by_status"] = jobsByStatus
	summary["nodes_by_kind"] = nodesByKind
	summary["queue_by_tool"] = queueByTool(queueByToolCount)
	summary["recent_failures"] = recentFailures
	summary["backend_summary"] = workerQueueBackendSummary()
	return summary, nil
}

func (s *Server) operationProjectMapForJobsGorm(ctx context.Context, jobs []GormWorkerJob) (map[string]string, error) {
	opIDs := make([]string, 0, len(jobs))
	seen := map[string]bool{}
	for _, job := range jobs {
		opID := cleanOptionalID(job.OperationRunID.String)
		if opID == "" || seen[opID] {
			continue
		}
		seen[opID] = true
		opIDs = append(opIDs, opID)
	}
	projects := map[string]string{}
	if len(opIDs) == 0 {
		return projects, nil
	}
	var ops []GormOperationRun
	if err := s.store.Gorm.WithContext(ctx).Where(gormField("id", opIDs)).Find(&ops).Error; err != nil {
		return nil, err
	}
	for _, op := range ops {
		projects[op.ID] = cleanOptionalID(op.ProjectID.String)
	}
	return projects, nil
}

func workerNodeKindCounts(nodes []GormWorkerNode) []map[string]any {
	counts := map[string]int{}
	for _, node := range nodes {
		counts[node.Kind]++
	}
	items := make([]map[string]any, 0, len(counts))
	for kind, count := range counts {
		items = append(items, map[string]any{"kind": kind, "count": count})
	}
	sort.Slice(items, func(i, j int) bool {
		left := intFromAny(items[i]["count"], 0)
		right := intFromAny(items[j]["count"], 0)
		if left != right {
			return left > right
		}
		return fmt.Sprint(items[i]["kind"]) < fmt.Sprint(items[j]["kind"])
	})
	return items
}
