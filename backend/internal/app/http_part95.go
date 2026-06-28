package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

func (s *Server) recordAgentToolAuditSnapshot(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	task, err := agentTaskForToolAuditSnapshot(r.Context(), s.store.Gorm, taskID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	if projectID == "" {
		writeError(w, http.StatusInternalServerError, "agent task has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: taskID, ProjectID: projectID}, "update") {
		return
	}
	result, err := RecordAgentToolAuditSnapshot(r.Context(), s.store, AgentToolAuditSnapshotOptions{
		AgentTaskID: taskID,
		DryRun:      req.DryRun,
		Task:        task,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("agent tool-call audit snapshot failed", "agent_task_id", taskID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record agent tool-call audit snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordAgentToolArmingSnapshot(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	task, err := agentTaskForToolAuditSnapshot(r.Context(), s.store.Gorm, taskID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	if projectID == "" {
		writeError(w, http.StatusInternalServerError, "agent task has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: taskID, ProjectID: projectID}, "update") {
		return
	}
	result, err := RecordAgentToolArmingSnapshot(r.Context(), s.store, AgentToolArmingSnapshotOptions{
		AgentTaskID: taskID,
		DryRun:      req.DryRun,
		Task:        task,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("agent tool arming snapshot failed", "agent_task_id", taskID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record agent tool arming snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordAgentCodeAuditSnapshot(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	task, err := agentTaskForToolAuditSnapshot(r.Context(), s.store.Gorm, taskID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	if projectID == "" {
		writeError(w, http.StatusInternalServerError, "agent task has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: taskID, ProjectID: projectID}, "update") {
		return
	}
	result, err := RecordAgentCodeAuditSnapshot(r.Context(), s.store, AgentCodeAuditSnapshotOptions{
		AgentTaskID: taskID,
		DryRun:      req.DryRun,
		Task:        task,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("agent code audit snapshot failed", "agent_task_id", taskID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record agent code audit snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) generatePlan(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	taskModel, err := agentTaskByID(r.Context(), s.store.Gorm, taskID)
	if err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := strings.TrimSpace(taskModel.ProjectID)
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: taskID, ProjectID: projectID}, "agent.generate_plan") {
		return
	}
	_, snapshot, err := s.BuildContextFiles(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not build agent context")
		return
	}
	task := agentTaskMap(taskModel, nil)
	content := agentPlanContent(task, snapshot)
	plan := GormAgentPlan{AgentTaskID: taskID, Content: content, Status: "generated"}
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&plan).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(r.Context(), tx); err != nil {
			return fmt.Errorf("syncing canonical assets for agent plan generate: %w", err)
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusBadRequest, "could not create agent plan")
		return
	}
	writeJSON(w, http.StatusCreated, agentPlanMap(plan))
}

func agentPlanContent(task, snapshot map[string]any) string {
	contextJSON := mapFromAny(snapshot["context_json"])
	project := mapFromAny(contextJSON["project"])
	repos := mapSliceFromAny(contextJSON["repositories"])
	remotes := mapSliceFromAny(contextJSON["remotes"])
	operations := mapSliceFromAny(contextJSON["operations"])
	approvals := mapSliceFromAny(contextJSON["approvals"])
	deploymentTargets := mapSliceFromAny(contextJSON["deployment_targets"])
	rollbackPoints := mapSliceFromAny(contextJSON["rollback_points"])
	sshMachines := mapSliceFromAny(contextJSON["ssh_machines"])
	githubRuns := mapSliceFromAny(contextJSON["github_action_runs"])
	assetGraph := mapFromAny(contextJSON["asset_graph"])
	assets := mapSliceFromAny(assetGraph["assets"])
	assetRelations := mapSliceFromAny(assetGraph["relations"])
	assetStatusSnapshots := mapSliceFromAny(assetGraph["status_snapshots"])
	assetTypeSummary := formatCountMap(countByStringField(assets, "asset_type"))
	if assetTypeSummary == "" {
		assetTypeSummary = "none"
	}
	assetHealthSummary := formatCountMap(countByStringField(assetStatusSnapshots, "health"))
	if assetHealthSummary == "" {
		assetHealthSummary = "none"
	}
	rollbackReadinessSummary := formatCountMap(countByStringField(rollbackPoints, "rollback_readiness"))
	if rollbackReadinessSummary == "" {
		rollbackReadinessSummary = "none"
	}
	deploymentExecutionSummary := formatCountMap(countNestedStringField(deploymentTargets, "deployment_execution_readiness", "status"))
	if deploymentExecutionSummary == "" {
		deploymentExecutionSummary = "none"
	}
	rollbackGuardrail := mapFromAny(contextJSON["rollback_guardrail"])
	if len(rollbackGuardrail) == 0 {
		rollbackGuardrail = rollbackGuardrailSummary(rollbackPoints)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent Read-Only Plan\n\n")
	fmt.Fprintf(&b, "Task: %s\n\n", strings.TrimSpace(fmt.Sprint(task["title"])))
	if prompt := strings.TrimSpace(fmt.Sprint(task["prompt"])); prompt != "" && prompt != "<nil>" {
		fmt.Fprintf(&b, "Prompt: %s\n\n", prompt)
	}
	fmt.Fprintf(&b, "## Context Snapshot\n\n")
	fmt.Fprintf(&b, "- Project: %s (`%s`)\n", strings.TrimSpace(fmt.Sprint(project["name"])), strings.TrimSpace(fmt.Sprint(project["slug"])))
	fmt.Fprintf(&b, "- Repositories: %d\n", len(repos))
	fmt.Fprintf(&b, "- Git remotes: %d\n", len(remotes))
	fmt.Fprintf(&b, "- Recent operations: %d\n", len(operations))
	fmt.Fprintf(&b, "- Pending/Recent approvals: %d\n", len(approvals))
	fmt.Fprintf(&b, "- Deployment targets: %d\n", len(deploymentTargets))
	fmt.Fprintf(&b, "- Deployment execution readiness: %s\n", deploymentExecutionSummary)
	fmt.Fprintf(&b, "- Rollback points: %d\n", len(rollbackPoints))
	fmt.Fprintf(&b, "- Rollback readiness: %s\n", rollbackReadinessSummary)
	fmt.Fprintf(&b, "- Rollback execution: %s (%d previewable, %d executable)\n",
		strings.TrimSpace(fmt.Sprint(rollbackGuardrail["execution_mode"])),
		intFromAny(rollbackGuardrail["previewable_count"], 0),
		intFromAny(rollbackGuardrail["executable_count"], 0),
	)
	fmt.Fprintf(&b, "- SSH machines: %d\n", len(sshMachines))
	fmt.Fprintf(&b, "- GitHub Actions runs: %d\n", len(githubRuns))
	fmt.Fprintf(&b, "- Asset graph assets: %d\n", len(assets))
	fmt.Fprintf(&b, "- Asset graph relations: %d\n", len(assetRelations))
	fmt.Fprintf(&b, "- Asset status snapshots: %d\n", len(assetStatusSnapshots))
	fmt.Fprintf(&b, "- Asset types: %s\n", assetTypeSummary)
	fmt.Fprintf(&b, "- Asset health: %s\n", assetHealthSummary)
	fmt.Fprintf(&b, "- Snapshot: %s\n\n", strings.TrimSpace(fmt.Sprint(snapshot["created_at"])))
	fmt.Fprintf(&b, "## Read-Only Checks\n\n")
	fmt.Fprintf(&b, "1. Review canonical asset graph entries, status snapshots, repositories, remotes, recent operations, deployment records, SSH runs, and approval state.\n")
	fmt.Fprintf(&b, "2. Summarize risks and missing operational evidence without mutating repositories, infrastructure, or databases.\n")
	fmt.Fprintf(&b, "3. If a mutation is needed, create a follow-up operation that goes through approval instead of executing directly.\n\n")
	fmt.Fprintf(&b, "## Allowed Tools\n\n")
	fmt.Fprintf(&b, "- context.generate\n- repo.sync status review\n- github.actions.sync status review\n- argo.apps.sync status review\n- ssh command audit review\n\n")
	fmt.Fprintf(&b, "## Guardrails\n\n")
	fmt.Fprintf(&b, "- No code changes, deployments, SSH execution, repository tags, or rollback actions in this plan.\n")
	fmt.Fprintf(&b, "- Deployment execution readiness is dry-run only; Helm/k8s execution remains disabled.\n")
	if msg := strings.TrimSpace(fmt.Sprint(rollbackGuardrail["message"])); msg != "" && msg != "<nil>" {
		fmt.Fprintf(&b, "- %s\n", msg)
	}
	patchGuardrail := agentPatchWorkflowGuardrail()
	if msg := strings.TrimSpace(fmt.Sprint(patchGuardrail["message"])); msg != "" && msg != "<nil>" {
		fmt.Fprintf(&b, "- %s\n", msg)
	}
	codexExecutionPlan := agentCodexExecutionPlan(nil)
	if msg := strings.TrimSpace(fmt.Sprint(codexExecutionPlan["message"])); msg != "" && msg != "<nil>" {
		fmt.Fprintf(&b, "- %s\n", msg)
	}
	fmt.Fprintf(&b, "- High-risk follow-up actions must use operation approvals.\n")
	return b.String()
}
