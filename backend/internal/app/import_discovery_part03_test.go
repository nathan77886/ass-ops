package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSuggestedKubernetesEnvironmentUsesSSHMachineKubeconfigRef(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		want     string
	}{
		{
			name:     "direct ref",
			metadata: map[string]any{"kubeconfig_secret_ref": "cluster/default-reader"},
			want:     "cluster/default-reader",
		},
		{
			name:     "nested ref",
			metadata: map[string]any{"kubernetes": map[string]any{"kubeconfig_ref": "cluster/nested-reader"}},
			want:     "cluster/nested-reader",
		},
		{
			name:     "missing ref",
			metadata: map[string]any{},
			want:     "default/cluster-machine/default.kubeconfig",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			machine := GormSSHMachine{Name: "cluster-machine", Metadata: JSONValue{Data: tt.metadata}}
			discovery := sshKubernetesDiscovery{Kind: "kubernetes", Namespace: "default", ClusterName: "cluster"}

			got := suggestedKubernetesEnvironment(machine, discovery)
			if got["kubeconfig_secret_ref"] != tt.want {
				t.Fatalf("kubeconfig_secret_ref = %q, want %q", got["kubeconfig_secret_ref"], tt.want)
			}
		})
	}
}

func TestImportedKubeconfigSecretReadsRewritesAndEncryptsConfig(t *testing.T) {
	store := newGormFixtureStore(t)
	server := &Server{store: store, cfg: Config{WebhookSecretKey: "test-secret-material"}}
	machine := GormSSHMachine{
		ProjectID: "project-1",
		Name:      "cluster-machine",
		Host:      "10.0.0.20",
		Port:      22,
		Username:  "ops",
		AuthType:  "key",
		Metadata:  JSONValue{Data: map[string]any{}},
	}
	discovery := sshKubernetesDiscovery{
		Kind:             "k3s",
		Namespace:        "default",
		ClusterName:      "cluster",
		RemoteKubeconfig: "/etc/rancher/k3s/k3s.yaml",
	}
	oldSSHRun := importedKubeconfigSSHRun
	t.Cleanup(func() {
		importedKubeconfigSSHRun = oldSSHRun
	})
	importedKubeconfigSSHRun = func(_ context.Context, request sshRunRequest) (string, string, int, error) {
		if !strings.Contains(request.Command, "k3s kubectl config view --raw --minify") {
			t.Fatalf("unexpected ssh command: %s", request.Command)
		}
		return "apiVersion: v1\nclusters:\n- cluster:\n    server: https://127.0.0.1:6443\n  name: test\ncontexts:\n- name: test\nusers:\n- name: test\n", "", 0, nil
	}

	ref, ciphertext, err := server.importedKubeconfigSecret(t.Context(), machine, discovery)
	if err != nil {
		t.Fatalf("import kubeconfig secret: %v", err)
	}
	wantRef := "project-1/cluster-machine/default.kubeconfig"
	if ref != wantRef {
		t.Fatalf("ref = %q, want %q", ref, wantRef)
	}
	plain, err := server.decryptWebhookSecret(ciphertext)
	if err != nil {
		t.Fatalf("decrypt kubeconfig: %v", err)
	}
	if !strings.Contains(plain, "server: https://10.0.0.20:6443") || strings.Contains(plain, "127.0.0.1") {
		t.Fatalf("kubeconfig server not rewritten: %s", plain)
	}
}

func TestValidateImportedKubeconfigContentRejectsExecutableCredentialSources(t *testing.T) {
	for _, field := range []string{"exec", "auth-provider", "tokenFile", "client-certificate", "client-key", "certificate-authority"} {
		t.Run(field, func(t *testing.T) {
			content := []byte("apiVersion: v1\nclusters:\n- name: test\ncontexts:\n- name: test\nusers:\n- name: test\n  user:\n    " + field + ": unsafe\n")
			if err := validateImportedKubeconfigContent(content); err == nil {
				t.Fatalf("unsafe kubeconfig field %q was accepted", field)
			}
		})
	}
}

func TestDiscoverArgoBlocksMissingKubeconfigSecret(t *testing.T) {
	server := &Server{}
	env := GormKubernetesEnvironment{
		Name:        "cluster",
		Namespace:   "argocd",
		ClusterName: "cluster",
		Metadata:    JSONValue{Data: map[string]any{"kubernetes_access_mode": "database_kubeconfig"}},
	}
	got := server.discoverArgoFromKubernetesEnvironment(t.Context(), env)
	if got["status"] != "blocked" {
		t.Fatalf("status = %q, want blocked", got["status"])
	}
	reasons := stringSliceFromAny(got["blocked_reasons"])
	if !containsString(reasons, "kubeconfig_secret_ref_missing") {
		t.Fatalf("blocked_reasons = %#v", reasons)
	}
}

