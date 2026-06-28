package main

import (
	"testing"
)

func TestFirstVersionReadinessReportRequiresGitHubActionGraphLink(t *testing.T) {
	withoutLink := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, nil, nil, map[string]any{
		"edges": []any{},
	})
	if got := readinessByKey(t, withoutLink, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 0 complete action chains / 0 action asset chains / 0 tag ops / 0 complete tag links / 0 tag asset links / 0 linked tag runs / 0 linked tag assets / 0 project links / 0 remote links / 0 action links / 0 tag links / 0 tag-action links" {
		t.Fatalf("github actions without graph link = %#v, want partial with link evidence", got)
	}

	withActionLinkOnly := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{
				"from_asset_id": "git_remote:42",
				"to_asset_id":   "github_action_run:101",
				"relation_type": "triggered_by",
			},
		},
	})
	if got := readinessByKey(t, withActionLinkOnly, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 0 complete action chains / 0 action asset chains / 0 tag ops / 0 complete tag links / 0 tag asset links / 0 linked tag runs / 0 linked tag assets / 0 project links / 0 remote links / 1 action links / 0 tag links / 0 tag-action links" {
		t.Fatalf("github actions with action link only = %#v, want partial without project chain", got)
	}

	withCompleteChain := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:42", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:42", "to_asset_id": "github_action_run:101", "relation_type": "triggered_by"},
		},
	})
	if got := readinessByKey(t, withCompleteChain, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 1 complete action chains / 0 action asset chains / 0 tag ops / 0 complete tag links / 0 tag asset links / 0 linked tag runs / 0 linked tag assets / 1 project links / 1 remote links / 1 action links / 0 tag links / 0 tag-action links" {
		t.Fatalf("github actions with complete project chain but no tag = %#v, want partial", got)
	}

	withFailedTag := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, []map[string]any{
		{"operation_type": "repo.create_tag"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:42", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:42", "to_asset_id": "github_action_run:101", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:201", "to_asset_id": "git_remote:42", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "failed", "tag_name": "v1.0.0"}},
		},
	})
	if got := readinessByKey(t, withFailedTag, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 1 complete action chains / 0 action asset chains / 1 tag ops / 0 complete tag links / 0 tag asset links / 0 linked tag runs / 0 linked tag assets / 1 project links / 1 remote links / 1 action links / 0 tag links / 0 tag-action links" {
		t.Fatalf("github actions with failed tag = %#v, want partial without successful tag link", got)
	}

	withCompleteChainAndTag := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, []map[string]any{
		{"operation_type": "repo.create_tag"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:42", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:42", "to_asset_id": "github_action_run:101", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:201", "to_asset_id": "git_remote:42", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed", "tag_name": "v1.0.0"}},
		},
	})
	if got := readinessByKey(t, withCompleteChainAndTag, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 1 complete action chains / 0 action asset chains / 1 tag ops / 1 complete tag links / 0 tag asset links / 0 linked tag runs / 0 linked tag assets / 1 project links / 1 remote links / 1 action links / 1 tag links / 0 tag-action links" {
		t.Fatalf("github actions with complete project chain and tag but no action match = %#v, want partial", got)
	}

	withOrphanActionMatch := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run"},
	}, []map[string]any{
		{"operation_type": "repo.create_tag"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:42", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:42", "to_asset_id": "github_action_run:101", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:201", "to_asset_id": "git_remote:42", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed", "tag_name": "v1.0.0"}},
			map[string]any{"from_asset_id": "repo_tag_run:201", "to_asset_id": "github_action_run:202", "relation_type": "matched_action_run"},
		},
	})
	if got := readinessByKey(t, withOrphanActionMatch, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 1 complete action chains / 0 action asset chains / 1 tag ops / 1 complete tag links / 0 tag asset links / 0 linked tag runs / 0 linked tag assets / 1 project links / 1 remote links / 1 action links / 1 tag links / 1 tag-action links" {
		t.Fatalf("github actions with tag matched to orphan action = %#v, want partial without project-linked tag run", got)
	}

	graphOnlyCompleteChainTagAndActionMatch := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "pipeline_run", "source_id": "102"},
		{"asset_type": "git_remote", "source_id": "43"},
		{"asset_type": "repo_tag_run", "source_id": "202"},
	}, []map[string]any{
		{"id": "202", "operation_type": "repo.create_tag"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:42", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:42", "to_asset_id": "github_action_run:101", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:201", "to_asset_id": "git_remote:42", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed", "tag_name": "v1.0.0"}},
			map[string]any{"from_asset_id": "repo_tag_run:201", "to_asset_id": "github_action_run:101", "relation_type": "matched_action_run"},
		},
	})
	if got := readinessByKey(t, graphOnlyCompleteChainTagAndActionMatch, "github_actions"); got.Status != "partial" || got.Evidence != "1 pipeline runs / 1 complete action chains / 0 action asset chains / 1 tag ops / 1 complete tag links / 0 tag asset links / 1 linked tag runs / 0 linked tag assets / 1 project links / 1 remote links / 1 action links / 1 tag links / 1 tag-action links" {
		t.Fatalf("github actions with graph-only complete tag/action chain = %#v, want partial without canonical asset chains", got)
	}

	withCompleteChainTagAndActionMatch := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "project", "source_id": "1"},
		{"asset_type": "repository", "source_id": "10"},
		{"asset_type": "pipeline_run", "source_id": "101"},
		{"asset_type": "git_remote", "source_id": "42"},
		{"asset_type": "repo_tag_run", "source_id": "201", "metadata": map[string]any{"operation_run_id": "201"}},
	}, []map[string]any{
		{"id": "201", "operation_type": "repo.create_tag"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:10", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:10", "to_asset_id": "git_remote:42", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:42", "to_asset_id": "github_action_run:101", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:201", "to_asset_id": "git_remote:42", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed", "tag_name": "v1.0.0"}},
			map[string]any{"from_asset_id": "repo_tag_run:201", "to_asset_id": "github_action_run:101", "relation_type": "matched_action_run"},
		},
	})
	if got := readinessByKey(t, withCompleteChainTagAndActionMatch, "github_actions"); got.Status != "ready" || got.Evidence != "1 pipeline runs / 1 complete action chains / 1 action asset chains / 1 tag ops / 1 complete tag links / 1 tag asset links / 1 linked tag runs / 1 linked tag assets / 1 project links / 1 remote links / 1 action links / 1 tag links / 1 tag-action links" {
		t.Fatalf("github actions with complete project chain, tag, and action match = %#v, want ready", got)
	}

	wrongLink := firstVersionReadinessReportWithGraph(nil, nil, nil, map[string]any{
		"edges": []any{
			map[string]any{
				"from_asset_id": "repository:repo-1",
				"to_asset_id":   "github_action_run:run-1",
				"relation_type": "owns",
			},
		},
	})
	if got := readinessByKey(t, wrongLink, "github_actions"); got.Status != "missing" || got.Evidence != "0 pipeline runs / 0 complete action chains / 0 action asset chains / 0 tag ops / 0 complete tag links / 0 tag asset links / 0 linked tag runs / 0 linked tag assets / 0 project links / 0 remote links / 0 action links / 0 tag links / 0 tag-action links" {
		t.Fatalf("github actions with unrelated graph edge = %#v, want missing", got)
	}
}

func TestCountGitHubActionGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:1", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:1", "to_asset_id": "git_remote:1", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:1", "to_asset_id": "github_action_run:1", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "repository:2", "to_asset_id": "git_remote:2", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:2", "to_asset_id": "github_action_run:2", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "git_remote:2", "to_asset_id": "github_action_run:2", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:1", "to_asset_id": "github_action_run:3", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:1", "to_asset_id": "git_remote:1", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed"}},
			map[string]any{"from_asset_id": "operation_run:2", "to_asset_id": "git_remote:2", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed"}},
			map[string]any{"from_asset_id": "operation_run:3", "to_asset_id": "git_remote:1", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "failed"}},
			map[string]any{"from_asset_id": "repo_tag_run:1", "to_asset_id": "github_action_run:1", "relation_type": "matched_action_run"},
			map[string]any{"from_asset_id": "repo_tag_run:1", "to_asset_id": "github_action_run:2", "relation_type": "matched_action_run"},
			map[string]any{"from_asset_id": "repo_tag_run:2", "to_asset_id": "repository:1", "relation_type": "matched_action_run"},
		},
	}
	got := countGitHubActionGraphLinks(
		graph,
		map[string]bool{"project:1": true},
		map[string]bool{"repository:1": true},
		map[string]bool{"git_remote:1": true},
		map[string]bool{"github_action_run:1": true},
		map[string]bool{"repo_tag_run:1": true},
		map[string]map[string]bool{"operation_run:1": {"repo_tag_run:1": true}},
		map[string]bool{"operation_run:1": true},
	)
	if got.ProjectRepositories != 1 || got.RepositoryRemotes != 2 || got.RemoteActionRuns != 2 || got.TaggedRemotes != 2 || got.TagActionRunLinks != 2 || got.CompleteActionRuns != 1 || got.CompleteActionAssets != 1 || got.CompleteTaggedRemotes != 1 || got.CompleteTaggedRemoteAssets != 1 || got.LinkedTagRuns != 1 || got.LinkedTagRunAssets != 1 {
		t.Fatalf("countGitHubActionGraphLinks = %#v, want project/remote/action/tag counts and one complete action/tag chain", got)
	}
}

