package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"log/slog"
	"net/http"
)

func (s *Server) getOperationApproval(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	if expired, err := s.expireOperationApprovalByIDGorm(r.Context(), s.store.Gorm, chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approval")
		return
	} else if expired != nil {
		s.dispatchApprovalNotification(r.Context(), expired, "expired")
	}
	var approvalModel GormOperationApproval
	if err := s.store.Gorm.WithContext(r.Context()).First(&approvalModel, &GormOperationApproval{GormBase: GormBase{ID: chi.URLParam(r, "id")}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	users, projects, decisionCounts, delegated, err := s.operationApprovalAnnotationDataGorm(r.Context(), []GormOperationApproval{approvalModel}, currentUser(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval annotations")
		return
	}
	approval := operationApprovalMap(approvalModel, users, projects, decisionCounts, delegated)
	if !s.requireApprovalRead(w, r, approval) {
		return
	}
	payloadAudit := operationApprovalPayloadAudit(approval)
	delete(approval, "request_payload")
	opID := cleanOptionalID(fmt.Sprint(approval["operation_run_id"]))
	response := map[string]any{"approval": approval, "approval_payload_audit": payloadAudit}
	if stringFromMap(approval, "action") == templateProviderReviewExecuteApprovalAction {
		attemptLedger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
			return
		}
		response["provider_review_attempt_ledger"] = attemptLedger
	}
	decisions, err := s.operationApprovalDecisions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval decisions")
		return
	}
	response["decisions"] = decisions
	delegations, err := s.operationApprovalDelegations(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval delegations")
		return
	}
	response["delegations"] = delegations
	if opID == "" {
		response["operation"] = nil
		response["operation_logs"] = []map[string]any{}
		response["worker_jobs"] = []map[string]any{}
		response["run_records"] = map[string]any{}
		writeJSON(w, http.StatusOK, response)
		return
	}
	var operationModel GormOperationRun
	err = s.store.Gorm.WithContext(r.Context()).First(&operationModel, &GormOperationRun{GormBase: GormBase{ID: opID}}).Error
	if err != nil && !errorsIsRecordNotFound(err) {
		writeError(w, http.StatusInternalServerError, "could not load approval operation")
		return
	}
	var operation map[string]any
	if err == nil {
		operation = operationRunMap(operationModel)
	}
	if operation != nil && !s.requireOperationRead(w, r, operation) {
		return
	}
	operation = safeOperationForAudit(operation)
	var logModels []GormOperationLog
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormOperationLog{OperationRunID: validNullString(opID)}).Order(gormOrderAsc("created_at")).Find(&logModels).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval operation logs")
		return
	}
	logs := make([]map[string]any, 0, len(logModels))
	for _, log := range logModels {
		logs = append(logs, operationLogMap(log))
	}
	var jobModels []GormWorkerJob
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormWorkerJob{OperationRunID: validNullString(opID)}).Order(gormOrderAsc("created_at")).Find(&jobModels).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval worker jobs")
		return
	}
	jobs := make([]map[string]any, 0, len(jobModels))
	for _, job := range jobModels {
		item := workerJobMap(job)
		delete(item, "payload")
		delete(item, "result")
		jobs = append(jobs, item)
	}
	canReadSSHOutput := NewPolicyChecker().Check(currentUser(r), PolicyResource{Type: "ssh_command_run"}, "read").Effect == PolicyAllow
	runRecords, err := s.operationRunRecords(r.Context(), opID, canReadSSHOutput)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval run records")
		return
	}
	response["operation"] = operation
	response["operation_logs"] = logs
	response["worker_jobs"] = jobs
	response["run_records"] = runRecords
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) recordProviderReviewMutationArmingSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "update") {
		return
	}
	approvalID := cleanOptionalID(chi.URLParam(r, "id"))
	if approvalID == "" {
		writeError(w, http.StatusBadRequest, "operation approval id is required")
		return
	}
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	approval, err := providerReviewApprovalForArmingSnapshot(r.Context(), s.store, approvalID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireApprovalRead(w, r, approval) {
		return
	}
	if stringFromMap(approval, "action") != templateProviderReviewExecuteApprovalAction {
		writeError(w, http.StatusConflict, "operation approval is not tied to provider review execution")
		return
	}
	ledger, err := providerReviewAttemptLedgerForApprovalSnapshot(r.Context(), s.store, approvalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewMutationArmingSnapshot(r.Context(), s.store, ProviderReviewMutationArmingSnapshotOptions{
		OperationApprovalID: approvalID,
		DryRun:              req.DryRun,
		Approval:            approval,
		AttemptLedger:       ledger,
	})
	if err != nil {
		log := s.log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("provider review mutation arming snapshot failed", "operation_approval_id", approvalID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review mutation arming snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProviderReviewCurrentAttemptLiveReadinessSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "update") {
		return
	}
	approvalID := cleanOptionalID(chi.URLParam(r, "id"))
	if approvalID == "" {
		writeError(w, http.StatusBadRequest, "operation approval id is required")
		return
	}
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	approval, err := providerReviewApprovalForArmingSnapshot(r.Context(), s.store, approvalID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireApprovalRead(w, r, approval) {
		return
	}
	if stringFromMap(approval, "action") != templateProviderReviewExecuteApprovalAction {
		writeError(w, http.StatusConflict, "operation approval is not tied to provider review execution")
		return
	}
	ledger, err := providerReviewAttemptLedgerForApprovalSnapshot(r.Context(), s.store, approvalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewCurrentAttemptLiveReadinessSnapshot(r.Context(), s.store, ProviderReviewCurrentAttemptLiveReadinessSnapshotOptions{
		OperationApprovalID: approvalID,
		DryRun:              req.DryRun,
		Approval:            approval,
		AttemptLedger:       ledger,
	})
	if err != nil {
		log := s.log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("provider review current attempt live-readiness snapshot failed", "operation_approval_id", approvalID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review current attempt live-readiness snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) providerReviewCurrentAttemptLiveExecutionLaunchPlan(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "update") {
		return
	}
	approvalID := cleanOptionalID(chi.URLParam(r, "id"))
	if approvalID == "" {
		writeError(w, http.StatusBadRequest, "operation approval id is required")
		return
	}
	approval, err := providerReviewApprovalForArmingSnapshot(r.Context(), s.store, approvalID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireApprovalRead(w, r, approval) {
		return
	}
	if stringFromMap(approval, "action") != templateProviderReviewExecuteApprovalAction {
		writeError(w, http.StatusConflict, "operation approval is not tied to provider review execution")
		return
	}
	ledger, err := providerReviewAttemptLedgerForApprovalSnapshot(r.Context(), s.store, approvalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := ProviderReviewCurrentAttemptLiveExecutionLaunchPlan(r.Context(), s.store, ProviderReviewCurrentAttemptLiveExecutionLaunchPlanOptions{
		OperationApprovalID: approvalID,
		Approval:            approval,
		AttemptLedger:       ledger,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "provider review current attempt live launch plan failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
