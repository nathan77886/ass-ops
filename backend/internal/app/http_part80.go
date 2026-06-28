package app

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Server) executeApprovedOperation(ctx context.Context, tx *gorm.DB, approval map[string]any) (map[string]any, string, error) {
	payload := mapFromAny(approval["request_payload"])
	actorID := cleanOptionalID(fmt.Sprint(approval["requested_by"]))
	if actorID == "" {
		return nil, "", fmt.Errorf("approval has no requester")
	}
	switch stringFromMap(payload, "kind") {
	case "repository_tag":
		var req repositoryTagRequest
		if err := decodePayloadField(payload, "request", &req); err != nil {
			return nil, "", fmt.Errorf("invalid repository tag approval payload")
		}
		runs, err := s.enqueueRepositoryTagRunsGorm(ctx, tx, stringFromMap(payload, "repo_id"), req, actorID)
		if err != nil {
			return nil, "", err
		}
		operationRunID := ""
		if len(runs) > 0 {
			operationRunID = cleanOptionalID(fmt.Sprint(runs[0]["operation_run_id"]))
		}
		return map[string]any{"items": runs}, operationRunID, nil
	case "remote_operation":
		op, err := s.enqueueRemoteOperationRunGorm(ctx, tx, stringFromMap(payload, "remote_id"), stringFromMap(payload, "tool"), mapFromAny(payload["input"]), actorID)
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"operation": op}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "ssh_command":
		op, run, err := s.enqueueSSHCommandRunGorm(ctx, tx, stringFromMap(payload, "machine_id"), mapFromAny(payload["input"]), actorID, "ssh.exec", "")
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"operation": op, "run": run}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "agent_execute":
		op, err := s.enqueueAgentTaskExecutionGorm(ctx, tx, stringFromMap(payload, "agent_task_id"))
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"operation": op}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "argo_pod_logs":
		op, err := s.enqueueArgoPodLogOperationGorm(ctx, tx, mapFromAny(payload["input"]))
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"operation": op}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "argo_pod_restart":
		op, err := s.enqueueArgoPodRestartOperationGorm(ctx, tx, mapFromAny(payload["input"]))
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"operation": op}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "config_git_commit":
		repoID := cleanOptionalID(stringFromMap(payload, "repo_id"))
		projectID := cleanOptionalID(stringFromMap(payload, "project_id"))
		if repoID == "" || projectID == "" {
			return nil, "", fmt.Errorf("config git workflow approval is missing repository metadata")
		}
		repo, remotes, _, preview, err := s.configRepositoryScaffoldPreviewForRequest(ctx, repoID, projectID)
		if err != nil {
			return nil, "", err
		}
		commitPlan := mapFromAny(preview["git_commit_plan"])
		if commitPlan["plan_state"] != "planned" {
			return nil, "", fmt.Errorf("config git workflow is not ready")
		}
		op, err := enqueueConfigRepositoryGitWorkflowGorm(ctx, tx, projectID, repo, remotes, preview, actorID)
		if err != nil {
			return nil, "", err
		}
		return map[string]any{
			"operation":                op,
			"operation_request_result": configRepositoryGitWorkflowRequestResult(op),
			"git_commit_plan":          commitPlan,
			"external_call_made":       false,
			"git_write_performed":      false,
			"file_content_included":    false,
			"secret_included":          false,
		}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "operation_cancel":
		op, err := s.cancelOperationRunGorm(ctx, tx, stringFromMap(payload, "operation_id"))
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"operation": op}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "project_template_provider_review_execute":
		request := mapFromAny(payload["execution_request"])
		starterFilePayload := s.providerReviewStarterFilePayloadForExecution(ctx, payload)
		providerAPIRequestPlan := templateProviderReviewAPIRequestPlan(
			stringFromMap(request, "provider_type"),
			stringFromMap(request, "review_kind"),
			stringFromMap(request, "source_branch"),
			stringFromMap(request, "target_branch"),
			starterFilePayload,
		)
		guardrail := templateProviderReviewExecutionGuardrailWithStaging(
			stringFromMap(request, "provider_type"),
			stringFromMap(request, "review_kind"),
			stringFromMap(request, "source_branch"),
			stringFromMap(request, "target_branch"),
			s.cfg.ProviderReviewExecutionEnabled,
			s.cfg.ProviderReviewMutationArmed,
			starterFilePayloadReady(starterFilePayload),
		)
		credentialStrategy := sanitizedProviderReviewCredentialStrategy(mapFromAny(payload["credential_strategy"]))
		if len(mapFromAny(payload["credential_strategy"])) == 0 {
			credentialStrategy = sanitizedProviderReviewCredentialStrategy(mapFromAny(mapFromAny(payload["provider_review_reconciliation"])["credential_strategy"]))
		}
		reconciliation := templateProviderReviewExecutionReconciliation(
			stringFromMap(request, "provider_type"),
			stringFromMap(request, "review_kind"),
			starterFilePayload,
			guardrail,
			providerAPIRequestPlan,
			credentialStrategy,
		)
		targetSummary := providerReviewExecutionTargetSummary(
			stringFromMap(request, "provider_type"),
			stringFromMap(request, "review_kind"),
			providerAPIRequestPlan,
			starterFilePayload,
			reconciliation,
		)
		attemptLedger, err := s.recordProviderReviewAttemptLedgerGorm(
			ctx,
			tx,
			cleanOptionalID(fmt.Sprint(approval["id"])),
			stringFromMap(payload, "project_template_run_id"),
			reconciliation,
		)
		if err != nil {
			return nil, "", err
		}
		return map[string]any{
			"project_template_run_id":        stringFromMap(payload, "project_template_run_id"),
			"execution_request":              request,
			"execution_guardrail":            guardrail,
			"credential_strategy":            credentialStrategy,
			"starter_file_payload":           starterFilePayload,
			"provider_api_request_plan":      providerAPIRequestPlan,
			"provider_review_reconciliation": reconciliation,
			"provider_review_target_summary": targetSummary,
			"provider_review_attempt_ledger": attemptLedger,
			"provider_api_call_made":         false,
			"provider_api_mutation":          "disabled",
			"execution_enabled":              false,
			"message":                        "Provider review execution approval was recorded; provider API branch creation and PR/MR mutation remain disabled.",
		}, "", nil
	default:
		return nil, "", fmt.Errorf("unsupported approval payload")
	}
}

