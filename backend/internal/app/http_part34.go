package app

import (
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) applyWebhookThresholdConfiguration(w http.ResponseWriter, r *http.Request) {
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
		EvidenceWindow string `json:"evidence_window"`
	}
	if !decodeJSON(w, r, &req) {
		return
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
	configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
	decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
	if boolOnlyFromAny(configurationPlan["threshold_configuration_written"]) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     "threshold configuration is already applied",
			"readiness": readiness,
		})
		return
	}
	if !boolOnlyFromAny(configurationPlan["configuration_write_enabled"]) ||
		!boolOnlyFromAny(decisionAuditPlan["operator_threshold_review_recorded"]) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     "threshold configuration requires a recorded threshold decision audit",
			"readiness": readiness,
		})
		return
	}
	thresholds := webhookProviderCallbackCurrentThresholds()
	evidence := map[string]any{
		"mode":                                     "provider_callback_threshold_configuration_evidence",
		"webhook_connection_id":                    connectionID,
		"provider":                                 strings.TrimSpace(fmt.Sprint(connection["provider"])),
		"evidence_window":                          evidenceWindow,
		"threshold_review_state":                   cleanPreviewString(decisionAuditPlan["threshold_review_state"]),
		"configuration_state":                      cleanPreviewString(decisionAuditPlan["configuration_state"]),
		"decision_state":                           cleanPreviewString(decisionAuditPlan["decision_state"]),
		"threshold_configuration_written":          true,
		"threshold_configuration_count":            len(thresholds),
		"operator_threshold_review_recorded":       true,
		"provider_metrics_fetched":                 false,
		"provider_pair_limits_compared":            false,
		"capacity_signals_recomputed":              true,
		"capacity_signal_recompute_mode":           "read_time_repo_sync_asset_detail",
		"external_call_made":                       false,
		"contains_token":                           false,
		"contains_secret":                          false,
		"contains_payload":                         false,
		"contains_provider_url":                    false,
		"raw_request_headers_recorded":             false,
		"raw_request_body_recorded":                false,
		"raw_provider_response_recorded":           false,
		"provider_metrics_comparison_review_ready": boolOnlyFromAny(decisionAuditPlan["decision_ready_for_review"]),
		"suppressed_fields":                        decisionAuditPlan["suppressed_fields"],
	}
	actorID := ""
	if user := currentUser(r); user != nil {
		actorID = strings.TrimSpace(user.ID)
	}
	provider := strings.TrimSpace(fmt.Sprint(connection["provider"]))
	var configurations []GormWebhookThresholdConfiguration
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var latestAudit GormWebhookThresholdDecisionAudit
		if err := tx.Where(&GormWebhookThresholdDecisionAudit{WebhookConnectionID: connectionID, EvidenceWindow: evidenceWindow}).Order(gormOrderDesc("created_at")).First(&latestAudit).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		for _, threshold := range thresholds {
			key := cleanPreviewString(threshold["key"])
			if key == "" {
				continue
			}
			configuration := GormWebhookThresholdConfiguration{
				ProjectID:           projectID,
				WebhookConnectionID: connectionID,
				Provider:            provider,
				ThresholdKey:        key,
				WarningAt:           intFromAny(threshold["warning_at"], 0),
				DangerAt:            intFromAny(threshold["danger_at"], 0),
				Unit:                cleanPreviewString(threshold["unit"]),
				EvidenceWindow:      evidenceWindow,
				SourceAuditID:       validNullString(latestAudit.ID),
				Evidence:            JSONValue{Data: evidence},
				AppliedBy:           validNullString(actorID),
				AppliedAt:           time.Now().UTC(),
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "webhook_connection_id"}, {Name: "threshold_key"}, {Name: "evidence_window"}},
				DoUpdates: clause.Assignments(map[string]any{
					"provider":        configuration.Provider,
					"warning_at":      configuration.WarningAt,
					"danger_at":       configuration.DangerAt,
					"unit":            configuration.Unit,
					"source_audit_id": configuration.SourceAuditID,
					"evidence":        configuration.Evidence,
					"applied_by":      configuration.AppliedBy,
					"applied_at":      configuration.AppliedAt,
				}),
			}).Create(&configuration).Error; err != nil {
				return err
			}
		}
		return tx.Where(&GormWebhookThresholdConfiguration{WebhookConnectionID: connectionID, EvidenceWindow: evidenceWindow}).Order(gormOrderAsc("threshold_key")).Find(&configurations).Error
	}); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":           "threshold configuration requires a matching threshold decision audit",
				"evidence_window": evidenceWindow,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "could not apply threshold configuration")
		return
	}
	if len(configurations) == 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":           "threshold configuration requires a matching threshold decision audit",
			"evidence_window": evidenceWindow,
		})
		return
	}
	configurationMaps := webhookThresholdConfigurationMaps(configurations)
	configurationPlan["threshold_configuration_written"] = true
	configurationPlan["configuration_state"] = "recorded"
	configurationPlan["threshold_configuration_count"] = len(configurationMaps)
	configurationPlan["provider_metrics_fetched"] = false
	recomputeEvidence := webhookThresholdCapacityRecomputeEvidence(connection, len(configurationMaps))
	configurationPlan["capacity_signals_recomputed"] = true
	configurationPlan["capacity_signal_recompute_mode"] = recomputeEvidence["recompute_mode"]
	configurationPlan["capacity_signal_recompute_evidence"] = recomputeEvidence
	configurationPlan["external_call_made"] = false
	decisionAuditPlan["threshold_configuration_written"] = true
	decisionAuditPlan["capacity_signals_recomputed"] = true
	decisionAuditPlan["capacity_signal_recompute_mode"] = recomputeEvidence["recompute_mode"]
	decisionAuditPlan["capacity_signal_recompute_evidence"] = recomputeEvidence
	configurationPlan["threshold_decision_audit_plan"] = decisionAuditPlan
	writeJSON(w, http.StatusCreated, map[string]any{
		"configurations":                       configurationMaps,
		"readiness":                            readiness,
		"threshold_configuration_plan":         configurationPlan,
		"threshold_configuration_written":      true,
		"threshold_configuration_count":        len(configurationMaps),
		"provider_metrics_fetched":             false,
		"provider_pair_limits_compared":        false,
		"capacity_signals_recomputed":          true,
		"capacity_signal_recompute_mode":       recomputeEvidence["recompute_mode"],
		"capacity_signal_recompute_evidence":   recomputeEvidence,
		"external_call_made":                   false,
		"raw_provider_response_recorded":       false,
		"raw_request_or_payload_body_recorded": false,
	})
}

