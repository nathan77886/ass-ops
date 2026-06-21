package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

type Store struct {
	DB *sqlx.DB
}

type MigrationRecord struct {
	Version   string `db:"version" json:"version"`
	Checksum  string `db:"checksum" json:"checksum"`
	AppliedAt string `db:"applied_at" json:"applied_at"`
}

type AssetSyncResult struct {
	SyncedAssets            int `db:"synced_assets" json:"synced_assets"`
	InsertedRelations       int `db:"inserted_relations" json:"inserted_relations"`
	InsertedStatusSnapshots int `db:"inserted_status_snapshots" json:"inserted_status_snapshots"`
}

type JSONValue struct {
	Data any
}

func (j *JSONValue) Scan(value any) error {
	if value == nil {
		j.Data = nil
		return nil
	}
	var data []byte
	switch typed := value.(type) {
	case []byte:
		data = typed
	case string:
		data = []byte(typed)
	default:
		return fmt.Errorf("scanning json value from %T", value)
	}
	if len(data) == 0 {
		j.Data = nil
		return nil
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("decoding json value: %w", err)
	}
	j.Data = decoded
	return nil
}

func (j JSONValue) Value() (driver.Value, error) {
	if j.Data == nil {
		return "null", nil
	}
	data, err := json.Marshal(j.Data)
	if err != nil {
		return nil, fmt.Errorf("encoding json value: %w", err)
	}
	return string(data), nil
}

func (j JSONValue) MarshalJSON() ([]byte, error) {
	if j.Data == nil {
		return []byte("null"), nil
	}
	return json.Marshal(j.Data)
}

func OpenStore(ctx context.Context, cfg Config) (*Store, error) {
	db, err := sqlx.ConnectContext(ctx, "postgres", cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connecting postgres: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(10 * time.Minute)
	return &Store{DB: db}, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

func (s *Store) ApplyMigrations(ctx context.Context, dir string) error {
	files, err := migrationFiles(dir)
	if err != nil {
		return err
	}
	if err := s.ensureSchemaMigrations(ctx); err != nil {
		return err
	}
	if err := s.lockMigrations(ctx); err != nil {
		return err
	}
	defer func() {
		_, _ = s.DB.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", migrationAdvisoryLockID)
	}()

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", file, err)
		}
		version := migrationVersion(file)
		checksum := migrationChecksum(content)
		if err := s.applyMigration(ctx, version, checksum, string(content)); err != nil {
			return fmt.Errorf("applying migration %s: %w", file, err)
		}
	}
	return nil
}

func (s *Store) ListMigrations(ctx context.Context) ([]MigrationRecord, error) {
	var exists bool
	if err := s.DB.GetContext(ctx, &exists, "SELECT to_regclass('public.schema_migrations') IS NOT NULL"); err != nil {
		return nil, fmt.Errorf("checking schema_migrations: %w", err)
	}
	if !exists {
		return []MigrationRecord{}, nil
	}
	var records []MigrationRecord
	if err := s.DB.SelectContext(ctx, &records, `
		SELECT version, checksum, applied_at::text
		FROM schema_migrations
		ORDER BY version`); err != nil {
		return nil, fmt.Errorf("listing migrations: %w", err)
	}
	return records, nil
}

func (s *Store) SyncCanonicalAssets(ctx context.Context) (AssetSyncResult, error) {
	var result AssetSyncResult
	if err := s.DB.GetContext(ctx, &result, canonicalAssetSyncSQL()); err != nil {
		return result, fmt.Errorf("syncing canonical assets: %w", err)
	}
	return result, nil
}

const migrationAdvisoryLockID int64 = 451127631724519

func migrationFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading migrations: %w", err)
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func migrationVersion(file string) string {
	return filepath.Base(file)
}

func migrationChecksum(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func (s *Store) ensureSchemaMigrations(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("ensuring schema_migrations: %w", err)
	}
	return nil
}

