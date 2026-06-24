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
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:10", "relation_type": "synced_argo_connection"},
		},
	})
	if got := readinessByKey(t, ready, "argo"); got.Status != "ready" || got.Evidence != "1 targets / 1 Argo connections / 1 apps / 1 sync ops / 1 complete app links" {
		t.Fatalf("argo status with complete app graph = %#v, want ready", got)
	}

	withoutSyncedConnection := firstVersionReadinessReportWithGraph([]map[string]any{
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
	if got := readinessByKey(t, withoutSyncedConnection, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 1 Argo connections / 1 apps / 1 sync ops / 0 complete app links" {
		t.Fatalf("argo status without synced connection edge = %#v, want partial", got)
	}

	withUnrelatedSyncedConnection := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target"},
		{"asset_type": "argo_connection"},
		{"asset_type": "argo_app"},
	}, []map[string]any{
		{"operation_type": "argo.apps.sync"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:11", "relation_type": "synced_argo_connection"},
		},
	})
	if got := readinessByKey(t, withUnrelatedSyncedConnection, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 1 Argo connections / 1 apps / 1 sync ops / 0 complete app links" {
		t.Fatalf("argo status with unrelated synced connection edge = %#v, want partial", got)
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
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:10", "relation_type": "synced_argo_connection"},
		},
	})
	if got := readinessByKey(t, crossAppAggregation, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 1 Argo connections / 2 apps / 1 sync ops / 0 complete app links" {
		t.Fatalf("argo status with cross-app aggregate links = %#v, want partial without a complete app link", got)
	}
}

