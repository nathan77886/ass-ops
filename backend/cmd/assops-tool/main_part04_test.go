package main

import (
	"testing"
)

func TestFirstVersionReadinessReportRequiresWebhookEventForSyncTrigger(t *testing.T) {
	withoutEvent := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "metadata": map[string]any{"provider": "gitea"}},
	}, []map[string]any{
		{"operation_type": "repo.sync"},
	}, nil, map[string]any{"edges": []any{}})
	if got := readinessByKey(t, withoutEvent, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 0 Gitea events / 0 any-provider complete webhook chains / 0 webhook asset chains" {
		t.Fatalf("sync trigger without webhook event = %#v, want partial with event evidence", got)
	}

	withoutGraphChain := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_event", "metadata": map[string]any{"provider": "gitea"}},
	}, []map[string]any{
		{"operation_type": "repo.sync_remote"},
	}, nil, map[string]any{"edges": []any{}})
	if got := readinessByKey(t, withoutGraphChain, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 0 any-provider complete webhook chains / 0 webhook asset chains" {
		t.Fatalf("sync trigger without complete graph chain = %#v, want partial", got)
	}

	withGraphChain := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "source_id": "1", "metadata": map[string]any{"provider": "Gitea"}},
		{"asset_type": "webhook_event", "source_id": "20", "metadata": map[string]any{"provider": "GITEA"}},
		{"asset_type": "repo_sync", "source_id": "30"},
	}, []map[string]any{
		{"id": "40", "operation_type": "repo.sync_remote"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "webhook_connection:1", "to_asset_id": "webhook_event:20", "relation_type": "received_webhook_event"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "repo_sync:30", "relation_type": "matched_repo_sync"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "operation_run:40", "relation_type": "triggered_operation"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "repo_sync:30", "relation_type": "ran_repo_sync"},
		},
	})
	if got := readinessByKey(t, withGraphChain, "sync_trigger"); got.Status != "ready" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 1 any-provider complete webhook chains / 1 webhook asset chains" {
		t.Fatalf("sync trigger with webhook graph chain = %#v, want ready with complete graph evidence", got)
	}

	githubOnlyGraphChain := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "source_id": "1", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_event", "source_id": "20", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_connection", "source_id": "2", "metadata": map[string]any{"provider": "github"}},
		{"asset_type": "webhook_event", "source_id": "21", "metadata": map[string]any{"provider": "github"}},
		{"asset_type": "repo_sync", "source_id": "30"},
	}, []map[string]any{
		{"id": "40", "operation_type": "repo.sync_remote"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "webhook_connection:2", "to_asset_id": "webhook_event:21", "relation_type": "received_webhook_event"},
			map[string]any{"from_asset_id": "webhook_event:21", "to_asset_id": "repo_sync:30", "relation_type": "matched_repo_sync"},
			map[string]any{"from_asset_id": "webhook_event:21", "to_asset_id": "operation_run:40", "relation_type": "triggered_operation"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "repo_sync:30", "relation_type": "ran_repo_sync"},
		},
	})
	if got := readinessByKey(t, githubOnlyGraphChain, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 1 any-provider complete webhook chains / 0 webhook asset chains / canonical evidence missing" {
		t.Fatalf("sync trigger with GitHub-only webhook graph chain = %#v, want partial without Gitea canonical asset chain", got)
	}

	graphOnlyWebhookChain := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "source_id": "2", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_event", "source_id": "21", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "repo_sync", "source_id": "31"},
	}, []map[string]any{
		{"id": "41", "operation_type": "repo.sync_remote"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "webhook_connection:1", "to_asset_id": "webhook_event:20", "relation_type": "received_webhook_event"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "repo_sync:30", "relation_type": "matched_repo_sync"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "operation_run:40", "relation_type": "triggered_operation"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "repo_sync:30", "relation_type": "ran_repo_sync"},
		},
	})
	if got := readinessByKey(t, graphOnlyWebhookChain, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 1 any-provider complete webhook chains / 0 webhook asset chains / canonical evidence missing" {
		t.Fatalf("sync trigger with graph-only webhook chain = %#v, want partial without canonical asset chain", got)
	}

	nonSyncOperationChain := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "source_id": "1", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_event", "source_id": "20", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "repo_sync", "source_id": "30"},
	}, []map[string]any{
		{"id": "40", "operation_type": "repo.tag"},
		{"id": "41", "operation_type": "repo.sync_remote"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "webhook_connection:1", "to_asset_id": "webhook_event:20", "relation_type": "received_webhook_event"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "repo_sync:30", "relation_type": "matched_repo_sync"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "operation_run:40", "relation_type": "triggered_operation"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "repo_sync:30", "relation_type": "ran_repo_sync"},
		},
	})
	if got := readinessByKey(t, nonSyncOperationChain, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 1 any-provider complete webhook chains / 0 webhook asset chains / canonical evidence missing" {
		t.Fatalf("sync trigger with non-sync operation chain = %#v, want partial without sync operation asset chain", got)
	}

	withoutOperationRepoSyncClosure := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "webhook_connection", "source_id": "1", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "webhook_event", "source_id": "20", "metadata": map[string]any{"provider": "gitea"}},
		{"asset_type": "repo_sync", "source_id": "30"},
		{"asset_type": "repo_sync", "source_id": "31"},
	}, []map[string]any{
		{"id": "40", "operation_type": "repo.sync_remote"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "webhook_connection:1", "to_asset_id": "webhook_event:20", "relation_type": "received_webhook_event"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "repo_sync:30", "relation_type": "matched_repo_sync"},
			map[string]any{"from_asset_id": "webhook_event:20", "to_asset_id": "operation_run:40", "relation_type": "triggered_operation"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "repo_sync:31", "relation_type": "ran_repo_sync"},
		},
	})
	if got := readinessByKey(t, withoutOperationRepoSyncClosure, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 0 any-provider complete webhook chains / 0 webhook asset chains" {
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
	if got := readinessByKey(t, crossEventAggregation, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 0 any-provider complete webhook chains / 0 webhook asset chains" {
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
	if got := readinessByKey(t, sameEventClosureWithoutConnection, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 1 Gitea webhooks / 1 Gitea events / 0 any-provider complete webhook chains / 0 webhook asset chains" {
		t.Fatalf("sync trigger with closed event but missing connection = %#v, want partial", got)
	}

	eventOnly := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "webhook_event", "metadata": map[string]any{"provider": "gitea"}},
	}, nil, nil)
	if got := readinessByKey(t, eventOnly, "sync_trigger"); got.Status != "partial" || got.Evidence != "0 sync ops / 0 Gitea webhooks / 1 Gitea events / 0 any-provider complete webhook chains / 0 webhook asset chains" {
		t.Fatalf("sync trigger event only = %#v, want partial", got)
	}

	githubOnly := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "webhook_connection", "metadata": map[string]any{"provider": "github"}},
		{"asset_type": "webhook_event", "metadata": map[string]any{"provider": "github"}},
	}, []map[string]any{
		{"operation_type": "repo.sync"},
	}, nil)
	if got := readinessByKey(t, githubOnly, "sync_trigger"); got.Status != "partial" || got.Evidence != "1 sync ops / 0 Gitea webhooks / 0 Gitea events / 0 any-provider complete webhook chains / 0 webhook asset chains" {
		t.Fatalf("sync trigger with GitHub webhook evidence = %#v, want partial without Gitea evidence", got)
	}
}

