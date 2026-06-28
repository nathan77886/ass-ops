package main

import (
	"testing"
)

func TestCountGitHubActionGraphLinksFindsCanonicalLinkedTagRunAcrossMultipleMatches(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:1", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:1", "to_asset_id": "git_remote:1", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:1", "to_asset_id": "github_action_run:1", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "git_remote:1", "to_asset_id": "github_action_run:2", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:1", "to_asset_id": "git_remote:1", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed"}},
			map[string]any{"from_asset_id": "repo_tag_run:1", "to_asset_id": "github_action_run:1", "relation_type": "matched_action_run"},
			map[string]any{"from_asset_id": "repo_tag_run:1", "to_asset_id": "github_action_run:2", "relation_type": "matched_action_run"},
		},
	}
	got := countGitHubActionGraphLinks(
		graph,
		map[string]bool{"project:1": true},
		map[string]bool{"repository:1": true},
		map[string]bool{"git_remote:1": true},
		map[string]bool{"github_action_run:2": true},
		map[string]bool{"repo_tag_run:1": true},
		map[string]map[string]bool{"operation_run:1": {"repo_tag_run:1": true}},
		map[string]bool{"operation_run:1": true},
	)
	if got.LinkedTagRuns != 1 || got.LinkedTagRunAssets != 1 {
		t.Fatalf("countGitHubActionGraphLinks with mixed canonical matched actions = %#v, want one canonical linked tag asset", got)
	}
}

func TestCountGitHubActionGraphLinksRequiresSameTaggedRunForLinkedAction(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:1", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:1", "to_asset_id": "git_remote:1", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:1", "to_asset_id": "github_action_run:1", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:1", "to_asset_id": "git_remote:1", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed"}},
			map[string]any{"from_asset_id": "repo_tag_run:2", "to_asset_id": "github_action_run:1", "relation_type": "matched_action_run"},
		},
	}
	got := countGitHubActionGraphLinks(
		graph,
		map[string]bool{"project:1": true},
		map[string]bool{"repository:1": true},
		map[string]bool{"git_remote:1": true},
		map[string]bool{"github_action_run:1": true},
		map[string]bool{"repo_tag_run:1": true, "repo_tag_run:2": true},
		map[string]map[string]bool{"operation_run:1": {"repo_tag_run:1": true}},
		map[string]bool{"operation_run:1": true},
	)
	if got.CompleteTaggedRemoteAssets != 1 || got.LinkedTagRuns != 1 || got.LinkedTagRunAssets != 0 {
		t.Fatalf("countGitHubActionGraphLinks with different tagged and matched runs = %#v, want tag asset but no linked tag asset", got)
	}
}

func TestCountGitHubActionGraphLinksRequiresProjectLinkedTagActionRun(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "project:1", "to_asset_id": "repository:1", "relation_type": "owns"},
			map[string]any{"from_asset_id": "repository:1", "to_asset_id": "git_remote:1", "relation_type": "has_remote"},
			map[string]any{"from_asset_id": "git_remote:1", "to_asset_id": "github_action_run:1", "relation_type": "triggered_by"},
			map[string]any{"from_asset_id": "operation_run:1", "to_asset_id": "git_remote:1", "relation_type": "tagged_remote", "metadata": map[string]any{"status": "completed"}},
			map[string]any{"from_asset_id": "repo_tag_run:1", "to_asset_id": "github_action_run:2", "relation_type": "matched_action_run"},
		},
	}
	got := countGitHubActionGraphLinks(
		graph,
		map[string]bool{"project:1": true},
		map[string]bool{"repository:1": true},
		map[string]bool{"git_remote:1": true},
		map[string]bool{"github_action_run:1": true, "github_action_run:2": true},
		map[string]bool{"repo_tag_run:1": true},
		map[string]map[string]bool{"operation_run:1": {"repo_tag_run:1": true}},
		map[string]bool{"operation_run:1": true},
	)
	if got.CompleteActionRuns != 1 || got.CompleteActionAssets != 1 || got.CompleteTaggedRemotes != 1 || got.CompleteTaggedRemoteAssets != 1 || got.TagActionRunLinks != 1 || got.LinkedTagRuns != 0 || got.LinkedTagRunAssets != 0 {
		t.Fatalf("countGitHubActionGraphLinks with orphan matched action = %#v, want action link but no linked tag run", got)
	}
}

func TestCountSSHGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "operation_run:11", "to_asset_id": "ssh_machine:31", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "operation_run:12", "to_asset_id": "ssh_command_run:21", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "ssh_command_run:22", "to_asset_id": "ssh_machine:32", "relation_type": "executed_on"},
		},
	}
	got := countSSHGraphLinks(
		graph,
		map[string]bool{"ssh_command_run:20": true},
		map[string]bool{"ssh_machine:30": true},
		map[string]bool{"operation_run:10": true},
		map[string]bool{"operation_run:10": true},
		nil,
	)
	if got.OperationCommands != 1 || got.CommandMachines != 2 || got.CompleteCommands != 1 || got.CompleteCommandAssets != 1 || got.CompleteVerifyCommandAssets != 1 || got.CompleteRunCommandAssets != 0 {
		t.Fatalf("countSSHGraphLinks = %#v, want one operation-command, two command-machine, one complete command, and one command asset chain", got)
	}

	withoutKnownMachine := countSSHGraphLinks(
		graph,
		map[string]bool{"ssh_command_run:20": true},
		nil,
		map[string]bool{"operation_run:10": true},
		map[string]bool{"operation_run:10": true},
		nil,
	)
	if withoutKnownMachine.CompleteCommands != 1 || withoutKnownMachine.CompleteCommandAssets != 0 {
		t.Fatalf("countSSHGraphLinks without known machine = %#v, want graph command but no canonical asset chain", withoutKnownMachine)
	}

	withoutKnownOperation := countSSHGraphLinks(
		graph,
		map[string]bool{"ssh_command_run:20": true},
		map[string]bool{"ssh_machine:30": true},
		nil,
		nil,
		nil,
	)
	if withoutKnownOperation.CompleteCommands != 1 || withoutKnownOperation.CompleteCommandAssets != 0 {
		t.Fatalf("countSSHGraphLinks without known operation = %#v, want graph command but no canonical asset chain", withoutKnownOperation)
	}
}

func TestCountArgoGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:10", "relation_type": "synced_argo_connection"},
			map[string]any{"from_asset_id": "deployment_target:30", "to_asset_id": "argo_app:20", "relation_type": "hosts"},
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "deployment_target:30", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:21", "to_asset_id": "deployment_target:31", "relation_type": "deployed_to"},
		},
	}
	got := countArgoGraphLinks(graph, map[string]bool{"argo_connection:10": true}, map[string]bool{"argo_app:20": true}, map[string]bool{"deployment_target:30": true}, map[string]bool{"operation_run:40": true})
	if got.ConnectionApps != 1 || got.AppTargets != 2 || got.CompleteApps != 1 || got.CompleteAppAssets != 1 {
		t.Fatalf("countArgoGraphLinks = %#v, want one connection-app, two app-targets, one complete app, and one app asset chain", got)
	}
}

func TestCountArgoGraphLinksRequiresSyncedConnection(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:11", "relation_type": "synced_argo_connection"},
		},
	}
	got := countArgoGraphLinks(graph, map[string]bool{"argo_connection:10": true}, map[string]bool{"argo_app:20": true}, map[string]bool{"deployment_target:30": true}, map[string]bool{"operation_run:40": true})
	if got.ConnectionApps != 1 || got.AppTargets != 1 || got.CompleteApps != 0 || got.CompleteAppAssets != 0 {
		t.Fatalf("countArgoGraphLinks with unrelated synced connection = %#v, want no complete app", got)
	}
}

