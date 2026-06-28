package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"log/slog"
	"net/http"
)

func (s *Server) cleanupProviderReviewAttemptLive(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "update") {
		return
	}
	attemptID := cleanOptionalID(chi.URLParam(r, "id"))
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "provider review attempt id is required")
		return
	}
	if !s.requireProviderReviewAttemptUpdatePolicy(w, r, attemptID) {
		return
	}
	locked, unlock, err := s.acquireProviderReviewLiveExecutionLock(r.Context(), attemptID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not lock provider review live cleanup")
		return
	}
	if !locked {
		writeJSON(w, http.StatusOK, providerReviewAttemptLiveExecutionConflictResponse(attemptID))
		return
	}
	defer unlock()
	input, attempt, blocked, err := s.providerReviewLiveCleanupInput(r.Context(), attemptID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not prepare provider review live cleanup")
		return
	}
	if blocked != nil {
		writeJSON(w, http.StatusOK, blocked)
		return
	}
	result, cleanupErr := (reviewBranchExecutor{HTTPClient: newTemplateProviderHTTPClient()}).Cleanup(r.Context(), input)
	recorded, err := s.recordProviderReviewAttemptLiveCleanupResult(r.Context(), attemptID, attempt, result, cleanupErr)
	if err != nil {
		log := s.log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("provider review live cleanup result recording failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not record provider review live cleanup result")
		return
	}
	writeJSON(w, http.StatusOK, recorded)
}

func (s *Server) providerReviewLiveExecutionInput(ctx context.Context, attemptID string) (reviewBranchExecutionInput, map[string]any, map[string]any, error) {
	_, _, attempt, err := providerReviewAttemptWithApprovalGorm(ctx, s.store.Gorm, attemptID, false)
	if err != nil {
		return reviewBranchExecutionInput{}, nil, nil, err
	}
	if runID := cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])); runID != "" {
		var run GormProjectTemplateRun
		if err := s.store.Gorm.WithContext(ctx).First(&run, &GormProjectTemplateRun{GormBase: GormBase{ID: runID}}).Error; err != nil && !errorsIsRecordNotFound(err) {
			return reviewBranchExecutionInput{}, nil, nil, err
		} else if err == nil {
			attempt["template_run_result"] = mapFromAny(run.Result.Data)
		}
	}
	missing := providerReviewLiveExecutionMissingEvidence(s.cfg, attempt)
	if len(missing) == 0 {
		assetID, assetErr := providerReviewAttemptAssetID(ctx, s.store.Gorm, attemptID)
		if assetErr != nil {
			missing = append(missing, "provider_review_attempt_asset_missing")
		} else {
			ready, err := providerReviewAttemptStatusObserved(ctx, s.store, assetID, "provider_review_attempt_live_execution_review_ready")
			if err != nil {
				return reviewBranchExecutionInput{}, attempt, nil, err
			}
			if !ready {
				missing = append(missing, "provider_review_attempt_live_execution_readiness")
			}
		}
		arming, err := providerReviewApprovalStatusObserved(ctx, s.store, cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])), "provider_review_mutation_arming_review_ready")
		if err != nil {
			return reviewBranchExecutionInput{}, attempt, nil, err
		}
		if !arming {
			missing = append(missing, "provider_review_mutation_arming_review")
		}
	}
	if len(missing) > 0 {
		return reviewBranchExecutionInput{}, attempt, providerReviewAttemptLiveExecutionBlockedResponse(attempt, "blocked", missing), nil
	}
	runID := cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"]))
	runResult := mapFromAny(attempt["template_run_result"])
	remoteID := cleanOptionalID(fmt.Sprint(mapFromAny(mapFromAny(runResult["details"])["repository_reconciliation"])["remote_id"]))
	if remoteID == "" {
		return reviewBranchExecutionInput{}, attempt, providerReviewAttemptLiveExecutionBlockedResponse(attempt, "blocked", []string{"provider_review_target_remote_missing"}), nil
	}
	remoteModel, repoProjectID, err := s.gitRemoteWithProjectGorm(ctx, remoteID)
	if err != nil {
		return reviewBranchExecutionInput{}, attempt, nil, err
	}
	repoID := cleanOptionalID(remoteModel.ProjectGitRepositoryID)
	repoModel, err := s.gitRepositoryByIDGorm(ctx, repoID)
	if err != nil {
		return reviewBranchExecutionInput{}, attempt, nil, err
	}
	remote := gitRemoteMap(remoteModel, nil, repoProjectID)
	repo := gitRepositoryMap(repoModel)
	spec, ok := buildExternalTemplateProviderSpec(repo, remote)
	if !ok || spec.Provider != "github" {
		return reviewBranchExecutionInput{}, attempt, providerReviewAttemptLiveExecutionBlockedResponse(attempt, "blocked", []string{"provider_review_github_target_missing"}), nil
	}
	if spec.Token == "" {
		return reviewBranchExecutionInput{}, attempt, providerReviewAttemptLiveExecutionBlockedResponse(attempt, "blocked", []string{"provider_token_env_present"}), nil
	}
	files, err := providerReviewStarterFilesForLiveExecutionGorm(ctx, s.store.Gorm, runID, repoID)
	if err != nil {
		return reviewBranchExecutionInput{}, attempt, nil, err
	}
	if len(files) == 0 {
		return reviewBranchExecutionInput{}, attempt, providerReviewAttemptLiveExecutionBlockedResponse(attempt, "blocked", []string{"starter_file_payload_staged"}), nil
	}
	request := mapFromAny(mapFromAny(attempt["approval_request_payload"])["execution_request"])
	reviewBranch := safeProviderReviewExecutionBranch(stringFromMap(request, "source_branch"))
	baseBranch := safeProviderReviewExecutionBranch(stringFromMap(request, "target_branch"))
	if reviewBranch == "" || baseBranch == "" {
		return reviewBranchExecutionInput{}, attempt, providerReviewAttemptLiveExecutionBlockedResponse(attempt, "blocked", []string{"review_branches_valid"}), nil
	}
	return reviewBranchExecutionInput{
		ProviderType: "github",
		APIBase:      spec.APIBase,
		Owner:        spec.Owner,
		Repository:   spec.RepositoryName,
		BaseBranch:   baseBranch,
		ReviewBranch: reviewBranch,
		Files:        files,
		Title:        "ASSOPS provider review",
		Body:         "Created by ASSOPS from an approved provider-review execution.",
		TokenEnv:     spec.TokenEnv,
	}, attempt, nil, nil
}

