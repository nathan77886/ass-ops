package app

import (
	"context"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"time"
)

const sshKubernetesDiscoveryCommand = `set -eu
if command -v kubectl >/dev/null 2>&1; then K="kubectl"; KIND="kubernetes"; elif command -v k3s >/dev/null 2>&1; then K="k3s kubectl"; KIND="k3s"; else echo "ASSOPS_STATUS=kubectl_missing"; exit 21; fi
CTX=$($K config current-context 2>/dev/null || true)
NS=$($K config view --minify -o 'jsonpath={..namespace}' 2>/dev/null || true)
CLUSTER=$($K config view --minify -o 'jsonpath={.contexts[0].context.cluster}' 2>/dev/null || true)
SERVER=$($K config view --minify -o 'jsonpath={.clusters[0].cluster.server}' 2>/dev/null || true)
USER=$($K config view --minify -o 'jsonpath={.contexts[0].context.user}' 2>/dev/null || true)
KUBECONFIG_PATH=""
if [ -n "${KUBECONFIG:-}" ]; then
  OLDIFS=$IFS
  IFS=:
  for CANDIDATE in $KUBECONFIG; do
    if [ -r "$CANDIDATE" ]; then KUBECONFIG_PATH=$CANDIDATE; break; fi
  done
  IFS=$OLDIFS
elif [ -r "$HOME/.kube/config" ]; then
  KUBECONFIG_PATH="$HOME/.kube/config"
elif [ -r /etc/rancher/k3s/k3s.yaml ]; then
  KUBECONFIG_PATH=/etc/rancher/k3s/k3s.yaml
fi
[ -n "$NS" ] || NS=default
printf 'ASSOPS_STATUS=ok\nASSOPS_KIND=%s\nASSOPS_CONTEXT=%s\nASSOPS_NAMESPACE=%s\nASSOPS_CLUSTER=%s\nASSOPS_SERVER=%s\nASSOPS_USER=%s\nASSOPS_KUBECONFIG_PATH=%s\n' "$KIND" "$CTX" "$NS" "$CLUSTER" "$SERVER" "$USER" "$KUBECONFIG_PATH"`

type sshKubernetesDiscovery struct {
	Status           string
	Kind             string
	Context          string
	Namespace        string
	ClusterName      string
	ServerHost       string
	ServiceAccount   string
	RemoteKubeconfig string
	BlockedReasons   []string
	SuppressedFields []string
}

type argoServiceCandidate struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Reason    string `json:"reason"`
}

func (s *Server) previewKubernetesImportFromSSHMachine(w http.ResponseWriter, r *http.Request) {
	machine, ok := s.sshMachineForImport(w, r, "read")
	if !ok {
		return
	}
	result := s.discoverKubernetesFromSSH(r.Context(), machine)
	status := http.StatusOK
	if result.Status != "ok" {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, kubernetesImportPreviewPayload(machine, result))
}

func (s *Server) importKubernetesFromSSHMachine(w http.ResponseWriter, r *http.Request) {
	machine, ok := s.sshMachineForImport(w, r, "read")
	if !ok {
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "kubernetes_environment", ProjectID: machine.ProjectID}, "create") {
		return
	}
	var req struct {
		Name                string `json:"name"`
		Environment         string `json:"environment"`
		KubeconfigSecretRef string `json:"kubeconfig_secret_ref"`
		ServiceAccount      string `json:"service_account"`
		Status              string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	discovery := s.discoverKubernetesFromSSH(r.Context(), machine)
	if discovery.Status != "ok" {
		writeJSON(w, http.StatusBadRequest, kubernetesImportPreviewPayload(machine, discovery))
		return
	}
	env, err := s.upsertImportedKubernetesEnvironment(r.Context(), machine, discovery, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"imported":                 true,
		"source_ssh_machine_id":    machine.ID,
		"source_ssh_machine_name":  machine.Name,
		"kubernetes_environment":   kubernetesEnvironmentMap(env),
		"discovery":                safeSSHDiscoveryMap(discovery),
		"stdout_included":          false,
		"stderr_included":          false,
		"kubeconfig_body_included": false,
	})
}

