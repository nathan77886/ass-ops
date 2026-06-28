package app

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

type approvalRoundTripFunc func(*http.Request) (*http.Response, error)

type skippedSQLTx struct{}

func (skippedSQLTx) Commit() error { return nil }

func (skippedSQLTx) Rollback() error { return nil }

func (f approvalRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func requireGormSchemaModel(t *testing.T, want any) {
	t.Helper()
	wantType := fmt.Sprintf("%T", want)
	for _, model := range gormSchemaModels() {
		if fmt.Sprintf("%T", model) == wantType {
			return
		}
	}
	t.Fatalf("gorm schema model missing %s", wantType)
}

type jsonEvidenceArg func(map[string]any) bool

func (fn jsonEvidenceArg) Match(value driver.Value) bool {
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	return fn(payload)
}

type providerReviewAttemptIDsArg []string

func (expected providerReviewAttemptIDsArg) Match(value driver.Value) bool {
	got := strings.TrimSpace(fmt.Sprint(value))
	got = strings.TrimPrefix(strings.TrimSuffix(got, "}"), "{")
	if got == "" && len(expected) == 0 {
		return true
	}
	parts := strings.Split(got, ",")
	if len(parts) != len(expected) {
		return false
	}
	actual := make([]string, 0, len(parts))
	for _, part := range parts {
		actual = append(actual, strings.Trim(strings.TrimSpace(part), `"`))
	}
	slices.Sort(actual)
	want := append([]string(nil), expected...)
	slices.Sort(want)
	return slices.Equal(actual, want)
}

func sliceMapsFromAnyForTest(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row := mapFromAny(item); len(row) > 0 {
				out = append(out, row)
			}
		}
		return out
	default:
		return nil
	}
}

func withRouteParam(r *http.Request, key, value string) *http.Request {
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeContext))
}