func providerReviewLiveExecutionMissingEvidence(cfg Config, attempt map[string]any) []string {
	missing := []string{}
	if stringFromMap(attempt, "approval_action") != templateProviderReviewExecuteApprovalAction {
		missing = append(missing, "provider_review_execution_approval_action")
	}
	if stringFromMap(attempt, "approval_status") != "approved" {
		missing = append(missing, "operation_approval_not_approved")
	}
	if !cfg.ProviderReviewExecutionEnabled {
		missing = append(missing, "provider_review_execution_enabled")
	}
	if !cfg.ProviderReviewMutationArmed {
		missing = append(missing, "provider_review_mutation_armed")
	}
	if safeProviderReviewProviderType(stringFromMap(attempt, "provider_type")) != "github" {
		missing = append(missing, "provider_supported")
	}
	if safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name")) != "create_branch_ref" ||
		safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key")) != "github.create_branch_ref" {
		missing = append(missing, "provider_review_claim_metadata")
	}
	if safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")) != "running" || !providerReviewAttemptClaimRecorded(attempt) {
		missing = append(missing, "provider_review_attempt_claim_not_recorded")
	}
	if !providerReviewAttemptClaimDependencyReady(stringFromMap(attempt, "dependency_status")) {
		missing = append(missing, "provider_review_dependency_not_ready")
	}
	if boolOnlyFromAny(attempt["provider_api_call_made"]) || boolOnlyFromAny(attempt["external_call_made"]) {
		missing = append(missing, "provider_review_attempt_already_executed")
	}
	if cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])) == "" {
		missing = append(missing, "project_template_run_missing")
	}
	return missing
}

