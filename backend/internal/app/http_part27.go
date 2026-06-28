package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
)

func (s *Server) rerunRepoSyncRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	var previousRun GormRepoSyncRun
	if err := s.store.Gorm.WithContext(r.Context()).First(&previousRun, &GormRepoSyncRun{ID: runID}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	assetID := cleanOptionalID(previousRun.RepoSyncAssetID.String)
	if assetID == "" {
		writeQueryOne(w, nil, ErrNotFound)
		return
	}
	var assetModel GormRepoSyncAsset
	if err := s.store.Gorm.WithContext(r.Context()).First(&assetModel, &GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := assetModel.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ID: assetID, ProjectID: projectID}, "repo.sync") {
		return
	}
	var newRun map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var lockedAsset GormRepoSyncAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedAsset, &GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		if !lockedAsset.Enabled {
			return errRepoSyncAssetDisabled
		}
		if lockedAsset.ArchivedAt.Valid {
			return errRepoSyncAssetArchived
		}
		repoID := cleanOptionalID(previousRun.ProjectGitRepositoryID.String)
		var repoModel GormProjectGitRepository
		if err := tx.First(&repoModel, &GormProjectGitRepository{GormBase: GormBase{ID: repoID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		repo := gitRepositoryMap(repoModel)
		source, err := remoteForRepositoryGorm(r.Context(), tx, repoID, cleanOptionalID(previousRun.SourceRemoteID.String))
		if err != nil {
			return fmt.Errorf("source remote not found in repository")
		}
		target, err := remoteForRepositoryGorm(r.Context(), tx, repoID, cleanOptionalID(previousRun.TargetRemoteID.String))
		if err != nil {
			return fmt.Errorf("target remote not found in repository")
		}
		refs := refsFromRunRef(previousRun.Ref, mapFromAny(lockedAsset.Refs.Data))
		newRun, err = s.enqueueRepoSyncRunGorm(r.Context(), tx, repo, source, target, refs, false, currentUser(r).ID, assetID)
		if err != nil {
			return fmt.Errorf("could not enqueue repo sync rerun")
		}
		if err := tx.Model(&GormRepoSyncAsset{}).
			Where(&GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).
			Updates(map[string]any{"last_sync_status": "queued", "last_sync_run_id": validNullString(cleanOptionalID(fmt.Sprint(newRun["id"])))}).Error; err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		if errors.Is(err, errRepoSyncAssetDisabled) {
			writeError(w, http.StatusConflict, "repo sync asset is disabled")
			return
		}
		if errors.Is(err, errRepoSyncAssetArchived) {
			writeError(w, http.StatusConflict, "repo sync asset is archived")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"run": newRun})
}

func (s *Server) enqueueRepoSyncRunGorm(ctx context.Context, tx *gorm.DB, repo, source, target map[string]any, refs map[string]any, allowForce bool, actorID, repoSyncAssetID string) (map[string]any, error) {
	repoID := cleanOptionalID(fmt.Sprint(repo["id"]))
	sourceID := cleanOptionalID(fmt.Sprint(source["id"]))
	targetID := cleanOptionalID(fmt.Sprint(target["id"]))
	if repoID == "" || sourceID == "" || targetID == "" {
		return nil, fmt.Errorf("repository, source, and target are required")
	}
	if sourceID == targetID {
		return nil, fmt.Errorf("source and target remotes must differ")
	}
	if cleanOptionalID(fmt.Sprint(source["project_git_repository_id"])) != repoID || cleanOptionalID(fmt.Sprint(target["project_git_repository_id"])) != repoID {
		return nil, fmt.Errorf("source and target remotes must belong to the repository")
	}
	input := map[string]any{
		"project_git_repository_id": repoID,
		"source_remote_id":          sourceID,
		"target_remote_id":          targetID,
		"refs":                      refs,
		"allow_force":               allowForce,
	}
	if repoSyncAssetID != "" {
		input["repo_sync_asset_id"] = repoSyncAssetID
	}
	op, err := enqueueOperationGorm(ctx, tx, cleanOptionalID(fmt.Sprint(repo["project_id"])), targetID, "repo.sync_remote", "sync "+fmt.Sprint(source["name"])+" -> "+fmt.Sprint(target["name"]), input, []string{"git"}, "")
	if err != nil {
		return nil, err
	}
	run := GormRepoSyncRun{
		OperationRunID:         cleanOptionalID(fmt.Sprint(op["id"])),
		GitRemoteID:            targetID,
		ProjectID:              validNullString(cleanOptionalID(fmt.Sprint(repo["project_id"]))),
		ProjectGitRepositoryID: validNullString(repoID),
		RepoSyncAssetID:        validNullString(repoSyncAssetID),
		SourceRemoteID:         validNullString(sourceID),
		TargetRemoteID:         validNullString(targetID),
		Ref:                    refsSummary(refs),
		ActorUserID:            validNullString(actorID),
		Status:                 "queued",
	}
	if err := tx.WithContext(ctx).Create(&run).Error; err != nil {
		return nil, err
	}
	return repoSyncRunMap(run), nil
}

func repoSyncRunMap(run GormRepoSyncRun) map[string]any {
	return map[string]any{
		"id":                        run.ID,
		"operation_run_id":          run.OperationRunID,
		"git_remote_id":             run.GitRemoteID,
		"project_id":                nullableStringValue(run.ProjectID),
		"project_git_repository_id": nullableStringValue(run.ProjectGitRepositoryID),
		"repo_sync_asset_id":        nullableStringValue(run.RepoSyncAssetID),
		"source_remote_id":          nullableStringValue(run.SourceRemoteID),
		"target_remote_id":          nullableStringValue(run.TargetRemoteID),
		"ref":                       run.Ref,
		"before_sha":                run.BeforeSHA,
		"after_sha":                 run.AfterSHA,
		"actor_user_id":             nullableStringValue(run.ActorUserID),
		"status":                    run.Status,
		"stdout":                    run.Stdout,
		"stderr":                    run.Stderr,
		"error_message":             run.ErrorMessage,
		"started_at":                nullableTimeAny(run.StartedAt),
		"finished_at":               nullableTimeAny(run.FinishedAt),
		"created_at":                run.CreatedAt,
	}
}

func (s *Server) listWebhookConnections(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_connection", ProjectID: projectID}, "read") {
		return
	}
	baseURL := s.publicBaseURL()
	items, err := s.webhookConnectionReadinessMapsGorm(r.Context(), projectID, "", baseURL)
	annotateWebhookConnectionHealth(items)
	annotateWebhookCallbackReadiness(items, baseURL)
	annotateWebhookThresholdDecisionAuditEvidence(items)
	annotateWebhookThresholdConfigurationEvidence(items)
	writeQueryResult(w, items, err)
}

