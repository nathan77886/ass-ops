package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type DemoReadinessSnapshotOptions struct {
	ProjectSlug string
	ProjectID   string
	DryRun      bool
}

type demoReadinessAssetRow struct {
	ID          string    `db:"id"`
	ProjectID   string    `db:"project_id"`
	AssetType   string    `db:"asset_type"`
	SourceTable string    `db:"source_table"`
	SourceID    string    `db:"source_id"`
	Name        string    `db:"name"`
	DisplayName string    `db:"display_name"`
	Status      string    `db:"status"`
	RiskLevel   string    `db:"risk_level"`
	Metadata    JSONValue `db:"metadata"`
}

type demoReadinessRelationRow struct {
	FromAssetID  string `db:"from_asset_id"`
	ToAssetID    string `db:"to_asset_id"`
	RelationType string `db:"relation_type"`
}

type demoReadinessSnapshotPath struct {
	ProjectAssetDBID    string
	ProjectAssetID      string
	RepositoryAssetDBID string
	RepositoryAssetID   string
	RemoteAssetDBIDs    []string
	RemoteAssetIDs      []string
}

func RecordDemoReadinessSnapshot(ctx context.Context, store *Store, opts DemoReadinessSnapshotOptions) (map[string]any, error) {
	projectID, err := ResolveDemoReadinessSnapshotProjectID(ctx, store, opts)
	if err != nil {
		return nil, err
	}
	assets, relations, err := loadCanonicalDemoReadinessEvidence(ctx, store, projectID)
	if err != nil {
		return nil, err
	}
	result, path, err := buildDemoReadinessSnapshotResult(assets, relations, opts)
	if err != nil {
		return result, err
	}
	if opts.DryRun {
		result["dry_run"] = true
		result["recording_enabled"] = false
		result["external_call_made"] = false
		result["snapshots_written"] = 0
		result["readiness_snapshot_written"] = false
		result["asset_graph_snapshot_written"] = false
		return result, nil
	}

	snapshot := mapFromAny(result["snapshot"])
	snapshotStatus := cleanOptionalText(fmt.Sprint(snapshot["readiness_status"]))
	if snapshotStatus == "" {
		snapshotStatus = "unknown"
	}
	snapshotHealth := "warning"
	if snapshotStatus == "ready" {
		snapshotHealth = "low"
	}
	targets := make([]string, 0, 4)
	for _, target := range append([]string{path.ProjectAssetDBID, path.RepositoryAssetDBID}, path.RemoteAssetDBIDs...) {
		if strings.TrimSpace(target) != "" {
			targets = append(targets, target)
		}
	}

	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting demo readiness snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	written := 0
	rowsAffectedUnknown := false
	for _, target := range targets {
		execResult, err := tx.ExecContext(ctx, `
			INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
			SELECT $1, $2, $3, 'first-version demo project/repository/remote graph snapshot recorded', $4
			WHERE NOT EXISTS (
				SELECT 1
				FROM asset_status_snapshots latest
				WHERE latest.asset_id=$1
					AND latest.status=$2
					AND latest.health=$3
					AND latest.raw=$4
					AND latest.collected_at=(
						SELECT max(collected_at)
						FROM asset_status_snapshots newest
						WHERE newest.asset_id=$1
					)
			)`,
			target, snapshotStatus, snapshotHealth, JSONValue{Data: snapshot})
		if err != nil {
			return nil, fmt.Errorf("inserting demo readiness snapshot: %w", err)
		}
		if rows, err := execResult.RowsAffected(); err == nil {
			written += int(rows)
		} else {
			rowsAffectedUnknown = true
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing demo readiness snapshot: %w", err)
	}
	committed = true
	result["recording_state"] = "recorded"
	if rowsAffectedUnknown {
		result["rows_affected_unknown"] = true
		result["snapshots_written"] = -1
		result["snapshots_skipped_as_duplicate"] = -1
		result["readiness_snapshot_written"] = false
		result["asset_graph_snapshot_written"] = false
	} else {
		result["snapshots_written"] = written
		result["snapshots_skipped_as_duplicate"] = len(targets) - written
		result["readiness_snapshot_written"] = written > 0
		result["asset_graph_snapshot_written"] = written > 0
	}
	return result, nil
}

func ResolveDemoReadinessSnapshotProjectID(ctx context.Context, store *Store, opts DemoReadinessSnapshotOptions) (string, error) {
	projectID := strings.TrimSpace(opts.ProjectID)
	projectSlug := strings.TrimSpace(opts.ProjectSlug)
	if projectID != "" && projectSlug != "" {
		return "", fmt.Errorf("use either project_id or project_slug, not both")
	}
	if projectID != "" {
		if _, err := uuid.Parse(projectID); err != nil {
			return "", fmt.Errorf("project id must be a uuid")
		}
		return projectID, nil
	}
	if projectSlug != "" {
		var id string
		if err := store.DB.GetContext(ctx, &id, `SELECT id::text FROM projects WHERE slug=$1`, projectSlug); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("project slug %q not found", projectSlug)
			}
			return "", fmt.Errorf("resolving project slug %q: %w", projectSlug, err)
		}
		return id, nil
	}
	var count int
	if err := store.DB.GetContext(ctx, &count, `SELECT count(*) FROM assets WHERE asset_type='project'`); err != nil {
		return "", fmt.Errorf("counting project assets: %w", err)
	}
	if count == 0 {
		return "", fmt.Errorf("no project asset found; run db sync-assets or pass a project after creating/importing it")
	}
	if count > 1 {
		return "", fmt.Errorf("multiple project assets found; pass project_slug or project_id")
	}
	var id string
	if err := store.DB.GetContext(ctx, &id, `SELECT source_id::text FROM assets WHERE asset_type='project' LIMIT 1`); err != nil {
		return "", fmt.Errorf("loading only project asset id: %w", err)
	}
	return id, nil
}

