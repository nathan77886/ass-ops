package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPostgresProcessDatabaseURLStripsPassword(t *testing.T) {
	got, env, secrets, err := postgresProcessDatabaseURL("postgres://assops:secret-pass@postgres:5432/assops?sslmode=disable")
	if err != nil {
		t.Fatalf("postgresProcessDatabaseURL: %v", err)
	}
	if strings.Contains(got, "secret-pass") {
		t.Fatalf("database URL leaked password: %q", got)
	}
	if got != "postgres://assops@postgres:5432/assops?sslmode=disable" {
		t.Fatalf("database URL = %q", got)
	}
	if len(env) != 1 || env[0] != "PGPASSWORD=secret-pass" {
		t.Fatalf("env = %#v", env)
	}
	if len(secrets) != 2 {
		t.Fatalf("secrets = %#v", secrets)
	}
}

func TestPostgresProcessDatabaseURLRejectsKeywordPassword(t *testing.T) {
	_, _, _, err := postgresProcessDatabaseURL("host=localhost user=assops password=secret dbname=assops")
	if err == nil {
		t.Fatal("expected keyword DSN with password to be rejected")
	}
}

func TestValidateRestoreRehearsalTarget(t *testing.T) {
	current := "postgres://assops:secret@localhost:5432/assops?sslmode=disable"
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@localhost:5432/assops?sslmode=disable", false); err == nil {
		t.Fatal("same target should be rejected")
	}
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@127.0.0.1:5432/assops?sslmode=disable", true); err == nil {
		t.Fatal("localhost and 127.0.0.1 same database should be rejected")
	}
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@0.0.0.0:5432/assops?sslmode=disable", true); err == nil {
		t.Fatal("localhost and 0.0.0.0 same database should be rejected")
	}
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@localhost:5432/assops?sslmode=require", true); err == nil {
		t.Fatal("same database with different query params should be rejected")
	}
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@localhost:5432/assops_prod?sslmode=disable", false); err == nil {
		t.Fatal("non-disposable target should be rejected without override")
	}
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@localhost:5432/assops_restore_test?sslmode=disable", false); err != nil {
		t.Fatalf("restore test target should be accepted: %v", err)
	}
	if err := validateRestoreRehearsalTarget("", "postgres://assops:secret@localhost:5432/assops_restore_test?sslmode=disable", false); err != nil {
		t.Fatalf("empty current database URL should not block disposable target: %v", err)
	}
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@localhost:5432/assops_prod?sslmode=disable", true); err != nil {
		t.Fatalf("override should accept target: %v", err)
	}
	if err := validateRestoreRehearsalTarget(current, "host=localhost dbname=assops_restore_test", true); err == nil || !strings.Contains(err.Error(), "URL-style") {
		t.Fatalf("keyword DSN should get URL-style error, got %v", err)
	}
}

func TestConfirmDestructiveRestore(t *testing.T) {
	dbURL := "postgres://assops:secret@localhost:5432/assops?sslmode=disable"
	if err := confirmDestructiveRestore(dbURL, ""); err == nil {
		t.Fatal("restore should require explicit database confirmation")
	}
	if err := confirmDestructiveRestore(dbURL, "wrong"); err == nil {
		t.Fatal("wrong confirmation should be rejected")
	}
	if err := confirmDestructiveRestore(dbURL, "assops"); err != nil {
		t.Fatalf("matching confirmation should be accepted: %v", err)
	}
}

func TestRedactedDatabaseURL(t *testing.T) {
	got := redactedDatabaseURL("postgres://assops:secret-pass@localhost:5432/assops_restore_test?sslmode=disable")
	if strings.Contains(got, "secret-pass") {
		t.Fatalf("redactedDatabaseURL leaked password: %q", got)
	}
	if !strings.Contains(got, "assops_restore_test") {
		t.Fatalf("redactedDatabaseURL should retain database name: %q", got)
	}
}

func TestWriteJSONReportCreatesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-notes", "restore-rehearsal.json")
	if err := writeJSONReport(path, map[string]any{"target_database": "postgres://assops@db:5432/assops_restore_test"}); err != nil {
		t.Fatalf("writeJSONReport: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat report: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("report mode = %#o, want 0600", info.Mode().Perm())
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(bytes), "assops_restore_test") || !strings.HasSuffix(string(bytes), "\n") {
		t.Fatalf("report content = %q", bytes)
	}
}

func TestValidateReleaseBundleAcceptsCompleteArtifacts(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-tool-v1.0.0-linux-amd64.tar.gz": "binary",
		"assops-web-v1.0.0.tar.gz":              "web",
		"assops-0.1.0.tgz":                      "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops@postgres:5432/assops_restore_test?sslmode=disable")

	result, err := validateReleaseBundle(artifactDir, reportPath)
	if err != nil {
		t.Fatalf("validateReleaseBundle: %v", err)
	}
	if result["valid"] != true {
		t.Fatalf("result valid = %#v", result["valid"])
	}
	if result["checksum_entries"] != len(files) || result["checksum_verified"] != len(files) {
		t.Fatalf("checksum result = %#v", result)
	}
}

func TestValidateReleaseBundleRejectsBadChecksum(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-tool-v1.0.0-linux-amd64.tar.gz": "binary",
		"assops-web-v1.0.0.tar.gz":              "web",
		"assops-0.1.0.tgz":                      "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	if err := os.WriteFile(filepath.Join(artifactDir, "assops-web-v1.0.0.tar.gz"), []byte("changed"), 0o600); err != nil {
		t.Fatalf("tamper artifact: %v", err)
	}
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops@postgres:5432/assops_restore_test?sslmode=disable")

	err := expectReleaseBundleError(artifactDir, reportPath)
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestValidateReleaseBundleRejectsLeakyRehearsalTarget(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-tool-v1.0.0-linux-amd64.tar.gz": "binary",
		"assops-web-v1.0.0.tar.gz":              "web",
		"assops-0.1.0.tgz":                      "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops:secret@postgres:5432/assops_restore_test?sslmode=disable")

	err := expectReleaseBundleError(artifactDir, reportPath)
	if !strings.Contains(err.Error(), "must not include a password") {
		t.Fatalf("expected password leak error, got %v", err)
	}
}

func TestReadChecksumFileRejectsUnsafePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SHA256SUMS")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("evil")))
	if err := os.WriteFile(path, []byte(hash+"  ../evil.tar.gz\n"), 0o600); err != nil {
		t.Fatalf("write SHA256SUMS: %v", err)
	}
	if _, err := readChecksumFile(path); err == nil {
		t.Fatal("expected unsafe checksum path to be rejected")
	}
}

func TestReleaseHelmValuesGeneratesGHCRImages(t *testing.T) {
	values, err := releaseHelmValues("Nathan77886", "v0.1.0")
	if err != nil {
		t.Fatalf("releaseHelmValues: %v", err)
	}
	for _, want := range []string{
		"registry: ghcr.io",
		"repository: nathan77886/assops-gateway",
		"repository: nathan77886/assops-worker",
		"repository: nathan77886/assops-node-worker",
		"repository: nathan77886/assops-web",
		"tag: v0.1.0",
	} {
		if !strings.Contains(values, want) {
			t.Fatalf("releaseHelmValues missing %q in:\n%s", want, values)
		}
	}
}

func TestReleaseHelmValuesRejectsRepositoryPath(t *testing.T) {
	if _, err := releaseHelmValues("owner/repo", "v0.1.0"); err == nil {
		t.Fatal("expected owner/repo to be rejected")
	}
	if _, err := releaseHelmValues("owner", "v0.1.0 bad"); err == nil {
		t.Fatal("expected version with whitespace to be rejected")
	}
}

func TestWriteTextFileCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release", "values.yaml")
	if err := writeTextFile(path, "image:\n"); err != nil {
		t.Fatalf("writeTextFile: %v", err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read values: %v", err)
	}
	if string(bytes) != "image:\n" {
		t.Fatalf("values content = %q", bytes)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat values: %v", err)
	}
	if info.Mode().Perm()&0o111 != 0 || info.Mode().Perm()&0o400 == 0 {
		t.Fatalf("values mode = %#o, want owner-readable non-executable file", info.Mode().Perm())
	}
}

func TestFirstVersionReadinessReportRequiresArgoSync(t *testing.T) {
	partial := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target"},
	}, nil, nil, nil)
	if got := readinessByKey(t, partial, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 0 Argo connections / 0 apps / 0 sync ops / 0 complete app links" {
		t.Fatalf("argo status with target only = %#v, want partial", got)
	}

	withoutGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target"},
		{"asset_type": "argo_connection"},
		{"asset_type": "argo_app"},
	}, []map[string]any{
		{"operation_type": "argo.apps.sync"},
	}, nil, map[string]any{"edges": []any{}})
	if got := readinessByKey(t, withoutGraphLinks, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 1 Argo connections / 1 apps / 1 sync ops / 0 complete app links" {
		t.Fatalf("argo status without graph links = %#v, want partial with graph evidence", got)
	}

	ready := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target"},
		{"asset_type": "argo_connection"},
		{"asset_type": "argo_app"},
	}, []map[string]any{
		{"operation_type": "argo.apps.sync"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
		},
	})
	if got := readinessByKey(t, ready, "argo"); got.Status != "ready" || got.Evidence != "1 targets / 1 Argo connections / 1 apps / 1 sync ops / 1 complete app links" {
		t.Fatalf("argo status with complete app graph = %#v, want ready", got)
	}

	crossAppAggregation := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target"},
		{"asset_type": "argo_connection"},
		{"asset_type": "argo_app"},
		{"asset_type": "argo_app"},
	}, []map[string]any{
		{"operation_type": "argo.apps.sync"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:21", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
		},
	})
	if got := readinessByKey(t, crossAppAggregation, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 1 Argo connections / 2 apps / 1 sync ops / 0 complete app links" {
		t.Fatalf("argo status with cross-app aggregate links = %#v, want partial without a complete app link", got)
	}
}

func TestFirstVersionReadinessReportRequiresSSHCommandGraphLinks(t *testing.T) {
	withoutGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host"},
		{"asset_type": "ssh_command_run"},
	}, []map[string]any{
		{"operation_type": "ssh.exec"},
	}, nil, map[string]any{"edges": []any{}})
	if got := readinessByKey(t, withoutGraphLinks, "ssh"); got.Status != "partial" || got.Evidence != "1 hosts / 1 command ops / 1 command assets / 0 complete command links" {
		t.Fatalf("ssh readiness without graph links = %#v, want partial with graph evidence", got)
	}

	ready := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host"},
		{"asset_type": "ssh_command_run"},
	}, []map[string]any{
		{"operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, ready, "ssh"); got.Status != "ready" || got.Evidence != "1 hosts / 1 command ops / 1 command assets / 1 complete command links" {
		t.Fatalf("ssh readiness with complete command graph = %#v, want ready", got)
	}

	crossCommandAggregation := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host"},
		{"asset_type": "ssh_command_run"},
		{"asset_type": "ssh_command_run"},
	}, []map[string]any{
		{"operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:21", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, crossCommandAggregation, "ssh"); got.Status != "partial" || got.Evidence != "1 hosts / 1 command ops / 2 command assets / 0 complete command links" {
		t.Fatalf("ssh readiness with cross-command aggregate links = %#v, want partial without a complete command", got)
	}
}

