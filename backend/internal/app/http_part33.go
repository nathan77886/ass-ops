package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
)

func (s *Server) recordWebhookThresholdDecisionAudit(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	connection, err := s.webhookConnectionWithCallbackReadinessGorm(r.Context(), connectionID, s.publicBaseURL())
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(connection["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_connection", ID: connectionID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		OperatorDecision string `json:"operator_decision"`
		EvidenceWindow   string `json:"evidence_window"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	operatorDecision := strings.TrimSpace(req.OperatorDecision)
	if operatorDecision == "" {
		operatorDecision = "record_metadata_review"
	}
	evidenceWindow := strings.TrimSpace(req.EvidenceWindow)
	if evidenceWindow == "" {
		evidenceWindow = "7d"
	}
	if !validWebhookEvidenceWindow(evidenceWindow) {
		writeError(w, http.StatusBadRequest, "invalid evidence window")
		return
	}
	readiness := mapFromAny(connection["callback_rehearsal"])
	providerPlan := mapFromAny(readiness["provider_rehearsal_plan"])
	thresholdPlan := mapFromAny(providerPlan["threshold_tuning_plan"])
	volumeEvidence := mapFromAny(thresholdPlan["volume_evidence"])
	configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
	decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
	if !boolOnlyFromAny(decisionAuditPlan["decision_ready_for_review"]) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     "threshold decision audit requires review-ready callback volume evidence",
			"readiness": readiness,
		})
		return
	}
	metricsComparisonPlan := mapFromAny(thresholdPlan["provider_metrics_comparison_plan"])
	evidence := map[string]any{
		"mode":                                     "provider_callback_threshold_decision_audit_evidence",
		"webhook_connection_id":                    connectionID,
		"provider":                                 strings.TrimSpace(fmt.Sprint(connection["provider"])),
		"operator_decision":                        operatorDecision,
		"evidence_window":                          evidenceWindow,
		"threshold_review_state":                   cleanPreviewString(decisionAuditPlan["threshold_review_state"]),
		"configuration_state":                      cleanPreviewString(decisionAuditPlan["configuration_state"]),
		"decision_state":                           cleanPreviewString(decisionAuditPlan["decision_state"]),
		"comparison_state":                         cleanPreviewString(metricsComparisonPlan["comparison_state"]),
		"delivery_count_7d":                        intFromAny(decisionAuditPlan["delivery_count_7d"], 0),
		"processed_count_7d":                       intFromAny(volumeEvidence["processed_count_7d"], 0),
		"failed_count_7d":                          intFromAny(decisionAuditPlan["failed_count_7d"], 0),
		"operation_run_count_7d":                   intFromAny(decisionAuditPlan["operation_run_count_7d"], 0),
		"matched_repo_sync_asset_count_7d":         intFromAny(decisionAuditPlan["matched_repo_sync_asset_count_7d"], 0),
		"local_volume_observed":                    boolOnlyFromAny(volumeEvidence["local_volume_observed"]),
		"repo_sync_volume_observed":                boolOnlyFromAny(volumeEvidence["repo_sync_volume_observed"]),
		"processed_or_bound_volume_observed":       boolOnlyFromAny(volumeEvidence["processed_or_bound_volume_observed"]),
		"provider_metrics_comparison_review_ready": boolOnlyFromAny(metricsComparisonPlan["comparison_ready_for_review"]),
		"threshold_configuration_written":          false,
		"provider_metrics_fetched":                 false,
		"provider_pair_limits_compared":            false,
		"external_call_made":                       false,
		"contains_token":                           false,
		"contains_secret":                          false,
		"contains_payload":                         false,
		"contains_provider_url":                    false,
		"raw_request_headers_recorded":             false,
		"raw_request_body_recorded":                false,
		"raw_provider_response_recorded":           false,
		"suppressed_fields":                        decisionAuditPlan["suppressed_fields"],
	}
	actorID := ""
	if user := currentUser(r); user != nil {
		actorID = strings.TrimSpace(user.ID)
	}
	provider := strings.TrimSpace(fmt.Sprint(connection["provider"]))
	auditModel := GormWebhookThresholdDecisionAudit{
		ProjectID:            projectID,
		WebhookConnectionID:  connectionID,
		Provider:             provider,
		ThresholdReviewState: cleanPreviewString(decisionAuditPlan["threshold_review_state"]),
		DecisionState:        cleanPreviewString(decisionAuditPlan["decision_state"]),
		OperatorDecision:     operatorDecision,
		EvidenceWindow:       evidenceWindow,
		Evidence:             JSONValue{Data: evidence},
		CreatedBy:            validNullString(actorID),
	}
	if err := s.store.Gorm.WithContext(r.Context()).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "webhook_connection_id"}, {Name: "decision_state"}, {Name: "evidence_window"}},
		DoUpdates: clause.Assignments(map[string]any{
			"provider":               provider,
			"threshold_review_state": auditModel.ThresholdReviewState,
			"operator_decision":      operatorDecision,
			"evidence":               JSONValue{Data: evidence},
			"created_by":             validNullString(actorID),
		}),
	}).Create(&auditModel).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not record threshold decision audit")
		return
	}
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormWebhookThresholdDecisionAudit{WebhookConnectionID: connectionID, DecisionState: auditModel.DecisionState, EvidenceWindow: evidenceWindow}).First(&auditModel).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not reload threshold decision audit")
		return
	}
	decisionAuditPlan["threshold_configuration_audit_inserted"] = true
	decisionAuditPlan["audit_insert_enabled"] = true
	decisionAuditPlan["operator_threshold_review_recorded"] = true
	writeJSON(w, http.StatusCreated, map[string]any{
		"audit":                         webhookThresholdDecisionAuditMap(auditModel),
		"readiness":                     readiness,
		"threshold_decision_audit_plan": decisionAuditPlan,
	})
}
