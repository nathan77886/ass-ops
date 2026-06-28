package main

import (
	"testing"
)

func TestFirstVersionReadinessReportRequiresRepositoryGraphLinks(t *testing.T) {
	withoutGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository", "source_id": "10"},
		{"asset_type": "git_remote", "source_id": "100"},
		{"asset_type": "git_remote", "source_id": "101"},
	}, nil, nil, map[string]any{
		"edges": []any{},
	})
	if got := readinessByKey(t, withoutGraphLinks, "repositories"); got.Status != "partial" || got.Evidence != "1 repos / 2 remotes / 0 complete repos / 0 repo asset paths / 0 project links / 0 remote links" {
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
		{"asset_type": "project", "source_id": "1"},
		{"asset_type": "repository", "source_id": "10"},
		{"asset_type": "git_remote", "source_id": "100"},
		{"asset_type": "git_remote", "source_id": "101"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:100", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:101", "relation_type": "has_remote"},
		},
	})
	if got := readinessByKey(t, withGraphLinks, "repositories"); got.Status != "ready" || got.Evidence != "1 repos / 2 remotes / 1 complete repos / 1 repo asset paths / 1 project links / 2 remote links" {
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

	withUnmatchedAssets := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository", "source_id": "11"},
		{"asset_type": "git_remote", "source_id": "102"},
		{"asset_type": "git_remote", "source_id": "103"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:100", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:101", "relation_type": "has_remote"},
		},
	})
	if got := readinessByKey(t, withUnmatchedAssets, "repositories"); got.Status != "partial" || got.Evidence != "1 repos / 2 remotes / 1 complete repos / 0 repo asset paths / 1 project links / 2 remote links" {
		t.Fatalf("repository readiness with unmatched canonical assets = %#v, want partial without repo asset path", got)
	} else {
		proof := mapFromAny(mapFromAny(got.DemoDataRehearsalPlan)["environment_demo_proof"])
		if proof["complete_repository_multi_remote_path_observed"] != false ||
			!containsString(stringSliceFromAny(proof["missing_evidence"]), "repository_to_two_remotes_graph_path") {
			t.Fatalf("unmatched repository asset proof should not report complete multi-remote path: %#v", proof)
		}
	}

	crossRepositoryAggregation := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository", "source_id": "10"},
		{"asset_type": "repository", "source_id": "11"},
		{"asset_type": "git_remote", "source_id": "100"},
		{"asset_type": "git_remote", "source_id": "101"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:11", "to_asset_id": "git_remote:100", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "repository:11", "to_asset_id": "git_remote:101", "relation_type": "has_remote"},
		},
	})
	if got := readinessByKey(t, crossRepositoryAggregation, "repositories"); got.Status != "partial" || got.Evidence != "2 repos / 2 remotes / 0 complete repos / 0 repo asset paths / 1 project links / 2 remote links" {
		t.Fatalf("repository readiness with cross-repository aggregate links = %#v, want partial without a complete repository", got)
	} else {
		proof := mapFromAny(mapFromAny(got.DemoDataRehearsalPlan)["environment_demo_proof"])
		if proof["complete_repository_multi_remote_path_observed"] != false ||
			!containsString(stringSliceFromAny(proof["missing_evidence"]), "repository_to_two_remotes_graph_path") {
			t.Fatalf("cross-repository aggregate proof should not report a complete multi-remote path: %#v", proof)
		}
	}

	unrelatedGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "repository", "source_id": "10"},
		{"asset_type": "git_remote", "source_id": "100"},
		{"asset_type": "git_remote", "source_id": "101"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "git_remote:100", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "webhook_connection:1", "relation_type": "has_remote"},
		},
	})
	if got := readinessByKey(t, unrelatedGraphLinks, "repositories"); got.Status != "partial" || got.Evidence != "1 repos / 2 remotes / 0 complete repos / 0 repo asset paths / 0 project links / 0 remote links" {
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
	got := countRepositoryGraphLinks(graph, map[string]bool{"project:1": true}, map[string]bool{"repository:10": true}, map[string]bool{"git_remote:100": true, "git_remote:101": true})
	if got.ProjectRepository != 1 || got.RepositoryRemotes != 2 || got.CompleteRepos != 1 || got.CompleteRepoAssets != 1 {
		t.Fatalf("countRepositoryGraphLinks = %#v, want 1 project link, 2 remote links, 1 complete repo, and 1 repo asset path", got)
	}

	got = countRepositoryGraphLinks(graph, nil, map[string]bool{"repository:10": true}, map[string]bool{"git_remote:100": true, "git_remote:101": true})
	if got.CompleteRepos != 1 || got.CompleteRepoAssets != 0 {
		t.Fatalf("countRepositoryGraphLinks without canonical project asset = %#v, want graph-complete repo without repo asset path", got)
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