func TestFirstVersionReadinessReportRequiresWebhookEventForSyncTrigger(t *testing.T) {
	withoutEvent := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "metadata": map[string]any{"provider": "gitea"}},
	}, []map[string]any{
		{"operation_type": "repo.sync"},
	}, nil, map[string]any{"edges": []any{}})
	if got := readinessByKey(t, withoutEvent, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 0 Gitea events / 0 complete webhook chains" {
		t.Fatalf("sync trigger without webhook event = %#v, want partial with event evidence", got)
	}

	withoutGraphChain := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_event", "metadata": map[string]any{"provider": "gitea"}},
	}, []map[string]any{
		{"operation_type": "repo.sync_remote"},
	}, nil, map[string]any{"edges": []any{}})
	if got := readinessByKey(t, withoutGraphChain, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 0 complete webhook chains" {
		t.Fatalf("sync trigger without complete graph chain = %#v, want partial", got)
	}

	withGraphChain := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_event", "metadata": map[string]any{"provider": "gitea"}},
	}, []map[string]any{
		{"operation_type": "repo.sync_remote"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "webhook_connection:1", "to_asset_id": "webhook_event:20", "relation_type": "received_webhook_event"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "repo_sync:30", "relation_type": "matched_repo_sync"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "operation_run:40", "relation_type": "triggered_operation"},
		},
	})
	if got := readinessByKey(t, withGraphChain, "sync_trigger"); got.Status != "ready" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 1 complete webhook chains" {
		t.Fatalf("sync trigger with webhook graph chain = %#v, want ready with complete graph evidence", got)
	}

	crossEventAggregation := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_event", "metadata": map[string]any{"provider": "gitea"}},
	}, []map[string]any{
		{"operation_type": "repo.sync_remote"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "webhook_connection:1", "to_asset_id": "webhook_event:20", "relation_type": "received_webhook_event"},
			map[string]any{"from_asset_id": "webhook_event:21", "to_asset_id": "repo_sync:30", "relation_type": "matched_repo_sync"},
			map[string]any{"from_asset_id": "webhook_event:21", "to_asset_id": "operation_run:40", "relation_type": "triggered_operation"},
		},
	})
	if got := readinessByKey(t, crossEventAggregation, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 0 complete webhook chains" {
		t.Fatalf("sync trigger with cross-event aggregate links = %#v, want partial without a complete event chain", got)
	}

	eventOnly := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "webhook_event", "metadata": map[string]any{"provider": "gitea"}},
	}, nil, nil)
	if got := readinessByKey(t, eventOnly, "sync_trigger"); got.Status != "partial" || got.Evidence != "0 sync ops / 0 Gitea webhooks / 1 Gitea events / 0 complete webhook chains" {
		t.Fatalf("sync trigger event only = %#v, want partial", got)
	}

	githubOnly := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "webhook_connection", "metadata": map[string]any{"provider": "github"}},
		{"asset_type": "webhook_event", "metadata": map[string]any{"provider": "github"}},
	}, []map[string]any{
		{"operation_type": "repo.sync"},
	}, nil)
	if got := readinessByKey(t, githubOnly, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 0 Gitea webhooks / 0 Gitea events / 0 complete webhook chains" {
		t.Fatalf("sync trigger with GitHub webhook evidence = %#v, want partial without Gitea evidence", got)
	}
}

func TestFirstVersionReadinessReportRequiresRepositoryGraphLinks(t *testing.T) {
	withoutGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository"},
		{"asset_type": "git_remote"},
		{"asset_type": "git_remote"},
	}, nil, nil, map[string]any{
		"edges": []any{},
	})
	if got := readinessByKey(t, withoutGraphLinks, "repositories"); got.Status != "partial" || got.Evidence != "1 repos / 2 remotes / 0 project links / 0 remote links" {
		t.Fatalf("repository readiness without graph links = %#v, want partial with graph evidence", got)
	}

	withGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository"},
		{"asset_type": "git_remote"},
		{"asset_type": "git_remote"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:100", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:101", "relation_type": "has_remote"},
		},
	})
	if got := readinessByKey(t, withGraphLinks, "repositories"); got.Status != "ready" || got.Evidence != "1 repos / 2 remotes / 1 project links / 2 remote links" {
		t.Fatalf("repository readiness with graph links = %#v, want ready", got)
	}

	unrelatedGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository"},
		{"asset_type": "git_remote"},
		{"asset_type": "git_remote"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "git_remote:100", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "webhook_connection:1", "relation_type": "has_remote"},
		},
	})
	if got := readinessByKey(t, unrelatedGraphLinks, "repositories"); got.Status != "partial" || got.Evidence != "1 repos / 2 remotes / 0 project links / 0 remote links" {
		t.Fatalf("repository readiness with unrelated graph links = %#v, want partial without repository graph evidence", got)
	}
}

func TestCountRepositoryGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:100", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:101", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "git_remote:100", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "webhook_connection:1", "relation_type": "has_remote"},
		},
	}
	got := countRepositoryGraphLinks(graph)
	if got.ProjectRepository != 1 || got.RepositoryRemotes != 2 {
		t.Fatalf("countRepositoryGraphLinks = %#v, want 1 project link and 2 remote links", got)
	}
}

func TestFirstVersionReadinessReportRequiresRepoSyncGraphLinks(t *testing.T) {
	withoutGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repo_sync"},
	}, nil, nil, map[string]any{
		"edges": []any{},
	})
	if got := readinessByKey(t, withoutGraphLinks, "repo_sync"); got.Status != "partial" || got.Evidence != "1 repo syncs / 0 complete syncs / 0 repository links / 0 source links / 0 target links" {
		t.Fatalf("repo sync readiness without graph links = %#v, want partial with graph evidence", got)
	}

	withGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repo_sync"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:101", "relation_type": "mirrors_to"},
		},
	})
	if got := readinessByKey(t, withGraphLinks, "repo_sync"); got.Status != "ready" || got.Evidence != "1 repo syncs / 1 complete syncs / 1 repository links / 1 source links / 1 target links" {
		t.Fatalf("repo sync readiness with graph links = %#v, want ready", got)
	}

	missingTargetLink := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repo_sync"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "webhook_connection:1", "relation_type": "mirrors_to"},
		},
	})
	if got := readinessByKey(t, missingTargetLink, "repo_sync"); got.Status != "partial" || got.Evidence != "1 repo syncs / 0 complete syncs / 1 repository links / 1 source links / 0 target links" {
		t.Fatalf("repo sync readiness with unrelated target link = %#v, want partial without target evidence", got)
	}

	crossSyncAggregation := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repo_sync"},
		{"asset_type": "repo_sync"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:21", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:21", "to_asset_id": "git_remote:101", "relation_type": "mirrors_to"},
		},
	})
	if got := readinessByKey(t, crossSyncAggregation, "repo_sync"); got.Status != "partial" || got.Evidence != "2 repo syncs / 0 complete syncs / 1 repository links / 1 source links / 1 target links" {
		t.Fatalf("repo sync readiness with cross-sync aggregate links = %#v, want partial without a complete sync", got)
	}
}

func TestCountRepoSyncGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:101", "relation_type": "mirrors_to"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "webhook_connection:1", "relation_type": "mirrors_to"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:100", "relation_type": "has_sync"},
		},
	}
	got := countRepoSyncGraphLinks(graph)
	if got.RepositorySync != 1 || got.SourceRemotes != 1 || got.TargetRemotes != 1 || got.CompleteSyncs != 1 {
		t.Fatalf("countRepoSyncGraphLinks = %#v, want repository/source/target/complete counts of 1", got)
	}
}

func TestCountAPITypeMetadata(t *testing.T) {
	rows := []map[string]any{
		{"asset_type": "webhook_event", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_event", "metadata": map[string]any{"provider": "github"}},
		{"asset_type": "webhook_connection", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_event"},
	}
	if got := countAPITypeMetadata(rows, "webhook_event", "provider", "gitea"); got != 1 {
		t.Fatalf("countAPITypeMetadata = %d, want 1", got)
	}
}

func TestCountWebhookSyncGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "webhook_connection:1", "to_asset_id": "webhook_event:20", "relation_type": "received_webhook_event"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "repo_sync:30", "relation_type": "matched_repo_sync"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "operation_run:40", "relation_type": "triggered_operation"},
			map[string]any{"from_asset_id": "webhook_connection:1", "to_asset_id": "operation_run:40", "relation_type": "triggered_operation"},
			map[string]any{"from_asset_id": "webhook_connection:2", "to_asset_id": "webhook_event:21", "relation_type": "received_webhook_event"},
			map[string]any{"from_asset_id": "webhook_event:22", "to_asset_id": "repo_sync:31", "relation_type": "matched_repo_sync"},
		},
	}
	got := countWebhookSyncGraphLinks(graph)
	if got.ConnectionEvents != 2 || got.EventRepoSyncs != 2 || got.EventOperations != 1 || got.CompleteChains != 1 {
		t.Fatalf("countWebhookSyncGraphLinks = %#v, want connection/event/repo/operation counts and one complete chain", got)
	}
}

func TestFirstVersionReadinessReportRequiresGitHubActionGraphLink(t *testing.T) {
	withoutLink := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, nil, nil, map[string]any{
		"edges": []any{},
	})
	if got := readinessByKey(t, withoutLink, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 0 graph links" {
		t.Fatalf("github actions without graph link = %#v, want partial with link evidence", got)
	}

	withLink := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{
				"from_asset_id": "git_remote:42",
				"to_asset_id":   "github_action_run:101",
				"relation_type": "triggered_by",
			},
		},
	})
	if got := readinessByKey(t, withLink, "github_actions"); got.Status != "ready" || got.Evidence != "1 pipeline runs / 1 graph links" {
		t.Fatalf("github actions with graph link = %#v, want ready", got)
	}

	wrongLink := firstVersionReadinessReportWithGraph(nil, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{
				"from_asset_id": "repository:repo-1",
				"to_asset_id":   "github_action_run:run-1",
				"relation_type": "owns",
			},
		},
	})
	if got := readinessByKey(t, wrongLink, "github_actions"); got.Status != "missing" || got.Evidence != "0 pipeline runs / 0 graph links" {
		t.Fatalf("github actions with unrelated graph edge = %#v, want missing", got)
	}
}

func TestCountGitHubActionGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "git_remote:1", "to_asset_id": "github_action_run:1", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "git_remote:2", "to_asset_id": "github_action_run:2", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:1", "to_asset_id": "github_action_run:3", "relation_type": "triggered_by"},
		},
	}
	if got := countGitHubActionGraphLinks(graph); got != 1 {
		t.Fatalf("countGitHubActionGraphLinks = %d, want 1", got)
	}
}

func TestCountSSHGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "operation_run:11", "to_asset_id": "ssh_machine:31", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "operation_run:12", "to_asset_id": "ssh_command_run:21", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "ssh_command_run:22", "to_asset_id": "ssh_machine:32", "relation_type": "executed_on"},
		},
	}
	got := countSSHGraphLinks(graph)
	if got.OperationCommands != 1 || got.CommandMachines != 2 || got.CompleteCommands != 1 {
		t.Fatalf("countSSHGraphLinks = %#v, want one operation-command, two command-machine, and one complete command", got)
	}
}

func TestCountArgoGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "deployment_target:30", "to_asset_id": "argo_app:20", "relation_type": "hosts"},
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "deployment_target:30", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:21", "to_asset_id": "deployment_target:31", "relation_type": "deployed_to"},
		},
	}
	got := countArgoGraphLinks(graph)
	if got.ConnectionApps != 1 || got.AppTargets != 2 || got.CompleteApps != 1 {
		t.Fatalf("countArgoGraphLinks = %#v, want one connection-app, two app-targets, and one complete app", got)
	}
}

func TestFirstVersionReadinessReportApprovalReadinessMatrix(t *testing.T) {
	withoutSummary := firstVersionReadinessReport(nil, []map[string]any{
		{"operation_type": "approval.notify", "status": "completed"},
	}, nil)
	if got := readinessByKey(t, withoutSummary, "approval"); got.Status != "missing" {
		t.Fatalf("approval status from operation_type alone = %q, want missing", got.Status)
	}

	withSummary := firstVersionReadinessReport(nil, nil, map[string]any{"total": float64(1)})
	if got := readinessByKey(t, withSummary, "approval"); got.Status != "partial" || got.Evidence != "1 approvals / 0 pending ops / 0 active rules" {
		t.Fatalf("approval status from summary without rule = %#v, want partial with rule evidence", got)
	}

	withRule := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_approval_rule", "status": "active"},
	}, nil, map[string]any{"total": float64(1)})
	if got := readinessByKey(t, withRule, "approval"); got.Status != "ready" || got.Evidence != "1 approvals / 0 pending ops / 1 active rules" {
		t.Fatalf("approval status from summary and rule = %#v, want ready", got)
	}

	ruleOnly := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_approval_rule", "status": "active"},
	}, nil, nil)
	if got := readinessByKey(t, ruleOnly, "approval"); got.Status != "partial" || got.Evidence != "0 approvals / 0 pending ops / 1 active rules" {
		t.Fatalf("approval status from rule without request evidence = %#v, want partial", got)
	}
}