func TestRefsSummary(t *testing.T) {
	tests := []struct {
		name string
		refs map[string]any
		want string
	}{
		{name: "empty refs", refs: nil, want: "default"},
		{name: "branches", refs: map[string]any{"branches": []any{"main"}}, want: `{"branches":["main"]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := refsSummary(tt.refs)
			if got != tt.want {
				t.Fatalf("refsSummary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRefsFromRunRef(t *testing.T) {
	fallback := map[string]any{"branches": []any{"main"}}
	got := refsFromRunRef(`{"branches":["release"],"tags":["v1"]}`, fallback)
	if branches := stringSliceFromAny(got["branches"]); len(branches) != 1 || branches[0] != "release" {
		t.Fatalf("branches = %#v, want release", branches)
	}
	if tags := stringSliceFromAny(got["tags"]); len(tags) != 1 || tags[0] != "v1" {
		t.Fatalf("tags = %#v, want v1", tags)
	}
	if refsFromRunRef("default", fallback)["branches"] == nil {
		t.Fatal("default run ref should fall back to asset refs")
	}
	if refsFromRunRef("not-json", fallback)["branches"] == nil {
		t.Fatal("invalid run ref should fall back to asset refs")
	}
}

func TestValidPublicHTTPURLRejectsUnsafeHosts(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "localhost", url: "http://localhost:8080"},
		{name: "loopback ip", url: "http://127.0.0.1:8080"},
		{name: "link local ip", url: "http://169.254.169.254"},
		{name: "private ip", url: "https://10.0.0.10"},
		{name: "userinfo", url: "https://token@example.com"},
		{name: "unresolvable host", url: "https://assops.invalid"},
		{name: "unsupported scheme", url: "file:///tmp/argocd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if validPublicHTTPURL(context.Background(), tt.url) {
				t.Fatalf("validPublicHTTPURL(%q) = true, want false", tt.url)
			}
		})
	}
}

func TestSensitiveArgoConfigRequiresElevatedRole(t *testing.T) {
	if !boolConfig(map[string]any{"insecure_skip_verify": true}, "insecure_skip_verify") {
		t.Fatal("expected insecure_skip_verify to parse as true")
	}
	if canUseSensitiveArgoConfig(&User{Role: "developer"}) {
		t.Fatal("developer should not be allowed to use sensitive Argo config")
	}
	if !canUseSensitiveArgoConfig(&User{Role: "owner"}) || !canUseSensitiveArgoConfig(&User{Role: "admin"}) {
		t.Fatal("owner and admin should be allowed to use sensitive Argo config")
	}
}

func TestUpdateAndDeleteArgoConnectionHandlers(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &GormUser{}, &GormProject{}, &GormConnectionCredential{}, &GormArgoConnection{}, &GormArgoApp{}, &GormDeploymentTarget{}, &GormDeploymentRecord{}, &GormRollbackPoint{}, &GormAsset{}, &GormAssetRelation{}, &sqliteAssetStatusSnapshotFixture{})
	server := &Server{store: store}
	admin := &User{ID: "user-admin", Email: "admin@example.test", Role: "admin"}
	project := GormProject{Name: "Demo", Slug: "demo"}
	if err := store.Gorm.Create(&project).Error; err != nil {
		t.Fatalf("create project: %v", err)
	}
	credential := GormConnectionCredential{ProjectID: validNullString(project.ID), Name: "argo-token", Kind: "argo_token", SecretCiphertext: "encrypted", Metadata: JSONValue{Data: map[string]any{}}}
	if err := store.Gorm.Create(&credential).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}
	connection := GormArgoConnection{ProjectID: project.ID, Name: "old", ServerURL: "https://example.com", AuthType: "token", CredentialID: validNullString(credential.ID), Config: JSONValue{Data: map[string]any{"use_env_token": true, "insecure_skip_verify": false}}}
	if err := store.Gorm.Create(&connection).Error; err != nil {
		t.Fatalf("create argo connection: %v", err)
	}
	if _, err := store.SyncCanonicalAssets(t.Context()); err != nil {
		t.Fatalf("sync assets: %v", err)
	}
	updateBody := strings.NewReader(fmt.Sprintf(`{"name":"new","server_url":"https://example.org","auth_type":"token","credential_id":%q,"config":{"insecure_skip_verify":false}}`, credential.ID))
	updateReq := withRouteParam(httptest.NewRequest(http.MethodPatch, "/api/argo/connections/"+connection.ID, updateBody), "id", connection.ID)
	updateReq = updateReq.WithContext(context.WithValue(updateReq.Context(), userContextKey{}, admin))
	updateRR := httptest.NewRecorder()
	server.updateArgoConnection(updateRR, updateReq)
	if updateRR.Code != http.StatusOK {
		t.Fatalf("update status = %d, body: %s", updateRR.Code, updateRR.Body.String())
	}
	var updated GormArgoConnection
	if err := store.Gorm.First(&updated, &GormArgoConnection{GormBase: GormBase{ID: connection.ID}}).Error; err != nil {
		t.Fatalf("load updated argo connection: %v", err)
	}
	if updated.Name != "new" || updated.ServerURL != "https://example.org" {
		t.Fatalf("unexpected updated argo connection: %#v", updated)
	}
	updatedConfig := mapFromAny(updated.Config.Data)
	if updatedConfig["use_env_token"] != true || updatedConfig["insecure_skip_verify"] != false {
		t.Fatalf("argo config was not preserved: %#v", updatedConfig)
	}
	var asset GormAsset
	if err := store.Gorm.First(&asset, &GormAsset{AssetType: "argo_connection", SourceTable: "argo_connections", SourceID: validNullString(connection.ID)}).Error; err != nil {
		t.Fatalf("load argo asset: %v", err)
	}
	if asset.Name != "new" || asset.ExternalID != "https://example.org" {
		t.Fatalf("asset not synced after update: %#v", asset)
	}
	target := GormDeploymentTarget{ProjectID: project.ID, Name: "cluster/ns", Environment: "test", ClusterName: "cluster", Namespace: "ns", Source: "argocd", ArgoConnectionID: validNullString(connection.ID), Metadata: JSONValue{Data: map[string]any{}}}
	if err := store.Gorm.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	app := GormArgoApp{ProjectID: project.ID, ArgoConnectionID: validNullString(connection.ID), DeploymentTargetID: validNullString(target.ID), Name: "app", Metadata: JSONValue{Data: map[string]any{}}}
	if err := store.Gorm.Create(&app).Error; err != nil {
		t.Fatalf("create app: %v", err)
	}
	record := GormDeploymentRecord{ProjectID: project.ID, DeploymentTargetID: validNullString(target.ID), ArgoConnectionID: validNullString(connection.ID), ArgoAppID: validNullString(app.ID), Name: "app", Environment: "test", Namespace: "ns", ClusterName: "cluster", Source: "argocd", ImageRefs: JSONValue{Data: []string{}}, Metadata: JSONValue{Data: map[string]any{}}, ObservedAt: time.Now()}
	if err := store.Gorm.Create(&record).Error; err != nil {
		t.Fatalf("create record: %v", err)
	}
	rollback := GormRollbackPoint{ProjectID: project.ID, DeploymentRecordID: validNullString(record.ID), DeploymentTargetID: validNullString(target.ID), Name: "app", Environment: "test", Revision: "rev", Source: "argocd", ImageRefs: JSONValue{Data: []string{}}, Metadata: JSONValue{Data: map[string]any{}}, CapturedAt: time.Now()}
	if err := store.Gorm.Create(&rollback).Error; err != nil {
		t.Fatalf("create rollback: %v", err)
	}
	deleteReq := withRouteParam(httptest.NewRequest(http.MethodDelete, "/api/argo/connections/"+connection.ID, nil), "id", connection.ID)
	deleteReq = deleteReq.WithContext(context.WithValue(deleteReq.Context(), userContextKey{}, admin))
	deleteRR := httptest.NewRecorder()
	server.deleteArgoConnection(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body: %s", deleteRR.Code, deleteRR.Body.String())
	}
	for name, model := range map[string]any{
		"connections": &GormArgoConnection{},
		"apps":        &GormArgoApp{},
		"targets":     &GormDeploymentTarget{},
		"records":     &GormDeploymentRecord{},
		"rollbacks":   &GormRollbackPoint{},
	} {
		var count int64
		if err := store.Gorm.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", name, count)
		}
	}
	var deletedAssetCount int64
	if err := store.Gorm.Model(&GormAsset{}).Where(&GormAsset{AssetType: "argo_connection", SourceTable: "argo_connections", SourceID: validNullString(connection.ID)}).Count(&deletedAssetCount).Error; err != nil {
		t.Fatalf("count deleted argo asset: %v", err)
	}
	if deletedAssetCount != 0 {
		t.Fatalf("deleted argo asset count = %d, want 0", deletedAssetCount)
	}
}
