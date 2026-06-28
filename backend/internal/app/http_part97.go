package app

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"sort"
	"strings"
)

func latestApprovedAgentPlanGorm(ctx context.Context, db *gorm.DB, taskID string) (GormAgentPlan, error) {
	plan, err := latestAgentPlanModel(ctx, db, taskID)
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrNotFound) {
		return GormAgentPlan{}, errAgentPlanNotApproved
	}
	if err != nil {
		return GormAgentPlan{}, err
	}
	if !agentPlanStatusApproved(plan.Status) {
		return GormAgentPlan{}, errAgentPlanNotApproved
	}
	return plan, nil
}

func agentPlanStatusApproved(status any) bool {
	return fmt.Sprint(status) == "approved"
}

func enqueueAgentToolCallAuditGorm(ctx context.Context, tx *gorm.DB, task GormAgentTask, plan GormAgentPlan, op map[string]any) error {
	taskMap := agentTaskMap(task, &plan)
	planMap := agentPlanMap(plan)
	runtime, err := latestProjectAIRuntimeGorm(ctx, tx, strings.TrimSpace(task.ProjectID))
	if err != nil {
		return err
	}
	for _, call := range agentExecutionAuditSteps(taskMap, planMap, op, runtime) {
		row := GormAgentToolCall{AgentTaskID: task.ID, OperationRunID: validNullString(cleanOptionalID(fmt.Sprint(op["id"]))), ProjectID: validNullString(task.ProjectID), ToolName: cleanOptionalText(fmt.Sprint(call["tool_name"])), Input: JSONValue{Data: mapFromAny(call["input"])}, Output: JSONValue{Data: mapFromAny(call["output"])}, Status: "queued"}
		if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
			return fmt.Errorf("inserting agent tool call audit: %w", err)
		}
	}
	return nil
}

func latestProjectAIRuntimeGorm(ctx context.Context, db *gorm.DB, projectID string) (map[string]any, error) {
	var runtimes []GormAIRuntime
	if err := db.WithContext(ctx).Find(&runtimes).Error; err != nil {
		return nil, fmt.Errorf("loading AI runtime for agent execution audit: %w", err)
	}
	filtered := make([]GormAIRuntime, 0, len(runtimes))
	for _, runtime := range runtimes {
		runtimeProjectID := cleanOptionalID(runtime.ProjectID.String)
		if runtimeProjectID == "" || runtimeProjectID == projectID {
			filtered = append(filtered, runtime)
		}
	}
	if len(filtered) == 0 {
		return nil, nil
	}
	sort.Slice(filtered, func(i, j int) bool {
		leftProject := cleanOptionalID(filtered[i].ProjectID.String) == projectID
		rightProject := cleanOptionalID(filtered[j].ProjectID.String) == projectID
		if leftProject != rightProject {
			return leftProject
		}
		leftVerified := filtered[i].Status == "verified"
		rightVerified := filtered[j].Status == "verified"
		if leftVerified != rightVerified {
			return leftVerified
		}
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})
	return aiRuntimeMap(filtered[0], nil), nil
}

func agentExecutionAuditSteps(task, plan, op, runtime map[string]any) []map[string]any {
	taskID := strings.TrimSpace(fmt.Sprint(task["id"]))
	planID := strings.TrimSpace(fmt.Sprint(plan["id"]))
	opID := strings.TrimSpace(fmt.Sprint(op["id"]))
	planContent := strings.TrimSpace(fmt.Sprint(plan["content"]))
	if planContent == "<nil>" {
		planContent = ""
	}
	return []map[string]any{
		{
			"tool_name": "context.generate",
			"input": map[string]any{
				"agent_task_id":    taskID,
				"operation_run_id": opID,
				"mode":             "read_only_snapshot",
			},
			"output": map[string]any{
				"message": "context snapshot is read by the approved plan; no repository mutation is performed",
			},
		},
		{
			"tool_name": "plan.review",
			"input": map[string]any{
				"agent_task_id": taskID,
				"agent_plan_id": planID,
				"plan_bytes":    len(planContent),
			},
			"output": map[string]any{
				"message": "approved plan accepted for execution audit",
			},
		},
		agentRuntimeCheckStep(taskID, opID, runtime),
		{
			"tool_name": "worker.dispatch.plan",
			"input": map[string]any{
				"agent_task_id":    taskID,
				"agent_plan_id":    planID,
				"operation_run_id": opID,
				"mode":             "redacted_worker_dispatch_plan",
			},
			"output": map[string]any{
				"message":              "worker-backed agent execution dispatch is planned for audit only; no worker is claimed and no tool is invoked",
				"worker_dispatch_plan": agentWorkerDispatchPlan(runtime),
			},
		},
		{
			"tool_name": "codex.execution.plan",
			"input": map[string]any{
				"agent_task_id":    taskID,
				"agent_plan_id":    planID,
				"operation_run_id": opID,
				"mode":             "redacted_execution_plan",
			},
			"output": map[string]any{
				"message":              "Codex CLI execution plan is recorded for audit only; process spawning and repository mutation remain disabled",
				"codex_execution_plan": agentCodexExecutionPlan(runtime),
			},
		},
		{
			"tool_name": "patch.prepare",
			"input": map[string]any{
				"agent_task_id": taskID,
				"agent_plan_id": planID,
				"mode":          "simulation_only",
			},
			"output": map[string]any{
				"message":                  "first-version agent execution records intent only; code mutation remains disabled",
				"patch_workflow_guardrail": agentPatchWorkflowGuardrail(),
			},
		},
	}
}

func agentPatchWorkflowGuardrail() map[string]any {
	return map[string]any{
		"execution_mode":              "simulation_only",
		"mutation_enabled":            false,
		"repository_mutation_allowed": false,
		"codex_cli_invocation":        "disabled",
		"pull_request_creation":       "disabled",
		"required_approvals": []string{
			"agent.execute",
			"future.patch.apply",
		},
		"blocked_reasons": []string{
			"codex CLI process execution is not enabled in the first version",
			"repository mutation requires a future approval-gated patch apply operation",
			"pull request creation is not wired to a provider account workflow yet",
		},
		"code_modification_plan": agentCodeModificationPlan(),
		"execution_readiness":    agentExecutionReadinessGates(),
		"next_step":              "Keep execution audit-only until Codex CLI runs, patch application, and PR creation are individually approval-gated.",
		"message":                "Agent patch workflow is audit-only: Codex CLI, repository mutation, and pull request creation are disabled.",
	}
}