func TestFirstVersionReadinessReportRequiresProjectGraphNode(t *testing.T) {
	withoutEvidence := firstVersionReadinessReportWithGraph(nil, nil, nil, nil)
	if got := readinessByKey(t, withoutEvidence, "project"); got.Status != "missing" || got.Evidence != "0 project assets / 0 project graph nodes / 0 project asset nodes" {
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
	if got := readinessByKey(t, withNilGraph, "project"); got.Status != "partial" || got.Evidence != "1 project assets / 0 project graph nodes / 0 project asset nodes" {
		t.Fatalf("project readiness with nil graph = %#v, want partial", got)
	} else {
		plan := mapFromAny(got.DemoDataRehearsalPlan)
		counts := intMapFromAny(plan["evidence_counts"])
		if plan["plan_state"] != "planned" ||
			counts["project_assets"] != 1 ||
			counts["project_graph_nodes"] != 0 ||
			counts["project_asset_nodes"] != 0 ||
			!containsString(stringSliceFromAny(plan["blocked_reasons"]), "live_demo_graph_evidence_incomplete") {
			t.Fatalf("project demo rehearsal plan with nil graph = %#v", plan)
		}
		assertDemoDataRehearsalPlanSafe(t, plan)
	}

	withoutGraphNode := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "project"},
	}, nil, nil, map[string]any{"nodes": []any{}})
	if got := readinessByKey(t, withoutGraphNode, "project"); got.Status != "partial" || got.Evidence != "1 project assets / 0 project graph nodes / 0 project asset nodes" {
		t.Fatalf("project readiness without graph node = %#v, want partial", got)
	} else {
		plan := mapFromAny(got.DemoDataRehearsalPlan)
		if plan["plan_state"] != "planned" ||
			!containsString(stringSliceFromAny(plan["blocked_reasons"]), "live_demo_graph_evidence_incomplete") {
			t.Fatalf("project demo rehearsal plan without graph node = %#v", plan)
		}
		assertDemoDataRehearsalPlanSafe(t, plan)
	}

	withGraphOnlyNode := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "project"},
	}, nil, nil, map[string]any{
		"nodes": []any{
			map[string]any{"id": "project:1"},
			map[string]any{"id": "repository:10"},
		},
	})
	if got := readinessByKey(t, withGraphOnlyNode, "project"); got.Status != "partial" || got.Evidence != "1 project assets / 1 project graph nodes / 0 project asset nodes" {
		t.Fatalf("project readiness with graph-only node = %#v, want partial without canonical project node", got)
	}

	withGraphNode := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "project", "source_id": "1"},
	}, nil, nil, map[string]any{
		"nodes": []any{
			map[string]any{"id": "project:1"},
			map[string]any{"id": "repository:10"},
		},
	})
	if got := readinessByKey(t, withGraphNode, "project"); got.Status != "ready" || got.Evidence != "1 project assets / 1 project graph nodes / 1 project asset nodes" {
		t.Fatalf("project readiness with graph node = %#v, want ready", got)
	} else {
		plan := mapFromAny(got.DemoDataRehearsalPlan)
		counts := intMapFromAny(plan["evidence_counts"])
		if plan["plan_state"] != "observed" ||
			plan["project_created"] != false ||
			plan["asset_graph_written"] != false ||
			counts["project_assets"] != 1 ||
			counts["project_graph_nodes"] != 1 ||
			counts["project_asset_nodes"] != 1 ||
			len(stringSliceFromAny(plan["blocked_reasons"])) != 0 {
			t.Fatalf("project demo rehearsal plan with graph node = %#v", plan)
		}
		assertDemoDataRehearsalPlanSafe(t, plan)
	}
}