func TestFirstVersionReadinessReportRequiresSSHCommandGraphLinks(t *testing.T) {
	withoutGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host"},
		{"asset_type": "ssh_command_run", "source_id": "10"},
	}, []map[string]any{
		{"operation_type": "ssh.verify"},
		{"operation_type": "ssh.exec"},
	}, nil, map[string]any{"edges": []any{}})
	if got := readinessByKey(t, withoutGraphLinks, "ssh"); got.Status != "partial" || got.Evidence != "1 hosts / 1 verify ops / 1 command ops / 1 command assets / 0 complete audit chains / 0 command asset chains" {
		t.Fatalf("ssh readiness without graph links = %#v, want partial with graph evidence", got)
	}

	withoutVerify := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host"},
		{"asset_type": "ssh_command_run", "source_id": "20"},
	}, []map[string]any{
		{"operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, withoutVerify, "ssh"); got.Status != "partial" || got.Evidence != "1 hosts / 0 verify ops / 1 command ops / 1 command assets / 1 complete audit chains / 1 command asset chains" {
		t.Fatalf("ssh readiness without verify op = %#v, want partial with verify gap", got)
	}

	singleCompleteChain := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host"},
		{"asset_type": "ssh_command_run", "source_id": "20"},
	}, []map[string]any{
		{"operation_type": "ssh.verify"},
		{"operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, singleCompleteChain, "ssh"); got.Status != "partial" || got.Evidence != "1 hosts / 1 verify ops / 1 command ops / 1 command assets / 1 complete audit chains / 1 command asset chains" {
		t.Fatalf("ssh readiness with only one complete command graph = %#v, want partial until verify and command audits are both represented", got)
	}

	withoutMatchingCommandAssets := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host"},
		{"asset_type": "ssh_command_run", "source_id": "90"},
		{"asset_type": "ssh_command_run", "source_id": "91"},
	}, []map[string]any{
		{"operation_type": "ssh.verify"},
		{"operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "operation_run:11", "to_asset_id": "ssh_command_run:21", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:21", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, withoutMatchingCommandAssets, "ssh"); got.Status != "partial" || got.Evidence != "1 hosts / 1 verify ops / 1 command ops / 2 command assets / 2 complete audit chains / 0 command asset chains" {
		t.Fatalf("ssh readiness with unmatched command assets = %#v, want partial without canonical command asset chains", got)
	}

	ready := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host"},
		{"asset_type": "ssh_command_run", "source_id": "20"},
		{"asset_type": "ssh_command_run", "source_id": "21"},
	}, []map[string]any{
		{"operation_type": "ssh.verify"},
		{"operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "operation_run:11", "to_asset_id": "ssh_command_run:21", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:21", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, ready, "ssh"); got.Status != "ready" || got.Evidence != "1 hosts / 1 verify ops / 1 command ops / 2 command assets / 2 complete audit chains / 2 command asset chains" {
		t.Fatalf("ssh readiness with complete verify and command graphs = %#v, want ready", got)
	}

	crossCommandAggregation := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host"},
		{"asset_type": "ssh_command_run", "source_id": "20"},
		{"asset_type": "ssh_command_run", "source_id": "21"},
	}, []map[string]any{
		{"operation_type": "ssh.verify"},
		{"operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:21", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, crossCommandAggregation, "ssh"); got.Status != "partial" || got.Evidence != "1 hosts / 1 verify ops / 1 command ops / 2 command assets / 0 complete audit chains / 0 command asset chains" {
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
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "repo_sync:30", "relation_type": "ran_repo_sync"},
		},
	})
	if got := readinessByKey(t, withGraphChain, "sync_trigger"); got.Status != "ready" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 1 complete webhook chains" {
		t.Fatalf("sync trigger with webhook graph chain = %#v, want ready with complete graph evidence", got)
	}

	withoutOperationRepoSyncClosure := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_event", "metadata": map[string]any{"provider": "gitea"}},
	}, []map[string]any{
		{"operation_type": "repo.sync_remote"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "webhook_connection:1", "to_asset_id": "webhook_event:20", "relation_type": "received_webhook_event"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "repo_sync:30", "relation_type": "matched_repo_sync"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "operation_run:40", "relation_type": "triggered_operation"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "repo_sync:31", "relation_type": "ran_repo_sync"},
		},
	})
	if got := readinessByKey(t, withoutOperationRepoSyncClosure, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 0 complete webhook chains" {
		t.Fatalf("sync trigger without operation-to-matched-sync closure = %#v, want partial", got)
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

	sameEventClosureWithoutConnection := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_event", "metadata": map[string]any{"provider": "gitea"}},
	}, []map[string]any{
		{"operation_type": "repo.sync_remote"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "webhook_connection:1", "to_asset_id": "webhook_event:20", "relation_type": "received_webhook_event"},
			map[string]any{"from_asset_id": "webhook_event:21", "to_asset_id": "repo_sync:30", "relation_type": "matched_repo_sync"},
			map[string]any{"from_asset_id": "webhook_event:21", "to_asset_id": "operation_run:40", "relation_type": "triggered_operation"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "repo_sync:30", "relation_type": "ran_repo_sync"},
		},
	})
	if got := readinessByKey(t, sameEventClosureWithoutConnection, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 0 complete webhook chains" {
		t.Fatalf("sync trigger with closed event but missing connection = %#v, want partial", got)
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

func TestFirstVersionReadinessReportRequiresProjectGraphNode(t *testing.T) {
	withoutEvidence := firstVersionReadinessReportWithGraph(nil, nil, nil, nil)
	if got := readinessByKey(t, withoutEvidence, "project"); got.Status != "missing" || got.Evidence != "0 project assets / 0 project graph nodes" {
		t.Fatalf("project readiness without evidence = %#v, want missing", got)
	} else {
		plan := mapFromAny(got.DemoDataRehearsalPlan)
		if plan["plan_state"] != "blocked" ||
			plan["execution_enabled"] != false ||
			plan["demo_seed_written"] != false ||
			!containsString(stringSliceFromAny(plan["blocked_reasons"]), "live_demo_graph_evidence_incomplete") {
			t.Fatalf("project demo rehearsal plan without evidence = %#v", plan)
		}
		assertDemoDataRehearsalPlanSafe(t, plan)
	}

	withNilGraph := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "project"},
	}, nil, nil, nil)
	if got := readinessByKey(t, withNilGraph, "project"); got.Status != "partial" || got.Evidence != "1 project assets / 0 project graph nodes" {
		t.Fatalf("project readiness with nil graph = %#v, want partial", got)
	} else {
		plan := mapFromAny(got.DemoDataRehearsalPlan)
		counts := intMapFromAny(plan["evidence_counts"])
		if plan["plan_state"] != "planned" ||
			counts["project_assets"] != 1 ||
			counts["project_graph_nodes"] != 0 ||
			!containsString(stringSliceFromAny(plan["blocked_reasons"]), "live_demo_graph_evidence_incomplete") {
			t.Fatalf("project demo rehearsal plan with nil graph = %#v", plan)
		}
		assertDemoDataRehearsalPlanSafe(t, plan)
	}

	withoutGraphNode := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "project"},
	}, nil, nil, map[string]any{"nodes": []any{}})
	if got := readinessByKey(t, withoutGraphNode, "project"); got.Status != "partial" || got.Evidence != "1 project assets / 0 project graph nodes" {
		t.Fatalf("project readiness without graph node = %#v, want partial", got)
	} else {
		plan := mapFromAny(got.DemoDataRehearsalPlan)
		if plan["plan_state"] != "planned" ||
			!containsString(stringSliceFromAny(plan["blocked_reasons"]), "live_demo_graph_evidence_incomplete") {
			t.Fatalf("project demo rehearsal plan without graph node = %#v", plan)
		}
		assertDemoDataRehearsalPlanSafe(t, plan)
	}

	withGraphNode := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "project"},
	}, nil, nil, map[string]any{
		"nodes": []any{
			map[string]any{"id": "project:1"},
			map[string]any{"id": "repository:10"},
		},
	})
	if got := readinessByKey(t, withGraphNode, "project"); got.Status != "ready" || got.Evidence != "1 project assets / 1 project graph nodes" {
		t.Fatalf("project readiness with graph node = %#v, want ready", got)
	} else {
		plan := mapFromAny(got.DemoDataRehearsalPlan)
		counts := intMapFromAny(plan["evidence_counts"])
		if plan["plan_state"] != "observed" ||
			plan["project_created"] != false ||
			plan["asset_graph_written"] != false ||
			counts["project_assets"] != 1 ||
			counts["project_graph_nodes"] != 1 ||
			len(stringSliceFromAny(plan["blocked_reasons"])) != 0 {
			t.Fatalf("project demo rehearsal plan with graph node = %#v", plan)
		}
		assertDemoDataRehearsalPlanSafe(t, plan)
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
	if got := readinessByKey(t, withoutGraphLinks, "repositories"); got.Status != "partial" || got.Evidence != "1 repos / 2 remotes / 0 complete repos / 0 project links / 0 remote links" {
		t.Fatalf("repository readiness without graph links = %#v, want partial with graph evidence", got)
	} else {
		plan := mapFromAny(got.DemoDataRehearsalPlan)
		counts := intMapFromAny(plan["evidence_counts"])
		if plan["plan_state"] != "planned" ||
			plan["repository_created"] != false ||
			plan["git_remote_created"] != false ||
			plan["contains_remote_url"] != false ||
			counts["repository_assets"] != 1 ||
			counts["git_remote_assets"] != 2 ||
			counts["complete_repository_paths"] != 0 {
			t.Fatalf("repository demo rehearsal plan without graph links = %#v", plan)
		}
		proof := mapFromAny(plan["environment_demo_proof"])
		if proof["complete_repository_multi_remote_path_observed"] != false ||
			!containsString(stringSliceFromAny(proof["missing_evidence"]), "repository_to_two_remotes_graph_path") {
			t.Fatalf("repository demo proof without graph links = %#v", proof)
		}
		assertDemoDataRehearsalPlanSafe(t, plan)
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
	if got := readinessByKey(t, withGraphLinks, "repositories"); got.Status != "ready" || got.Evidence != "1 repos / 2 remotes / 1 complete repos / 1 project links / 2 remote links" {
		t.Fatalf("repository readiness with graph links = %#v, want ready", got)
	} else {
		plan := mapFromAny(got.DemoDataRehearsalPlan)
		counts := intMapFromAny(plan["evidence_counts"])
		if plan["plan_state"] != "observed" ||
			counts["complete_repository_paths"] != 1 ||
			counts["project_repository_links"] != 1 ||
			counts["repository_remote_links"] != 2 ||
			len(stringSliceFromAny(plan["blocked_reasons"])) != 0 {
			t.Fatalf("repository demo rehearsal plan with graph links = %#v", plan)
		}
		proof := mapFromAny(plan["environment_demo_proof"])
		if proof["proof_state"] != "observed" ||
			proof["proof_ready"] != true ||
			proof["complete_repository_multi_remote_path_observed"] != true ||
			len(stringSliceFromAny(proof["missing_evidence"])) != 0 {
			t.Fatalf("repository demo proof with graph links = %#v", proof)
		}
		assertDemoDataRehearsalPlanSafe(t, plan)
	}

	crossRepositoryAggregation := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository"},
		{"asset_type": "repository"},
		{"asset_type": "git_remote"},
		{"asset_type": "git_remote"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:11", "to_asset_id": "git_remote:100", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "repository:11", "to_asset_id": "git_remote:101", "relation_type": "has_remote"},
		},
	})
	if got := readinessByKey(t, crossRepositoryAggregation, "repositories"); got.Status != "partial" || got.Evidence != "2 repos / 2 remotes / 0 complete repos / 1 project links / 2 remote links" {
		t.Fatalf("repository readiness with cross-repository aggregate links = %#v, want partial without a complete repository", got)
	} else {
		proof := mapFromAny(mapFromAny(got.DemoDataRehearsalPlan)["environment_demo_proof"])
		if proof["complete_repository_multi_remote_path_observed"] != false ||
			!containsString(stringSliceFromAny(proof["missing_evidence"]), "repository_to_two_remotes_graph_path") {
			t.Fatalf("cross-repository aggregate proof should not report a complete multi-remote path: %#v", proof)
		}
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
	if got := readinessByKey(t, unrelatedGraphLinks, "repositories"); got.Status != "partial" || got.Evidence != "1 repos / 2 remotes / 0 complete repos / 0 project links / 0 remote links" {
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
	if got.ProjectRepository != 1 || got.RepositoryRemotes != 2 || got.CompleteRepos != 1 {
		t.Fatalf("countRepositoryGraphLinks = %#v, want 1 project link, 2 remote links, and 1 complete repo", got)
	}
}

func TestDemoDataEnvironmentProofPartialWhenReadyStatusLacksRequiredEvidence(t *testing.T) {
	proof := demoDataEnvironmentProof("ready", map[string]int{
		"repository_assets":        1,
		"git_remote_assets":        2,
		"project_repository_links": 1,
	}, []string{"repository_asset", "two_git_remote_assets", "project_to_repository_graph_link", "repository_to_two_remotes_graph_path"})
	if proof["proof_state"] != "partial" ||
		proof["proof_ready"] != false ||
		proof["live_environment_data_observed"] != false ||
		proof["complete_repository_multi_remote_path_observed"] != false ||
		!containsString(stringSliceFromAny(proof["missing_evidence"]), "repository_to_two_remotes_graph_path") {
		t.Fatalf("ready status without required graph evidence should stay partial: %#v", proof)
	}
	if proof["external_call_made"] != false ||
		proof["demo_seed_written"] != false ||
		proof["project_created"] != false ||
		proof["repository_created"] != false ||
		proof["git_remote_created"] != false ||
		proof["asset_graph_written"] != false ||
		proof["contains_remote_url"] != false ||
		proof["contains_credentials"] != false {
		t.Fatalf("direct demo environment proof should stay no-call and redacted: %#v", proof)
	}
}

func TestDemoDataEnvironmentProofBlockedStatusDoesNotReportObservedEvidence(t *testing.T) {
	proof := demoDataEnvironmentProof("missing", map[string]int{
		"repository_assets":         1,
		"git_remote_assets":         2,
		"project_repository_links":  1,
		"repository_remote_links":   2,
		"complete_repository_paths": 1,
	}, []string{"repository_asset", "two_git_remote_assets", "project_to_repository_graph_link", "repository_to_two_remotes_graph_path"})
	if proof["proof_state"] != "blocked" ||
		proof["proof_ready"] != false ||
		proof["live_environment_data_observed"] != false ||
		proof["complete_repository_multi_remote_path_observed"] != false {
		t.Fatalf("missing status should suppress observed proof signals even with complete counts: %#v", proof)
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

	sameRemoteSourceAndTarget := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repo_sync"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "mirrors_to"},
		},
	})
	if got := readinessByKey(t, sameRemoteSourceAndTarget, "repo_sync"); got.Status != "partial" || got.Evidence != "1 repo syncs / 0 complete syncs / 1 repository links / 1 source links / 1 target links" {
		t.Fatalf("repo sync readiness with same source and target remote = %#v, want partial without distinct mirror evidence", got)
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

func TestCountRepoSyncGraphLinksRequiresDistinctSourceAndTarget(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "mirrors_to"},
		},
	}
	got := countRepoSyncGraphLinks(graph)
	if got.RepositorySync != 1 || got.SourceRemotes != 1 || got.TargetRemotes != 1 || got.CompleteSyncs != 0 {
		t.Fatalf("countRepoSyncGraphLinks with same source and target = %#v, want no complete sync", got)
	}
}

