package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestJSONValueScanAndMarshal(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "object", input: `{"adapter":true}`},
		{name: "array", input: `["main","dev"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var value JSONValue
			if err := value.Scan([]byte(tt.input)); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got, err := json.Marshal(value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tt.input {
				t.Fatalf("json = %s, want %s", got, tt.input)
			}
		})
	}
}

func TestSanitizeMetadataRedactsSensitiveKeys(t *testing.T) {
	got := sanitizeMetadata(map[string]any{
		"github_token": "secret",
		"github": map[string]any{
			"access_token": "nested-secret",
			"owner":        "acme",
		},
		"owner": "acme",
	})
	if got["github_token"] != "<redacted>" {
		t.Fatalf("github_token was not redacted: %v", got["github_token"])
	}
	nested := got["github"].(map[string]any)
	if nested["access_token"] != "<redacted>" {
		t.Fatalf("nested access_token was not redacted: %v", nested["access_token"])
	}
	if nested["owner"] != "acme" || got["owner"] != "acme" {
		t.Fatalf("non-sensitive metadata changed: %#v", got)
	}
}

func TestSanitizeMetadataNilReturnsEmptyMap(t *testing.T) {
	got := sanitizeMetadata(nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("sanitizeMetadata(nil) = %#v, want empty map", got)
	}
}

func TestSanitizeURLUserInfo(t *testing.T) {
	got := sanitizeURLUserInfo("clone https://token@example.com/org/repo.git and ssh://user:pass@git.example.com/org/repo.git")
	for _, leaked := range []string{"token@example.com", "user:pass@git.example.com"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("sanitizeURLUserInfo leaked %q in %q", leaked, got)
		}
	}
	if !strings.Contains(got, "https://<redacted>@example.com") || !strings.Contains(got, "ssh://<redacted>@git.example.com") {
		t.Fatalf("sanitizeURLUserInfo did not preserve redacted hosts: %q", got)
	}
}

func TestSanitizeRowValueRedactsURLArrays(t *testing.T) {
	got, ok := sanitizeRowValue("urls", []any{"https://token@example.com/org/repo.git"}).([]any)
	if !ok || len(got) != 1 {
		t.Fatalf("sanitizeRowValue returned %#v", got)
	}
	text, _ := got[0].(string)
	if strings.Contains(text, "token@example.com") {
		t.Fatalf("sanitizeRowValue leaked URL credentials in %q", text)
	}
	if text != "https://<redacted>@example.com/org/repo.git" {
		t.Fatalf("sanitized URL = %q", text)
	}
}

func TestNormalizeRowRedactsSensitiveByteStrings(t *testing.T) {
	row := map[string]any{"secret_token": []byte("plain-secret")}
	normalizeRow(row)
	if row["secret_token"] != "<redacted>" {
		t.Fatalf("secret_token = %v, want redacted", row["secret_token"])
	}
}

func TestCanonicalAssetSyncSQLIncludesUpsertAndRelationDedupe(t *testing.T) {
	sql := canonicalAssetSyncSQL()
	for _, token := range []string{
		"WITH asset_inventory AS",
		"asset_relation_inventory AS",
		"INSERT INTO assets",
		"ON CONFLICT (asset_type, source_table, source_id) DO UPDATE",
		"INSERT INTO asset_status_snapshots",
		"status_snapshot_inserts",
		"inserted_status_snapshots",
		"latest.status=candidate.status",
		"INSERT INTO asset_relations",
		"WHERE NOT EXISTS",
		"existing.project_id IS NOT DISTINCT FROM rc.project_id",
		"ON CONFLICT (from_asset_id, to_asset_id, relation_type) DO NOTHING",
		"relation_prunes AS",
		"DELETE FROM asset_relations existing",
		"COALESCE(existing.metadata->>'source', '') <> 'manual'",
		"rc.relation_type=existing.relation_type",
		"synced_assets",
		"inserted_relations",
		"pruned_relations",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("canonicalAssetSyncSQL missing %s", token)
		}
	}
}

func TestWorkerNodeCanonicalAssetSyncSQLIsNarrow(t *testing.T) {
	sql := workerNodeCanonicalAssetSyncSQL()
	for _, token := range []string{
		"WITH worker_node_inventory AS",
		"FROM worker_nodes wn",
		"WHERE wn.id=$1",
		"'' AS project_id",
		"wn.id::text AS source_id",
		"'node_agent' AS asset_type",
		"INSERT INTO assets",
		"project_id, asset_type, source_table, source_id",
		"NULLIF(project_id, '')::uuid",
		"NULLIF(source_id, '')::uuid",
		"ON CONFLICT (asset_type, source_table, source_id) DO UPDATE",
		"project_id=EXCLUDED.project_id",
		"INSERT INTO asset_status_snapshots",
		"'source_id', source_id::text",
		"0 AS inserted_relations",
		"0 AS pruned_relations",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("workerNodeCanonicalAssetSyncSQL missing %s", token)
		}
	}
	if strings.Contains(sql, "asset_inventory AS") {
		t.Fatal("workerNodeCanonicalAssetSyncSQL should not run full asset inventory")
	}
}

func TestSyncCanonicalAssetsWithScansPrunedRelations(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	sqlDB := sqlx.NewDb(db, "sqlmock")
	rows := sqlmock.NewRows([]string{
		"synced_assets",
		"inserted_relations",
		"pruned_relations",
		"inserted_status_snapshots",
	}).AddRow(3, 2, 1, 4)
	mock.ExpectQuery(regexp.QuoteMeta(canonicalAssetSyncSQL())).WillReturnRows(rows)
	result, err := SyncCanonicalAssetsWith(t.Context(), sqlDB)
	if err != nil {
		t.Fatalf("SyncCanonicalAssetsWith: %v", err)
	}
	if result.SyncedAssets != 3 || result.InsertedRelations != 2 || result.PrunedRelations != 1 || result.InsertedStatusSnapshots != 4 {
		t.Fatalf("result = %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCTEWithoutLeadingWith(t *testing.T) {
	got := cteWithoutLeadingWith(" \n WITH sample AS (SELECT 1)")
	if !strings.HasPrefix(got, "sample AS") {
		t.Fatalf("cteWithoutLeadingWith = %q", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("cteWithoutLeadingWith should panic when WITH is missing")
		}
	}()
	_ = cteWithoutLeadingWith("SELECT 1")
}

func TestMigrationFilesSortedSQLOnly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"002_second.sql", "notes.md", "001_first.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("-- test"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	files, err := migrationFiles(dir)
	if err != nil {
		t.Fatalf("migrationFiles: %v", err)
	}
	got := []string{filepath.Base(files[0]), filepath.Base(files[1])}
	want := []string{"001_first.sql", "002_second.sql"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestMigrationVersionAndChecksum(t *testing.T) {
	if got := migrationVersion("/tmp/migrations/001_init.sql"); got != "001_init.sql" {
		t.Fatalf("migrationVersion = %q", got)
	}
	first := migrationChecksum([]byte("select 1;"))
	second := migrationChecksum([]byte("select 1;"))
	third := migrationChecksum([]byte("select 2;"))
	if first == "" || first != second {
		t.Fatalf("checksum should be stable: %q %q", first, second)
	}
	if first == third {
		t.Fatalf("checksum should change with content")
	}
}

func TestDemoSeedDefaultsAreSafeForLocalDemos(t *testing.T) {
	defaults := defaultDemoSeedDefaults()
	if defaults.ProjectSlug != "assops-demo" {
		t.Fatalf("ProjectSlug = %q, want assops-demo", defaults.ProjectSlug)
	}
	if defaults.SourceRemoteKey == defaults.TargetRemoteKey {
		t.Fatalf("source and target remotes should be distinct: %#v", defaults)
	}
	if defaults.RepoSyncEnabled {
		t.Fatalf("repo sync demo asset should be disabled by default")
	}
	if !strings.HasPrefix(defaults.SSHHost, "192.0.2.") {
		t.Fatalf("SSHHost = %q, want TEST-NET-1 demo address", defaults.SSHHost)
	}
	if demoSeedAdvisoryLockID == migrationAdvisoryLockID {
		t.Fatalf("demo seed advisory lock should not reuse migration lock id")
	}
}

func TestDemoSeedFixturesAreNonDestructive(t *testing.T) {
	if got := demoRepoSyncStdout("completed"); !strings.Contains(got, "Fetched refs/heads/main") {
		t.Fatalf("completed stdout = %q", got)
	}
	if got := demoRepoSyncStdout("failed"); !strings.Contains(got, "push was rejected") {
		t.Fatalf("failed stdout = %q", got)
	}
	if prompt := demoAgentPrompt(); !strings.Contains(prompt, "Do not mutate anything") {
		t.Fatalf("demo agent prompt should be read-only: %q", prompt)
	}
}
