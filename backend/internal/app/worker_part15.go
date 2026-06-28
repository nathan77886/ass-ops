package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func (w *ControlWorker) recordArgoSyncAdapterRun(ctx context.Context, tx *gorm.DB, result map[string]any) error {
	syncResult, ok := result["_argo_sync_result"].(*ArgoSyncResult)
	delete(result, "_argo_sync_result")
	if !ok || syncResult == nil || syncResult.ProjectID == "" || syncResult.ConnectionID == "" {
		return nil
	}
	result["project_id"] = syncResult.ProjectID
	result["connection_id"] = syncResult.ConnectionID
	result["server_url"] = syncResult.ServerURL
	result["count"] = len(syncResult.Apps)
	if err := tx.WithContext(ctx).Where(&GormArgoApp{ArgoConnectionID: validNullString(syncResult.ConnectionID)}).Delete(&GormArgoApp{}).Error; err != nil {
		return err
	}
	for _, app := range syncResult.Apps {
		target, err := upsertDeploymentTargetForArgoApp(ctx, tx, syncResult, app)
		if err != nil {
			return err
		}
		argoApp := GormArgoApp{
			ProjectID:          syncResult.ProjectID,
			ArgoConnectionID:   validNullString(syncResult.ConnectionID),
			DeploymentTargetID: validNullString(target.ID),
			Name:               app.Name,
			Namespace:          app.Namespace,
			Status:             app.Status,
			Metadata:           JSONValue{Data: app.Metadata},
			SyncedAt:           validNullTime(time.Now()),
		}
		if err := tx.WithContext(ctx).Create(&argoApp).Error; err != nil {
			return err
		}
		if err := upsertDeploymentRecordForArgoApp(ctx, tx, syncResult, app, target, argoApp); err != nil {
			return err
		}
	}
	if err := refreshArgoDeploymentTargetStatus(ctx, tx, syncResult.ProjectID, syncResult.ConnectionID); err != nil {
		return err
	}
	if err := cleanupOrphanArgoDeploymentTargets(ctx, tx, syncResult.ConnectionID); err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Model(&GormArgoConnection{}).
		Where(&GormArgoConnection{GormBase: GormBase{ID: syncResult.ConnectionID}}).
		Updates(map[string]any{"last_sync_status": "completed", "last_sync_error": ""}).Error; err != nil {
		return err
	}
	if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
		return fmt.Errorf("syncing canonical assets for Argo app sync: %w", err)
	}
	return nil
}

func upsertDeploymentRecordForArgoApp(ctx context.Context, tx *gorm.DB, syncResult *ArgoSyncResult, app ArgoAppInput, target GormDeploymentTarget, argoApp GormArgoApp) error {
	metadata := mapFromAny(app.Metadata)
	revision := firstNonEmptyString(stringFromMap(metadata, "revision"), stringFromMap(metadata, "target_revision"))
	images := stringSliceFromAny(metadata["images"])
	recordMetadata := map[string]any{
		"source":             "argocd",
		"argo_connection_id": syncResult.ConnectionID,
		"server_url":         syncResult.ServerURL,
		"health_status":      stringFromMap(metadata, "health_status"),
		"sync_status":        stringFromMap(metadata, "sync_status"),
	}
	environment := firstNonEmptyString(app.Environment, target.Environment)
	namespace := firstNonEmptyString(app.Namespace, target.Namespace)
	clusterName := firstNonEmptyString(app.ClusterName, target.ClusterName)
	var record GormDeploymentRecord
	where := GormDeploymentRecord{ProjectID: syncResult.ProjectID, Source: "argocd", Name: app.Name, Environment: environment, Namespace: namespace, ClusterName: clusterName}
	if err := tx.WithContext(ctx).Where(&where).First(&record).Error; err != nil && !errorsIsRecordNotFound(err) {
		return err
	}
	record.ProjectID = syncResult.ProjectID
	record.DeploymentTargetID = validNullString(target.ID)
	record.ArgoConnectionID = validNullString(syncResult.ConnectionID)
	record.ArgoAppID = validNullString(argoApp.ID)
	record.Name = app.Name
	record.Environment = environment
	record.Namespace = namespace
	record.ClusterName = clusterName
	record.Source = "argocd"
	record.Status = app.Status
	record.Revision = revision
	record.ImageRefs = JSONValue{Data: images}
	record.Metadata = JSONValue{Data: recordMetadata}
	record.ObservedAt = time.Now()
	if err := tx.WithContext(ctx).Save(&record).Error; err != nil {
		return err
	}
	if revision == "" && len(images) == 0 {
		return nil
	}
	rollbackMetadata := map[string]any{
		"source":               "argocd",
		"deployment_record_id": record.ID,
		"argo_app_id":          argoApp.ID,
	}
	var point GormRollbackPoint
	pointWhere := GormRollbackPoint{ProjectID: syncResult.ProjectID, Source: "argocd", Name: app.Name, Environment: environment, Revision: revision}
	if err := tx.WithContext(ctx).Where(&pointWhere).First(&point).Error; err != nil && !errorsIsRecordNotFound(err) {
		return err
	}
	point.ProjectID = syncResult.ProjectID
	point.DeploymentRecordID = validNullString(record.ID)
	point.DeploymentTargetID = validNullString(target.ID)
	point.Name = app.Name
	point.Environment = environment
	point.Revision = revision
	point.ImageRefs = JSONValue{Data: images}
	point.Source = "argocd"
	point.Status = "available"
	point.Metadata = JSONValue{Data: rollbackMetadata}
	point.CapturedAt = time.Now()
	return tx.WithContext(ctx).Save(&point).Error
}

