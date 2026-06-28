package main

import (
	"testing"
)

func TestFirstVersionReadinessReportRequiresRepoSyncGraphLinks(t *testing.T) {
	withoutGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repo_sync", "source_id": "20"},
	}, nil, nil, map[string]any{
		"edges": []any{},
	})
	if got := readinessByKey(t, withoutGraphLinks, "repo_sync"); got.Status != "partial" || got.Evidence != "1 repo syncs / 0 graph-complete syncs / 0 sync asset paths / 0 repository links / 0 source links / 0 target links" {
		t.Fatalf("repo sync readiness without graph links = %#v, want partial with graph evidence", got)
	}

	withGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository", "source_id": "10"},
		{"asset_type": "repo_sync", "source_id": "20"},
		{"asset_type": "git_remote", "source_id": "100"},
		{"asset_type": "git_remote", "source_id": "101"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:101", "relation_type": "mirrors_to"},
		},
	})
	if got := readinessByKey(t, withGraphLinks, "repo_sync"); got.Status != "ready" || got.Evidence != "1 repo syncs / 1 graph-complete syncs / 1 sync asset paths / 1 repository links / 1 source links / 1 target links" {
		t.Fatalf("repo sync readiness with graph links = %#v, want ready", got)
	}

	missingRepositoryAsset := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repo_sync", "source_id": "20"},
		{"asset_type": "git_remote", "source_id": "100"},
		{"asset_type": "git_remote", "source_id": "101"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:101", "relation_type": "mirrors_to"},
		},
	})
	if got := readinessByKey(t, missingRepositoryAsset, "repo_sync"); got.Status != "partial" || got.Evidence != "1 repo syncs / 1 graph-complete syncs / 0 sync asset paths / 1 repository links / 1 source links / 1 target links" {
		t.Fatalf("repo sync readiness without canonical repository asset = %#v, want partial without sync asset path", got)
	}

	graphOnlyCompleteSync := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repo_sync", "source_id": "21"},
		{"asset_type": "git_remote", "source_id": "102"},
		{"asset_type": "git_remote", "source_id": "103"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:101", "relation_type": "mirrors_to"},
		},
	})
	if got := readinessByKey(t, graphOnlyCompleteSync, "repo_sync"); got.Status != "partial" || got.Evidence != "1 repo syncs / 1 graph-complete syncs / 0 sync asset paths / 1 repository links / 1 source links / 1 target links" {
		t.Fatalf("repo sync readiness with graph-only complete sync = %#v, want partial without canonical asset path", got)
	}

	missingRemoteAsset := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository", "source_id": "10"},
		{"asset_type": "repo_sync", "source_id": "20"},
		{"asset_type": "git_remote", "source_id": "100"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:101", "relation_type": "mirrors_to"},
		},
	})
	if got := readinessByKey(t, missingRemoteAsset, "repo_sync"); got.Status != "partial" || got.Evidence != "1 repo syncs / 1 graph-complete syncs / 0 sync asset paths / 1 repository links / 1 source links / 1 target links" {
		t.Fatalf("repo sync readiness with missing remote asset = %#v, want partial without canonical remote path", got)
	}

	missingTargetLink := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository", "source_id": "10"},
		{"asset_type": "repo_sync", "source_id": "20"},
		{"asset_type": "git_remote", "source_id": "100"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "webhook_connection:1", "relation_type": "mirrors_to"},
		},
	})
	if got := readinessByKey(t, missingTargetLink, "repo_sync"); got.Status != "partial" || got.Evidence != "1 repo syncs / 0 graph-complete syncs / 0 sync asset paths / 1 repository links / 1 source links / 0 target links" {
		t.Fatalf("repo sync readiness with unrelated target link = %#v, want partial without target evidence", got)
	}

	sameRemoteSourceAndTarget := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository", "source_id": "10"},
		{"asset_type": "repo_sync", "source_id": "20"},
		{"asset_type": "git_remote", "source_id": "100"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:20", "to_asset_id": "git_remote:100", "relation_type": "mirrors_to"},
		},
	})
	if got := readinessByKey(t, sameRemoteSourceAndTarget, "repo_sync"); got.Status != "partial" || got.Evidence != "1 repo syncs / 0 graph-complete syncs / 0 sync asset paths / 1 repository links / 1 source links / 1 target links" {
		t.Fatalf("repo sync readiness with same source and target remote = %#v, want partial without distinct mirror evidence", got)
	}

	crossSyncAggregation := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository", "source_id": "10"},
		{"asset_type": "repo_sync", "source_id": "20"},
		{"asset_type": "repo_sync", "source_id": "21"},
		{"asset_type": "git_remote", "source_id": "100"},
		{"asset_type": "git_remote", "source_id": "101"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "repo_sync:20", "relation_type": "has_sync"},
			map[string]any{"from_asset_id": "repo_sync:21", "to_asset_id": "git_remote:100", "relation_type": "synced_from"},
			map[string]any{"from_asset_id": "repo_sync:21", "to_asset_id": "git_remote:101", "relation_type": "mirrors_to"},
		},
	})
	if got := readinessByKey(t, crossSyncAggregation, "repo_sync"); got.Status != "partial" || got.Evidence != "2 repo syncs / 0 graph-complete syncs / 0 sync asset paths / 1 repository links / 1 source links / 1 target links" {
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
	got := countRepoSyncGraphLinks(graph, map[string]bool{"repository:10": true}, map[string]bool{"repo_sync:20": true}, map[string]bool{"git_remote:100": true, "git_remote:101": true})
	if got.RepositorySync != 1 || got.SourceRemotes != 1 || got.TargetRemotes != 1 || got.CompleteSyncs != 1 || got.CompleteSyncAssets != 1 {
		t.Fatalf("countRepoSyncGraphLinks = %#v, want repository/source/target/complete counts of 1", got)
	}

	got = countRepoSyncGraphLinks(graph, nil, map[string]bool{"repo_sync:20": true}, map[string]bool{"git_remote:100": true, "git_remote:101": true})
	if got.CompleteSyncs != 1 || got.CompleteSyncAssets != 0 {
		t.Fatalf("countRepoSyncGraphLinks without canonical repository asset = %#v, want graph-complete sync without asset path", got)
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
	got := countRepoSyncGraphLinks(graph, map[string]bool{"repository:10": true}, map[string]bool{"repo_sync:20": true}, map[string]bool{"git_remote:100": true})
	if got.RepositorySync != 1 || got.SourceRemotes != 1 || got.TargetRemotes != 1 || got.CompleteSyncs != 0 || got.CompleteSyncAssets != 0 {
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
	got := countRepoSyncGraphLinks(graph, map[string]bool{"repository:10": true}, map[string]bool{"repo_sync:20": true}, map[string]bool{"git_remote:100": true, "git_remote:101": true})
	if got.RepositorySync != 1 || got.SourceRemotes != 2 || got.TargetRemotes != 1 || got.CompleteSyncs != 1 || got.CompleteSyncAssets != 1 {
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
	got := countWebhookSyncGraphLinks(
		graph,
		map[string]bool{"webhook_connection:1": true},
		map[string]bool{"webhook_event:20": true},
		map[string]bool{"repo_sync:30": true},
		map[string]bool{"operation_run:40": true},
	)
	if got.ConnectionEvents != 2 || got.EventRepoSyncs != 2 || got.EventOperations != 1 || got.CompleteChains != 1 || got.CompleteChainAssets != 1 {
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
	got := countWebhookSyncGraphLinks(
		graph,
		map[string]bool{"webhook_connection:1": true},
		map[string]bool{"webhook_event:20": true},
		map[string]bool{"repo_sync:30": true},
		map[string]bool{"operation_run:40": true},
	)
	if got.ConnectionEvents != 1 || got.EventRepoSyncs != 1 || got.EventOperations != 0 || got.CompleteChains != 0 || got.CompleteChainAssets != 0 {
		t.Fatalf("countWebhookSyncGraphLinks without event operation = %#v, want no complete chain", got)
	}
}
