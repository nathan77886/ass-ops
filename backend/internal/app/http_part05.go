package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
)

func (s *Server) requestProjectTemplateProviderReviewExecution(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template"}, "create") {
		return
	}
	user := currentUser(r)
	run, err := s.projectTemplateRunForProviderReviewGorm(r.Context(), chi.URLParam(r, "id"), user)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	payload, err := projectTemplateProviderReviewApprovalPayloadForConfig(run, s.cfg.ProviderReviewExecutionEnabled, s.cfg.ProviderReviewMutationArmed)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	requestedBy := ""
	if user != nil {
		requestedBy = user.ID
	}
	if requestedBy == "" {
		writeError(w, http.StatusForbidden, "provider review execution approval requires a user")
		return
	}
	title := "execute provider review for template run " + fmt.Sprint(run["template_name"])
	approval, err := s.createOperationApproval(
		r.Context(),
		PolicyResource{
			Type:      "project_template_run",
			ID:        cleanOptionalID(fmt.Sprint(run["id"])),
			ProjectID: cleanOptionalID(fmt.Sprint(run["project_id"])),
		},
		templateProviderReviewExecuteApprovalAction,
		title,
		payload,
		requestedBy,
	)
	if err != nil {
		if isUniqueViolation(err, "idx_operation_approvals_pending_once") {
			writeError(w, http.StatusConflict, "provider review execution approval is already pending")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not request provider review execution approval")
		return
	}
	delete(approval, "request_payload")
	writeJSON(w, http.StatusAccepted, map[string]any{"approval": approval})
}

func (s *Server) projectTemplateRunForProviderReviewGorm(ctx context.Context, runID string, user *User) (map[string]any, error) {
	var run GormProjectTemplateRun
	if err := s.store.Gorm.WithContext(ctx).Where(&GormProjectTemplateRun{GormBase: GormBase{ID: runID}}).First(&run).Error; err != nil {
		return nil, gormNotFoundAsErrNotFound(err)
	}
	if !userCanReadAllProjects(user) {
		userID := ""
		if user != nil {
			userID = strings.TrimSpace(user.ID)
		}
		allowed := cleanOptionalID(run.RequestedBy.String) == userID
		projectID := cleanOptionalID(run.ProjectID.String)
		if !allowed && projectID != "" && userID != "" {
			var count int64
			if err := s.store.Gorm.WithContext(ctx).Model(&GormProjectMember{}).Where(&GormProjectMember{ProjectID: projectID, UserID: userID}).Count(&count).Error; err != nil {
				return nil, err
			}
			allowed = count > 0
		}
		if !allowed {
			return nil, ErrNotFound
		}
	}
	item := projectTemplateRunMap(run)
	if templateID := cleanOptionalID(run.ProjectTemplateID.String); templateID != "" {
		var template GormProjectTemplate
		if err := s.store.Gorm.WithContext(ctx).Where(&GormProjectTemplate{GormBase: GormBase{ID: templateID}}).First(&template).Error; err == nil {
			item["template_name"] = template.Name
		} else if !errorsIsRecordNotFound(err) {
			return nil, err
		}
	}
	return item, nil
}

func projectTemplateProviderReviewApprovalPayload(run map[string]any) (map[string]any, error) {
	return projectTemplateProviderReviewApprovalPayloadForConfig(run, false, false)
}

