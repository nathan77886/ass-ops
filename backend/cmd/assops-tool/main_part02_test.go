package main

import (
	"testing"
)

func TestFirstVersionReadinessReportRequiresArgoSync(t *testing.T) {
	partial := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target"},
	}, nil, nil, nil)
	if got := readinessByKey(t, partial, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 0 Argo connections / 0 apps / 0 sync ops / 0 complete app links / 0 app asset chains" {
		t.Fatalf("argo status with target only = %#v, want partial", got)
	}

	withoutGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target", "source_id": "30"},
		{"asset_type": "argo_connection", "source_id": "10"},
		{"asset_type": "argo_app", "source_id": "20"},
	}, []map[string]any{
		{"id": "40", "operation_type": "argo.apps.sync"},
	}, nil, map[string]any{"edges": []any{}})
	if got := readinessByKey(t, withoutGraphLinks, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 1 Argo connections / 1 apps / 1 sync ops / 0 complete app links / 0 app asset chains" {
		t.Fatalf("argo status without graph links = %#v, want partial with graph evidence", got)
	}

	ready := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target", "source_id": "30"},
		{"asset_type": "argo_connection", "source_id": "10"},
		{"asset_type": "argo_app", "source_id": "20"},
	}, []map[string]any{
		{"id": "40", "operation_type": "argo.apps.sync"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:10", "relation_type": "synced_argo_connection"},
		},
	})
	if got := readinessByKey(t, ready, "argo"); got.Status != "ready" || got.Evidence != "1 targets / 1 Argo connections / 1 apps / 1 sync ops / 1 complete app links / 1 app asset chains" {
		t.Fatalf("argo status with complete app graph = %#v, want ready", got)
	}

	withoutMatchingAssets := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target", "source_id": "31"},
		{"asset_type": "argo_connection", "source_id": "11"},
		{"asset_type": "argo_app", "source_id": "21"},
	}, []map[string]any{
		{"id": "41", "operation_type": "argo.apps.sync"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:10", "relation_type": "synced_argo_connection"},
		},
	})
	if got := readinessByKey(t, withoutMatchingAssets, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 1 Argo connections / 1 apps / 1 sync ops / 1 complete app links / 0 app asset chains / canonical evidence missing" {
		t.Fatalf("argo status with unmatched canonical evidence = %#v, want partial without app asset chain", got)
	}

	withoutOperationID := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target", "source_id": "30"},
		{"asset_type": "argo_connection", "source_id": "10"},
		{"asset_type": "argo_app", "source_id": "20"},
	}, []map[string]any{
		{"operation_type": "argo.apps.sync"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:10", "relation_type": "synced_argo_connection"},
		},
	})
	if got := readinessByKey(t, withoutOperationID, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 1 Argo connections / 1 apps / 1 sync ops / 1 complete app links / 0 app asset chains / canonical evidence missing" {
		t.Fatalf("argo status with sync op missing id = %#v, want partial without canonical operation evidence", got)
	}

	withoutSyncedConnection := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target", "source_id": "30"},
		{"asset_type": "argo_connection", "source_id": "10"},
		{"asset_type": "argo_app", "source_id": "20"},
	}, []map[string]any{
		{"id": "40", "operation_type": "argo.apps.sync"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
		},
	})
	if got := readinessByKey(t, withoutSyncedConnection, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 1 Argo connections / 1 apps / 1 sync ops / 0 complete app links / 0 app asset chains" {
		t.Fatalf("argo status without synced connection edge = %#v, want partial", got)
	}

	withUnrelatedSyncedConnection := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target", "source_id": "30"},
		{"asset_type": "argo_connection", "source_id": "10"},
		{"asset_type": "argo_app", "source_id": "20"},
	}, []map[string]any{
		{"id": "40", "operation_type": "argo.apps.sync"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:11", "relation_type": "synced_argo_connection"},
		},
	})
	if got := readinessByKey(t, withUnrelatedSyncedConnection, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 1 Argo connections / 1 apps / 1 sync ops / 0 complete app links / 0 app asset chains" {
		t.Fatalf("argo status with unrelated synced connection edge = %#v, want partial", got)
	}

	crossAppAggregation := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "deployment_target", "source_id": "30"},
		{"asset_type": "argo_connection", "source_id": "10"},
		{"asset_type": "argo_app", "source_id": "20"},
		{"asset_type": "argo_app", "source_id": "21"},
	}, []map[string]any{
		{"id": "40", "operation_type": "argo.apps.sync"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:21", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:10", "relation_type": "synced_argo_connection"},
		},
	})
	if got := readinessByKey(t, crossAppAggregation, "argo"); got.Status != "partial" || got.Evidence != "1 targets / 1 Argo connections / 2 apps / 1 sync ops / 0 complete app links / 0 app asset chains" {
		t.Fatalf("argo status with cross-app aggregate links = %#v, want partial without a complete app link", got)
	}
}