func (s *Server) recordWebhookProviderCallbackRehearsalSnapshot(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	connection, err := s.webhookConnectionWithCallbackReadinessGorm(r.Context(), connectionID, s.publicBaseURL())
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(connection["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_connection", ID: connectionID, ProjectID: projectID}, "update") {
		return
	}
	result, err := RecordWebhookProviderCallbackRehearsalSnapshot(r.Context(), s.store, WebhookProviderCallbackRehearsalSnapshotOptions{
		ConnectionID: connectionID,
		DryRun:       req.DryRun,
		Connection:   connection,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("webhook provider callback rehearsal snapshot failed", "webhook_connection_id", connectionID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusInternalServerError, "record webhook provider callback rehearsal snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func validWebhookEvidenceWindow(value string) bool {
	if len(value) < 2 {
		return false
	}
	unit := value[len(value)-1]
	if unit != 'h' && unit != 'd' && unit != 'w' && unit != 'm' {
		return false
	}
	amount, err := strconv.Atoi(value[:len(value)-1])
	return err == nil && amount > 0
}

func webhookThresholdDecisionAuditMap(audit GormWebhookThresholdDecisionAudit) map[string]any {
	return map[string]any{
		"id":                     audit.ID,
		"project_id":             audit.ProjectID,
		"webhook_connection_id":  audit.WebhookConnectionID,
		"provider":               audit.Provider,
		"threshold_review_state": audit.ThresholdReviewState,
		"decision_state":         audit.DecisionState,
		"operator_decision":      audit.OperatorDecision,
		"evidence_window":        audit.EvidenceWindow,
		"evidence":               mapFromAny(audit.Evidence.Data),
		"created_by":             nullableStringValue(audit.CreatedBy),
		"created_at":             audit.CreatedAt,
	}
}

func webhookThresholdConfigurationMaps(configurations []GormWebhookThresholdConfiguration) []map[string]any {
	items := make([]map[string]any, 0, len(configurations))
	for _, configuration := range configurations {
		items = append(items, map[string]any{
			"id":                    configuration.ID,
			"project_id":            configuration.ProjectID,
			"webhook_connection_id": configuration.WebhookConnectionID,
			"provider":              configuration.Provider,
			"threshold_key":         configuration.ThresholdKey,
			"warning_at":            configuration.WarningAt,
			"danger_at":             configuration.DangerAt,
			"unit":                  configuration.Unit,
			"evidence_window":       configuration.EvidenceWindow,
			"source_audit_id":       nullableStringValue(configuration.SourceAuditID),
			"evidence":              mapFromAny(configuration.Evidence.Data),
			"applied_by":            nullableStringValue(configuration.AppliedBy),
			"applied_at":            configuration.AppliedAt,
		})
	}
	return items
}