func projectTemplateProviderReviewApprovalPayloadForConfig(run map[string]any, providerReviewExecutionEnabled, providerReviewMutationArmed bool) (map[string]any, error) {
	result := mapFromAny(run["result"])
	details := mapFromAny(result["details"])
	repositoryReconciliation := mapFromAny(details["repository_reconciliation"])
	readiness := mapFromAny(repositoryReconciliation["provider_review_readiness"])
	executionPlan := mapFromAny(readiness["execution_plan"])
	executionRequest := mapFromAny(executionPlan["execution_request"])
	if executionRequest["status"] != "approval_ready" {
		return nil, fmt.Errorf("provider review execution request is not approval ready")
	}
	starterFilePayload := projectTemplateStarterFilePayloadSummary(run)
	executionGuardrail := templateProviderReviewExecutionGuardrailWithStaging(
		stringFromMap(executionRequest, "provider_type"),
		stringFromMap(executionRequest, "review_kind"),
		stringFromMap(executionRequest, "source_branch"),
		stringFromMap(executionRequest, "target_branch"),
		providerReviewExecutionEnabled,
		providerReviewMutationArmed,
		starterFilePayloadReady(starterFilePayload),
	)
	providerAPIRequestPlan := templateProviderReviewAPIRequestPlan(
		stringFromMap(executionRequest, "provider_type"),
		stringFromMap(executionRequest, "review_kind"),
		stringFromMap(executionRequest, "source_branch"),
		stringFromMap(executionRequest, "target_branch"),
		starterFilePayload,
	)
	credentialStrategy := sanitizedProviderReviewCredentialStrategy(mapFromAny(repositoryReconciliation["credential_strategy"]))
	if len(mapFromAny(repositoryReconciliation["credential_strategy"])) == 0 {
		credentialStrategy = sanitizedProviderReviewCredentialStrategy(mapFromAny(executionPlan["credential_strategy"]))
	}
	reconciliation := templateProviderReviewExecutionReconciliation(
		stringFromMap(executionRequest, "provider_type"),
		stringFromMap(executionRequest, "review_kind"),
		starterFilePayload,
		executionGuardrail,
		providerAPIRequestPlan,
		credentialStrategy,
	)
	targetSummary := providerReviewExecutionTargetSummary(
		stringFromMap(executionRequest, "provider_type"),
		stringFromMap(executionRequest, "review_kind"),
		providerAPIRequestPlan,
		starterFilePayload,
		reconciliation,
	)
	projectTemplateRunID := cleanOptionalID(fmt.Sprint(run["id"]))
	if projectTemplateRunID == "" {
		return nil, fmt.Errorf("template run id is required")
	}
	request := map[string]any{
		"status":                   executionRequest["status"],
		"approval_action":          executionRequest["approval_action"],
		"resource_type":            executionRequest["resource_type"],
		"provider_type":            executionRequest["provider_type"],
		"review_kind":              executionRequest["review_kind"],
		"source_branch":            executionRequest["source_branch"],
		"target_branch":            executionRequest["target_branch"],
		"payload_redacted":         true,
		"contains_token":           false,
		"provider_api_mutation":    "disabled",
		"requires_operator_review": true,
	}
	return map[string]any{
		"kind":                           "project_template_provider_review_execute",
		"project_template_run_id":        projectTemplateRunID,
		"project_id":                     cleanOptionalID(fmt.Sprint(run["project_id"])),
		"execution_request":              request,
		"execution_guardrail":            executionGuardrail,
		"credential_strategy":            credentialStrategy,
		"starter_file_payload":           starterFilePayload,
		"provider_api_request_plan":      providerAPIRequestPlan,
		"provider_review_reconciliation": reconciliation,
		"provider_review_target_summary": targetSummary,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"message":                        "Provider review execution is approval-gated; provider API mutation remains disabled in the first version.",
	}, nil
}

func projectTemplateStarterFilePayloadSummary(run map[string]any) map[string]any {
	result := mapFromAny(run["result"])
	return starterFilePayloadSummaryFromFiles(mapSliceFromAny(result["template_files"]))
}

func starterFilePayloadSummaryFromFiles(files []map[string]any) map[string]any {
	if len(files) == 0 {
		return map[string]any{
			"status":           "blocked",
			"file_count":       0,
			"files":            []map[string]any{},
			"payload_redacted": true,
			"content_included": false,
			"blocked_reason":   "template run result does not include starter file summaries",
		}
	}
	summaries := make([]map[string]any, 0, len(files))
	for _, file := range files {
		path := safeTemplateFilePath(stringFromMap(file, "path"))
		if path == "" {
			continue
		}
		summaries = append(summaries, map[string]any{
			"id":     cleanOptionalID(fmt.Sprint(file["id"])),
			"path":   path,
			"kind":   cleanOptionalText(firstNonEmptyString(stringFromMap(file, "kind"), "text")),
			"status": cleanOptionalText(firstNonEmptyString(stringFromMap(file, "status"), "planned")),
		})
	}
	if len(summaries) == 0 {
		return map[string]any{
			"status":           "blocked",
			"file_count":       0,
			"files":            []map[string]any{},
			"payload_redacted": true,
			"content_included": false,
			"blocked_reason":   "template run result does not include safe starter file paths",
		}
	}
	return map[string]any{
		"status":           "ready",
		"file_count":       len(summaries),
		"files":            summaries,
		"payload_redacted": true,
		"content_included": false,
	}
}

func sanitizedStarterFilePayloadSummary(payload map[string]any) map[string]any {
	return starterFilePayloadSummaryFromFiles(mapSliceFromAny(payload["files"]))
}

func starterFilePayloadReady(payload map[string]any) bool {
	return payload["status"] == "ready" && intFromAny(payload["file_count"], 0) > 0 && payload["content_included"] == false
}

func (s *Server) providerReviewStarterFilePayloadForExecution(ctx context.Context, payload map[string]any) map[string]any {
	runID := cleanOptionalID(stringFromMap(payload, "project_template_run_id"))
	if s != nil && s.store != nil && s.store.Gorm != nil && runID != "" {
		var run GormProjectTemplateRun
		if err := s.store.Gorm.WithContext(ctx).First(&run, &GormProjectTemplateRun{GormBase: GormBase{ID: runID}}).Error; err == nil {
			return projectTemplateStarterFilePayloadSummary(projectTemplateRunMap(run))
		}
	}
	return sanitizedStarterFilePayloadSummary(mapFromAny(payload["starter_file_payload"]))
}

func canRetryTemplateProvision(run map[string]any) bool {
	if run == nil {
		return false
	}
	result := mapFromAny(run["result"])
	if provisioned, _ := result["repository_provisioned"].(bool); provisioned {
		return false
	}
	if cleanOptionalID(fmt.Sprint(run["project_id"])) == "" {
		return false
	}
	status := strings.TrimSpace(fmt.Sprint(run["status"]))
	return status == "failed" || status == "completed"
}
