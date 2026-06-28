package app

import (
	"context"
	"strings"
	"time"
)

func (s *Server) repoSyncAssetCapacityMapGorm(ctx context.Context, assetID, sourceID, targetID string) (map[string]any, error) {
	now := time.Now()
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)
	twentyFourHoursAgo := now.Add(-24 * time.Hour)
	raw := map[string]any{}
	var source, target GormGitRemote
	if err := s.store.Gorm.WithContext(ctx).First(&source, "id = ?", sourceID).Error; err != nil {
		return nil, err
	}
	if err := s.store.Gorm.WithContext(ctx).First(&target, "id = ?", targetID).Error; err != nil {
		return nil, err
	}
	raw["source_provider"] = source.ProviderType
	raw["target_provider"] = target.ProviderType
	raw["source_last_sync_status"] = source.LastSyncStatus
	raw["target_last_sync_status"] = target.LastSyncStatus
	var runs []GormRepoSyncRun
	if err := s.store.Gorm.WithContext(ctx).Where(gormField("repo_sync_asset_id", assetID)).Find(&runs).Error; err != nil {
		return nil, err
	}
	activeRuns := 0
	failedRuns7d := 0
	for _, run := range runs {
		if isActiveRepoSyncRunStatus(run.Status) {
			activeRuns++
		}
		if run.Status == "failed" && !run.CreatedAt.Before(sevenDaysAgo) {
			failedRuns7d++
		}
	}
	raw["active_runs"] = activeRuns
	raw["failed_runs_7d"] = failedRuns7d
	var events []GormWebhookEvent
	if err := s.store.Gorm.WithContext(ctx).Where(gormField("matched_repo_sync_asset_id", assetID)).Find(&events).Error; err != nil {
		return nil, err
	}
	webhookFailures7d := 0
	lastWebhookError := ""
	lastWebhookErrorAt := time.Time{}
	for _, event := range events {
		if !isWebhookFailureStatus(event.Status) || event.ReceivedAt.Before(sevenDaysAgo) {
			continue
		}
		webhookFailures7d++
		if strings.TrimSpace(event.ErrorMessage) != "" && event.ReceivedAt.After(lastWebhookErrorAt) {
			lastWebhookError = event.ErrorMessage
			lastWebhookErrorAt = event.ReceivedAt
		}
	}
	raw["webhook_failures_7d"] = webhookFailures7d
	raw["last_webhook_error"] = lastWebhookError
	var githubRuns []GormGitHubActionRun
	if err := s.store.Gorm.WithContext(ctx).
		Where("git_remote_id IN ?", []string{sourceID, targetID}).
		Where("created_at >= ?", twentyFourHoursAgo).
		Find(&githubRuns).Error; err != nil {
		return nil, err
	}
	raw["github_runs_24h"] = len(githubRuns)
	pairAssetIDs, err := s.repoSyncAssetIDsForProviderPairGorm(ctx, source.ProviderType, target.ProviderType)
	if err != nil {
		return nil, err
	}
	pairActiveRuns := 0
	pairRuns24h := 0
	pairFailedRuns24h := 0
	if len(pairAssetIDs) > 0 {
		var pairRuns []GormRepoSyncRun
		if err := s.store.Gorm.WithContext(ctx).Where(gormField("repo_sync_asset_id", pairAssetIDs)).Find(&pairRuns).Error; err != nil {
			return nil, err
		}
		for _, run := range pairRuns {
			if isActiveRepoSyncRunStatus(run.Status) {
				pairActiveRuns++
			}
			if !run.CreatedAt.Before(twentyFourHoursAgo) {
				pairRuns24h++
				if run.Status == "failed" {
					pairFailedRuns24h++
				}
			}
		}
	}
	raw["provider_pair_active_runs"] = pairActiveRuns
	raw["provider_pair_runs_24h"] = pairRuns24h
	raw["provider_pair_failed_runs_24h"] = pairFailedRuns24h
	return raw, nil
}

func (s *Server) repoSyncAssetIDsForProviderPairGorm(ctx context.Context, sourceProvider, targetProvider string) ([]string, error) {
	var assets []GormRepoSyncAsset
	if err := s.store.Gorm.WithContext(ctx).Find(&assets).Error; err != nil {
		return nil, err
	}
	remoteIDs := make([]string, 0, len(assets)*2)
	for _, asset := range assets {
		remoteIDs = append(remoteIDs, asset.SourceRemoteID, asset.TargetRemoteID)
	}
	remotesByID := map[string]GormGitRemote{}
	if len(remoteIDs) > 0 {
		var remotes []GormGitRemote
		if err := s.store.Gorm.WithContext(ctx).Where(gormField("id", remoteIDs)).Find(&remotes).Error; err != nil {
			return nil, err
		}
		for _, remote := range remotes {
			remotesByID[remote.ID] = remote
		}
	}
	assetIDs := make([]string, 0, len(assets))
	for _, asset := range assets {
		source := remotesByID[asset.SourceRemoteID]
		target := remotesByID[asset.TargetRemoteID]
		if source.ProviderType == sourceProvider && target.ProviderType == targetProvider {
			assetIDs = append(assetIDs, asset.ID)
		}
	}
	return assetIDs, nil
}

func isActiveRepoSyncRunStatus(status string) bool {
	switch status {
	case "queued", "running", "provisioning":
		return true
	default:
		return false
	}
}

func isWebhookFailureStatus(status string) bool {
	switch status {
	case "failed", "rejected":
		return true
	default:
		return false
	}
}

const (
	repoSyncCapacityActiveWarningThreshold       = 1
	repoSyncCapacityActiveDangerThreshold        = 3
	repoSyncCapacityFailure7dWarningThreshold    = 1
	repoSyncCapacityFailure7dDangerThreshold     = 5
	repoSyncCapacityWebhookWarningThreshold      = 1
	repoSyncCapacityWebhookDangerThreshold       = 3
	repoSyncCapacityGitHubVolumeWarningThreshold = 50
	repoSyncCapacityGitHubVolumeDangerThreshold  = 200
	repoSyncCapacityPairActiveWarningThreshold   = 3
	repoSyncCapacityPairActiveDangerThreshold    = 8
	repoSyncCapacityPairFailureWarningThreshold  = 1
	repoSyncCapacityPairFailureDangerThreshold   = 3
)

func repoSyncCapacitySignals(asset, raw map[string]any, sourceID, targetID string) []map[string]any {
	return repoSyncCapacitySignalsWithThresholds(asset, raw, sourceID, targetID, nil)
}
