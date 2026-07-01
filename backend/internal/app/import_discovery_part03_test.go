package app

import (
	"context"
	"os"
	"path/filepath"
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

func TestUpsertImportedKubernetesEnvironmentAllowsEmptyKubeconfigRefInSSHKubectlMode(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &GormKubernetesEnvironment{})
	server := &Server{store: store, cfg: Config{KubernetesSSHKubectlEnabled: true}}
	machine := GormSSHMachine{
		ProjectID: "project-1",
		Name:      "cluster-machine",
		Metadata:  JSONValue{Data: map[string]any{"kubeconfig_secret_ref": "cluster/default-reader"}},
	}
	discovery := sshKubernetesDiscovery{
		Kind:           "kubernetes",
		Namespace:      "default",
		ClusterName:    "cluster",
		Context:        "cluster-context",
		ServerHost:     "private-cluster-endpoint",
		ServiceAccount: "system:serviceaccount:default:reader",
	}

	env, err := server.upsertImportedKubernetesEnvironment(t.Context(), machine, discovery, struct {
		Name                string `json:"name"`
		Environment         string `json:"environment"`
		KubeconfigSecretRef string `json:"kubeconfig_secret_ref"`
		ServiceAccount      string `json:"service_account"`
		Status              string `json:"status"`
	}{Status: "metadata_only"})
	if err != nil {
		t.Fatalf("upsert imported kubernetes environment: %v", err)
	}
	if env.KubeconfigSecretRef != "" {
		t.Fatalf("KubeconfigSecretRef = %q, want empty in ssh kubectl mode", env.KubeconfigSecretRef)
	}
	metadata := mapFromAny(env.Metadata.Data)
	if metadata["kubernetes_access_mode"] != "ssh_kubectl" {
		t.Fatalf("kubernetes_access_mode = %q, want ssh_kubectl", metadata["kubernetes_access_mode"])
	}
}

func TestMaterializeImportedKubeconfigReadsRewritesAndTestsLocalConfig(t *testing.T) {
	store := newGormFixtureStore(t)
	dir := t.TempDir()
	server := &Server{store: store, cfg: Config{KubeconfigSecretDir: dir, KubectlPath: "kubectl"}}
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
	oldConnectionTest := importedKubeconfigConnectionTest
	t.Cleanup(func() {
		importedKubeconfigSSHRun = oldSSHRun
		importedKubeconfigConnectionTest = oldConnectionTest
	})
	importedKubeconfigSSHRun = func(_ context.Context, request sshRunRequest) (string, string, int, error) {
		if !strings.Contains(request.Command, "k3s kubectl config view --raw --minify") {
			t.Fatalf("unexpected ssh command: %s", request.Command)
		}
		return "apiVersion: v1\nclusters:\n- cluster:\n    server: https://127.0.0.1:6443\n  name: test\ncontexts:\n- name: test\nusers:\n- name: test\n", "", 0, nil
	}
	importedKubeconfigConnectionTest = func(_ context.Context, _ Config, path string) error {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read materialized kubeconfig: %v", err)
		}
		if !strings.Contains(string(content), "server: https://10.0.0.20:6443") || strings.Contains(string(content), "127.0.0.1") {
			t.Fatalf("kubeconfig server not rewritten: %s", content)
		}
		return nil
	}

	ref, err := server.materializeImportedKubeconfig(t.Context(), machine, discovery)
	if err != nil {
		t.Fatalf("materialize imported kubeconfig: %v", err)
	}
	wantRef := "project-1/cluster-machine/default.kubeconfig"
	if ref != wantRef {
		t.Fatalf("ref = %q, want %q", ref, wantRef)
	}
	if info, err := os.Stat(filepath.Join(dir, wantRef)); err != nil {
		t.Fatalf("stat materialized kubeconfig: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("materialized kubeconfig mode = %o, want 600", info.Mode().Perm())
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

func TestWriteImportedKubeconfigTempAcceptsSingleSegmentRef(t *testing.T) {
	dir := t.TempDir()
	content := []byte("apiVersion: v1\nclusters:\n- name: test\ncontexts:\n- name: test\nusers:\n- name: test\n")
	tempPath, finalPath, err := writeImportedKubeconfigTemp(Config{KubeconfigSecretDir: dir}, "cluster-reader", content)
	if err != nil {
		t.Fatalf("write imported kubeconfig temp: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tempPath) })
	if filepath.Dir(tempPath) != dir {
		t.Fatalf("temp path dir = %q, want %q", filepath.Dir(tempPath), dir)
	}
	if finalPath != filepath.Join(dir, "cluster-reader") {
		t.Fatalf("final path = %q, want %q", finalPath, filepath.Join(dir, "cluster-reader"))
	}
}

func TestDiscoverArgoBlocksSSHKubectlImportedEnvironment(t *testing.T) {
	server := &Server{}
	env := GormKubernetesEnvironment{
		Name:        "cluster",
		Namespace:   "argocd",
		ClusterName: "cluster",
		Metadata:    JSONValue{Data: map[string]any{"kubernetes_access_mode": "ssh_kubectl"}},
	}
	got := server.discoverArgoFromKubernetesEnvironment(t.Context(), env)
	if got["status"] != "blocked" {
		t.Fatalf("status = %q, want blocked", got["status"])
	}
	reasons := stringSliceFromAny(got["blocked_reasons"])
	if !containsString(reasons, "ssh_kubectl_argo_discovery_not_implemented") {
		t.Fatalf("blocked_reasons = %#v", reasons)
	}
}
