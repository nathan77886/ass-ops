package app

import (
	"context"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
)

func (s *Server) updateArgoConnection(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	var current GormArgoConnection
	if err := s.store.Gorm.WithContext(r.Context()).First(&current, &GormArgoConnection{GormBase: GormBase{ID: connectionID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "argo_connection", ID: connectionID, ProjectID: current.ProjectID}, "update") {
		return
	}
	var req struct {
		Name         *string        `json:"name"`
		ServerURL    *string        `json:"server_url"`
		AuthType     *string        `json:"auth_type"`
		CredentialID *string        `json:"credential_id"`
		Config       map[string]any `json:"config"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	next := current
	if req.Name != nil {
		next.Name = strings.TrimSpace(*req.Name)
	}
	if req.ServerURL != nil {
		next.ServerURL = strings.TrimSpace(*req.ServerURL)
	}
	if req.AuthType != nil {
		next.AuthType = strings.TrimSpace(*req.AuthType)
	}
	if next.AuthType == "" {
		next.AuthType = "token"
	}
	if next.AuthType != "token" {
		writeError(w, http.StatusBadRequest, "auth_type must be token")
		return
	}
	if !validPublicHTTPURL(r.Context(), next.ServerURL) {
		writeError(w, http.StatusBadRequest, "server_url must be a public http or https URL")
		return
	}
	if req.Config != nil {
		config := mapFromAny(next.Config.Data)
		for key, value := range req.Config {
			config[key] = value
		}
		next.Config = JSONValue{Data: config}
	}
	if (boolConfig(mapFromAny(next.Config.Data), "insecure_skip_verify") || boolConfig(mapFromAny(next.Config.Data), "use_env_token")) && !canUseSensitiveArgoConfig(currentUser(r)) {
		writeError(w, http.StatusForbidden, "sensitive Argo connection config requires an owner role")
		return
	}
	credentialID := cleanOptionalID(next.CredentialID.String)
	if req.CredentialID != nil {
		credentialID = cleanOptionalID(*req.CredentialID)
		next.CredentialID = validNullString(credentialID)
	}
	credential, err := s.connectionCredentialForProjectOrGlobal(r.Context(), next.ProjectID, credentialID, "argo_token")
	if err != nil {
		writeError(w, http.StatusBadRequest, "credential_id must reference an Argo token credential in this project")
		return
	}
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var locked GormArgoConnection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, &GormArgoConnection{GormBase: GormBase{ID: connectionID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		next.GormBase = locked.GormBase
		next.ProjectID = locked.ProjectID
		if err := tx.Save(&next).Error; err != nil {
			return err
		}
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, argoConnectionMap(next, credential))
}

func (s *Server) deleteArgoConnection(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	var connection GormArgoConnection
	if err := s.store.Gorm.WithContext(r.Context()).First(&connection, &GormArgoConnection{GormBase: GormBase{ID: connectionID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "argo_connection", ID: connectionID, ProjectID: connection.ProjectID}, "delete") {
		return
	}
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var locked GormArgoConnection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, &GormArgoConnection{GormBase: GormBase{ID: connectionID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		var targets []GormDeploymentTarget
		if err := tx.Where(&GormDeploymentTarget{ArgoConnectionID: validNullString(connectionID)}).Find(&targets).Error; err != nil {
			return err
		}
		targetIDs := make([]string, 0, len(targets))
		for _, target := range targets {
			targetIDs = append(targetIDs, target.ID)
		}
		var records []GormDeploymentRecord
		if err := tx.Where(&GormDeploymentRecord{ArgoConnectionID: validNullString(connectionID)}).Find(&records).Error; err != nil {
			return err
		}
		recordIDs := make([]string, 0, len(records))
		for _, record := range records {
			recordIDs = append(recordIDs, record.ID)
		}
		if len(recordIDs) > 0 {
			if err := tx.Where("deployment_record_id IN ?", recordIDs).Delete(&GormRollbackPoint{}).Error; err != nil {
				return err
			}
		}
		if len(targetIDs) > 0 {
			if err := tx.Where("deployment_target_id IN ?", targetIDs).Delete(&GormRollbackPoint{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where(&GormDeploymentRecord{ArgoConnectionID: validNullString(connectionID)}).Delete(&GormDeploymentRecord{}).Error; err != nil {
			return err
		}
		if err := tx.Where(&GormArgoApp{ArgoConnectionID: validNullString(connectionID)}).Delete(&GormArgoApp{}).Error; err != nil {
			return err
		}
		if err := tx.Where(&GormDeploymentTarget{ArgoConnectionID: validNullString(connectionID)}).Delete(&GormDeploymentTarget{}).Error; err != nil {
			return err
		}
		if err := deleteCanonicalAssetForSourceGorm(r.Context(), tx, "argo_connection", "argo_connections", connectionID); err != nil {
			return err
		}
		if err := tx.Delete(&locked).Error; err != nil {
			return err
		}
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": connectionID})
}

func (s *Server) syncArgoApps(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	var connection GormArgoConnection
	if err := s.store.Gorm.WithContext(r.Context()).First(&connection, &GormArgoConnection{GormBase: GormBase{ID: connectionID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := connection.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "argo_connection", ID: connectionID, ProjectID: projectID}, "argo.apps.sync") {
		return
	}
	var existingOps []GormOperationRun
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormOperationRun{OperationType: "argo.apps.sync"}).Find(&existingOps).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not check existing Argo sync")
		return
	}
	for _, existing := range existingOps {
		if existing.Status != "queued" && existing.Status != "running" {
			continue
		}
		if stringFromMap(mapFromAny(existing.Input.Data), "argo_connection_id") == connectionID {
			writeError(w, http.StatusConflict, "Argo app sync is already queued or running")
			return
		}
	}
	title := "sync Argo apps"
	if name := strings.TrimSpace(connection.Name); name != "" {
		title += " " + name
	}
	var op map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		op, err = enqueueOperationGorm(r.Context(), tx, projectID, "", "argo.apps.sync", title, map[string]any{"argo_connection_id": connectionID}, []string{"argo"}, "control-worker")
		if err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeCreatedOne(w, op, err)
		return
	}
	writeJSON(w, http.StatusCreated, op)
}

func (s *Server) listArgoApps(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "argo_app", ProjectID: projectID}, "read") {
		return
	}
	items, err := s.argoAppMapsGorm(r.Context(), projectID)
	writeQueryResult(w, items, err)
}

func (s *Server) argoAppMapsGorm(ctx context.Context, projectID string) ([]map[string]any, error) {
	var apps []GormArgoApp
	if err := s.store.Gorm.WithContext(ctx).Where(&GormArgoApp{ProjectID: projectID}).Order(gormOrderDesc("created_at")).Limit(500).Find(&apps).Error; err != nil {
		return nil, err
	}
	targets, err := s.deploymentTargetsByIDGorm(ctx, deploymentTargetIDsFromArgoApps(apps))
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(apps))
	for _, app := range apps {
		item := map[string]any{"id": app.ID, "project_id": app.ProjectID, "argo_connection_id": nullableStringValue(app.ArgoConnectionID), "deployment_target_id": nullableStringValue(app.DeploymentTargetID), "name": app.Name, "namespace": app.Namespace, "status": app.Status, "metadata": mapFromAny(app.Metadata.Data), "synced_at": nullableTimeAny(app.SyncedAt), "created_at": app.CreatedAt, "updated_at": app.UpdatedAt}
		if target, ok := targets[cleanOptionalID(app.DeploymentTargetID.String)]; ok {
			item["deployment_target_name"] = target.Name
			item["environment"] = target.Environment
			item["cluster_name"] = target.ClusterName
		}
		items = append(items, item)
	}
	return items, nil
}

func deploymentTargetIDsFromArgoApps(apps []GormArgoApp) []string {
	ids := make([]string, 0, len(apps))
	seen := map[string]bool{}
	for _, app := range apps {
		id := cleanOptionalID(app.DeploymentTargetID.String)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Server) deploymentTargetsByIDGorm(ctx context.Context, ids []string) (map[string]GormDeploymentTarget, error) {
	out := map[string]GormDeploymentTarget{}
	if len(ids) == 0 {
		return out, nil
	}
	var targets []GormDeploymentTarget
	if err := s.store.Gorm.WithContext(ctx).Find(&targets, ids).Error; err != nil {
		return nil, err
	}
	for _, target := range targets {
		out[target.ID] = target
	}
	return out, nil
}

func kubernetesEnvironmentMap(env GormKubernetesEnvironment) map[string]any {
	return map[string]any{"id": env.ID, "project_id": env.ProjectID, "name": env.Name, "environment": env.Environment, "cluster_name": env.ClusterName, "namespace": env.Namespace, "kubeconfig_secret_ref": env.KubeconfigSecretRef, "service_account": env.ServiceAccount, "token_subject_review_status": env.TokenSubjectReviewStatus, "rbac_read_logs_status": env.RBACReadLogsStatus, "rbac_restart_pods_status": env.PodRestartStatus, "status": env.Status, "metadata": mapFromAny(env.Metadata.Data), "created_at": env.CreatedAt, "updated_at": env.UpdatedAt, "kubeconfig_secret_ref_present": env.KubeconfigSecretRef != "", "service_account_present": env.ServiceAccount != "", "token_subject_review_ready": env.TokenSubjectReviewStatus == "reviewed", "rbac_read_logs_ready": env.RBACReadLogsStatus == "reviewed", "rbac_restart_pods_ready": env.PodRestartStatus == "reviewed", "log_access_metadata_ready": env.KubeconfigSecretRef != "" && env.TokenSubjectReviewStatus == "reviewed" && env.RBACReadLogsStatus == "reviewed", "pod_restart_metadata_ready": env.KubeconfigSecretRef != "" && env.TokenSubjectReviewStatus == "reviewed" && env.PodRestartStatus == "reviewed"}
}

func (s *Server) kubernetesEnvironmentMapsGorm(ctx context.Context, projectID string) ([]map[string]any, error) {
	var envs []GormKubernetesEnvironment
	if err := s.store.Gorm.WithContext(ctx).Where(&GormKubernetesEnvironment{ProjectID: projectID}).Order(gormOrderAsc("environment")).Order(gormOrderAsc("namespace")).Order(gormOrderDesc("created_at")).Find(&envs).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(envs))
	for _, env := range envs {
		items = append(items, kubernetesEnvironmentMap(env))
	}
	return items, nil
}