func loadCanonicalDemoReadinessEvidence(ctx context.Context, store *Store, projectID string) ([]demoReadinessAssetRow, []demoReadinessRelationRow, error) {
	var assets []demoReadinessAssetRow
	if err := store.DB.SelectContext(ctx, &assets, `
		SELECT
			id::text AS id,
			COALESCE(project_id::text, '') AS project_id,
			asset_type,
			source_table,
			COALESCE(source_id::text, '') AS source_id,
			name,
			display_name,
			status,
			risk_level,
			metadata
		FROM assets
		WHERE asset_type IN ('project', 'repository', 'git_remote')
			AND (
				project_id=$1::uuid
				OR (asset_type='project' AND source_id=$1::uuid)
			)
		ORDER BY asset_type, created_at, id`, projectID); err != nil {
		return nil, nil, fmt.Errorf("loading canonical demo assets: %w", err)
	}
	if len(assets) == 0 {
		return nil, nil, fmt.Errorf("no canonical project/repository/remote assets found for project %s; run db sync-assets first", projectID)
	}
	var relations []demoReadinessRelationRow
	if err := store.DB.SelectContext(ctx, &relations, `
		SELECT from_asset_id::text AS from_asset_id, to_asset_id::text AS to_asset_id, relation_type
		FROM asset_relations
		WHERE relation_type IN ('owns', 'has_remote')
			AND (
				from_asset_id IN (SELECT id FROM assets WHERE project_id=$1::uuid OR (asset_type='project' AND source_id=$1::uuid))
				OR to_asset_id IN (SELECT id FROM assets WHERE project_id=$1::uuid OR (asset_type='project' AND source_id=$1::uuid))
			)
		ORDER BY created_at, id`, projectID); err != nil {
		return nil, nil, fmt.Errorf("loading canonical demo asset relations: %w", err)
	}
	return assets, relations, nil
}

