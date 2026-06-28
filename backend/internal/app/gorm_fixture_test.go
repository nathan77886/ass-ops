package app

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newGormFixtureStore(t *testing.T) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite gorm fixture: %v", err)
	}
	store := &Store{Gorm: db}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

type sqliteAssetStatusSnapshotFixture struct {
	ID          string    `gorm:"type:uuid;primaryKey"`
	AssetID     string    `gorm:"type:uuid;not null;index"`
	Status      string    `gorm:"not null;index"`
	Health      string    `gorm:"not null;default:''"`
	Summary     string    `gorm:"not null;default:''"`
	Raw         JSONValue `gorm:"type:jsonb;not null"`
	CollectedAt time.Time `gorm:"not null;index"`
}

func (sqliteAssetStatusSnapshotFixture) TableName() string { return "asset_status_snapshots" }

func migrateGormFixture(t *testing.T, store *Store, models ...any) {
	t.Helper()
	if err := store.Gorm.AutoMigrate(models...); err != nil {
		t.Fatalf("fixture AutoMigrate: %v", err)
	}
}

func TestGormSchemaModelsForAutoMigrateKeepsCoreTables(t *testing.T) {
	store := newGormFixtureStore(t)
	models, err := gormSchemaModelsForAutoMigrate(store.Gorm)
	if err != nil {
		t.Fatalf("gormSchemaModelsForAutoMigrate: %v", err)
	}
	want := map[string]bool{
		"users":                    false,
		"projects":                 false,
		"project_git_repositories": false,
		"assets":                   false,
		"asset_relations":          false,
	}
	for _, model := range models {
		stmt := &gorm.Statement{DB: store.Gorm}
		if err := stmt.Parse(model); err != nil {
			t.Fatalf("parse model %T: %v", model, err)
		}
		if _, ok := want[stmt.Schema.Table]; ok {
			want[stmt.Schema.Table] = true
		}
	}
	for table, found := range want {
		if !found {
			t.Fatalf("schema model list missing %s", table)
		}
	}
}

func TestStoreSeedAdminAndLookupUseGormModels(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &GormUser{})
	cfg := Config{AdminEmail: "admin@example.test", AdminPassword: "password-for-test"}
	if err := store.SeedAdmin(t.Context(), cfg); err != nil {
		t.Fatalf("SeedAdmin: %v", err)
	}
	if err := store.SeedAdmin(t.Context(), cfg); err != nil {
		t.Fatalf("SeedAdmin idempotent: %v", err)
	}
	user, err := store.UserByEmail(t.Context(), cfg.AdminEmail)
	if err != nil {
		t.Fatalf("UserByEmail: %v", err)
	}
	if user.Role != "admin" || user.PasswordHash == "" || user.PasswordHash == cfg.AdminPassword {
		t.Fatalf("admin user not seeded safely: %#v", user)
	}
	var count int64
	if err := store.Gorm.Model(&GormUser{}).Where(&GormUser{Email: cfg.AdminEmail}).Count(&count).Error; err != nil {
		t.Fatalf("count admin users: %v", err)
	}
	if count != 1 {
		t.Fatalf("admin count = %d, want 1", count)
	}
}

func TestSyncCanonicalAssetsUsesGormModels(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(
		t,
		store,
		&GormProject{},
		&GormProjectGitRepository{},
		&GormGitRemote{},
		&GormAsset{},
		&GormAssetRelation{},
		&sqliteAssetStatusSnapshotFixture{},
	)
	project := GormProject{Name: "Demo", Slug: "demo", Description: "fixture"}
	if err := store.Gorm.Create(&project).Error; err != nil {
		t.Fatalf("create project: %v", err)
	}
	repo := GormProjectGitRepository{
		ProjectID:     project.ID,
		Name:          "service",
		RepoKey:       "service",
		DisplayName:   "Service",
		RepoRole:      "service",
		Status:        "active",
		DefaultBranch: "main",
	}
	if err := store.Gorm.Create(&repo).Error; err != nil {
		t.Fatalf("create repo: %v", err)
	}
	remote := GormGitRemote{
		ProjectGitRepositoryID: repo.ID,
		Name:                   "origin",
		RemoteKey:              "origin",
		ProviderType:           "git",
		RemoteRole:             "target",
		RemoteURL:              "https://example.test/acme/service.git",
		SyncEnabled:            true,
		LastSyncStatus:         "ready",
		DefaultBranch:          "main",
	}
	if err := store.Gorm.Create(&remote).Error; err != nil {
		t.Fatalf("create remote: %v", err)
	}
	result, err := store.SyncCanonicalAssets(t.Context())
	if err != nil {
		t.Fatalf("SyncCanonicalAssets: %v", err)
	}
	if result.SyncedAssets < 3 || result.InsertedRelations < 2 || result.InsertedStatusSnapshots < 3 {
		t.Fatalf("sync result too small: %+v", result)
	}
	var assets []GormAsset
	if err := store.Gorm.Where(gormField("source_table", []string{"projects", "project_git_repositories", "git_remotes"})).Find(&assets).Error; err != nil {
		t.Fatalf("load assets: %v", err)
	}
	if len(assets) != 3 {
		t.Fatalf("asset count = %d, want 3", len(assets))
	}
}