func (s *Server) providerReviewLiveCleanupInput(ctx context.Context, attemptID string) (reviewBranchExecutionInput, map[string]any, map[string]any, error) {
	_, _, attempt, err := providerReviewAttemptWithApprovalGorm(ctx, s.store.Gorm, attemptID, false)
	if err != nil {
		return reviewBranchExecutionInput{}, nil, nil, err
	}
	if runID := cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])); runID != "" {
		var run GormProjectTemplateRun
		if err := s.store.Gorm.WithContext(ctx).First(&run, &GormProjectTemplateRun{GormBase: GormBase{ID: runID}}).Error; err != nil && !errorsIsRecordNotFound(err) {
			return reviewBranchExecutionInput{}, nil, nil, err
		} else if err == nil {
			attempt["template_run_result"] = mapFromAny(run.Result.Data)
		}
	}
	missing := providerReviewLiveCleanupMissingEvidence(s.cfg, attempt)
	if len(missing) > 0 {
		return reviewBranchExecutionInput{}, attempt, providerReviewAttemptLiveCleanupBlockedResponse(attempt, "blocked", missing), nil
	}
	runResult := mapFromAny(attempt["template_run_result"])
	remoteID := cleanOptionalID(fmt.Sprint(mapFromAny(mapFromAny(runResult["details"])["repository_reconciliation"])["remote_id"]))
	if remoteID == "" {
		return reviewBranchExecutionInput{}, attempt, providerReviewAttemptLiveCleanupBlockedResponse(attempt, "blocked", []string{"provider_review_target_remote_missing"}), nil
	}
	remoteModel, repoProjectID, err := s.gitRemoteWithProjectGorm(ctx, remoteID)
	if err != nil {
		return reviewBranchExecutionInput{}, attempt, nil, err
	}
	repoModel, err := s.gitRepositoryByIDGorm(ctx, cleanOptionalID(remoteModel.ProjectGitRepositoryID))
	if err != nil {
		return reviewBranchExecutionInput{}, attempt, nil, err
	}
	remote := gitRemoteMap(remoteModel, nil, repoProjectID)
	repo := gitRepositoryMap(repoModel)
	spec, ok := buildExternalTemplateProviderSpec(repo, remote)
	if !ok || spec.Provider != "github" {
		return reviewBranchExecutionInput{}, attempt, providerReviewAttemptLiveCleanupBlockedResponse(attempt, "blocked", []string{"provider_review_github_target_missing"}), nil
	}
	if spec.Token == "" {
		return reviewBranchExecutionInput{}, attempt, providerReviewAttemptLiveCleanupBlockedResponse(attempt, "blocked", []string{"provider_token_env_present"}), nil
	}
	request := mapFromAny(mapFromAny(attempt["approval_request_payload"])["execution_request"])
	reviewBranch := safeProviderReviewExecutionBranch(stringFromMap(request, "source_branch"))
	if reviewBranch == "" {
		return reviewBranchExecutionInput{}, attempt, providerReviewAttemptLiveCleanupBlockedResponse(attempt, "blocked", []string{"review_branch_valid"}), nil
	}
	return reviewBranchExecutionInput{
		ProviderType: "github",
		APIBase:      spec.APIBase,
		Owner:        spec.Owner,
		Repository:   spec.RepositoryName,
		ReviewBranch: reviewBranch,
		TokenEnv:     spec.TokenEnv,
		Files:        map[string]string{"cleanup-marker": ""},
		BaseBranch:   "cleanup-base",
		Title:        "ASSOPS provider review cleanup",
		Body:         "",
	}, attempt, nil, nil
}

func providerReviewLiveCleanupMissingEvidence(cfg Config, attempt map[string]any) []string {
	missing := []string{}
	if stringFromMap(attempt, "approval_action") != templateProviderReviewExecuteApprovalAction {
		missing = append(missing, "provider_review_execution_approval_action")
	}
	if stringFromMap(attempt, "approval_status") != "approved" {
		missing = append(missing, "operation_approval_not_approved")
	}
	if !cfg.ProviderReviewExecutionEnabled {
		missing = append(missing, "provider_review_execution_enabled")
	}
	if !cfg.ProviderReviewMutationArmed {
		missing = append(missing, "provider_review_mutation_armed")
	}
	if safeProviderReviewProviderType(stringFromMap(attempt, "provider_type")) != "github" {
		missing = append(missing, "provider_supported")
	}
	if safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name")) != "create_branch_ref" ||
		safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key")) != "github.create_branch_ref" {
		missing = append(missing, "provider_review_claim_metadata")
	}
	if safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")) != "failed" {
		missing = append(missing, "provider_review_attempt_not_failed")
	}
	if !boolOnlyFromAny(attempt["cleanup_required"]) ||
		safeProviderReviewManualCleanupHint(stringFromMap(attempt, "live_execution_manual_cleanup_hint")) != "review_branch_delete_required" {
		missing = append(missing, "provider_review_cleanup_not_required")
	}
	if cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])) == "" {
		missing = append(missing, "project_template_run_missing")
	}
	return missing
}