func decodePayloadField(payload map[string]any, key string, target any) error {
	data, err := json.Marshal(payload[key])
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func (s *Server) recordProviderReviewAttemptLedgerGorm(ctx context.Context, tx *gorm.DB, approvalID, runID string, reconciliation map[string]any) (map[string]any, error) {
	summary := providerReviewAttemptLedgerSummary(nil)
	if tx == nil || approvalID == "" {
		return summary, nil
	}
	idempotencyPlan := mapFromAny(reconciliation["idempotency_plan"])
	operations := mapSliceFromAny(idempotencyPlan["operations"])
	if len(operations) == 0 {
		return summary, nil
	}
	provider := cleanOptionalText(stringFromMap(reconciliation, "provider_type"))
	reviewKind := cleanOptionalText(stringFromMap(reconciliation, "review_kind"))
	projectTemplateRunID := cleanOptionalID(runID)
	attempts := make([]map[string]any, 0, len(operations))
	for _, operation := range operations {
		name := cleanOptionalText(stringFromMap(operation, "name"))
		if name == "" {
			continue
		}
		endpointKey := cleanOptionalText(stringFromMap(operation, "endpoint_key"))
		dependency := providerReviewAttemptDependency(name)
		requestSummary := providerReviewAttemptRequestSummary(operation, providerReviewExecutionBlueprintOperationForEndpoint(reconciliation, endpointKey))
		responseDiagnostics := providerReviewAttemptResponseDiagnostics(reconciliation, endpointKey)
		attempt := GormProviderReviewAttempt{
			OperationApprovalID:    approvalID,
			ProjectTemplateRunID:   validNullString(projectTemplateRunID),
			ProviderType:           provider,
			ReviewKind:             reviewKind,
			OperationName:          name,
			EndpointKey:            endpointKey,
			Status:                 "planned",
			ReplayCheck:            cleanOptionalText(stringFromMap(operation, "replay_check")),
			ConflictPolicy:         cleanOptionalText(stringFromMap(operation, "conflict_policy")),
			RetryPolicy:            cleanOptionalText(stringFromMap(operation, "retry_policy")),
			OperationOrder:         intFromAny(dependency["operation_order"], 0),
			DependsOnOperation:     cleanOptionalText(fmt.Sprint(dependency["depends_on_operation"])),
			DependencyStatus:       cleanOptionalText(fmt.Sprint(dependency["dependency_status"])),
			IdempotencyKeyKind:     "operation_scope_hash",
			IdempotencyKeyHash:     "",
			IdempotencyKeyMaterial: JSONValue{Data: map[string]any{"material": "redacted_required_material_only"}},
			RequestSummary:         JSONValue{Data: requestSummary},
			ResponseDiagnostics:    JSONValue{Data: responseDiagnostics},
			ProviderAPICallMade:    false,
			ProviderAPIMutation:    "disabled",
			ExternalCallMade:       false,
		}
		if err := tx.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "operation_approval_id"}, {Name: "operation_name"}},
			DoUpdates: clause.Assignments(map[string]any{
				"endpoint_key":         attempt.EndpointKey,
				"status":               attempt.Status,
				"replay_check":         attempt.ReplayCheck,
				"conflict_policy":      attempt.ConflictPolicy,
				"retry_policy":         attempt.RetryPolicy,
				"operation_order":      attempt.OperationOrder,
				"depends_on_operation": attempt.DependsOnOperation,
				"dependency_status":    attempt.DependencyStatus,
				"request_summary":      attempt.RequestSummary,
				"response_diagnostics": attempt.ResponseDiagnostics,
			}),
		}).Create(&attempt).Error; err != nil {
			return summary, fmt.Errorf("recording provider review attempt: %w", err)
		}
		if err := tx.WithContext(ctx).Where(&GormProviderReviewAttempt{OperationApprovalID: approvalID, OperationName: name}).First(&attempt).Error; err != nil {
			return summary, fmt.Errorf("loading provider review attempt: %w", err)
		}
		attempts = append(attempts, providerReviewAttemptMap(attempt, nil))
	}
	return providerReviewAttemptLedgerSummary(attempts), nil
}

func providerReviewExecutionBlueprintOperationForEndpoint(reconciliation map[string]any, endpointKey string) map[string]any {
	if endpointKey == "" {
		return map[string]any{}
	}
	blueprint := mapFromAny(reconciliation["execution_blueprint"])
	for _, operation := range mapSliceFromAny(blueprint["operations"]) {
		if cleanOptionalText(stringFromMap(operation, "endpoint_key")) == endpointKey {
			return operation
		}
	}
	return map[string]any{}
}

func providerReviewAttemptDependency(operationName string) map[string]any {
	switch cleanOptionalText(operationName) {
	case "create_branch_ref":
		return map[string]any{"operation_order": 10, "depends_on_operation": "", "dependency_status": "independent"}
	case "commit_starter_files":
		return map[string]any{"operation_order": 20, "depends_on_operation": "create_branch_ref", "dependency_status": "waiting_for_dependency"}
	case "open_review_request":
		return map[string]any{"operation_order": 30, "depends_on_operation": "commit_starter_files", "dependency_status": "waiting_for_dependency"}
	default:
		return map[string]any{"operation_order": 100, "depends_on_operation": "", "dependency_status": "independent"}
	}
}