func TestCountGitHubActionGraphLinksRequiresCanonicalRemoteAndActionAssets(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:1", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:1", "to_asset_id": "git_remote:1", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:1", "to_asset_id": "github_action_run:1", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:1", "to_asset_id": "git_remote:1", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed"}},
		},
	}
	remoteOnly := countGitHubActionGraphLinks(
		graph,
		map[string]bool{"project:1": true},
		map[string]bool{"repository:1": true},
		map[string]bool{"git_remote:1": true},
		map[string]bool{"github_action_run:2": true},
		nil,
		nil,
		map[string]bool{"operation_run:1": true},
	)
	if remoteOnly.CompleteActionRuns != 1 || remoteOnly.CompleteActionAssets != 0 || remoteOnly.CompleteTaggedRemoteAssets != 0 {
		t.Fatalf("countGitHubActionGraphLinks with only canonical remote = %#v, want graph action but no action or tag asset chain", remoteOnly)
	}

	withTagRunAsset := countGitHubActionGraphLinks(
		graph,
		map[string]bool{"project:1": true},
		map[string]bool{"repository:1": true},
		map[string]bool{"git_remote:1": true},
		nil,
		map[string]bool{"repo_tag_run:1": true},
		map[string]map[string]bool{"operation_run:1": {"repo_tag_run:1": true}},
		map[string]bool{"operation_run:1": true},
	)
	if withTagRunAsset.CompleteTaggedRemoteAssets != 1 {
		t.Fatalf("countGitHubActionGraphLinks with canonical tag operation and tag run asset = %#v, want canonical tag asset chain", withTagRunAsset)
	}

	actionOnly := countGitHubActionGraphLinks(
		graph,
		map[string]bool{"project:1": true},
		map[string]bool{"repository:1": true},
		map[string]bool{"git_remote:2": true},
		map[string]bool{"github_action_run:1": true},
		nil,
		nil,
		map[string]bool{"operation_run:1": true},
	)
	if actionOnly.CompleteActionRuns != 1 || actionOnly.CompleteActionAssets != 0 || actionOnly.CompleteTaggedRemoteAssets != 0 {
		t.Fatalf("countGitHubActionGraphLinks with only canonical action = %#v, want graph action but no canonical remote-backed chains", actionOnly)
	}

	withoutProjectRepositoryAssets := countGitHubActionGraphLinks(
		graph,
		nil,
		nil,
		map[string]bool{"git_remote:1": true},
		map[string]bool{"github_action_run:1": true},
		nil,
		nil,
		map[string]bool{"operation_run:1": true},
	)
	if withoutProjectRepositoryAssets.CompleteActionRuns != 1 || withoutProjectRepositoryAssets.CompleteActionAssets != 0 || withoutProjectRepositoryAssets.CompleteTaggedRemoteAssets != 0 {
		t.Fatalf("countGitHubActionGraphLinks without canonical project/repository assets = %#v, want graph action but no canonical asset chains", withoutProjectRepositoryAssets)
	}
}

func TestCountGitHubActionGraphLinksIgnoresInvalidTagActionTarget(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "repo_tag_run:1", "to_asset_id": "repository:1", "relation_type": "matched_action_run"},
			map[string]any{"from_asset_id": "operation_run:1", "to_asset_id": "github_action_run:1", "relation_type": "matched_action_run"},
		},
	}
	got := countGitHubActionGraphLinks(graph, nil, nil, nil, nil, nil, nil, nil)
	if got.TagActionRunLinks != 0 || got.LinkedTagRuns != 0 || got.LinkedTagRunAssets != 0 {
		t.Fatalf("countGitHubActionGraphLinks with invalid tag-action targets = %#v, want no tag-action evidence", got)
	}
}