func TestCountAPITypeStatus(t *testing.T) {
	rows := []map[string]any{
		{"asset_type": "operation_approval_rule", "status": "active"},
		{"asset_type": "operation_approval_rule", "status": "disabled"},
		{"asset_type": "operation_approval", "status": "active"},
	}
	if got := countAPITypeStatus(rows, "operation_approval_rule", "active"); got != 1 {
		t.Fatalf("countAPITypeStatus = %d, want 1", got)
	}
}

func TestFirstVersionReadinessReportUsesPendingApprovalOperation(t *testing.T) {
	report := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_approval_rule", "status": "active"},
	}, []map[string]any{
		{"operation_type": "ssh.exec", "status": "pending_approval"},
	}, nil)
	if got := readinessByKey(t, report, "approval"); got.Status != "ready" || got.Evidence != "0 approvals / 1 pending ops / 1 active rules" {
		t.Fatalf("approval status from pending operation and rule = %#v, want ready", got)
	}

	withoutRule := firstVersionReadinessReport(nil, []map[string]any{
		{"operation_type": "ssh.exec", "status": "pending_approval"},
	}, nil)
	if got := readinessByKey(t, withoutRule, "approval"); got.Status != "partial" || got.Evidence != "0 approvals / 1 pending ops / 0 active rules" {
		t.Fatalf("approval status from pending operation without rule = %#v, want partial", got)
	}
}

func TestFirstVersionReadinessReportIgnoresDisabledApprovalRules(t *testing.T) {
	report := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_approval_rule", "status": "disabled"},
	}, nil, map[string]any{"total": float64(1)})
	if got := readinessByKey(t, report, "approval"); got.Status != "partial" || got.Evidence != "1 approvals / 0 pending ops / 0 active rules" {
		t.Fatalf("approval status with disabled rule = %#v, want partial without active rule evidence", got)
	}
}

func TestFirstVersionReadinessReportRequiresOperationLogs(t *testing.T) {
	withoutLogs := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_run"},
	}, []map[string]any{
		{"operation_type": "repo.sync", "log_count": 0},
	}, nil)
	if got := readinessByKey(t, withoutLogs, "operations"); got.Status != "partial" || got.Evidence != "1 runs / 0 with logs" {
		t.Fatalf("operations readiness without logs = %#v, want partial with log evidence", got)
	}

	withLogs := firstVersionReadinessReport(nil, []map[string]any{
		{"operation_type": "repo.sync", "log_count": 2},
	}, nil)
	if got := readinessByKey(t, withLogs, "operations"); got.Status != "ready" || got.Evidence != "1 runs / 1 with logs" {
		t.Fatalf("operations readiness with logs = %#v, want ready with log evidence", got)
	}
}

func TestFirstVersionReadinessReportRequiresContextGraphEvidence(t *testing.T) {
	missing := firstVersionReadinessReportWithGraph(nil, nil, nil, nil)
	if got := readinessByKey(t, missing, "context"); got.Status != "missing" || got.Evidence != "0 context assets / 0 context generations / 0 graph nodes / 0 graph edges" {
		t.Fatalf("context readiness without context or graph evidence = %#v, want missing", got)
	}

	withoutGraph := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
		{"asset_type": "agent_task"},
	}, nil, nil, nil)
	if got := readinessByKey(t, withoutGraph, "context"); got.Status != "partial" || got.Evidence != "2 context assets / 0 context generations / 0 graph nodes / 0 graph edges" {
		t.Fatalf("context readiness without graph evidence = %#v, want partial with graph evidence", got)
	}

	graphOnly := firstVersionReadinessReportWithGraph(nil, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
	})
	if got := readinessByKey(t, graphOnly, "context"); got.Status != "partial" || got.Evidence != "0 context assets / 0 context generations / 1 graph nodes / 0 graph edges" {
		t.Fatalf("context readiness without context assets = %#v, want partial with graph evidence", got)
	}

	withoutGeneration := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
	}, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
		"edges": []any{map[string]any{"from_asset_id": "project:1", "to_asset_id": "repo:1"}},
	})
	if got := readinessByKey(t, withoutGeneration, "context"); got.Status != "partial" || got.Evidence != "1 context assets / 0 context generations / 1 graph nodes / 1 graph edges" {
		t.Fatalf("context readiness without generation evidence = %#v, want partial", got)
	}

	withGeneration := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
		{"asset_type": "agent_tool_call", "status": "completed", "metadata": map[string]any{"tool_name": "context.generate"}},
	}, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
		"edges": []any{map[string]any{"from_asset_id": "project:1", "to_asset_id": "repo:1"}},
	})
	if got := readinessByKey(t, withGeneration, "context"); got.Status != "ready" || got.Evidence != "1 context assets / 1 context generations / 1 graph nodes / 1 graph edges" {
		t.Fatalf("context readiness with graph evidence = %#v, want ready with graph evidence", got)
	}
}

func TestCountContextGenerationEvidence(t *testing.T) {
	assets := []map[string]any{
		{"asset_type": "agent_tool_call", "status": "queued", "metadata": map[string]any{"tool_name": "context.generate"}},
		{"asset_type": "agent_tool_call", "status": "completed", "metadata": map[string]any{"tool_name": "context.generate"}},
		{"asset_type": "agent_tool_call", "status": "running", "metadata": map[string]any{"tool_name": "context.generate"}},
		{"asset_type": "agent_tool_call", "status": "failed", "metadata": map[string]any{"tool_name": "context.generate"}},
		{"asset_type": "agent_tool_call", "status": "completed", "metadata": map[string]any{"tool_name": "plan.review"}},
	}
	if got := countContextGenerationEvidence(assets); got != 2 {
		t.Fatalf("countContextGenerationEvidence = %d, want 2", got)
	}
}

