package app

import (
	"testing"

	"github.com/lib/pq"
)

func TestLocalWorkerPreferredKinds(t *testing.T) {
	got := localWorkerPreferredKinds()
	want := []string{"", "control-worker", "local"}
	if len(got) != len(want) {
		t.Fatalf("local worker kinds = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("local worker kinds = %#v", got)
		}
	}
}

func TestWorkerJobMatchesRemoteNodeRequiresExplicitRemoteKind(t *testing.T) {
	for _, preferred := range []string{"", "control-worker", "local"} {
		job := GormWorkerJob{PreferredNodeKind: preferred}
		if workerJobMatchesRemoteNode(job, "host", nil) {
			t.Fatalf("remote node matched local preferred kind %q", preferred)
		}
	}
}

func TestWorkerJobMatchesRemoteNodeRequiresKindAndCapabilities(t *testing.T) {
	job := GormWorkerJob{
		PreferredNodeKind:    "host",
		RequiredCapabilities: pq.StringArray{"exec", "docker"},
	}
	if workerJobMatchesRemoteNode(job, "k8s", []string{"exec", "docker"}) {
		t.Fatal("remote node matched wrong kind")
	}
	if workerJobMatchesRemoteNode(job, "host", []string{"exec"}) {
		t.Fatal("remote node matched missing capability")
	}
	if !workerJobMatchesRemoteNode(job, "host", []string{"exec", "docker", "kubernetes"}) {
		t.Fatal("remote node should match kind and required capabilities")
	}
}