func buildDemoReadinessSnapshotResult(assets []demoReadinessAssetRow, relations []demoReadinessRelationRow, opts DemoReadinessSnapshotOptions) (map[string]any, demoReadinessSnapshotPath, error) {
	graph, path := canonicalDemoReadinessPayload(assets, relations)
	projectStatus := "ready"
	if path.ProjectAssetID == "" {
		projectStatus = "missing"
	}
	repositoryStatus := "ready"
	if path.RepositoryAssetID == "" || len(path.RemoteAssetIDs) < 2 {
		if path.RepositoryAssetID != "" || len(path.RemoteAssetIDs) > 0 {
			repositoryStatus = "partial"
		} else {
			repositoryStatus = "missing"
		}
	}
	if path.ProjectAssetDBID == "" {
		return map[string]any{
			"mode":              "first_version_demo_readiness_snapshot_recording",
			"recording_state":   "blocked",
			"recording_ready":   false,
			"recording_enabled": false,
			"project_status":    projectStatus,
			"repository_status": repositoryStatus,
			"missing_evidence":  demoReadinessSnapshotMissingEvidence(projectStatus, repositoryStatus, path),
			"message":           "Canonical project asset evidence is required before recording a sanitized demo readiness snapshot.",
		}, path, fmt.Errorf("demo readiness snapshot requires a canonical project asset")
	}
	counts := map[string]any{
		"project_assets":            countDemoAssetsByType(assets, "project"),
		"repository_assets":         countDemoAssetsByType(assets, "repository"),
		"git_remote_assets":         countDemoAssetsByType(assets, "git_remote"),
		"project_repository_links":  countDemoRelationsByType(relations, "owns"),
		"repository_remote_links":   countDemoRelationsByType(relations, "has_remote"),
		"complete_repository_paths": countCompleteDemoRepositoryPaths(graph),
	}
	readinessStatus := combinedDemoReadinessStatus(projectStatus, repositoryStatus)
	snapshot := map[string]any{
		"mode":                        "first_version_demo_readiness_snapshot",
		"readiness_status":            readinessStatus,
		"project_readiness_status":    projectStatus,
		"repository_readiness_status": repositoryStatus,
		"graph_proof_status":          graphProofStatusFromPath(path),
		"project_asset_id":            path.ProjectAssetID,
		"repository_asset_id":         path.RepositoryAssetID,
		"remote_asset_ids":            firstNStrings(path.RemoteAssetIDs, 2),
		"evidence_counts":             counts,
		"missing_required_evidence":   demoReadinessSnapshotMissingEvidence(projectStatus, repositoryStatus, path),
		"scope": map[string]any{
			"project_id":   strings.TrimSpace(opts.ProjectID),
			"project_slug": strings.TrimSpace(opts.ProjectSlug),
		},
		"external_call_made":    false,
		"demo_seed_written":     false,
		"project_created":       false,
		"repository_created":    false,
		"git_remote_created":    false,
		"asset_graph_written":   false,
		"operation_log_written": false,
	}
	return map[string]any{
		"mode":                         "first_version_demo_readiness_snapshot_recording",
		"recording_state":              "ready_to_record",
		"recording_ready":              true,
		"recording_enabled":            !opts.DryRun,
		"dry_run":                      opts.DryRun,
		"project_status":               projectStatus,
		"repository_status":            repositoryStatus,
		"snapshot":                     snapshot,
		"snapshots_written":            0,
		"readiness_snapshot_written":   false,
		"asset_graph_snapshot_written": false,
		"operation_log_written":        false,
		"external_call_made":           false,
		"message":                      "Sanitized first-version demo readiness snapshot is ready to record from canonical graph evidence.",
	}, path, nil
}

func canonicalDemoReadinessPayload(assets []demoReadinessAssetRow, relations []demoReadinessRelationRow) (map[string]any, demoReadinessSnapshotPath) {
	graphIDByDBID := map[string]string{}
	dbIDByGraphID := map[string]string{}
	nodes := make([]any, 0, len(assets))
	for _, asset := range assets {
		graphID := demoReadinessGraphID(asset)
		if graphID == "" {
			continue
		}
		graphIDByDBID[asset.ID] = graphID
		dbIDByGraphID[graphID] = asset.ID
		nodes = append(nodes, map[string]any{"id": graphID, "asset_type": asset.AssetType, "status": asset.Status})
	}
	edges := make([]any, 0, len(relations))
	remotesByRepository := map[string][]string{}
	projectByRepository := map[string]string{}
	for _, relation := range relations {
		from := graphIDByDBID[relation.FromAssetID]
		to := graphIDByDBID[relation.ToAssetID]
		if from == "" || to == "" {
			continue
		}
		edges = append(edges, map[string]any{"from_asset_id": from, "to_asset_id": to, "relation_type": relation.RelationType})
		switch relation.RelationType {
		case "owns":
			if strings.HasPrefix(from, "project:") && strings.HasPrefix(to, "repository:") {
				projectByRepository[to] = from
			}
		case "has_remote":
			if strings.HasPrefix(from, "repository:") && strings.HasPrefix(to, "git_remote:") {
				remotesByRepository[from] = append(remotesByRepository[from], to)
			}
		}
	}
	path := demoReadinessSnapshotPath{}
	for graphID, dbID := range dbIDByGraphID {
		if strings.HasPrefix(graphID, "project:") {
			path.ProjectAssetDBID = dbID
			path.ProjectAssetID = graphID
			break
		}
	}
	repositories := make([]string, 0, len(remotesByRepository))
	for repository := range remotesByRepository {
		repositories = append(repositories, repository)
	}
	sort.Strings(repositories)
	for _, repository := range repositories {
		project := projectByRepository[repository]
		remotes := append([]string{}, remotesByRepository[repository]...)
		sort.Strings(remotes)
		if project == "" || len(remotes) < 2 {
			if path.RepositoryAssetID == "" && project != "" {
				path.ProjectAssetDBID = dbIDByGraphID[project]
				path.ProjectAssetID = project
				path.RepositoryAssetDBID = dbIDByGraphID[repository]
				path.RepositoryAssetID = repository
				path.RemoteAssetIDs = firstNStrings(remotes, 2)
				for _, remote := range path.RemoteAssetIDs {
					path.RemoteAssetDBIDs = append(path.RemoteAssetDBIDs, dbIDByGraphID[remote])
				}
			}
			continue
		}
		path = demoReadinessSnapshotPath{
			ProjectAssetDBID:    dbIDByGraphID[project],
			ProjectAssetID:      project,
			RepositoryAssetDBID: dbIDByGraphID[repository],
			RepositoryAssetID:   repository,
			RemoteAssetDBIDs:    []string{dbIDByGraphID[remotes[0]], dbIDByGraphID[remotes[1]]},
			RemoteAssetIDs:      []string{remotes[0], remotes[1]},
		}
		break
	}
	return map[string]any{"nodes": nodes, "edges": edges}, path
}