func TestArgoCredentialFromKubernetesPodEncryptsTokenWithoutLeakingMetadata(t *testing.T) {
	server := &Server{cfg: Config{WebhookSecretKey: "test-secret-material"}}
	ciphertext, err := server.encryptWebhookSecret("apiVersion: v1\nclusters:\n- name: test\ncontexts:\n- name: test\nusers:\n- name: test\n")
	if err != nil {
		t.Fatalf("encrypt kubeconfig: %v", err)
	}
	env := GormKubernetesEnvironment{
		GormBase:                   GormBase{ID: "kube-env-1"},
		ProjectID:                  "project-1",
		Name:                       "cluster",
		Namespace:                  "argocd",
		KubeconfigSecretRef:        "project/cluster/argocd.kubeconfig",
		KubeconfigSecretCiphertext: ciphertext,
	}
	oldDiscover := discoverArgoTokenFromKubernetesPodRun
	t.Cleanup(func() { discoverArgoTokenFromKubernetesPodRun = oldDiscover })
	discoverArgoTokenFromKubernetesPodRun = func(_ context.Context, kubeconfig, namespace string) (string, argoCredentialPodCandidate, error) {
		if !strings.Contains(kubeconfig, "apiVersion: v1") {
			t.Fatalf("kubeconfig not decrypted")
		}
		if namespace != "argocd" {
			t.Fatalf("namespace = %q, want argocd", namespace)
		}
		return "argo-secret-token", argoCredentialPodCandidate{Name: "argocd-server-0", Namespace: "argocd", Container: "argocd-server"}, nil
	}

	credential, err := server.argoCredentialFromKubernetesPod(t.Context(), env, "prod argo")
	if err != nil {
		t.Fatalf("argo credential from pod: %v", err)
	}
	if credential.Kind != "argo_token" || nullableStringValue(credential.ProjectID) != "project-1" {
		t.Fatalf("credential identity = %#v", credential)
	}
	plain, err := server.decryptWebhookSecret(credential.SecretCiphertext)
	if err != nil {
		t.Fatalf("decrypt token: %v", err)
	}
	if plain != "argo-secret-token" {
		t.Fatalf("token was not encrypted correctly")
	}
	metadata, err := json.Marshal(credential.Metadata.Data)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if strings.Contains(string(metadata), "argo-secret-token") {
		t.Fatalf("metadata leaked token: %s", metadata)
	}
}

func TestExistingAutoArgoCredentialReusesImportedToken(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &GormConnectionCredential{}, &GormArgoConnection{})
	server := &Server{store: store}
	env := GormKubernetesEnvironment{
		GormBase:  GormBase{ID: "kube-env-1"},
		ProjectID: "project-1",
		Name:      "cluster",
	}
	credential := GormConnectionCredential{
		ProjectID:        validNullString(env.ProjectID),
		Name:             "prod auto Argo token",
		Kind:             "argo_token",
		SecretCiphertext: "encrypted",
		Metadata: JSONValue{Data: map[string]any{
			"source":                           "kubernetes_argocd_pod_exec",
			"source_kubernetes_environment_id": env.ID,
		}},
	}
	if err := store.Gorm.Create(&credential).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}
	connection := GormArgoConnection{
		ProjectID:    env.ProjectID,
		Name:         "prod argo",
		ServerURL:    "https://argo.example.test",
		AuthType:     "token",
		CredentialID: validNullString(credential.ID),
		Config:       JSONValue{Data: map[string]any{}},
	}
	if err := store.Gorm.Create(&connection).Error; err != nil {
		t.Fatalf("create connection: %v", err)
	}

	got, err := server.existingAutoArgoCredential(t.Context(), env, connection.ServerURL)
	if err != nil {
		t.Fatalf("existing auto credential: %v", err)
	}
	if got == nil || got.ID != credential.ID {
		t.Fatalf("credential = %#v, want %s", got, credential.ID)
	}
}