func TestAssetGraphPayloadAvailableRequiresNodesOrEdgesKey(t *testing.T) {
	cases := []struct {
		name  string
		graph map[string]any
		want  bool
	}{
		{name: "nil", graph: nil, want: false},
		{name: "empty", graph: map[string]any{}, want: false},
		{name: "nodes", graph: map[string]any{"nodes": []any{}}, want: true},
		{name: "edges", graph: map[string]any{"edges": []any{}}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := assetGraphPayloadAvailable(tc.graph); got != tc.want {
				t.Fatalf("assetGraphPayloadAvailable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCountOperationRowsWithLogsHandlesMissingLogCount(t *testing.T) {
	rows := []map[string]any{
		{"operation_type": "repo.sync"},
		{"operation_type": "ssh.exec", "log_count": 1},
	}
	if got := countOperationRowsWithLogs(rows); got != 1 {
		t.Fatalf("countOperationRowsWithLogs with missing log_count = %d, want 1", got)
	}
}

func TestReleasePromotionPlanIncludesVerificationAndRollout(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-v0.1.0-linux-amd64.tar.gz": "binary",
		"assops-web-v0.1.0.tar.gz":         "web",
		"assops-0.1.0.tgz":                 "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops@postgres:5432/assops_restore_test?sslmode=disable")
	valuesPath := filepath.Join(artifactDir, "helm-values.yaml")
	values, err := releaseHelmValues("Nathan77886", "v0.1.0")
	if err != nil {
		t.Fatalf("releaseHelmValues: %v", err)
	}
	if err := writeTextFile(valuesPath, values); err != nil {
		t.Fatalf("write values: %v", err)
	}

	plan, err := releasePromotionPlan("nathan77886/ass-ops", "Nathan77886", "v0.1.0", artifactDir, reportPath, valuesPath)
	if err != nil {
		t.Fatalf("releasePromotionPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Promotion Plan v0.1.0",
		"Helm values sha256:",
		"assops-tool release validate-bundle",
		"gh attestation verify",
		"oci://ghcr.io/nathan77886/assops-gateway:v0.1.0",
		"## Rollout Guardrails",
		"preflight-only",
		"protected environment",
		"namespace-scoped kubeconfig",
		"rollback point",
		"operator approval",
		"helm template assops deploy/helm/assops",
		"helm upgrade --install assops deploy/helm/assops",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("promotion plan missing %q in:\n%s", want, plan)
		}
	}
}

func TestReleasePromotionPlanRejectsInvalidRepo(t *testing.T) {
	if _, err := releasePromotionPlan("owner", "owner", "v0.1.0", "missing", "missing", "missing"); err == nil {
		t.Fatal("expected invalid owner/repo to be rejected")
	}
}

func TestReleasePromotionPlanRejectsMismatchedHelmValues(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-v0.1.0-linux-amd64.tar.gz": "binary",
		"assops-web-v0.1.0.tar.gz":         "web",
		"assops-0.1.0.tgz":                 "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops@postgres:5432/assops_restore_test?sslmode=disable")
	valuesPath := filepath.Join(artifactDir, "helm-values.yaml")
	values, err := releaseHelmValues("nathan77886", "v0.2.0")
	if err != nil {
		t.Fatalf("releaseHelmValues: %v", err)
	}
	if err := writeTextFile(valuesPath, values); err != nil {
		t.Fatalf("write values: %v", err)
	}

	_, err = releasePromotionPlan("nathan77886/ass-ops", "nathan77886", "v0.1.0", artifactDir, reportPath, valuesPath)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected mismatched helm values error, got %v", err)
	}
}

func TestReleaseBackupSchedulePlanForArtifactSource(t *testing.T) {
	plan, err := releaseBackupSchedulePlan("nathan77886/ass-ops", "production", "ubuntu-latest", "17 3 * * 1", "artifact:retained-assops-backup", "14")
	if err != nil {
		t.Fatalf("releaseBackupSchedulePlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Production Backup Schedule Plan",
		"production-restore-rehearsal.yml",
		"ASSOPS_REHEARSAL_DATABASE_URL",
		"ASSOPS_ACTIVE_DATABASE_URL",
		"ASSOPS_PRODUCTION_RESTORE_REHEARSAL_ENABLED=true",
		"ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_ARTIFACT=retained-assops-backup",
		"backup_artifact_name=\"retained-assops-backup\"",
		"backup_path=''",
		"Retained Backup Publication Contract",
		"Publication must be produced by the environment-owned retained backup job",
		"must contain exactly one `assops-*.dump` backup",
		"production-retained-backup.yml",
		"raw `pg_dump` custom-format file",
		"external storage, additional encryption, and large-database handling remain environment-owned",
		"must stay unexpired for at least `14 days`",
		"do not include `.env`, database URLs, kubeconfigs, or raw logs",
		"The checked-in schedule is `17 3 * * 1`",
		"external",
	} {
		if want == "external" {
			if strings.Contains(plan, "ASSOPS_REHEARSAL_DATABASE_PASSWORD=") {
				t.Fatalf("schedule plan should not include secret values:\n%s", plan)
			}
			continue
		}
		if !strings.Contains(plan, want) {
			t.Fatalf("schedule plan missing %q in:\n%s", want, plan)
		}
	}
}

func TestReleaseBackupSchedulePlanForMountedPathSource(t *testing.T) {
	plan, err := releaseBackupSchedulePlan("nathan77886/ass-ops", "production", "self-hosted-prod", "23 2 * * 0", "path:/mnt/backups/assops-20260622-120000.dump", "30")
	if err != nil {
		t.Fatalf("releaseBackupSchedulePlan path source: %v", err)
	}
	for _, want := range []string{
		"runner-local backup path `/mnt/backups/assops-20260622-120000.dump`",
		"must be self-hosted",
		"ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_PATH=/mnt/backups/assops-20260622-120000.dump",
		"backup_artifact_name=''",
		"backup_path=\"/mnt/backups/assops-20260622-120000.dump\"",
		"Retained Backup Publication Contract",
		"must be mounted read-only on runner `self-hosted-prod`",
		"must handle backup retention, checksum publication, and deletion outside this workflow",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("path schedule plan missing %q in:\n%s", want, plan)
		}
	}
	if strings.Contains(plan, "ASSOPS_REHEARSAL_DATABASE_PASSWORD=") {
		t.Fatalf("path schedule plan should not include secret values:\n%s", plan)
	}
}

func TestProductionRestoreRehearsalWorkflowValidatesArtifactContents(t *testing.T) {
	content, err := os.ReadFile("../../../.github/workflows/production-restore-rehearsal.yml")
	if err != nil {
		t.Fatalf("read production restore rehearsal workflow: %v", err)
	}
	source := string(content)
	for _, want := range []string{
		"Retained backup artifact must contain exactly one assops-*.dump file",
		"-iname '.env*'",
		"-iname '*kubeconfig*'",
		"-iname '*.log'",
		"-iname '*.key'",
		"-iname '*.pem'",
		"Retained backup artifact contains disallowed secret/log-like files",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("production restore rehearsal workflow missing %q", want)
		}
	}
}