func TestCountRepoSyncGraphLinksAllowsMixedDistinctMirror(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:101", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "mirrors_to"},
		},
	}
	got := countRepoSyncGraphLinks(graph)
	if got.RepositorySync != 1 || got.SourceRemotes != 2 || got.TargetRemotes != 1 || got.CompleteSyncs != 1 {
		t.Fatalf("countRepoSyncGraphLinks with mixed distinct mirror = %#v, want one complete sync", got)
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
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "repo_sync:30", "relation_type": "ran_repo_sync"},
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

func TestCountWebhookSyncGraphLinksRequiresEventOperation(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "webhook_connection:1", "to_asset_id": "webhook_event:20", "relation_type": "received_webhook_event"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "repo_sync:30", "relation_type": "matched_repo_sync"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "repo_sync:30", "relation_type": "ran_repo_sync"},
		},
	}
	got := countWebhookSyncGraphLinks(graph)
	if got.ConnectionEvents != 1 || got.EventRepoSyncs != 1 || got.EventOperations != 0 || got.CompleteChains != 0 {
		t.Fatalf("countWebhookSyncGraphLinks without event operation = %#v, want no complete chain", got)
	}
}

func TestFirstVersionReadinessReportRequiresGitHubActionGraphLink(t *testing.T) {
	withoutLink := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, nil, nil, map[string]any{
		"edges": []any{},
	})
	if got := readinessByKey(t, withoutLink, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 0 complete action chains / 0 tag ops / 0 complete tag links / 0 linked tag runs / 0 project links / 0 remote links / 0 action links / 0 tag links / 0 tag-action links" {
		t.Fatalf("github actions without graph link = %#v, want partial with link evidence", got)
	}

	withActionLinkOnly := firstVersionReadinessReportWithGraph([]map[string]any{
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
	if got := readinessByKey(t, withActionLinkOnly, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 0 complete action chains / 0 tag ops / 0 complete tag links / 0 linked tag runs / 0 project links / 0 remote links / 1 action links / 0 tag links / 0 tag-action links" {
		t.Fatalf("github actions with action link only = %#v, want partial without project chain", got)
	}

	withCompleteChain := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:42", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:42", "to_asset_id": "github_action_run:101", "relation_type": "triggered_by"},
		},
	})
	if got := readinessByKey(t, withCompleteChain, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 1 complete action chains / 0 tag ops / 0 complete tag links / 0 linked tag runs / 1 project links / 1 remote links / 1 action links / 0 tag links / 0 tag-action links" {
		t.Fatalf("github actions with complete project chain but no tag = %#v, want partial", got)
	}

	withFailedTag := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, []map[string]any{
		{"operation_type": "repo.create_tag"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:42", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:42", "to_asset_id": "github_action_run:101", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:201", "to_asset_id": "git_remote:42", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "failed", "tag_name": "v1.0.0"}},
		},
	})
	if got := readinessByKey(t, withFailedTag, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 1 complete action chains / 1 tag ops / 0 complete tag links / 0 linked tag runs / 1 project links / 1 remote links / 1 action links / 0 tag links / 0 tag-action links" {
		t.Fatalf("github actions with failed tag = %#v, want partial without successful tag link", got)
	}

	withCompleteChainAndTag := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, []map[string]any{
		{"operation_type": "repo.create_tag"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:42", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:42", "to_asset_id": "github_action_run:101", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:201", "to_asset_id": "git_remote:42", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed", "tag_name": "v1.0.0"}},
		},
	})
	if got := readinessByKey(t, withCompleteChainAndTag, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 1 complete action chains / 1 tag ops / 1 complete tag links / 0 linked tag runs / 1 project links / 1 remote links / 1 action links / 1 tag links / 0 tag-action links" {
		t.Fatalf("github actions with complete project chain and tag but no action match = %#v, want partial", got)
	}

	withOrphanActionMatch := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, []map[string]any{
		{"operation_type": "repo.create_tag"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:42", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:42", "to_asset_id": "github_action_run:101", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:201", "to_asset_id": "git_remote:42", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed", "tag_name": "v1.0.0"}},
			map[string]any{"from_asset_id": "repo_tag_run:201", "to_asset_id": "github_action_run:202", "relation_type": "matched_action_run"},
		},
	})
	if got := readinessByKey(t, withOrphanActionMatch, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 1 complete action chains / 1 tag ops / 1 complete tag links / 0 linked tag runs / 1 project links / 1 remote links / 1 action links / 1 tag links / 1 tag-action links" {
		t.Fatalf("github actions with tag matched to orphan action = %#v, want partial without project-linked tag run", got)
	}

	withCompleteChainTagAndActionMatch := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, []map[string]any{
		{"operation_type": "repo.create_tag"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:42", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:42", "to_asset_id": "github_action_run:101", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:201", "to_asset_id": "git_remote:42", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed", "tag_name": "v1.0.0"}},
			map[string]any{"from_asset_id": "repo_tag_run:201", "to_asset_id": "github_action_run:101", "relation_type": "matched_action_run"},
		},
	})
	if got := readinessByKey(t, withCompleteChainTagAndActionMatch, "github_actions"); got.Status != "ready" || got.Evidence != "1 pipeline runs / 1 complete action chains / 1 tag ops / 1 complete tag links / 1 linked tag runs / 1 project links / 1 remote links / 1 action links / 1 tag links / 1 tag-action links" {
		t.Fatalf("github actions with complete project chain, tag, and action match = %#v, want ready", got)
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
	if got := readinessByKey(t, wrongLink, "github_actions"); got.Status != "missing" || got.Evidence != "0 pipeline runs / 0 complete action chains / 0 tag ops / 0 complete tag links / 0 linked tag runs / 0 project links / 0 remote links / 0 action links / 0 tag links / 0 tag-action links" {
		t.Fatalf("github actions with unrelated graph edge = %#v, want missing", got)
	}
}

func TestCountGitHubActionGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:1", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:1", "to_asset_id": "git_remote:1", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:1", "to_asset_id": "github_action_run:1", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "repository:2", "to_asset_id": "git_remote:2", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:2", "to_asset_id": "github_action_run:2", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "git_remote:2", "to_asset_id": "github_action_run:2", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:1", "to_asset_id": "github_action_run:3", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:1", "to_asset_id": "git_remote:1", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed"}},
			map[string]any{"from_asset_id": "operation_run:2", "to_asset_id": "git_remote:2", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed"}},
			map[string]any{"from_asset_id": "operation_run:3", "to_asset_id": "git_remote:1", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "failed"}},
			map[string]any{"from_asset_id": "repo_tag_run:1", "to_asset_id": "github_action_run:1", "relation_type": "matched_action_run"},
			map[string]any{"from_asset_id": "repo_tag_run:1", "to_asset_id": "github_action_run:2", "relation_type": "matched_action_run"},
			map[string]any{"from_asset_id": "repo_tag_run:2", "to_asset_id": "repository:1", "relation_type": "matched_action_run"},
		},
	}
	got := countGitHubActionGraphLinks(graph)
	if got.ProjectRepositories != 1 || got.RepositoryRemotes != 2 || got.RemoteActionRuns != 2 || got.TaggedRemotes != 2 || got.TagActionRunLinks != 2 || got.CompleteActionRuns != 1 || got.CompleteTaggedRemotes != 1 || got.LinkedTagRuns != 1 {
		t.Fatalf("countGitHubActionGraphLinks = %#v, want project/remote/action/tag counts and one complete action/tag chain", got)
	}
}

func TestCountGitHubActionGraphLinksIgnoresInvalidTagActionTarget(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repo_tag_run:1", "to_asset_id": "repository:1", "relation_type": "matched_action_run"},
			map[string]any{"from_asset_id": "operation_run:1", "to_asset_id": "github_action_run:1", "relation_type": "matched_action_run"},
		},
	}
	got := countGitHubActionGraphLinks(graph)
	if got.TagActionRunLinks != 0 || got.LinkedTagRuns != 0 {
		t.Fatalf("countGitHubActionGraphLinks with invalid tag-action targets = %#v, want no tag-action evidence", got)
	}
}

func TestCountGitHubActionGraphLinksRequiresProjectLinkedTagActionRun(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:1", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:1", "to_asset_id": "git_remote:1", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:1", "to_asset_id": "github_action_run:1", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:1", "to_asset_id": "git_remote:1", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed"}},
			map[string]any{"from_asset_id": "repo_tag_run:1", "to_asset_id": "github_action_run:2", "relation_type": "matched_action_run"},
		},
	}
	got := countGitHubActionGraphLinks(graph)
	if got.CompleteActionRuns != 1 || got.CompleteTaggedRemotes != 1 || got.TagActionRunLinks != 1 || got.LinkedTagRuns != 0 {
		t.Fatalf("countGitHubActionGraphLinks with orphan matched action = %#v, want action link but no linked tag run", got)
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
	got := countSSHGraphLinks(graph, map[string]bool{"ssh_command_run:20": true})
	if got.OperationCommands != 1 || got.CommandMachines != 2 || got.CompleteCommands != 1 || got.CompleteCommandAssets != 1 {
		t.Fatalf("countSSHGraphLinks = %#v, want one operation-command, two command-machine, one complete command, and one command asset chain", got)
	}
}

func TestCountArgoGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:10", "relation_type": "synced_argo_connection"},
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

func TestCountArgoGraphLinksRequiresSyncedConnection(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:11", "relation_type": "synced_argo_connection"},
		},
	}
	got := countArgoGraphLinks(graph)
	if got.ConnectionApps != 1 || got.AppTargets != 1 || got.CompleteApps != 0 {
		t.Fatalf("countArgoGraphLinks with unrelated synced connection = %#v, want no complete app", got)
	}
}

func TestCountArgoGraphLinksAllowsOneSyncedConnectionForSharedApp(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_connection:11", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:10", "relation_type": "synced_argo_connection"},
		},
	}
	got := countArgoGraphLinks(graph)
	if got.ConnectionApps != 2 || got.AppTargets != 1 || got.CompleteApps != 1 {
		t.Fatalf("countArgoGraphLinks with shared app and one synced connection = %#v, want one complete app", got)
	}
}

func TestCountApprovalGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_approval_rule:10", "to_asset_id": "operation_approval:20", "relation_type": "governs"},
			map[string]any{"from_asset_id": "operation_approval:20", "to_asset_id": "operation_run:30", "relation_type": "gates_operation"},
			map[string]any{"from_asset_id": "operation_approval_rule:11", "to_asset_id": "operation_approval:21", "relation_type": "governs"},
			map[string]any{"from_asset_id": "operation_approval:21", "to_asset_id": "operation_approval_rule:12", "relation_type": "gates_operation"},
		},
	}
	got := countApprovalGraphLinks(graph, map[string]bool{"operation_approval_rule:10": true})
	if got.RuleApprovals != 1 || got.ApprovalOperations != 1 {
		t.Fatalf("countApprovalGraphLinks = %#v, want one rule-approval and one approval-operation link", got)
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
	if got := readinessByKey(t, withSummary, "approval"); got.Status != "partial" || got.Evidence != "1 approvals / 0 approval assets / 0 pending ops / 0 active rules / 0 governed approvals" {
		t.Fatalf("approval status from summary without rule = %#v, want partial with rule evidence", got)
	}

	withRule := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_approval_rule", "source_id": "10", "status": "active"},
	}, nil, map[string]any{"total": float64(1)})
	if got := readinessByKey(t, withRule, "approval"); got.Status != "partial" || got.Evidence != "1 approvals / 0 approval assets / 0 pending ops / 1 active rules / 0 governed approvals" {
		t.Fatalf("approval status from summary and rule without graph = %#v, want partial", got)
	}

	withGovernedApproval := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "operation_approval", "source_id": "20", "status": "pending"},
		{"asset_type": "operation_approval_rule", "source_id": "10", "status": "active"},
	}, nil, map[string]any{"total": float64(1)}, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_approval_rule:10", "to_asset_id": "operation_approval:20", "relation_type": "governs"},
		},
	})
	if got := readinessByKey(t, withGovernedApproval, "approval"); got.Status != "ready" || got.Evidence != "1 approvals / 1 approval assets / 0 pending ops / 1 active rules / 1 governed approvals" {
		t.Fatalf("approval status from governed approval asset and active rule = %#v, want ready", got)
	}

	withDisabledRuleGovernedApproval := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "operation_approval", "source_id": "20", "status": "pending"},
		{"asset_type": "operation_approval_rule", "source_id": "10", "status": "active"},
		{"asset_type": "operation_approval_rule", "source_id": "11", "status": "disabled"},
	}, nil, map[string]any{"total": float64(1)}, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_approval_rule:11", "to_asset_id": "operation_approval:20", "relation_type": "governs"},
		},
	})
	if got := readinessByKey(t, withDisabledRuleGovernedApproval, "approval"); got.Status != "partial" || got.Evidence != "1 approvals / 1 approval assets / 0 pending ops / 1 active rules / 0 governed approvals" {
		t.Fatalf("approval status from disabled governed rule and separate active rule = %#v, want partial", got)
	}

	ruleOnly := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_approval_rule", "source_id": "10", "status": "active"},
	}, nil, nil)
	if got := readinessByKey(t, ruleOnly, "approval"); got.Status != "partial" || got.Evidence != "0 approvals / 0 approval assets / 0 pending ops / 1 active rules / 0 governed approvals" {
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

func TestFirstVersionReadinessReportTreatsPendingApprovalOperationAsPartialEvidence(t *testing.T) {
	report := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_approval_rule", "source_id": "10", "status": "active"},
	}, []map[string]any{
		{"operation_type": "ssh.exec", "status": "pending_approval"},
	}, nil)
	if got := readinessByKey(t, report, "approval"); got.Status != "partial" || got.Evidence != "0 approvals / 0 approval assets / 1 pending ops / 1 active rules / 0 governed approvals" {
		t.Fatalf("approval status from pending operation and rule without graph = %#v, want partial", got)
	}

	withoutRule := firstVersionReadinessReport(nil, []map[string]any{
		{"operation_type": "ssh.exec", "status": "pending_approval"},
	}, nil)
	if got := readinessByKey(t, withoutRule, "approval"); got.Status != "partial" || got.Evidence != "0 approvals / 0 approval assets / 1 pending ops / 0 active rules / 0 governed approvals" {
		t.Fatalf("approval status from pending operation without rule = %#v, want partial", got)
	}
}

func TestFirstVersionReadinessReportIgnoresDisabledApprovalRules(t *testing.T) {
	report := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_approval_rule", "status": "disabled"},
	}, nil, map[string]any{"total": float64(1)})
	if got := readinessByKey(t, report, "approval"); got.Status != "partial" || got.Evidence != "1 approvals / 0 approval assets / 0 pending ops / 0 active rules / 0 governed approvals" {
		t.Fatalf("approval status with disabled rule = %#v, want partial without active rule evidence", got)
	}
}

func TestFirstVersionReadinessReportRequiresOperationLogs(t *testing.T) {
	allZero := firstVersionReadinessReport(nil, nil, nil)
	if got := readinessByKey(t, allZero, "operations"); got.Status != "missing" || got.Evidence != "0 operation assets / 0 listed runs / 0 with logs" {
		t.Fatalf("operations readiness without evidence = %#v, want missing", got)
	}

	withoutLogs := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_run", "source_id": "op-1"},
	}, []map[string]any{
		{"id": "op-1", "operation_type": "repo.sync", "log_count": 0},
	}, nil)
	if got := readinessByKey(t, withoutLogs, "operations"); got.Status != "partial" || got.Evidence != "1 operation assets / 1 listed runs / 0 with logs" {
		t.Fatalf("operations readiness without logs = %#v, want partial with log evidence", got)
	}

	withoutAsset := firstVersionReadinessReport(nil, []map[string]any{
		{"id": "op-1", "operation_type": "repo.sync", "log_count": 2},
	}, nil)
	if got := readinessByKey(t, withoutAsset, "operations"); got.Status != "partial" || got.Evidence != "0 operation assets / 1 listed runs / 0 with logs" {
		t.Fatalf("operations readiness without operation asset = %#v, want partial with asset evidence", got)
	}

	withMismatchedAssetAndLogs := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_run", "source_id": "op-1"},
	}, []map[string]any{
		{"id": "op-2", "operation_type": "repo.sync", "log_count": 2},
	}, nil)
	if got := readinessByKey(t, withMismatchedAssetAndLogs, "operations"); got.Status != "partial" || got.Evidence != "1 operation assets / 1 listed runs / 0 with logs" {
		t.Fatalf("operations readiness with mismatched asset and logged run = %#v, want partial without canonical log evidence", got)
	}

	withAssetAndLogs := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_run", "source_id": "op-1"},
	}, []map[string]any{
		{"id": "op-1", "operation_type": "repo.sync", "log_count": 2},
	}, nil)
	if got := readinessByKey(t, withAssetAndLogs, "operations"); got.Status != "ready" || got.Evidence != "1 operation assets / 1 listed runs / 1 with logs" {
		t.Fatalf("operations readiness with operation asset and logs = %#v, want ready", got)
	}
}

