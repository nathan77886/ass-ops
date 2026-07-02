package app

import (
	"context"
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