func upsertDeploymentTargetForArgoApp(ctx context.Context, tx *gorm.DB, syncResult *ArgoSyncResult, app ArgoAppInput) (GormDeploymentTarget, error) {
	environment := strings.TrimSpace(app.Environment)
	if environment == "" {
		environment = strings.TrimSpace(app.Namespace)
	}
	if environment == "" {
		environment = "default"
	}
	namespace := strings.TrimSpace(app.Namespace)
	clusterName := strings.TrimSpace(app.ClusterName)
	name := environment
	if namespace != "" && namespace != environment {
		name = environment + "/" + namespace
	}
	metadata := map[string]any{
		"source":             "argocd",
		"argo_connection_id": syncResult.ConnectionID,
		"server_url":         syncResult.ServerURL,
	}
	var target GormDeploymentTarget
	where := GormDeploymentTarget{ProjectID: syncResult.ProjectID, Environment: environment, ClusterName: clusterName, Namespace: namespace}
	if err := tx.WithContext(ctx).Where(&where).First(&target).Error; err != nil && !errorsIsRecordNotFound(err) {
		return target, err
	}
	target.ProjectID = syncResult.ProjectID
	target.Name = name
	target.Environment = environment
	target.ClusterName = clusterName
	target.Namespace = namespace
	target.Source = "argocd"
	target.ArgoConnectionID = validNullString(syncResult.ConnectionID)
	if target.Status == "" {
		target.Status = "unknown"
	}
	target.Metadata = JSONValue{Data: metadata}
	return target, tx.WithContext(ctx).Save(&target).Error
}

func refreshArgoDeploymentTargetStatus(ctx context.Context, tx *gorm.DB, projectID, connectionID string) error {
	var apps []GormArgoApp
	if err := tx.WithContext(ctx).Where(&GormArgoApp{ProjectID: projectID, ArgoConnectionID: validNullString(connectionID)}).Find(&apps).Error; err != nil {
		return err
	}
	statusesByTarget := map[string][]string{}
	for _, app := range apps {
		targetID := cleanOptionalID(app.DeploymentTargetID.String)
		if targetID == "" {
			continue
		}
		statusesByTarget[targetID] = append(statusesByTarget[targetID], app.Status)
	}
	for targetID, statuses := range statusesByTarget {
		if err := tx.WithContext(ctx).Model(&GormDeploymentTarget{}).
			Where(&GormDeploymentTarget{GormBase: GormBase{ID: targetID}, ProjectID: projectID, Source: "argocd"}).
			Updates(map[string]any{"status": argoDeploymentTargetStatusFromApps(statuses)}).Error; err != nil {
			return err
		}
	}
	return nil
}

func argoDeploymentTargetStatusFromApps(statuses []string) string {
	if len(statuses) == 0 {
		return "unknown"
	}
	allSynced := true
	for _, status := range statuses {
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "outofsync", "failed", "error", "degraded":
			return "OutOfSync"
		case "synced":
		default:
			allSynced = false
		}
	}
	if allSynced {
		return "Synced"
	}
	return "Unknown"
}

func cleanupOrphanArgoDeploymentTargets(ctx context.Context, tx *gorm.DB, connectionID string) error {
	var targets []GormDeploymentTarget
	if err := tx.WithContext(ctx).Where(&GormDeploymentTarget{Source: "argocd", ArgoConnectionID: validNullString(connectionID)}).Find(&targets).Error; err != nil {
		return err
	}
	for _, target := range targets {
		var count int64
		if err := tx.WithContext(ctx).Model(&GormArgoApp{}).Where(&GormArgoApp{DeploymentTargetID: validNullString(target.ID)}).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := tx.WithContext(ctx).Delete(&target).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func argoConnectionIDFromResult(result map[string]any) string {
	if syncResult, ok := result["_argo_sync_result"].(*ArgoSyncResult); ok && syncResult != nil {
		return syncResult.ConnectionID
	}
	if value, ok := result["connection_id"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

type NodeWorker struct {
	cfg          Config
	name         string
	kind         string
	capabilities []string
	log          *slog.Logger
	client       *http.Client
	token        string
}

func NewNodeWorker(cfg Config, name, kind string, capabilities []string, log *slog.Logger) *NodeWorker {
	return &NodeWorker{
		cfg:          cfg,
		name:         name,
		kind:         kind,
		capabilities: capabilities,
		log:          log,
		client:       &http.Client{Timeout: 10 * time.Second},
	}
}

func (n *NodeWorker) Run(ctx context.Context) error {
	if err := n.register(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(n.cfg.WorkerInterval)
	defer ticker.Stop()
	for {
		if err := n.heartbeat(ctx); err != nil {
			n.log.Error("heartbeat failed", "error", err)
		}
		if err := n.claimAndRun(ctx); err != nil {
			n.log.Error("claim failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