func TestCountArgoGraphLinksAllowsOneSyncedConnectionForSharedApp(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "argo_connection:10", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_connection:11", "to_asset_id": "argo_app:20", "relation_type": "manages"},
			map[string]any{"from_asset_id": "argo_app:20", "to_asset_id": "deployment_target:30", "relation_type": "deployed_to"},
			map[string]any{"from_asset_id": "operation_run:40", "to_asset_id": "argo_connection:10", "relation_type": "synced_argo_connection"},
		},
	}
	got := countArgoGraphLinks(graph, map[string]bool{"argo_connection:10": true}, map[string]bool{"argo_app:20": true}, map[string]bool{"deployment_target:30": true}, map[string]bool{"operation_run:40": true})
	if got.ConnectionApps != 2 || got.AppTargets != 1 || got.CompleteApps != 1 || got.CompleteAppAssets != 1 {
		t.Fatalf("countArgoGraphLinks with shared app and one synced connection = %#v, want one complete app and one app asset chain", got)
	}
}

func TestCountApprovalGraphLinks(t *testing.T) {
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_approval_rule:10", "to_asset_id": "operation_approval:20", "relation_type": "governs"},
			map[string]any{"from_asset_id": "operation_approval:20", "to_asset_id": "operation_run:30", "relation_type": "gates_operation"},
			map[string]any{"from_asset_id": "operation_approval_rule:11", "to_asset_id": "operation_approval:21", "relation_type": "governs"},
			map[string]any{"from_asset_id": "operation_approval:21", "to_asset_id": "operation_approval_rule:12", "relation_type": "gates_operation"},
		},
	}
	got := countApprovalGraphLinks(
		graph,
		map[string]bool{"operation_approval_rule:10": true},
		map[string]bool{"operation_approval:20": true},
		map[string]bool{"operation_run:30": true},
		map[string]bool{"operation_run:30": true},
	)
	if got.RuleApprovals != 1 || got.ApprovalOperations != 1 || got.CompleteApprovalChains != 1 || got.CompleteApprovalAssetChains != 1 {
		t.Fatalf("countApprovalGraphLinks = %#v, want one complete approval asset chain", got)
	}

	crossApproval := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_approval_rule:10", "to_asset_id": "operation_approval:20", "relation_type": "governs"},
			map[string]any{"from_asset_id": "operation_approval:21", "to_asset_id": "operation_run:30", "relation_type": "gates_operation"},
		},
	}
	got = countApprovalGraphLinks(
		crossApproval,
		map[string]bool{"operation_approval_rule:10": true},
		map[string]bool{"operation_approval:20": true, "operation_approval:21": true},
		map[string]bool{"operation_run:30": true},
		map[string]bool{"operation_run:30": true},
	)
	if got.RuleApprovals != 1 || got.ApprovalOperations != 1 || got.CompleteApprovalChains != 0 || got.CompleteApprovalAssetChains != 0 {
		t.Fatalf("countApprovalGraphLinks with cross-approval links = %#v, want no complete chain", got)
	}

	splitOperationEvidence := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_approval_rule:10", "to_asset_id": "operation_approval:20", "relation_type": "governs"},
			map[string]any{"from_asset_id": "operation_approval:20", "to_asset_id": "operation_run:30", "relation_type": "gates_operation"},
			map[string]any{"from_asset_id": "operation_approval:20", "to_asset_id": "operation_run:31", "relation_type": "gates_operation"},
		},
	}
	got = countApprovalGraphLinks(
		splitOperationEvidence,
		map[string]bool{"operation_approval_rule:10": true},
		map[string]bool{"operation_approval:20": true},
		map[string]bool{"operation_run:30": true},
		map[string]bool{"operation_run:31": true},
	)
	if got.CompleteApprovalChains != 1 || got.CompleteApprovalAssetChains != 0 {
		t.Fatalf("countApprovalGraphLinks with split operation evidence = %#v, want complete graph chain without asset chain", got)
	}

	got = countApprovalGraphLinks(
		splitOperationEvidence,
		map[string]bool{"operation_approval_rule:10": true},
		map[string]bool{"operation_approval:20": true},
		map[string]bool{"operation_run:30": true},
		map[string]bool{"operation_run:30": true, "operation_run:31": true},
	)
	if got.CompleteApprovalChains != 1 || got.CompleteApprovalAssetChains != 1 {
		t.Fatalf("countApprovalGraphLinks with overlapping operation evidence = %#v, want complete asset chain", got)
	}
}
