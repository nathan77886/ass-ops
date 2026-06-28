package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *Server) getRepoSyncAsset(w http.ResponseWriter, r *http.Request) {
	assetID := chi.URLParam(r, "id")
	asset, err := s.repoSyncAssetDetailMapGorm(r.Context(), assetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(asset["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ID: assetID, ProjectID: projectID}, "read") {
		return
	}
	runs, err := s.repoSyncAssetRunMapsGorm(r.Context(), assetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list repo sync runs")
		return
	}
	events, err := s.repoSyncAssetWebhookEventMapsGorm(r.Context(), assetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list webhook events")
		return
	}
	logs, err := s.repoSyncAssetOperationLogMapsGorm(r.Context(), assetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list operation logs")
		return
	}
	trend, err := s.repoSyncAssetTrendGorm(r.Context(), assetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load repo sync trend")
		return
	}
	capacity, err := s.repoSyncAssetCapacitySignals(r.Context(), asset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load repo sync capacity signals")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"asset":            asset,
		"runs":             runs,
		"webhook_events":   events,
		"operation_logs":   logs,
		"trend":            trend,
		"capacity_signals": capacity,
	})
}

func (s *Server) repoSyncAssetRunMapsGorm(ctx context.Context, assetID string) ([]map[string]any, error) {
	var runs []GormRepoSyncRun
	if err := s.store.Gorm.WithContext(ctx).
		Where("repo_sync_asset_id = ?", assetID).
		Order("created_at DESC").
		Limit(50).
		Find(&runs).Error; err != nil {
		return nil, err
	}
	opIDs := make([]string, 0, len(runs))
	for _, run := range runs {
		if strings.TrimSpace(run.OperationRunID) != "" {
			opIDs = append(opIDs, run.OperationRunID)
		}
	}
	opsByID := make(map[string]GormOperationRun, len(opIDs))
	if len(opIDs) > 0 {
		var ops []GormOperationRun
		if err := s.store.Gorm.WithContext(ctx).Where(gormField("id", opIDs)).Find(&ops).Error; err != nil {
			return nil, err
		}
		for _, op := range ops {
			opsByID[op.ID] = op
		}
	}
	items := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		item := repoSyncRunMap(run)
		if op, ok := opsByID[run.OperationRunID]; ok {
			item["operation_type"] = op.OperationType
			item["operation_title"] = op.Title
			item["operation_status"] = op.Status
			item["operation_error"] = op.Error
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Server) repoSyncAssetWebhookEventMapsGorm(ctx context.Context, assetID string) ([]map[string]any, error) {
	var events []GormWebhookEvent
	if err := s.store.Gorm.WithContext(ctx).
		Where("matched_repo_sync_asset_id = ?", assetID).
		Order("received_at DESC").
		Limit(50).
		Find(&events).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(events))
	for _, event := range events {
		items = append(items, map[string]any{
			"id":                         event.ID,
			"webhook_connection_id":      nullableStringValue(event.WebhookConnectionID),
			"project_id":                 nullableStringValue(event.ProjectID),
			"provider":                   event.Provider,
			"event_type":                 event.EventType,
			"delivery_id":                event.DeliveryID,
			"signature_valid":            event.SignatureValid,
			"matched_repo_sync_asset_id": nullableStringValue(event.MatchedRepoSyncAssetID),
			"operation_run_id":           nullableStringValue(event.OperationRunID),
			"status":                     event.Status,
			"error_message":              event.ErrorMessage,
			"received_at":                event.ReceivedAt,
			"processed_at":               nullableTimeAny(event.ProcessedAt),
		})
	}
	return items, nil
}

func (s *Server) repoSyncAssetOperationLogMapsGorm(ctx context.Context, assetID string) ([]map[string]any, error) {
	var runs []GormRepoSyncRun
	if err := s.store.Gorm.WithContext(ctx).
		Where("repo_sync_asset_id = ?", assetID).
		Where("operation_run_id <> ''").
		Find(&runs).Error; err != nil {
		return nil, err
	}
	opIDs := make([]string, 0, len(runs))
	for _, run := range runs {
		opID := strings.TrimSpace(run.OperationRunID)
		if opID != "" {
			opIDs = append(opIDs, opID)
		}
	}
	if len(opIDs) == 0 {
		return []map[string]any{}, nil
	}
	var logs []GormOperationLog
	if err := s.store.Gorm.WithContext(ctx).
		Where("operation_run_id IN ?", opIDs).
		Order("created_at DESC").
		Limit(100).
		Find(&logs).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(logs))
	for _, log := range logs {
		items = append(items, operationLogMap(log))
	}
	return items, nil
}

func (s *Server) repoSyncAssetTrendGorm(ctx context.Context, assetID string) ([]map[string]any, error) {
	since := time.Now().Add(-14 * 24 * time.Hour)
	var runs []GormRepoSyncRun
	if err := s.store.Gorm.WithContext(ctx).
		Where("repo_sync_asset_id = ?", assetID).
		Where("created_at >= ?", since).
		Find(&runs).Error; err != nil {
		return nil, err
	}
	type bucket struct {
		Day              string
		TotalRuns        int
		CompletedRuns    int
		FailedRuns       int
		ActiveRuns       int
		DurationTotalSec float64
		DurationCount    int
	}
	buckets := map[string]*bucket{}
	for _, run := range runs {
		day := run.CreatedAt.Format("2006-01-02")
		b, ok := buckets[day]
		if !ok {
			b = &bucket{Day: day}
			buckets[day] = b
		}
		b.TotalRuns++
		switch run.Status {
		case "completed":
			b.CompletedRuns++
		case "failed":
			b.FailedRuns++
		case "queued", "running", "provisioning":
			b.ActiveRuns++
		}
		if run.StartedAt.Valid && run.FinishedAt.Valid {
			b.DurationTotalSec += run.FinishedAt.Time.Sub(run.StartedAt.Time).Seconds()
			b.DurationCount++
		}
	}
	days := make([]string, 0, len(buckets))
	for day := range buckets {
		days = append(days, day)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	if len(days) > 14 {
		days = days[:14]
	}
	items := make([]map[string]any, 0, len(days))
	for _, day := range days {
		b := buckets[day]
		var avg any
		if b.DurationCount > 0 {
			avg = math.Round((b.DurationTotalSec/float64(b.DurationCount))*100) / 100
		}
		items = append(items, map[string]any{
			"day":                  b.Day,
			"total_runs":           b.TotalRuns,
			"completed_runs":       b.CompletedRuns,
			"failed_runs":          b.FailedRuns,
			"active_runs":          b.ActiveRuns,
			"avg_duration_seconds": avg,
		})
	}
	return items, nil
}

func (s *Server) repoSyncAssetCapacitySignals(ctx context.Context, asset map[string]any) ([]map[string]any, error) {
	assetID := strings.TrimSpace(fmt.Sprint(asset["id"]))
	sourceID := strings.TrimSpace(fmt.Sprint(asset["source_remote_id"]))
	targetID := strings.TrimSpace(fmt.Sprint(asset["target_remote_id"]))
	raw, err := s.repoSyncAssetCapacityMapGorm(ctx, assetID, sourceID, targetID)
	if err != nil {
		return nil, err
	}
	thresholds, err := s.queryWebhookThresholdConfigurationOverridesGorm(ctx, sourceID, "7d")
	if err != nil {
		return nil, err
	}
	return repoSyncCapacitySignalsWithThresholds(asset, raw, sourceID, targetID, thresholds), nil
}