func demoReadinessGraphID(asset demoReadinessAssetRow) string {
	typ := strings.TrimSpace(asset.AssetType)
	sourceID := strings.TrimSpace(asset.SourceID)
	if typ == "" {
		return ""
	}
	if sourceID == "" {
		sourceID = strings.TrimSpace(asset.ID)
	}
	if sourceID == "" {
		return ""
	}
	return typ + ":" + sourceID
}

func combinedDemoReadinessStatus(projectStatus, repositoryStatus string) string {
	if projectStatus == "ready" && repositoryStatus == "ready" {
		return "ready"
	}
	if projectStatus == "missing" && repositoryStatus == "missing" {
		return "missing"
	}
	return "partial"
}

func graphProofStatusFromPath(path demoReadinessSnapshotPath) string {
	if path.ProjectAssetID != "" && path.RepositoryAssetID != "" && len(path.RemoteAssetIDs) >= 2 {
		return "observed"
	}
	if path.ProjectAssetID != "" || path.RepositoryAssetID != "" || len(path.RemoteAssetIDs) > 0 {
		return "partial"
	}
	return "missing"
}

func firstNStrings(values []string, n int) []string {
	if len(values) <= n {
		return append([]string{}, values...)
	}
	return append([]string{}, values[:n]...)
}

func demoReadinessSnapshotMissingEvidence(projectStatus, repositoryStatus string, path demoReadinessSnapshotPath) []string {
	missing := []string{}
	if projectStatus != "ready" {
		missing = append(missing, "project_readiness_not_ready")
	}
	if repositoryStatus != "ready" {
		missing = append(missing, "repository_readiness_not_ready")
	}
	if path.ProjectAssetID == "" {
		missing = append(missing, "project_asset_path_missing")
	}
	if path.RepositoryAssetID == "" {
		missing = append(missing, "repository_asset_path_missing")
	}
	if len(path.RemoteAssetIDs) < 2 {
		missing = append(missing, "two_remote_asset_path_missing")
	}
	return missing
}

func countDemoAssetsByType(assets []demoReadinessAssetRow, assetType string) int {
	count := 0
	for _, asset := range assets {
		if asset.AssetType == assetType {
			count++
		}
	}
	return count
}

func countDemoRelationsByType(relations []demoReadinessRelationRow, relationType string) int {
	count := 0
	for _, relation := range relations {
		if relation.RelationType == relationType {
			count++
		}
	}
	return count
}

func countCompleteDemoRepositoryPaths(graph map[string]any) int {
	type repositoryLinks struct {
		project bool
		remotes map[string]bool
	}
	byRepository := map[string]*repositoryLinks{}
	repositoryEntry := func(assetID string) *repositoryLinks {
		entry := byRepository[assetID]
		if entry == nil {
			entry = &repositoryLinks{remotes: map[string]bool{}}
			byRepository[assetID] = entry
		}
		return entry
	}
	for _, edge := range demoSliceOfMapsFromAny(graph["edges"]) {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "owns":
			if strings.HasPrefix(from, "project:") && strings.HasPrefix(to, "repository:") {
				repositoryEntry(to).project = true
			}
		case "has_remote":
			if strings.HasPrefix(from, "repository:") && strings.HasPrefix(to, "git_remote:") {
				repositoryEntry(from).remotes[to] = true
			}
		}
	}
	count := 0
	for _, entry := range byRepository {
		if entry.project && len(entry.remotes) >= 2 {
			count++
		}
	}
	return count
}

func demoSliceOfMapsFromAny(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if mapped, ok := item.(map[string]any); ok {
			out = append(out, mapped)
		}
	}
	return out
}