func TestFirstVersionReadinessReportRequiresContextGraphEvidence(t *testing.T) {
	missing := firstVersionReadinessReportWithGraph(nil, nil, nil, nil)
	if got := readinessByKey(t, missing, "context"); got.Status != "missing" || got.Evidence != "0 context assets / 0 context generations / 0 complete context tasks / 0 runtime links / 0 context tool links / 0 graph nodes / 0 graph edges" {
		t.Fatalf("context readiness without context or graph evidence = %#v, want missing", got)
	}

	withoutGraph := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
		{"asset_type": "agent_task"},
	}, nil, nil, nil)
	if got := readinessByKey(t, withoutGraph, "context"); got.Status != "partial" || got.Evidence != "2 context assets / 0 context generations / 0 complete context tasks / 0 runtime links / 0 context tool links / 0 graph nodes / 0 graph edges" {
		t.Fatalf("context readiness without graph evidence = %#v, want partial with graph evidence", got)
	}

	graphOnly := firstVersionReadinessReportWithGraph(nil, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
	})
	if got := readinessByKey(t, graphOnly, "context"); got.Status != "partial" || got.Evidence != "0 context assets / 0 context generations / 0 complete context tasks / 0 runtime links / 0 context tool links / 1 graph nodes / 0 graph edges" {
		t.Fatalf("context readiness without context assets = %#v, want partial with graph evidence", got)
	}

	withoutGeneration := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
	}, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
		"edges": []any{map[string]any{"from_asset_id": "project:1", "to_asset_id": "repo:1"}},
	})
	if got := readinessByKey(t, withoutGeneration, "context"); got.Status != "partial" || got.Evidence != "1 context assets / 0 context generations / 0 complete context tasks / 0 runtime links / 0 context tool links / 1 graph nodes / 1 graph edges" {
		t.Fatalf("context readiness without generation evidence = %#v, want partial", got)
	}

	withoutContextGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
		{"asset_type": "agent_task"},
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:20", "status": "completed", "metadata": map[string]any{"tool_name": "context.generate"}},
	}, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
		"edges": []any{map[string]any{"from_asset_id": "project:1", "to_asset_id": "repo:1"}},
	})
	if got := readinessByKey(t, withoutContextGraphLinks, "context"); got.Status != "partial" || got.Evidence != "2 context assets / 1 context generations / 0 complete context tasks / 0 runtime links / 0 context tool links / 1 graph nodes / 1 graph edges" {
		t.Fatalf("context readiness without task graph links = %#v, want partial", got)
	}

	crossTaskAggregation := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
		{"asset_type": "agent_task"},
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:20", "status": "completed", "metadata": map[string]any{"tool_name": "context.generate"}},
	}, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
		"edges": []any{
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "ai_runtime:30", "relation_type": "uses_runtime"},
			map[string]any{"from_asset_id": "agent_task:11", "to_asset_id": "agent_tool_call:20", "relation_type": "records_tool_call"},
		},
	})
	if got := readinessByKey(t, crossTaskAggregation, "context"); got.Status != "partial" || got.Evidence != "2 context assets / 1 context generations / 0 complete context tasks / 1 runtime links / 1 context tool links / 1 graph nodes / 2 graph edges" {
		t.Fatalf("context readiness with cross-task links = %#v, want partial without complete context task", got)
	}

	withGeneration := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
		{"asset_type": "agent_task"},
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:20", "status": "completed", "metadata": map[string]any{"tool_name": "context.generate"}},
	}, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
		"edges": []any{
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "ai_runtime:30", "relation_type": "uses_runtime"},
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "agent_tool_call:20", "relation_type": "records_tool_call"},
		},
	})
	if got := readinessByKey(t, withGeneration, "context"); got.Status != "ready" || got.Evidence != "2 context assets / 1 context generations / 1 complete context tasks / 1 runtime links / 1 context tool links / 1 graph nodes / 2 graph edges" {
		t.Fatalf("context readiness with complete task graph = %#v, want ready", got)
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

func TestCountContextGraphLinks(t *testing.T) {
	assets := []map[string]any{
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:20", "status": "completed", "metadata": map[string]any{"tool_name": "context.generate"}},
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:21", "status": "failed", "metadata": map[string]any{"tool_name": "context.generate"}},
		{"asset_type": "agent_tool_call", "source_id": "22", "status": "queued", "metadata": map[string]any{"tool_name": "context.generate"}},
	}
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "ai_runtime:30", "relation_type": "uses_runtime"},
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "agent_tool_call:20", "relation_type": "records_tool_call"},
			map[string]any{"from_asset_id": "agent_task:11", "to_asset_id": "agent_tool_call:21", "relation_type": "records_tool_call"},
			map[string]any{"from_asset_id": "agent_task:12", "to_asset_id": "agent_tool_call:22", "relation_type": "records_tool_call"},
			map[string]any{"from_asset_id": "operation_run:1", "to_asset_id": "agent_tool_call:20", "relation_type": "records_tool_call"},
		},
	}
	got := countContextGraphLinks(assets, graph)
	if got.TaskRuntimes != 1 || got.TaskContextToolCalls != 2 || got.CompleteContextTasks != 1 {
		t.Fatalf("countContextGraphLinks = %#v, want one runtime, two context tool links, and one complete context task", got)
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
		{"id": "op-1", "operation_type": "ssh.exec", "log_count": 1},
	}
	operationAssetIDs := map[string]bool{"operation_run:op-1": true}
	if got := countOperationRowsWithLogs(rows, operationAssetIDs); got != 1 {
		t.Fatalf("countOperationRowsWithLogs with missing log_count = %d, want 1", got)
	}
}

func TestCountOperationRowsWithLogsRequiresMatchingCanonicalAsset(t *testing.T) {
	rows := []map[string]any{
		{"id": "op-1", "operation_type": "repo.sync", "log_count": 2},
		{"asset_id": "operation_run:op-2", "operation_type": "ssh.exec", "log_count": 1},
		{"id": "op-3", "operation_type": "argo.apps.sync", "log_count": 4},
	}
	operationAssetIDs := map[string]bool{
		"operation_run:op-1": true,
		"operation_run:op-2": true,
	}
	if got := countOperationRowsWithLogs(rows, operationAssetIDs); got != 2 {
		t.Fatalf("countOperationRowsWithLogs with mixed canonical assets = %d, want 2", got)
	}
}

