package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
)

func (s *Server) deploymentTargetMapsGorm(ctx context.Context, projectID string, limit int) ([]map[string]any, error) {
	var targets []GormDeploymentTarget
	if err := s.store.Gorm.WithContext(ctx).Where(&GormDeploymentTarget{ProjectID: projectID}).Order(gormOrderAsc("environment")).Order(gormOrderAsc("namespace")).Order(gormOrderDesc("created_at")).Limit(limit).Find(&targets).Error; err != nil {
		return nil, err
	}
	connections, err := s.argoConnectionNamesByIDGorm(ctx, targets)
	if err != nil {
		return nil, err
	}
	appCounts, err := s.argoAppCountsByTargetGorm(ctx, projectID)
	if err != nil {
		return nil, err
	}
	envs, err := s.kubernetesEnvironmentsForProjectGorm(ctx, projectID)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		item := deploymentTargetMap(target)
		item["argo_connection_name"] = connections[cleanOptionalID(target.ArgoConnectionID.String)]
		item["argo_app_count"] = appCounts[target.ID]
		if env, ok := envs[kubernetesEnvironmentScopeKey(target.ProjectID, target.Environment, target.ClusterName, target.Namespace)]; ok {
			item = mergeMaps(item, kubernetesEnvironmentTargetFields(env))
		}
		items = append(items, item)
	}
	return items, nil
}

func deploymentTargetMap(target GormDeploymentTarget) map[string]any {
	return map[string]any{"id": target.ID, "project_id": target.ProjectID, "name": target.Name, "environment": target.Environment, "cluster_name": target.ClusterName, "namespace": target.Namespace, "source": target.Source, "argo_connection_id": nullableStringValue(target.ArgoConnectionID), "status": target.Status, "metadata": mapFromAny(target.Metadata.Data), "created_at": target.CreatedAt, "updated_at": target.UpdatedAt}
}

func kubernetesEnvironmentTargetFields(env GormKubernetesEnvironment) map[string]any {
	kubeconfigConfigured := env.KubeconfigSecretRef != "" && env.KubeconfigSecretCiphertext != ""
	return map[string]any{"kubernetes_environment_id": env.ID, "kubernetes_environment_name": env.Name, "kubeconfig_secret_ref_present": kubeconfigConfigured, "kubeconfig_secret_configured": kubeconfigConfigured, "kubeconfig_secret_ref": env.KubeconfigSecretRef, "service_account_present": env.ServiceAccount != "", "token_subject_review_status": env.TokenSubjectReviewStatus, "rbac_read_logs_status": env.RBACReadLogsStatus, "rbac_restart_pods_status": env.PodRestartStatus, "kubernetes_environment_status": env.Status}
}