func TestProductionRetainedBackupWorkflowGuardrails(t *testing.T) {
	content, err := os.ReadFile("../../../.github/workflows/production-retained-backup.yml")
	if err != nil {
		t.Fatalf("read production retained backup workflow: %v", err)
	}
	source := string(content)
	for _, want := range []string{
		"ASSOPS_PRODUCTION_RETAINED_BACKUP_ENABLED == 'true'",
		"production-retained-backup-${{",
		"cancel-in-progress: false",
		"name: ${{ github.event_name == 'workflow_dispatch' && inputs.github_environment || vars.ASSOPS_PRODUCTION_RETAINED_BACKUP_ENVIRONMENT || 'production' }}",
		"DATABASE_URL: ${{ secrets.ASSOPS_ACTIVE_DATABASE_URL }}",
		"PGPASSWORD: ${{ secrets.ASSOPS_ACTIVE_DATABASE_PASSWORD }}",
		"ASSOPS_ACTIVE_DATABASE_URL environment secret is required",
		"set +x",
		"bin/assops-tool db backup-retain .assops/retained-backups \"$INPUT_KEEP_COUNT\"",
		"Retained backup artifact must contain exactly one assops-*.dump file",
		"-iname '.env*'",
		"-iname '*kubeconfig*'",
		"-iname '*.log'",
		"-iname '*.key'",
		"-iname '*.pem'",
		"No database URL, password, kubeconfig, or raw log files are written to the artifact staging directory.",
		"actions/upload-artifact@v4",
		"retention-days: ${{ env.INPUT_RETENTION_DAYS }}",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("production retained backup workflow missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"pg_dump -Fc",
		"pg_dump --file",
		"/tmp/assops-backup-retain.json",
		"cat /tmp/assops-backup-retain.json",
		"echo \"$DATABASE_URL\"",
		"echo $DATABASE_URL",
		"ASSOPS_ACTIVE_DATABASE_URL=",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("production retained backup workflow contains forbidden pattern %q", forbidden)
		}
	}
}

func TestReleaseBackupSchedulePlanRejectsUnsafeInputs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "invalid repo",
			args: []string{"owner", "production", "ubuntu-latest", "17 3 * * 1", "artifact:backup", "14"},
			want: "owner/repo",
		},
		{
			name: "unsafe cron",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1; curl bad", "artifact:backup", "14"},
			want: "five-field",
		},
		{
			name: "timezone cron prefix rejected",
			args: []string{"owner/repo", "production", "ubuntu-latest", "CRON_TZ=Asia/Shanghai 17 3 * * 1", "artifact:backup", "14"},
			want: "five-field",
		},
		{
			name: "empty environment",
			args: []string{"owner/repo", "", "ubuntu-latest", "17 3 * * 1", "artifact:backup", "14"},
			want: "environment",
		},
		{
			name: "empty runner",
			args: []string{"owner/repo", "production", "", "17 3 * * 1", "artifact:backup", "14"},
			want: "runner",
		},
		{
			name: "empty source",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "", "14"},
			want: "backup source",
		},
		{
			name: "artifact source without value",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "artifact:", "14"},
			want: "value is required",
		},
		{
			name: "artifact source with path characters",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "artifact:/mnt/backup.dump", "14"},
			want: "artifact name",
		},
		{
			name: "path source needs self hosted",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "path:/mnt/backups/assops.dump", "14"},
			want: "self-hosted",
		},
		{
			name: "unsafe path",
			args: []string{"owner/repo", "production", "self-hosted", "17 3 * * 1", "path:/mnt/../secret.dump", "14"},
			want: "unsupported",
		},
		{
			name: "retention too long",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "artifact:backup", "120"},
			want: "between 1 and 90",
		},
		{
			name: "retention zero",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "artifact:backup", "0"},
			want: "between 1 and 90",
		},
		{
			name: "retention negative",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "artifact:backup", "-1"},
			want: "between 1 and 90",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := releaseBackupSchedulePlan(tc.args[0], tc.args[1], tc.args[2], tc.args[3], tc.args[4], tc.args[5])
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releaseBackupSchedulePlan error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestCountNonEmptyLines(t *testing.T) {
	if got := countNonEmptyLines("one\n\n two \n"); got != 2 {
		t.Fatalf("countNonEmptyLines = %d, want 2", got)
	}
}

func TestPGRestoreListObjectCounts(t *testing.T) {
	counts := pgRestoreListObjectCounts(`
;
; Archive created at 2026-06-22
;
1234; 1259 20001 TABLE public projects assops
1235; 1259 20002 TABLE public users assops
1236; 1259 20003 INDEX public projects_pkey assops
1237; 0 0 ACL public projects assops
`)
	if counts["TABLE"] != 2 || counts["INDEX"] != 1 || counts["ACL"] != 1 {
		t.Fatalf("counts = %#v", counts)
	}
}