func (s *Server) previewArgoImportFromKubernetesEnvironment(w http.ResponseWriter, r *http.Request) {
	env, ok := s.kubernetesEnvironmentForArgoImport(w, r, "read")
	if !ok {
		return
	}
	result := s.discoverArgoFromKubernetesEnvironment(r.Context(), env)
	status := http.StatusOK
	if result["status"] != "ok" {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (s *Server) importArgoFromKubernetesEnvironment(w http.ResponseWriter, r *http.Request) {
	env, ok := s.kubernetesEnvironmentForArgoImport(w, r, "read")
	if !ok {
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "argo_connection", ProjectID: env.ProjectID}, "create") {
		return
	}
	var req struct {
		Name         string         `json:"name"`
		ServerURL    string         `json:"server_url"`
		CredentialID string         `json:"credential_id"`
		Config       map[string]any `json:"config"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = cleanOptionalText(req.Name)
	req.ServerURL = cleanOptionalText(req.ServerURL)
	if req.Name == "" || req.ServerURL == "" {
		writeError(w, http.StatusBadRequest, "name and server_url are required")
		return
	}
	if !validPublicHTTPURL(r.Context(), req.ServerURL) {
		writeError(w, http.StatusBadRequest, "server_url must be a public http or https URL")
		return
	}
	if (boolConfig(req.Config, "insecure_skip_verify") || boolConfig(req.Config, "use_env_token")) && !canUseSensitiveArgoConfig(currentUser(r)) {
		writeError(w, http.StatusForbidden, "sensitive Argo connection config requires an owner role")
		return
	}
	credential, err := s.connectionCredentialForProjectOrGlobal(r.Context(), env.ProjectID, req.CredentialID, "argo_token")
	if err != nil {
		writeError(w, http.StatusBadRequest, "credential_id must reference an Argo token credential in this project")
		return
	}
	config := map[string]any{
		"insecure_skip_verify": boolConfig(req.Config, "insecure_skip_verify"),
		"use_env_token":        boolConfig(req.Config, "use_env_token"),
	}
	config["source"] = "kubernetes_environment_import"
	config["source_kubernetes_environment_id"] = env.ID
	config["source_kubernetes_environment_name"] = env.Name
	connection := GormArgoConnection{
		ProjectID:    env.ProjectID,
		Name:         req.Name,
		ServerURL:    req.ServerURL,
		AuthType:     "token",
		CredentialID: validNullString(req.CredentialID),
		Config:       JSONValue{Data: config},
	}
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var existing GormArgoConnection
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(&GormArgoConnection{ProjectID: env.ProjectID, ServerURL: req.ServerURL}).First(&existing).Error
		if err == nil {
			connection.GormBase = existing.GormBase
			connection.ProjectID = existing.ProjectID
			if err := tx.Save(&connection).Error; err != nil {
				return err
			}
		} else if errorsIsRecordNotFound(err) {
			if err := tx.Create(&connection).Error; err != nil {
				return err
			}
		} else {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeError(w, http.StatusBadRequest, "could not import Argo connection")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"imported":                  true,
		"kubernetes_environment_id": env.ID,
		"argo_connection":           argoConnectionMap(connection, credential),
	})
}

func (s *Server) sshMachineForImport(w http.ResponseWriter, r *http.Request, action string) (GormSSHMachine, bool) {
	machineID := cleanOptionalID(chi.URLParam(r, "id"))
	var machine GormSSHMachine
	if err := s.store.Gorm.WithContext(r.Context()).First(&machine, &GormSSHMachine{GormBase: GormBase{ID: machineID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return machine, false
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: machine.ProjectID}, action) {
		return machine, false
	}
	return machine, true
}

func (s *Server) kubernetesEnvironmentForArgoImport(w http.ResponseWriter, r *http.Request, action string) (GormKubernetesEnvironment, bool) {
	envID := cleanOptionalID(chi.URLParam(r, "id"))
	var env GormKubernetesEnvironment
	if err := s.store.Gorm.WithContext(r.Context()).First(&env, &GormKubernetesEnvironment{GormBase: GormBase{ID: envID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return env, false
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "kubernetes_environment", ID: envID, ProjectID: env.ProjectID}, action) {
		return env, false
	}
	return env, true
}

func (s *Server) discoverKubernetesFromSSH(ctx context.Context, machine GormSSHMachine) sshKubernetesDiscovery {
	result := sshKubernetesDiscovery{
		Status:           "blocked",
		BlockedReasons:   []string{},
		SuppressedFields: []string{"stdout", "stderr", "kubeconfig", "cluster_token", "client_certificate", "client_key", "private_key", "password"},
	}
	request, err := sshCommandInvocation(ctx, s.store.Gorm, machine, sshMachineMap(machine, nil), sshKubernetesDiscoveryCommand)
	if err != nil {
		result.BlockedReasons = append(result.BlockedReasons, err.Error())
		return result
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stdout, _, exitCode, err := nativeSSHRunner{}.Run(runCtx, request)
	if err != nil {
		result.Status = "failed"
		if exitCode == 21 {
			result.BlockedReasons = append(result.BlockedReasons, "kubectl_or_k3s_not_found")
		} else {
			result.BlockedReasons = append(result.BlockedReasons, "ssh_probe_failed")
		}
		return result
	}
	fields := parseAssopsKeyValueLines(stdout)
	if fields["ASSOPS_STATUS"] != "ok" {
		result.Status = firstNonEmptyString(fields["ASSOPS_STATUS"], "blocked")
		result.BlockedReasons = append(result.BlockedReasons, "kubernetes_environment_not_detected")
		return result
	}
	result.Status = "ok"
	result.Kind = firstNonEmptyString(fields["ASSOPS_KIND"], "kubernetes")
	result.Context = cleanOptionalText(fields["ASSOPS_CONTEXT"])
	result.Namespace = cleanOptionalText(firstNonEmptyString(fields["ASSOPS_NAMESPACE"], "default"))
	result.ClusterName = cleanOptionalText(fields["ASSOPS_CLUSTER"])
	result.ServerHost = publicURLHostOnly(fields["ASSOPS_SERVER"])
	result.ServiceAccount = cleanOptionalText(fields["ASSOPS_USER"])
	result.RemoteKubeconfig = cleanOptionalText(fields["ASSOPS_KUBECONFIG_PATH"])
	if result.Context == "" {
		result.BlockedReasons = append(result.BlockedReasons, "current_context_empty")
	}
	if result.ClusterName == "" {
		result.BlockedReasons = append(result.BlockedReasons, "cluster_name_empty")
	}
	if result.Namespace == "" {
		result.BlockedReasons = append(result.BlockedReasons, "namespace_empty")
	}
	if result.RemoteKubeconfig == "" && result.Kind != "k3s" {
		result.BlockedReasons = append(result.BlockedReasons, "kubeconfig_not_found")
	}
	if containsSecretLikeMaterial(result.Context) || containsSecretLikeMaterial(result.ClusterName) || containsSecretLikeMaterial(result.Namespace) || containsSecretLikeMaterial(result.ServiceAccount) || containsSecretLikeMaterial(result.RemoteKubeconfig) {
		result.Status = "blocked"
		result.BlockedReasons = append(result.BlockedReasons, "secret_like_material_detected")
	}
	if len(result.BlockedReasons) > 0 {
		result.Status = "blocked"
	}
	return result
}
