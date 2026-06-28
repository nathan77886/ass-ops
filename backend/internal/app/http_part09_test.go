package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssetGraphLimitBounds(t *testing.T) {
	tests := map[string]int{
		"":     80,
		"25":   25,
		"0":    1,
		"-10":  1,
		"9999": 200,
		"bad":  80,
	}
	for input, want := range tests {
		if got := assetGraphLimit(input); got != want {
			t.Fatalf("assetGraphLimit(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestGormSchemaIncludesAssetRelations(t *testing.T) {
	requireGormSchemaModel(t, &GormAssetRelation{})
}

func TestCleanAssetRelationType(t *testing.T) {
	tests := map[string]string{
		" Depends On ":      "depends_on",
		"deploys/to":        "deploysto",
		"uses.service-v1":   "uses.service-v1",
		"___observes---":    "observes",
		"contains spaces":   "contains_spaces",
		"DROP TABLE assets": "drop_table_assets",
	}
	for input, want := range tests {
		if got := cleanAssetRelationType(input); got != want {
			t.Fatalf("cleanAssetRelationType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRelationProjectID(t *testing.T) {
	if got := relationProjectID(map[string]any{"project_id": "project-1"}, map[string]any{"project_id": "project-1"}); got != "project-1" {
		t.Fatalf("same project = %q", got)
	}
	if got := relationProjectID(map[string]any{"project_id": "project-1"}, map[string]any{"project_id": ""}); got != "project-1" {
		t.Fatalf("from project = %q", got)
	}
	if got := relationProjectID(map[string]any{"project_id": ""}, map[string]any{"project_id": "project-2"}); got != "project-2" {
		t.Fatalf("to project = %q", got)
	}
	if got := relationProjectID(map[string]any{"project_id": "project-1"}, map[string]any{"project_id": "project-2"}); got != "" {
		t.Fatalf("cross project = %q, want empty", got)
	}
}

func TestCreateAssetRelationRejectsSameAssetBeforeTransaction(t *testing.T) {
	server := &Server{}
	body := strings.NewReader(`{"from_asset_id":"asset-1","to_asset_id":"asset-1","relation_type":"depends_on"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/asset-relations", body)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
	rr := httptest.NewRecorder()

	server.createAssetRelation(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}
