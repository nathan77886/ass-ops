package app

import (
	"context"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm/clause"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func deploymentExecutionPlan(readiness string, blockedReasons []string) map[string]any {
	prerequisiteState := strings.ToLower(strings.TrimSpace(readiness))
	if prerequisiteState != "planned" {
		prerequisiteState = "blocked"
	}
	return map[string]any{
		"mode":                            "redacted_deployment_execution_plan",
		"plan_state":                      "blocked",
		"prerequisite_state":              prerequisiteState,
		"plan_ready":                      false,
		"plan_ready_reason":               "deployment_execution_backend_disabled",
		"execution_enabled":               false,
		"execution_backend":               "disabled",
		"requires_approval":               true,
		"approval_action":                 "deployment.execute",
		"requires_environment_review":     true,
		"requires_kubeconfig_binding":     true,
		"requires_manifest_render":        true,
		"requires_dry_run_preflight":      true,
		"requires_rollback_plan":          true,
		"requires_operator_confirmation":  true,
		"target_metadata_ready":           prerequisiteState == "planned",
		"deployment_request_materialized": false,
		"manifest_rendered":               false,
		"dry_run_performed":               false,
		"helm_release_bound":              false,
		"kubernetes_client_constructed":   false,
		"rollout_started":                 false,
		"rollback_point_selected":         false,
		"external_call_made":              false,
		"kubernetes_api_call_made":        false,
		"helm_command_invoked":            false,
		"deployment_mutation":             "disabled",
		"kubeconfig_included":             false,
		"secret_included":                 false,
		"manifest_body_included":          false,
		"helm_values_included":            false,
		"cluster_credential_included":     false,
		"contains_token":                  false,
		"contains_kubeconfig":             false,
		"contains_secret":                 false,
		"contains_manifest_body":          false,
		"execution_boundary_redacted":     true,
		"blocked_reasons":                 append([]string{"deployment_execution_backend_disabled"}, blockedReasons...),
		"required_controls":               []string{"operation_approval", "environment_review", "kubeconfig_binding", "manifest_render", "server_side_dry_run", "rollback_plan", "operator_confirmation"},
		"disabled_backends":               []string{"helm_upgrade", "kubectl_apply", "kubectl_rollout", "argocd_sync", "rollback_execute"},
		"suppressed_fields":               []string{"kubeconfig", "cluster_token", "authorization_header", "secret_manifest", "rendered_manifest", "helm_values", "image_pull_secret", "environment_secret"},
		"execution_sequence":              []string{"request_approval", "bind_environment", "bind_kubeconfig", "render_manifest", "run_server_side_dry_run", "record_deployment_audit", "start_rollout"},
	}
}

func deploymentTargetStatusBlocksExecution(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "<nil>", "healthy", "synced", "running", "available", "active", "ok", "completed":
		return false
	case "failed", "error", "degraded", "outofsync", "missing", "unknown":
		return true
	default:
		return true
	}
}

func validPublicHTTPURL(ctx context.Context, value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.User != nil {
		return false
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return isPublicIP(ip)
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return false
		}
	}
	return true
}

func boolConfig(config map[string]any, key string) bool {
	value, ok := config[key].(bool)
	return ok && value
}

func canUseSensitiveArgoConfig(user *User) bool {
	return user != nil && (user.Role == "admin" || user.Role == "owner")
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return !ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast() &&
		!ip.IsUnspecified()
}

func (s *Server) requireProjectPolicy(w http.ResponseWriter, r *http.Request, resource PolicyResource, action string) bool {
	user := currentUser(r)
	if user != nil && resource.ProjectID != "" && user.Role != "admin" && user.Role != "owner" {
		var count int64
		if err := s.store.Gorm.WithContext(r.Context()).Model(&GormProjectMember{}).Where(&GormProjectMember{ProjectID: resource.ProjectID, UserID: user.ID}).Count(&count).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "could not check project membership")
			return false
		}
		if count == 0 {
			writeJSON(w, http.StatusForbidden, PolicyDecision{Effect: PolicyDeny, Reason: "user is not a member of this project"})
			return false
		}
	}
	if !s.requirePolicy(w, r, resource, action) {
		return false
	}
	return true
}

func (s *Server) createSSHMachine(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name         string         `json:"name"`
		Host         string         `json:"host"`
		Port         int            `json:"port"`
		Username     string         `json:"username"`
		AuthType     string         `json:"auth_type"`
		CredentialID string         `json:"credential_id"`
		Metadata     map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	if req.AuthType == "" {
		req.AuthType = "key"
	}
	credentialKind := connectionCredentialKindForSSHAuth(req.AuthType)
	if credentialKind == "" {
		writeError(w, http.StatusBadRequest, "auth_type must be key or password")
		return
	}
	credential, err := s.connectionCredentialForProjectOrGlobal(r.Context(), projectID, req.CredentialID, credentialKind)
	if err != nil {
		writeError(w, http.StatusBadRequest, "credential_id must reference a matching SSH credential in this project")
		return
	}
	machine := GormSSHMachine{
		ProjectID:    projectID,
		Name:         req.Name,
		Host:         req.Host,
		Port:         req.Port,
		Username:     req.Username,
		AuthType:     req.AuthType,
		CredentialID: validNullString(req.CredentialID),
		Metadata:     JSONValue{Data: req.Metadata},
	}
	if err := s.store.Gorm.WithContext(r.Context()).Create(&machine).Error; err != nil {
		writeError(w, http.StatusBadRequest, "could not create resource")
		return
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync ssh machine asset")
		return
	}
	writeJSON(w, http.StatusCreated, sshMachineMap(machine, credential))
}

func (s *Server) listSSHMachines(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ProjectID: projectID}, "read") {
		return
	}
	var machines []GormSSHMachine
	err := s.store.Gorm.WithContext(r.Context()).
		Where(map[string]any{"project_id": projectID}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).
		Find(&machines).Error
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	credentials, err := s.connectionCredentialsForSSHMachine(r.Context(), machines)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": sshMachineMaps(machines, credentials)})
}