func TestSanitizeCommandOutput(t *testing.T) {
	got := sanitizeCommandOutput("failed postgres://assops:secret@db/assops password secret", []string{"postgres://assops:secret@db/assops", "secret"})
	if strings.Contains(got, "secret") || strings.Contains(got, "postgres://assops:secret@db/assops") {
		t.Fatalf("output leaked secret: %q", got)
	}
}

func TestBackupAndRestoreCommandsUseNoPasswordPrompt(t *testing.T) {
	content, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read assops-tool main.go: %v", err)
	}
	source := string(content)
	if got := strings.Count(source, `"pg_dump", "-Fc", "--no-owner", "--no-password", "--file"`); got < 2 {
		t.Fatalf("backup pg_dump commands should use --no-password, found %d guarded invocations", got)
	}
	if got := strings.Count(source, `"pg_restore", "--clean", "--if-exists", "--no-owner", "--no-password", "--dbname"`); got < 2 {
		t.Fatalf("restore pg_restore commands should use --no-password, found %d guarded invocations", got)
	}
}

func TestNextBackupPathAllocatesSuffixWhenTimestampExists(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 22, 12, 34, 56, 0, time.UTC)
	first, err := nextBackupPath(dir, now)
	if err != nil {
		t.Fatalf("nextBackupPath first: %v", err)
	}
	if filepath.Base(first) != "assops-20260622-123456.dump" {
		t.Fatalf("first path = %s", first)
	}
	if err := os.WriteFile(first, []byte("backup"), 0o600); err != nil {
		t.Fatalf("write first backup: %v", err)
	}
	second, err := nextBackupPath(dir, now)
	if err != nil {
		t.Fatalf("nextBackupPath second: %v", err)
	}
	if filepath.Base(second) != "assops-20260622-123456-001.dump" {
		t.Fatalf("second path = %s", second)
	}
}

func TestPruneBackupsKeepsNewestManagedFilesOnly(t *testing.T) {
	dir := t.TempDir()
	files := []struct {
		name string
		age  time.Duration
	}{
		{name: "assops-20260622-120000.dump", age: 4 * time.Hour},
		{name: "assops-20260622-130000.dump", age: 3 * time.Hour},
		{name: "assops-20260622-140000.dump", age: 2 * time.Hour},
		{name: "assops-20260622-150000.dump", age: 1 * time.Hour},
		{name: "manual.dump", age: 5 * time.Hour},
	}
	now := time.Now()
	for _, file := range files {
		path := filepath.Join(dir, file.name)
		if err := os.WriteFile(path, []byte(file.name), 0o600); err != nil {
			t.Fatalf("write %s: %v", file.name, err)
		}
		ts := now.Add(-file.age)
		if err := os.Chtimes(path, ts, ts); err != nil {
			t.Fatalf("chtimes %s: %v", file.name, err)
		}
	}

	pruned, err := pruneBackups(dir, 2)
	if err != nil {
		t.Fatalf("pruneBackups: %v", err)
	}
	if len(pruned) != 2 {
		t.Fatalf("pruned = %#v, want 2 files", pruned)
	}
	for _, name := range []string{"assops-20260622-140000.dump", "assops-20260622-150000.dump", "manual.dump"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s should remain: %v", name, err)
		}
	}
	for _, name := range []string{"assops-20260622-120000.dump", "assops-20260622-130000.dump"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should be pruned, stat err = %v", name, err)
		}
	}
}

func TestAcquireBackupDirLockPreventsConcurrentLock(t *testing.T) {
	dir := t.TempDir()
	unlock, err := acquireBackupDirLock(dir)
	if err != nil {
		t.Fatalf("acquireBackupDirLock first: %v", err)
	}
	if _, err := acquireBackupDirLock(dir); err == nil {
		t.Fatal("expected second lock acquisition to fail")
	}
	unlock()
	unlockAgain, err := acquireBackupDirLock(dir)
	if err != nil {
		t.Fatalf("acquireBackupDirLock after unlock: %v", err)
	}
	unlockAgain()
}

func TestListManagedBackupsSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	backupPath := filepath.Join(dir, "assops-20260622-150000.dump")
	if err := os.WriteFile(backupPath, []byte("backup"), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	targetPath := filepath.Join(dir, "manual-target.dump")
	if err := os.WriteFile(targetPath, []byte("manual"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(targetPath, filepath.Join(dir, "assops-manual-snapshot.dump")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	backups, err := listManagedBackups(dir)
	if err != nil {
		t.Fatalf("listManagedBackups: %v", err)
	}
	if len(backups) != 1 || backups[0].name != filepath.Base(backupPath) {
		t.Fatalf("backups = %#v", backups)
	}
}

func readinessByKey(t *testing.T, report map[string]any, key string) readinessRow {
	t.Helper()
	items, ok := report["items"].([]readinessRow)
	if !ok {
		t.Fatalf("report items type = %T", report["items"])
	}
	for _, item := range items {
		if item.Key == key {
			return item
		}
	}
	t.Fatalf("readiness item %q not found", key)
	return readinessRow{}
}

func writeSHA256SUMS(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	var lines []string
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write artifact %s: %v", name, err)
		}
		lines = append(lines, fmt.Sprintf("%x  %s", sha256.Sum256([]byte(content)), name))
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write SHA256SUMS: %v", err)
	}
}

func writeValidRehearsalReport(t *testing.T, dir, targetDatabase string) string {
	t.Helper()
	path := filepath.Join(dir, "restore-rehearsal.json")
	if err := writeJSONReport(path, map[string]any{
		"backup":               "/backups/assops-20260622-120000.dump",
		"target_database":      targetDatabase,
		"backup_object_counts": map[string]int{"TABLE": 2},
		"migrations":           []map[string]string{{"filename": "001_init.sql"}},
		"rehearsed_at":         time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("write rehearsal report: %v", err)
	}
	return path
}

func expectReleaseBundleError(artifactDir, reportPath string) error {
	_, err := validateReleaseBundle(artifactDir, reportPath)
	if err == nil {
		return fmt.Errorf("expected validateReleaseBundle to fail")
	}
	return err
}