func (s *Server) webhookConnectionWithCallbackReadinessGorm(ctx context.Context, connectionID, baseURL string) (map[string]any, error) {
	items, err := s.webhookConnectionReadinessMapsGorm(ctx, "", connectionID, baseURL)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	item := items[0]
	annotateWebhookConnectionHealth([]map[string]any{item})
	annotateWebhookCallbackReadiness([]map[string]any{item}, baseURL)
	annotateWebhookThresholdDecisionAuditEvidence([]map[string]any{item})
	annotateWebhookThresholdConfigurationEvidence([]map[string]any{item})
	return item, nil
}

func (s *Server) webhookConnectionReadinessMapsGorm(ctx context.Context, projectID, connectionID, baseURL string) ([]map[string]any, error) {
	if s.store == nil || s.store.Gorm == nil {
		return nil, errors.New("gorm store is not configured")
	}
	db := s.store.Gorm.WithContext(ctx).Model(&GormWebhookConnection{})
	if projectID != "" {
		db = db.Where(&GormWebhookConnection{ProjectID: projectID})
	}
	if connectionID != "" {
		db = db.Where(&GormWebhookConnection{GormBase: GormBase{ID: connectionID}})
	}
	var connections []GormWebhookConnection
	if err := db.Order(gormOrderDesc("created_at")).Find(&connections).Error; err != nil {
		return nil, err
	}
	if connectionID != "" && len(connections) == 0 {
		return nil, ErrNotFound
	}
	connectionIDs := make([]string, 0, len(connections))
	sourceRemoteIDs := make([]string, 0, len(connections))
	for _, connection := range connections {
		connectionIDs = append(connectionIDs, connection.ID)
		if id := cleanOptionalID(connection.SourceRemoteID.String); id != "" {
			sourceRemoteIDs = append(sourceRemoteIDs, id)
		}
	}
	sourceNames := map[string]string{}
	if len(sourceRemoteIDs) > 0 {
		var remotes []GormGitRemote
		if err := s.store.Gorm.WithContext(ctx).Where(gormField("id", sourceRemoteIDs)).Find(&remotes).Error; err != nil {
			return nil, err
		}
		for _, remote := range remotes {
			sourceNames[remote.ID] = remote.Name
		}
	}
	stats, err := s.webhookConnectionStatsGorm(ctx, connectionIDs)
	if err != nil {
		return nil, err
	}
	audits, err := s.webhookThresholdAuditStatsGorm(ctx, connectionIDs)
	if err != nil {
		return nil, err
	}
	configs, err := s.webhookThresholdConfigurationStatsGorm(ctx, connectionIDs)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(connections))
	for _, connection := range connections {
		stat := stats[connection.ID]
		audit := audits[connection.ID]
		config := configs[connection.ID]
		path := "/api/webhooks/" + connection.Provider + "/" + connection.ID
		items = append(items, map[string]any{
			"id":                               connection.ID,
			"project_id":                       connection.ProjectID,
			"provider":                         connection.Provider,
			"name":                             connection.Name,
			"source_remote_id":                 nullableStringValue(connection.SourceRemoteID),
			"enabled":                          connection.Enabled,
			"event_types":                      mapFromAny(connection.EventTypes.Data),
			"last_delivery_status":             connection.LastDeliveryStatus,
			"metadata":                         mapFromAny(connection.Metadata.Data),
			"created_at":                       connection.CreatedAt,
			"updated_at":                       connection.UpdatedAt,
			"source_remote_name":               sourceNames[cleanOptionalID(connection.SourceRemoteID.String)],
			"deliveries_7d":                    stat.Deliveries7d,
			"failures_7d":                      stat.Failures7d,
			"processed_7d":                     stat.Processed7d,
			"ignored_7d":                       stat.Ignored7d,
			"replayed_7d":                      stat.Replayed7d,
			"signature_valid_7d":               stat.SignatureValid7d,
			"matched_repo_sync_asset_7d":       stat.MatchedRepoSyncAsset7d,
			"operation_run_7d":                 stat.OperationRun7d,
			"last_event_at":                    nullableTimeAny(sql.NullTime{Time: stat.LastEventAt, Valid: !stat.LastEventAt.IsZero()}),
			"last_event_status":                stat.LastEventStatus,
			"last_event_type":                  stat.LastEventType,
			"last_event_signature_valid":       stat.LastEventSignatureValid,
			"threshold_decision_audit_count":   audit.Count,
			"last_threshold_decision_audit_at": nullableTimeAny(sql.NullTime{Time: audit.LastAt, Valid: !audit.LastAt.IsZero()}),
			"threshold_configuration_count":    config.Count,
			"last_threshold_configuration_at":  nullableTimeAny(sql.NullTime{Time: config.LastAt, Valid: !config.LastAt.IsZero()}),
			"webhook_path":                     path,
			"webhook_url":                      baseURL + path,
		})
	}
	return items, nil
}