func (s *Server) argoConnectionNamesByIDGorm(ctx context.Context, targets []GormDeploymentTarget) (map[string]string, error) {
	ids := make([]string, 0, len(targets))
	seen := map[string]bool{}
	for _, target := range targets {
		id := cleanOptionalID(target.ArgoConnectionID.String)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var connections []GormArgoConnection
	if err := s.store.Gorm.WithContext(ctx).Find(&connections, ids).Error; err != nil {
		return nil, err
	}
	for _, connection := range connections {
		out[connection.ID] = connection.Name
	}
	return out, nil
}

func (s *Server) argoAppCountsByTargetGorm(ctx context.Context, projectID string) (map[string]int, error) {
	var apps []GormArgoApp
	if err := s.store.Gorm.WithContext(ctx).Where(&GormArgoApp{ProjectID: projectID}).Find(&apps).Error; err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, app := range apps {
		id := cleanOptionalID(app.DeploymentTargetID.String)
		if id != "" {
			out[id]++
		}
	}
	return out, nil
}

func (s *Server) kubernetesEnvironmentsForProjectGorm(ctx context.Context, projectID string) (map[string]GormKubernetesEnvironment, error) {
	var envs []GormKubernetesEnvironment
	if err := s.store.Gorm.WithContext(ctx).Where(&GormKubernetesEnvironment{ProjectID: projectID}).Find(&envs).Error; err != nil {
		return nil, err
	}
	out := map[string]GormKubernetesEnvironment{}
	for _, env := range envs {
		out[kubernetesEnvironmentScopeKey(env.ProjectID, env.Environment, env.ClusterName, env.Namespace)] = env
	}
	return out, nil
}

func kubernetesEnvironmentScopeKey(projectID, environment, clusterName, namespace string) string {
	return projectID + "\x00" + environment + "\x00" + clusterName + "\x00" + namespace
}

func (s *Server) loadDeploymentTargetForKubernetesAccessGorm(ctx context.Context, deploymentTargetID string) (map[string]any, error) {
	var target GormDeploymentTarget
	if err := s.store.Gorm.WithContext(ctx).First(&target, &GormDeploymentTarget{GormBase: GormBase{ID: deploymentTargetID}}).Error; err != nil {
		return nil, gormNotFoundAsErrNotFound(err)
	}
	item := deploymentTargetMap(target)
	if env, ok, err := s.kubernetesEnvironmentForTargetGorm(ctx, target); err != nil {
		return nil, err
	} else if ok {
		item = mergeMaps(item, kubernetesEnvironmentTargetFields(env))
	}
	return item, nil
}

func (s *Server) loadDeploymentTargetForExecutionGateGorm(ctx context.Context, deploymentTargetID string) (map[string]any, error) {
	item, err := s.loadDeploymentTargetForKubernetesAccessGorm(ctx, deploymentTargetID)
	if err != nil {
		return nil, err
	}
	targetID := cleanOptionalID(fmt.Sprint(item["id"]))
	appCounts, err := s.argoAppCountsByTargetGorm(ctx, cleanOptionalID(fmt.Sprint(item["project_id"])))
	if err != nil {
		return nil, err
	}
	item["argo_app_count"] = appCounts[targetID]
	return item, nil
}

func (s *Server) kubernetesEnvironmentForTargetGorm(ctx context.Context, target GormDeploymentTarget) (GormKubernetesEnvironment, bool, error) {
	var env GormKubernetesEnvironment
	err := s.store.Gorm.WithContext(ctx).Where(&GormKubernetesEnvironment{ProjectID: target.ProjectID, Environment: target.Environment, ClusterName: target.ClusterName, Namespace: target.Namespace}).First(&env).Error
	if errorsIsRecordNotFound(err) {
		return env, false, nil
	}
	return env, err == nil, err
}

func (s *Server) deploymentRecordMapsGorm(ctx context.Context, projectID string, limit int) ([]map[string]any, error) {
	var records []GormDeploymentRecord
	if err := s.store.Gorm.WithContext(ctx).Where(&GormDeploymentRecord{ProjectID: projectID}).Order(gormOrderDesc("observed_at")).Limit(limit).Find(&records).Error; err != nil {
		return nil, err
	}
	targets, err := s.deploymentTargetsByIDGorm(ctx, deploymentTargetIDsFromDeploymentRecords(records))
	if err != nil {
		return nil, err
	}
	apps, err := s.argoAppsByIDGorm(ctx, argoAppIDsFromDeploymentRecords(records))
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		item := deploymentRecordMap(record)
		if target, ok := targets[cleanOptionalID(record.DeploymentTargetID.String)]; ok {
			item["deployment_target_name"] = target.Name
		}
		if app, ok := apps[cleanOptionalID(record.ArgoAppID.String)]; ok {
			item["argo_app_name"] = app.Name
		}
		items = append(items, item)
	}
	return items, nil
}

func deploymentRecordMap(record GormDeploymentRecord) map[string]any {
	return map[string]any{"id": record.ID, "project_id": record.ProjectID, "deployment_target_id": nullableStringValue(record.DeploymentTargetID), "argo_connection_id": nullableStringValue(record.ArgoConnectionID), "argo_app_id": nullableStringValue(record.ArgoAppID), "name": record.Name, "environment": record.Environment, "namespace": record.Namespace, "cluster_name": record.ClusterName, "source": record.Source, "status": record.Status, "revision": record.Revision, "image_refs": mapFromAny(record.ImageRefs.Data), "metadata": mapFromAny(record.Metadata.Data), "observed_at": record.ObservedAt, "created_at": record.CreatedAt, "updated_at": record.UpdatedAt}
}

