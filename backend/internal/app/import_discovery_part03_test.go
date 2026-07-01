package app

import "testing"

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
			want:     "",
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

func TestUpsertImportedKubernetesEnvironmentDefaultsKubeconfigRefFromSSHMachine(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &GormKubernetesEnvironment{})
	server := &Server{store: store}
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
	if env.KubeconfigSecretRef != "cluster/default-reader" {
		t.Fatalf("KubeconfigSecretRef = %q, want %q", env.KubeconfigSecretRef, "cluster/default-reader")
	}
}