func TestAssetIDsByTypeUsesCanonicalOperationSourceID(t *testing.T) {
	rows := []map[string]any{
		{"id": "asset-row-1", "asset_type": "operation_run", "source_id": "op-1"},
		{"id": "op-2", "asset_type": "operation_run"},
		{"asset_id": "operation_run:op-3", "asset_type": "operation_run"},
		{"asset_id": "<nil>", "asset_type": "operation_run", "source_id": "<nil>"},
		{"id": "asset-row-4", "asset_type": "repository", "source_id": "repo-1"},
	}
	got := assetIDsByType(rows, "operation_run")
	for _, want := range []string{"operation_run:op-1", "operation_run:op-2", "operation_run:op-3"} {
		if !got[want] {
			t.Fatalf("assetIDsByType missing %q from %#v", want, got)
		}
	}
	if got["operation_run:asset-row-1"] || got["operation_run:<nil>"] || got["repository:repo-1"] {
		t.Fatalf("assetIDsByType included non-canonical ids: %#v", got)
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

func mapFromAny(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	default:
		return nil
	}
}

func intMapFromAny(value any) map[string]int {
	switch typed := value.(type) {
	case map[string]int:
		return typed
	case map[string]any:
		items := map[string]int{}
		for key, value := range typed {
			switch number := value.(type) {
			case int:
				items[key] = number
			case int64:
				items[key] = int(number)
			case float64:
				items[key] = int(number)
			}
		}
		return items
	default:
		return nil
	}
}

func assertDemoDataRehearsalPlanSafe(t *testing.T, plan map[string]any) {
	t.Helper()
	if plan["mode"] != "first_version_demo_data_rehearsal_plan" ||
		plan["execution_enabled"] != false ||
		plan["external_call_made"] != false ||
		plan["demo_seed_written"] != false ||
		plan["project_created"] != false ||
		plan["repository_created"] != false ||
		plan["git_remote_created"] != false ||
		plan["asset_graph_written"] != false ||
		plan["contains_remote_url"] != false ||
		plan["contains_credentials"] != false {
		t.Fatalf("demo data rehearsal plan should stay audit-only and redacted: %#v", plan)
	}
	for _, backend := range []string{"project_create", "repository_create", "git_remote_create", "demo_seed_write", "asset_graph_write"} {
		if !containsString(stringSliceFromAny(plan["disabled_backends"]), backend) {
			t.Fatalf("demo data rehearsal disabled backends missing %q: %#v", backend, plan["disabled_backends"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"} {
		if !containsString(stringSliceFromAny(plan["suppressed_fields"]), field) {
			t.Fatalf("demo data rehearsal suppressed fields missing %q: %#v", field, plan["suppressed_fields"])
		}
	}
	environmentPlan := mapFromAny(plan["environment_evidence_plan"])
	if environmentPlan["mode"] != "first_version_demo_environment_evidence_plan" ||
		environmentPlan["evidence_ready"] != false ||
		environmentPlan["evidence_ready_reason"] != "demo_environment_execution_disabled" ||
		environmentPlan["execution_enabled"] != false ||
		environmentPlan["demo_seed_written"] != false ||
		environmentPlan["project_created"] != false ||
		environmentPlan["repository_created"] != false ||
		environmentPlan["git_remote_created"] != false ||
		environmentPlan["external_call_made"] != false ||
		environmentPlan["contains_remote_url"] != false ||
		environmentPlan["contains_credentials"] != false {
		t.Fatalf("demo environment evidence plan should stay disabled and redacted: %#v", environmentPlan)
	}
	if plan["readiness_status"] == "ready" && environmentPlan["metadata_ready"] != true {
		t.Fatalf("ready demo environment plan should mark metadata ready: %#v", environmentPlan)
	}
	if plan["readiness_status"] != "ready" && environmentPlan["metadata_ready"] != false {
		t.Fatalf("non-ready demo environment plan should mark metadata not ready: %#v", environmentPlan)
	}
	for _, field := range []string{"project_asset", "project_graph_node", "repository_asset", "two_git_remote_assets", "project_repository_graph_link", "repository_to_two_remotes_graph_path"} {
		if !containsString(stringSliceFromAny(environmentPlan["required_environment_fields"]), field) {
			t.Fatalf("demo environment required fields missing %q: %#v", field, environmentPlan["required_environment_fields"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"} {
		if !containsString(stringSliceFromAny(environmentPlan["suppressed_fields"]), field) {
			t.Fatalf("demo environment suppressed fields missing %q: %#v", field, environmentPlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"demo_seed_execution_disabled", "live_environment_not_recorded"} {
		if !containsString(stringSliceFromAny(environmentPlan["blocked_reasons"]), reason) {
			t.Fatalf("demo environment blocked reasons missing %q: %#v", reason, environmentPlan["blocked_reasons"])
		}
	}
	if plan["readiness_status"] == "ready" {
		if containsString(stringSliceFromAny(environmentPlan["blocked_reasons"]), "required_graph_evidence_missing") {
			t.Fatalf("ready demo environment should not report missing graph evidence: %#v", environmentPlan["blocked_reasons"])
		}
	} else if !containsString(stringSliceFromAny(environmentPlan["blocked_reasons"]), "required_graph_evidence_missing") {
		t.Fatalf("non-ready demo environment should report missing graph evidence: %#v", environmentPlan["blocked_reasons"])
	}

	environmentProof := mapFromAny(plan["environment_demo_proof"])
	if environmentProof["mode"] != "first_version_demo_environment_proof" ||
		environmentProof["external_call_made"] != false ||
		environmentProof["demo_seed_written"] != false ||
		environmentProof["project_created"] != false ||
		environmentProof["repository_created"] != false ||
		environmentProof["git_remote_created"] != false ||
		environmentProof["asset_graph_written"] != false ||
		environmentProof["contains_remote_url"] != false ||
		environmentProof["contains_credentials"] != false {
		t.Fatalf("demo environment proof should stay observed-only and redacted: %#v", environmentProof)
	}
	if plan["readiness_status"] == "ready" {
		if environmentProof["proof_state"] != "observed" ||
			environmentProof["proof_ready"] != true ||
			environmentProof["live_environment_data_observed"] != true ||
			len(stringSliceFromAny(environmentProof["missing_evidence"])) != 0 {
			t.Fatalf("ready demo environment proof should be observed: %#v", environmentProof)
		}
	} else if environmentProof["proof_ready"] != false {
		t.Fatalf("non-ready demo environment proof should not be ready: %#v", environmentProof)
	}
	for _, field := range []string{"project_asset_id", "repository_asset_id", "source_remote_asset_id", "mirror_remote_asset_id", "remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"} {
		if !containsString(stringSliceFromAny(environmentProof["suppressed_fields"]), field) {
			t.Fatalf("demo environment proof suppressed fields missing %q: %#v", field, environmentProof["suppressed_fields"])
		}
	}

	graphPlan := mapFromAny(plan["graph_proof_plan"])
	if graphPlan["mode"] != "first_version_demo_graph_proof_plan" ||
		graphPlan["proof_ready"] != false ||
		graphPlan["proof_ready_reason"] != "demo_graph_proof_execution_disabled" ||
		graphPlan["asset_graph_written"] != false ||
		graphPlan["asset_sync_triggered"] != false ||
		graphPlan["graph_query_performed"] != false ||
		graphPlan["external_call_made"] != false {
		t.Fatalf("demo graph proof plan should stay disabled and redacted: %#v", graphPlan)
	}
	if plan["readiness_status"] == "ready" && graphPlan["metadata_ready"] != true {
		t.Fatalf("ready demo graph proof plan should mark metadata ready: %#v", graphPlan)
	}
	if plan["readiness_status"] != "ready" && graphPlan["metadata_ready"] != false {
		t.Fatalf("non-ready demo graph proof plan should mark metadata not ready: %#v", graphPlan)
	}
	for _, path := range []string{"project:*", "project:* -> repository:*", "repository:* -> git_remote:*", "repository:* -> second git_remote:*"} {
		if !containsString(stringSliceFromAny(graphPlan["required_graph_paths"]), path) {
			t.Fatalf("demo graph required paths missing %q: %#v", path, graphPlan["required_graph_paths"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"} {
		if !containsString(stringSliceFromAny(graphPlan["suppressed_fields"]), field) {
			t.Fatalf("demo graph suppressed fields missing %q: %#v", field, graphPlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"asset_graph_write_disabled"} {
		if !containsString(stringSliceFromAny(graphPlan["blocked_reasons"]), reason) {
			t.Fatalf("demo graph blocked reasons missing %q: %#v", reason, graphPlan["blocked_reasons"])
		}
	}
	if plan["readiness_status"] == "ready" {
		if containsString(stringSliceFromAny(graphPlan["blocked_reasons"]), "graph_proof_incomplete") {
			t.Fatalf("ready demo graph proof should not report incomplete graph proof: %#v", graphPlan["blocked_reasons"])
		}
	} else if !containsString(stringSliceFromAny(graphPlan["blocked_reasons"]), "graph_proof_incomplete") {
		t.Fatalf("non-ready demo graph proof should report incomplete graph proof: %#v", graphPlan["blocked_reasons"])
	}

	resultPlan := mapFromAny(plan["result_recording_plan"])
	if resultPlan["mode"] != "first_version_demo_data_result_recording_plan" ||
		resultPlan["result_recording_state"] != "blocked" ||
		resultPlan["result_recording_ready"] != false ||
		resultPlan["result_recording_ready_reason"] != "demo_data_execution_not_performed" ||
		resultPlan["recording_enabled"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["operation_log_written"] != false ||
		resultPlan["readiness_snapshot_written"] != false ||
		resultPlan["asset_graph_snapshot_written"] != false ||
		resultPlan["raw_remote_url_recorded"] != false ||
		resultPlan["raw_credentials_recorded"] != false {
		t.Fatalf("demo result recording plan should stay disabled and redacted: %#v", resultPlan)
	}
	for _, field := range []string{"project_asset_id", "repository_asset_id", "source_remote_asset_id", "mirror_remote_asset_id", "graph_proof_status", "readiness_status"} {
		if !containsString(stringSliceFromAny(resultPlan["required_result_fields"]), field) {
			t.Fatalf("demo result required fields missing %q: %#v", field, resultPlan["required_result_fields"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"} {
		if !containsString(stringSliceFromAny(resultPlan["suppressed_fields"]), field) {
			t.Fatalf("demo result suppressed fields missing %q: %#v", field, resultPlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"demo_data_execution_not_performed", "readiness_snapshot_not_recorded", "asset_graph_snapshot_not_recorded"} {
		if !containsString(stringSliceFromAny(resultPlan["blocked_reasons"]), reason) {
			t.Fatalf("demo result blocked reasons missing %q: %#v", reason, resultPlan["blocked_reasons"])
		}
	}
	preflight := mapFromAny(resultPlan["result_recording_preflight"])
	if preflight["mode"] != "first_version_demo_data_result_recording_preflight" ||
		preflight["readiness_status"] != plan["readiness_status"] ||
		preflight["snapshot_write_enabled"] != false ||
		preflight["asset_graph_write_enabled"] != false ||
		preflight["operation_log_write_enabled"] != false ||
		preflight["external_call_made"] != false ||
		preflight["contains_remote_url"] != false ||
		preflight["contains_credentials"] != false {
		t.Fatalf("demo result preflight should stay review-only and redacted: %#v", preflight)
	}
	if plan["readiness_status"] == "ready" {
		if preflight["readiness_snapshot_ready_for_review"] != true ||
			preflight["asset_graph_snapshot_ready_for_review"] != true ||
			preflight["snapshot_contract_ready"] != true ||
			len(stringSliceFromAny(preflight["missing_required_evidence"])) != 0 {
			t.Fatalf("ready demo result preflight should be review-ready without writes: %#v", preflight)
		}
	} else if preflight["readiness_snapshot_ready_for_review"] != false ||
		preflight["asset_graph_snapshot_ready_for_review"] != false ||
		preflight["snapshot_contract_ready"] != false ||
		!containsString(stringSliceFromAny(preflight["blocked_reasons"]), "required_demo_evidence_missing") {
		t.Fatalf("non-ready demo result preflight should stay blocked: %#v", preflight)
	}
	for _, field := range []string{"project_asset_id", "repository_asset_id", "source_remote_asset_id", "mirror_remote_asset_id", "graph_proof_status", "readiness_status", "evidence_counts", "missing_required_evidence"} {
		if !containsString(stringSliceFromAny(preflight["required_snapshot_fields"]), field) {
			t.Fatalf("demo result preflight required snapshot field missing %q: %#v", field, preflight["required_snapshot_fields"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret", "raw_graph_payload", "operation_log_body"} {
		if !containsString(stringSliceFromAny(preflight["suppressed_fields"]), field) {
			t.Fatalf("demo result preflight suppressed field missing %q: %#v", field, preflight["suppressed_fields"])
		}
	}
	for _, backend := range []string{"demo_result_write", "readiness_snapshot_write", "asset_graph_snapshot_write", "operation_log_write"} {
		if !containsString(stringSliceFromAny(preflight["disabled_backends"]), backend) {
			t.Fatalf("demo result preflight disabled backend missing %q: %#v", backend, preflight["disabled_backends"])
		}
	}
	for _, reason := range []string{"demo_result_write_disabled", "readiness_snapshot_write_disabled", "asset_graph_snapshot_write_disabled"} {
		if !containsString(stringSliceFromAny(preflight["blocked_reasons"]), reason) {
			t.Fatalf("demo result preflight blocked reason missing %q: %#v", reason, preflight["blocked_reasons"])
		}
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
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