func deploymentTargetIDsFromDeploymentRecords(records []GormDeploymentRecord) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, record := range records {
		id := cleanOptionalID(record.DeploymentTargetID.String)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func argoAppIDsFromDeploymentRecords(records []GormDeploymentRecord) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, record := range records {
		id := cleanOptionalID(record.ArgoAppID.String)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Server) argoAppsByIDGorm(ctx context.Context, ids []string) (map[string]GormArgoApp, error) {
	out := map[string]GormArgoApp{}
	if len(ids) == 0 {
		return out, nil
	}
	var apps []GormArgoApp
	if err := s.store.Gorm.WithContext(ctx).Find(&apps, ids).Error; err != nil {
		return nil, err
	}
	for _, app := range apps {
		out[app.ID] = app
	}
	return out, nil
}

func (s *Server) createKubernetesEnvironment(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "kubernetes_environment", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name                     string         `json:"name"`
		Environment              string         `json:"environment"`
		ClusterName              string         `json:"cluster_name"`
		Namespace                string         `json:"namespace"`
		KubeconfigSecretRef      string         `json:"kubeconfig_secret_ref"`
		KubeconfigSecret         string         `json:"kubeconfig_secret"`
		ServiceAccount           string         `json:"service_account"`
		TokenSubjectReviewStatus string         `json:"token_subject_review_status"`
		RBACReadLogsStatus       string         `json:"rbac_read_logs_status"`
		RBACRestartPodsStatus    string         `json:"rbac_restart_pods_status"`
		Status                   string         `json:"status"`
		Metadata                 map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = cleanOptionalText(req.Name)
	req.Environment = cleanOptionalText(req.Environment)
	req.ClusterName = cleanOptionalText(req.ClusterName)
	req.Namespace = cleanOptionalText(req.Namespace)
	req.KubeconfigSecretRef = cleanOptionalText(req.KubeconfigSecretRef)
	req.KubeconfigSecret = strings.TrimSpace(req.KubeconfigSecret)
	req.ServiceAccount = cleanOptionalText(req.ServiceAccount)
	req.TokenSubjectReviewStatus = cleanKubernetesReviewStatus(req.TokenSubjectReviewStatus)
	req.RBACReadLogsStatus = cleanKubernetesReviewStatus(req.RBACReadLogsStatus)
	req.RBACRestartPodsStatus = cleanKubernetesReviewStatus(req.RBACRestartPodsStatus)
	req.Status = cleanKubernetesEnvironmentStatus(req.Status)
	if req.Name == "" || req.Environment == "" || req.ClusterName == "" || req.Namespace == "" {
		writeError(w, http.StatusBadRequest, "name, environment, cluster_name, and namespace are required")
		return
	}
	if len(req.Name) > 253 || len(req.Environment) > 63 || len(req.ClusterName) > 253 || len(req.Namespace) > 63 || len(req.KubeconfigSecretRef) > 253 || len(req.KubeconfigSecret) > importedKubeconfigMaxBytes || len(req.ServiceAccount) > 253 {
		writeError(w, http.StatusBadRequest, "kubernetes environment fields exceed allowed length")
		return
	}
	if containsSecretLikeMaterial(req.KubeconfigSecretRef) || containsSecretLikeMaterial(req.ServiceAccount) {
		writeError(w, http.StatusBadRequest, "kubernetes environment metadata must reference names only, not credential material")
		return
	}
	kubeconfigCiphertext := ""
	if req.KubeconfigSecret != "" {
		if !looksLikeKubeconfig([]byte(req.KubeconfigSecret), 0) {
			writeError(w, http.StatusBadRequest, "kubeconfig_secret is invalid")
			return
		}
		if err := validateImportedKubeconfigContent([]byte(req.KubeconfigSecret)); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ciphertext, err := s.encryptWebhookSecret(req.KubeconfigSecret)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not encrypt kubeconfig secret")
			return
		}
		kubeconfigCiphertext = ciphertext
	}
	model := GormKubernetesEnvironment{ProjectID: projectID, Name: req.Name, Environment: req.Environment, ClusterName: req.ClusterName, Namespace: req.Namespace, KubeconfigSecretRef: req.KubeconfigSecretRef, KubeconfigSecretCiphertext: kubeconfigCiphertext, ServiceAccount: req.ServiceAccount, TokenSubjectReviewStatus: req.TokenSubjectReviewStatus, RBACReadLogsStatus: req.RBACReadLogsStatus, PodRestartStatus: req.RBACRestartPodsStatus, Status: req.Status, Metadata: JSONValue{Data: req.Metadata}}
	assignments := map[string]any{"name": model.Name, "kubeconfig_secret_ref": model.KubeconfigSecretRef, "service_account": model.ServiceAccount, "token_subject_review_status": model.TokenSubjectReviewStatus, "rbac_read_logs_status": model.RBACReadLogsStatus, "pod_restart_status": model.PodRestartStatus, "status": model.Status, "metadata": model.Metadata}
	if kubeconfigCiphertext != "" {
		assignments["kubeconfig_secret_ciphertext"] = kubeconfigCiphertext
	}
	if err := s.store.Gorm.WithContext(r.Context()).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "project_id"}, {Name: "environment"}, {Name: "cluster_name"}, {Name: "namespace"}},
		DoUpdates: clause.Assignments(assignments),
	}).Create(&model).Error; err != nil {
		writeCreatedOne(w, nil, err)
		return
	}
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormKubernetesEnvironment{ProjectID: projectID, Environment: req.Environment, ClusterName: req.ClusterName, Namespace: req.Namespace}).First(&model).Error; err != nil {
		writeCreatedOne(w, nil, err)
		return
	}
	writeCreatedOne(w, kubernetesEnvironmentMap(model), nil)
}

func (s *Server) listKubernetesEnvironments(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "kubernetes_environment", ProjectID: projectID}, "read") {
		return
	}
	items, err := s.kubernetesEnvironmentMapsGorm(r.Context(), projectID)
	writeQueryResult(w, items, err)
}