func TestExecuteArgoTokenCreateCreatesCredentialWithoutLeakingToken(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &GormOperationRun{}, &GormConnectionCredential{}, &GormKubernetesEnvironment{})
	server := &Server{store: store, cfg: Config{WebhookSecretKey: "test-secret-material"}}
	kubeconfigCiphertext, err := server.encryptWebhookSecret("apiVersion: v1\nclusters:\n- name: test\ncontexts:\n- name: test\nusers:\n- name: test\n")
	if err != nil {
		t.Fatalf("encrypt kubeconfig: %v", err)
	}
	env := GormKubernetesEnvironment{
		GormBase:                   GormBase{ID: "kube-env-1"},
		ProjectID:                  "project-1",
		Name:                       "cluster",
		Namespace:                  "argocd",
		KubeconfigSecretRef:        "project/cluster/argocd.kubeconfig",
		KubeconfigSecretCiphertext: kubeconfigCiphertext,
		Metadata:                   JSONValue{Data: map[string]any{}},
	}
	if err := store.Gorm.Create(&env).Error; err != nil {
		t.Fatalf("create env: %v", err)
	}
	op := GormOperationRun{
		ProjectID:     validNullString(env.ProjectID),
		OperationType: "argo.token_create",
		Status:        "running",
		Title:         "create token",
		Input: JSONValue{Data: map[string]any{
			"kubernetes_environment_id": env.ID,
			"connection_name":           "prod argo",
		}},
		Result: JSONValue{Data: map[string]any{}},
	}
	if err := store.Gorm.Create(&op).Error; err != nil {
		t.Fatalf("create op: %v", err)
	}
	oldDiscover := discoverArgoTokenFromKubernetesPodRun
	t.Cleanup(func() { discoverArgoTokenFromKubernetesPodRun = oldDiscover })
	discoverArgoTokenFromKubernetesPodRun = func(_ context.Context, kubeconfig, namespace string) (string, argoCredentialPodCandidate, error) {
		if !strings.Contains(kubeconfig, "apiVersion: v1") {
			t.Fatalf("kubeconfig not decrypted")
		}
		if namespace != "argocd" {
			t.Fatalf("namespace = %q, want argocd", namespace)
		}
		return "worker-created-token", argoCredentialPodCandidate{Name: "argocd-server-0", Namespace: "argocd", Container: "argocd-server"}, nil
	}

	worker := &ControlWorker{store: store, cfg: server.cfg}
	result, err := worker.executeArgoTokenCreate(t.Context(), op.ID, map[string]any{})
	if err != nil {
		t.Fatalf("execute token create: %v", err)
	}
	if result["credential_id"] == "" || result["secret_included"] != false || result["stdout_included"] != false || result["stderr_included"] != false {
		t.Fatalf("unsafe or missing result: %#v", result)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(raw), "worker-created-token") {
		t.Fatalf("operation result leaked token: %s", raw)
	}
	var credential GormConnectionCredential
	if err := store.Gorm.First(&credential, &GormConnectionCredential{Kind: "argo_token"}).Error; err != nil {
		t.Fatalf("load credential: %v", err)
	}
	plain, err := server.decryptWebhookSecret(credential.SecretCiphertext)
	if err != nil {
		t.Fatalf("decrypt credential: %v", err)
	}
	if plain != "worker-created-token" {
		t.Fatalf("credential secret = %q, want token", plain)
	}
}

func TestExecuteArgoTokenCreateReusesExistingCredential(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &GormOperationRun{}, &GormConnectionCredential{}, &GormKubernetesEnvironment{})
	env := GormKubernetesEnvironment{
		GormBase:  GormBase{ID: "kube-env-1"},
		ProjectID: "project-1",
		Name:      "cluster",
		Namespace: "argocd",
		Metadata:  JSONValue{Data: map[string]any{}},
	}
	if err := store.Gorm.Create(&env).Error; err != nil {
		t.Fatalf("create env: %v", err)
	}
	credential := GormConnectionCredential{
		ProjectID:        validNullString(env.ProjectID),
		Name:             "existing auto Argo token",
		Kind:             "argo_token",
		SecretCiphertext: "encrypted",
		Metadata: JSONValue{Data: map[string]any{
			"source":                           "kubernetes_argocd_pod_exec",
			"source_kubernetes_environment_id": env.ID,
			"namespace":                        "argocd",
			"pod_name":                         "argocd-server-0",
			"container_name":                   "argocd-server",
		}},
	}
	if err := store.Gorm.Create(&credential).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}
	op := GormOperationRun{
		ProjectID:     validNullString(env.ProjectID),
		OperationType: "argo.token_create",
		Status:        "running",
		Title:         "create token",
		Input:         JSONValue{Data: map[string]any{"kubernetes_environment_id": env.ID}},
		Result:        JSONValue{Data: map[string]any{}},
	}
	if err := store.Gorm.Create(&op).Error; err != nil {
		t.Fatalf("create op: %v", err)
	}
	oldDiscover := discoverArgoTokenFromKubernetesPodRun
	t.Cleanup(func() { discoverArgoTokenFromKubernetesPodRun = oldDiscover })
	discoverArgoTokenFromKubernetesPodRun = func(context.Context, string, string) (string, argoCredentialPodCandidate, error) {
		t.Fatal("token discovery should not run when reusable credential exists")
		return "", argoCredentialPodCandidate{}, nil
	}

	result, err := (&ControlWorker{store: store}).executeArgoTokenCreate(t.Context(), op.ID, map[string]any{})
	if err != nil {
		t.Fatalf("execute token create: %v", err)
	}
	if result["credential_id"] != credential.ID || result["message"] != "Argo token credential reused" {
		t.Fatalf("result = %#v, want reused credential %s", result, credential.ID)
	}
	var count int64
	if err := store.Gorm.Model(&GormConnectionCredential{}).Count(&count).Error; err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if count != 1 {
		t.Fatalf("credential count = %d, want 1", count)
	}
}