func (s *Store) lockMigrations(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationAdvisoryLockID); err != nil {
		return fmt.Errorf("locking migrations: %w", err)
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, version, checksum, content string) error {
	tx, err := s.DB.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting migration transaction: %w", err)
	}
	defer tx.Rollback()

	var existing string
	err = tx.GetContext(ctx, &existing, "SELECT checksum FROM schema_migrations WHERE version=$1 FOR UPDATE", version)
	if err == nil {
		if existing != checksum {
			return fmt.Errorf("checksum mismatch for %s", version)
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("checking migration history: %w", err)
	}
	if _, err := tx.ExecContext(ctx, content); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, checksum)
		VALUES ($1, $2)`, version, checksum); err != nil {
		return fmt.Errorf("recording migration: %w", err)
	}
	return tx.Commit()
}

func (s *Store) SeedAdmin(ctx context.Context, cfg Config) error {
	var exists bool
	if err := s.DB.GetContext(ctx, &exists, "SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)", cfg.AdminEmail); err != nil {
		return fmt.Errorf("checking admin user: %w", err)
	}
	if exists {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing admin password: %w", err)
	}
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO users(email, name, password_hash, role)
		VALUES ($1, $2, $3, 'admin')`,
		cfg.AdminEmail, "ASSOPS Admin", string(hash))
	if err != nil {
		return fmt.Errorf("creating admin user: %w", err)
	}
	return nil
}

type User struct {
	ID           string `db:"id" json:"id"`
	Email        string `db:"email" json:"email"`
	Name         string `db:"name" json:"name"`
	PasswordHash string `db:"password_hash" json:"-"`
	Role         string `db:"role" json:"role"`
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	var user User
	err := s.DB.GetContext(ctx, &user, "SELECT id, email, name, password_hash, role FROM users WHERE email=$1", email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying user by email: %w", err)
	}
	return &user, nil
}

func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	var user User
	err := s.DB.GetContext(ctx, &user, "SELECT id, email, name, password_hash, role FROM users WHERE id=$1", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying user by id: %w", err)
	}
	return &user, nil
}

func queryMaps(ctx context.Context, db sqlx.ExtContext, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		row := map[string]any{}
		if err := rows.MapScan(row); err != nil {
			return nil, err
		}
		normalizeRow(row)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func queryOne(ctx context.Context, db sqlx.ExtContext, query string, args ...any) (map[string]any, error) {
	items, err := queryMaps(ctx, db, query, args...)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return items[0], nil
}

func normalizeRow(row map[string]any) {
	for key, value := range row {
		switch typed := value.(type) {
		case []byte:
			var decoded any
			if json.Unmarshal(typed, &decoded) == nil {
				row[key] = sanitizeRowValue(key, decoded)
			} else {
				row[key] = sanitizeRowValue(key, string(typed))
			}
		case time.Time:
			row[key] = typed.Format(time.RFC3339)
		default:
			row[key] = sanitizeRowValue(key, value)
		}
	}
}

func sanitizeRowValue(key string, value any) any {
	return sanitizeAnyValue(key, value)
}

func sanitizeMetadata(metadata map[string]any) map[string]any {
	sanitized, _ := sanitizeAnyValue("metadata", metadata).(map[string]any)
	return sanitized
}

func sanitizeAnyValue(key string, value any) any {
	if isSensitiveMetadataKey(key) {
		return "<redacted>"
	}
	switch typed := value.(type) {
	case string:
		return sanitizeURLUserInfo(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for nestedKey, nestedValue := range typed {
			out[nestedKey] = sanitizeAnyValue(nestedKey, nestedValue)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeAnyValue("", item))
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeURLUserInfo(item))
		}
		return out
	default:
		return value
	}
}

func isSensitiveMetadataKey(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "token") ||
		strings.Contains(key, "secret") ||
		strings.Contains(key, "password") ||
		strings.Contains(key, "private_key") ||
		strings.Contains(key, "credential")
}

var urlUserInfoPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s@]+@`)

func sanitizeURLUserInfo(value string) string {
	return urlUserInfoPattern.ReplaceAllString(value, "${1}<redacted>@")
}

func jsonParam(value any) (string, error) {
	if value == nil {
		return "{}", nil
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encoding json parameter: %w", err)
	}
	return string(bytes), nil
}

func jsonPatchParam(value any) (string, error) {
	if value == nil {
		return "null", nil
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encoding json patch parameter: %w", err)
	}
	return string(bytes), nil
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() string {
	return uuid.NewString() + "." + uuid.NewString()
}

func canonicalAssetSyncSQL() string {
	return combinedAssetInventoryRelationSQL() + `,
	asset_upserts AS (
		INSERT INTO assets(
			project_id, asset_type, source_table, source_id, name, display_name, description,
			source, external_id, status, risk_level, metadata, created_at, updated_at
		)
		SELECT
			NULLIF(project_id, '')::uuid,
			asset_type,
			source_table,
			NULLIF(source_id, '')::uuid,
			name,
			display_name,
			description,
			source,
			external_id,
			status,
			risk_level,
			metadata,
			created_at,
			updated_at
		FROM asset_inventory
		WHERE source_table <> '' AND source_id <> ''
		ON CONFLICT (asset_type, source_table, source_id) DO UPDATE SET
			project_id=EXCLUDED.project_id,
			name=EXCLUDED.name,
			display_name=EXCLUDED.display_name,
			description=EXCLUDED.description,
			source=EXCLUDED.source,
			external_id=EXCLUDED.external_id,
			status=EXCLUDED.status,
			risk_level=EXCLUDED.risk_level,
			metadata=EXCLUDED.metadata,
			updated_at=EXCLUDED.updated_at
		RETURNING id, asset_type, source_table, source_id, name, status, risk_level, metadata
	),
	status_snapshot_candidates AS (
		SELECT
			id AS asset_id,
			status,
			risk_level AS health,
			concat(asset_type, ' ', name, ' is ', status) AS summary,
			jsonb_build_object(
				'asset_type', asset_type,
				'source_table', source_table,
				'source_id', source_id,
				'name', name,
				'metadata', metadata
			) AS raw
		FROM asset_upserts
	),
	status_snapshot_inserts AS (
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT asset_id, status, health, summary, raw
		FROM status_snapshot_candidates candidate
		WHERE NOT EXISTS (
			SELECT 1
			FROM asset_status_snapshots latest
			WHERE latest.asset_id=candidate.asset_id
				AND latest.status=candidate.status
				AND latest.health=candidate.health
				AND latest.raw=candidate.raw
				AND latest.collected_at=(
					SELECT max(collected_at)
					FROM asset_status_snapshots newest
					WHERE newest.asset_id=candidate.asset_id
				)
		)
		RETURNING id
	),
	relation_candidates AS (
		SELECT
			NULLIF(ari.project_id, '')::uuid AS project_id,
			from_asset.id AS from_asset_id,
			to_asset.id AS to_asset_id,
			ari.relation_type,
			ari.metadata,
			ari.created_at
		FROM asset_relation_inventory ari
		JOIN asset_inventory from_inv ON from_inv.id=ari.from_asset_id
		JOIN asset_inventory to_inv ON to_inv.id=ari.to_asset_id
		JOIN assets from_asset ON from_asset.asset_type=from_inv.asset_type
			AND from_asset.source_table=from_inv.source_table
			AND from_asset.source_id=NULLIF(from_inv.source_id, '')::uuid
		JOIN assets to_asset ON to_asset.asset_type=to_inv.asset_type
			AND to_asset.source_table=to_inv.source_table
			AND to_asset.source_id=NULLIF(to_inv.source_id, '')::uuid
		WHERE from_inv.source_table <> '' AND from_inv.source_id <> ''
			AND to_inv.source_table <> '' AND to_inv.source_id <> ''
	),
	relation_inserts AS (
		INSERT INTO asset_relations(project_id, from_asset_id, to_asset_id, relation_type, metadata, created_at)
		SELECT project_id, from_asset_id, to_asset_id, relation_type, metadata, created_at
		FROM relation_candidates rc
		WHERE NOT EXISTS (
			SELECT 1
			FROM asset_relations existing
			WHERE existing.from_asset_id=rc.from_asset_id
				AND existing.to_asset_id=rc.to_asset_id
				AND existing.relation_type=rc.relation_type
				AND existing.project_id IS NOT DISTINCT FROM rc.project_id
		)
		ON CONFLICT (from_asset_id, to_asset_id, relation_type) DO NOTHING
		RETURNING id
	)
	SELECT
		(SELECT count(*) FROM asset_upserts) AS synced_assets,
		(SELECT count(*) FROM relation_inserts) AS inserted_relations,
		(SELECT count(*) FROM status_snapshot_inserts) AS inserted_status_snapshots`
}

func combinedAssetInventoryRelationSQL() string {
	return strings.TrimSpace(assetInventorySQL()) + ",\n" + cteWithoutLeadingWith(assetRelationInventorySQL())
}

func cteWithoutLeadingWith(sql string) string {
	trimmed := strings.TrimSpace(sql)
	if len(trimmed) < len("WITH") || !strings.EqualFold(trimmed[:len("WITH")], "WITH") {
		panic("asset relation inventory SQL must start with WITH")
	}
	return strings.TrimSpace(trimmed[len("WITH"):])
}
