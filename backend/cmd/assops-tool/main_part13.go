package main

import (
	"fmt"
	"strings"
)

func firstVersionReadinessReportWithGraph(assets, operations []map[string]any, approvals, graph map[string]any) map[string]any {
	assetCounts := countAPIField(assets, "asset_type")
	operationCounts := countAPIField(operations, "operation_type")
	syncTriggered := operationCounts["repo.sync"] + operationCounts["repo.sync_remote"]
	giteaWebhooks := countAPITypeMetadata(assets, "webhook_connection", "provider", "gitea")
	giteaWebhookEvents := countAPITypeMetadata(assets, "webhook_event", "provider", "gitea")
	sshVerifyRuns := operationCounts["ssh.verify"]
	sshCommandRuns := operationCounts["ssh.exec"] + operationCounts["ssh.command"]
	approvalEvidence := intFromAPI(approvals["total"])
	pendingApprovalOps := countAPIStatus(operations, "pending_approval")
	approvalAssets := assetCounts["operation_approval"]
	activeApprovalRules := countAPITypeStatus(assets, "operation_approval_rule", "active")
	operationAssets := assetCounts["operation_run"]
	listedOperationRuns := len(operations)
	operationLogs := countOperationRowsWithLogs(operations, assetIDsByType(assets, "operation_run"))
	contextEvidence := assetCounts["agent_task"] + assetCounts["ai_runtime"]
	contextGenerations := countContextGenerationEvidence(assets)
	graphNodes := len(apiItemsByKey(graph, "nodes"))
	graphEdges := len(apiItemsByKey(graph, "edges"))
	graphEvidence := graphNodes + graphEdges
	projectGraphNodes := countGraphNodesByPrefix(graph, "project:")
	projectAssetGraphNodes := countGraphNodesByKnownIDs(graph, assetIDsByType(assets, "project"))
	repositoryGraphLinks := countRepositoryGraphLinks(graph, assetIDsByType(assets, "project"), assetIDsByType(assets, "repository"), assetIDsByType(assets, "git_remote"))
	repoSyncGraphLinks := countRepoSyncGraphLinks(graph, assetIDsByType(assets, "repository"), assetIDsByType(assets, "repo_sync"), assetIDsByType(assets, "git_remote"))
	syncOperationIDs := mergeBoolMaps(operationIDsByType(operations, "repo.sync"), operationIDsByType(operations, "repo.sync_remote"))
	webhookSyncGraphLinks := countWebhookSyncGraphLinks(
		graph,
		assetIDsByTypeMetadata(assets, "webhook_connection", "provider", "gitea"),
		assetIDsByTypeMetadata(assets, "webhook_event", "provider", "gitea"),
		assetIDsByType(assets, "repo_sync"),
		syncOperationIDs,
	)
	tagOperationIDs := mergeBoolMaps(operationIDsByType(operations, "repo.tag"), operationIDsByType(operations, "repo.create_tag"))
	githubActionLinks := countGitHubActionGraphLinks(
		graph,
		assetIDsByType(assets, "project"),
		assetIDsByType(assets, "repository"),
		assetIDsByType(assets, "git_remote"),
		assetIDsByGraphType(assets, "pipeline_run", "github_action_run"),
		assetIDsByType(assets, "repo_tag_run"),
		tagRunAssetIDsByOperation(assets),
		tagOperationIDs,
	)
	repoTagRuns := operationCounts["repo.tag"] + operationCounts["repo.create_tag"]
	sshVerifyOperationIDs := operationIDsByType(operations, "ssh.verify")
	sshRunOperationIDs := mergeBoolMaps(operationIDsByType(operations, "ssh.exec"), operationIDsByType(operations, "ssh.command"))
	sshOperationIDs := mergeBoolMaps(sshVerifyOperationIDs, sshRunOperationIDs)
	sshMachineAssetIDs := mergeBoolMaps(assetIDsByGraphType(assets, "host", "ssh_machine"), assetIDsByType(assets, "ssh_machine"))
	sshMachineAssets := assetCounts["host"] + assetCounts["ssh_machine"]
	sshGraphLinks := countSSHGraphLinks(graph, assetIDsByType(assets, "ssh_command_run"), sshMachineAssetIDs, sshOperationIDs, sshVerifyOperationIDs, sshRunOperationIDs)
	argoGraphLinks := countArgoGraphLinks(
		graph,
		assetIDsByType(assets, "argo_connection"),
		assetIDsByType(assets, "argo_app"),
		assetIDsByType(assets, "deployment_target"),
		operationIDsByType(operations, "argo.apps.sync"),
	)
	activeApprovalRuleIDs := activeAssetIDsByTypeStatus(assets, "operation_approval_rule", "active")
	approvalGraphLinks := countApprovalGraphLinks(
		graph,
		activeApprovalRuleIDs,
		assetIDsByType(assets, "operation_approval"),
		assetIDsByType(assets, "operation_run"),
		operationIDsByStatus(operations, "pending_approval"),
	)
	contextGraphLinks := countContextGraphLinks(assets, graph)
	argoEvidence := assetCounts["argo_connection"] + assetCounts["argo_app"] + assetCounts["deployment_target"] + operationCounts["argo.apps.sync"] + argoGraphLinks.ConnectionApps + argoGraphLinks.AppTargets + argoGraphLinks.CompleteAppAssets

	projectRow := readinessItem("project", "Create/import project asset", "Create a project or run the demo seed.", assetCounts["project"] > 0 && projectAssetGraphNodes > 0, fmt.Sprintf("%d project assets / %d project graph nodes / %d project asset nodes", assetCounts["project"], projectGraphNodes, projectAssetGraphNodes), assetCounts["project"] > 0 || projectGraphNodes > 0 || projectAssetGraphNodes > 0)
	projectRow.DemoDataRehearsalPlan = projectDemoDataRehearsalPlan(projectRow.Status, map[string]int{
		"project_assets":      assetCounts["project"],
		"project_graph_nodes": projectGraphNodes,
		"project_asset_nodes": projectAssetGraphNodes,
	}, []string{"project_asset", "project_asset_node"})
	repositoriesRow := readinessItem("repositories", "Attach source and mirror repositories", "Add repository metadata and at least two Git remotes.", assetCounts["repository"] > 0 && assetCounts["git_remote"] >= 2 && repositoryGraphLinks.CompleteRepoAssets > 0, fmt.Sprintf("%d repos / %d remotes / %d complete repos / %d repo asset paths / %d project links / %d remote links", assetCounts["repository"], assetCounts["git_remote"], repositoryGraphLinks.CompleteRepos, repositoryGraphLinks.CompleteRepoAssets, repositoryGraphLinks.ProjectRepository, repositoryGraphLinks.RepositoryRemotes), assetCounts["repository"] > 0 || assetCounts["git_remote"] > 0 || repositoryGraphLinks.ProjectRepository > 0 || repositoryGraphLinks.RepositoryRemotes > 0 || repositoryGraphLinks.CompleteRepoAssets > 0)
	repositoriesRow.DemoDataRehearsalPlan = projectDemoDataRehearsalPlan(repositoriesRow.Status, map[string]int{
		"repository_assets":         assetCounts["repository"],
		"git_remote_assets":         assetCounts["git_remote"],
		"complete_repository_paths": repositoryGraphLinks.CompleteRepoAssets,
		"project_repository_links":  repositoryGraphLinks.ProjectRepository,
		"repository_remote_links":   repositoryGraphLinks.RepositoryRemotes,
	}, []string{"repository_asset", "two_git_remote_assets", "project_to_repository_graph_link", "repository_to_two_remotes_graph_path"})
	argoEvidenceText := fmt.Sprintf("%d targets / %d Argo connections / %d apps / %d sync ops / %d complete app links / %d app asset chains", assetCounts["deployment_target"], assetCounts["argo_connection"], assetCounts["argo_app"], operationCounts["argo.apps.sync"], argoGraphLinks.CompleteApps, argoGraphLinks.CompleteAppAssets)
	if argoGraphLinks.CompleteApps > 0 && argoGraphLinks.CompleteAppAssets == 0 {
		argoEvidenceText += " / canonical evidence missing"
	}
	syncTriggerEvidenceText := fmt.Sprintf("%d sync ops / %d Gitea webhooks / %d Gitea events / %d any-provider complete webhook chains / %d webhook asset chains", syncTriggered, giteaWebhooks, giteaWebhookEvents, webhookSyncGraphLinks.CompleteChains, webhookSyncGraphLinks.CompleteChainAssets)
	if webhookSyncGraphLinks.CompleteChains > 0 && webhookSyncGraphLinks.CompleteChainAssets == 0 {
		syncTriggerEvidenceText += " / canonical evidence missing"
	}
	rows := []readinessRow{
		projectRow,
		repositoriesRow,
		readinessItem("repo_sync", "Define RepoSyncAsset", "Create a RepoSyncAsset between source and mirror remotes.", assetCounts["repo_sync"] > 0 && repoSyncGraphLinks.CompleteSyncAssets > 0, fmt.Sprintf("%d repo syncs / %d graph-complete syncs / %d sync asset paths / %d repository links / %d source links / %d target links", assetCounts["repo_sync"], repoSyncGraphLinks.CompleteSyncs, repoSyncGraphLinks.CompleteSyncAssets, repoSyncGraphLinks.RepositorySync, repoSyncGraphLinks.SourceRemotes, repoSyncGraphLinks.TargetRemotes), assetCounts["repo_sync"] > 0 || repoSyncGraphLinks.RepositorySync > 0 || repoSyncGraphLinks.SourceRemotes > 0 || repoSyncGraphLinks.TargetRemotes > 0 || repoSyncGraphLinks.CompleteSyncAssets > 0),
		readinessItem("sync_trigger", "Trigger sync manually and from webhook", "Run a manual sync and receive or replay a Gitea webhook event.", syncTriggered > 0 && giteaWebhooks > 0 && giteaWebhookEvents > 0 && webhookSyncGraphLinks.CompleteChainAssets > 0, syncTriggerEvidenceText, syncTriggered > 0 || giteaWebhooks > 0 || giteaWebhookEvents > 0 || webhookSyncGraphLinks.ConnectionEvents > 0 || webhookSyncGraphLinks.EventRepoSyncs > 0 || webhookSyncGraphLinks.EventOperations > 0 || webhookSyncGraphLinks.CompleteChainAssets > 0),
		readinessItem("github_actions", "See GitHub tags and Actions state", "Create a repository tag and sync GitHub Actions for the mirror remote or receive workflow_run webhooks.", assetCounts["pipeline_run"] > 0 && githubActionLinks.CompleteActionAssets > 0 && repoTagRuns > 0 && githubActionLinks.CompleteTaggedRemoteAssets > 0 && githubActionLinks.LinkedTagRunAssets > 0, fmt.Sprintf("%d pipeline runs / %d complete action chains / %d action asset chains / %d tag ops / %d complete tag links / %d tag asset links / %d linked tag runs / %d linked tag assets / %d project links / %d remote links / %d action links / %d tag links / %d tag-action links", assetCounts["pipeline_run"], githubActionLinks.CompleteActionRuns, githubActionLinks.CompleteActionAssets, repoTagRuns, githubActionLinks.CompleteTaggedRemotes, githubActionLinks.CompleteTaggedRemoteAssets, githubActionLinks.LinkedTagRuns, githubActionLinks.LinkedTagRunAssets, githubActionLinks.ProjectRepositories, githubActionLinks.RepositoryRemotes, githubActionLinks.RemoteActionRuns, githubActionLinks.TaggedRemotes, githubActionLinks.TagActionRunLinks), assetCounts["pipeline_run"] > 0 || repoTagRuns > 0 || githubActionLinks.ProjectRepositories > 0 || githubActionLinks.RepositoryRemotes > 0 || githubActionLinks.RemoteActionRuns > 0 || githubActionLinks.TaggedRemotes > 0 || githubActionLinks.TagActionRunLinks > 0 || githubActionLinks.CompleteActionAssets > 0 || githubActionLinks.CompleteTaggedRemoteAssets > 0 || githubActionLinks.LinkedTagRunAssets > 0),
		readinessItem("ssh", "Register SSH machines and audited commands", "Verify an SSH machine, then run an approval-gated command.", sshMachineAssets > 0 && sshVerifyRuns > 0 && sshCommandRuns > 0 && sshGraphLinks.CompleteVerifyCommandAssets > 0 && sshGraphLinks.CompleteRunCommandAssets > 0, fmt.Sprintf("%d machines / %d verify ops / %d command ops / %d command assets / %d complete audit chains / %d command asset chains / %d verify chains / %d run chains", sshMachineAssets, sshVerifyRuns, sshCommandRuns, assetCounts["ssh_command_run"], sshGraphLinks.CompleteCommands, sshGraphLinks.CompleteCommandAssets, sshGraphLinks.CompleteVerifyCommandAssets, sshGraphLinks.CompleteRunCommandAssets), sshMachineAssets > 0 || sshVerifyRuns > 0 || sshCommandRuns > 0 || assetCounts["ssh_command_run"] > 0 || sshGraphLinks.OperationCommands > 0 || sshGraphLinks.CommandMachines > 0 || sshGraphLinks.CompleteCommandAssets > 0),
		readinessItem("argo", "Sync Argo apps to deployment targets", "Create an Argo connection, sync apps, and inspect deployment targets.", assetCounts["argo_connection"] > 0 && assetCounts["argo_app"] > 0 && assetCounts["deployment_target"] > 0 && operationCounts["argo.apps.sync"] > 0 && argoGraphLinks.CompleteAppAssets > 0, argoEvidenceText, argoEvidence > 0),
		readinessItem("operations", "View operation history and logs", "Run any controlled operation and inspect its logs.", operationAssets > 0 && operationLogs > 0, fmt.Sprintf("%d operation assets / %d listed runs / %d with logs", operationAssets, listedOperationRuns, operationLogs), operationAssets > 0 || listedOperationRuns > 0 || operationLogs > 0),
		readinessItem("approval", "Enforce approval for high-risk operations", "Queue a high-risk action that creates an approval request.", approvalAssets > 0 && pendingApprovalOps > 0 && activeApprovalRules > 0 && approvalGraphLinks.CompleteApprovalAssetChains > 0, fmt.Sprintf("%d approvals / %d approval assets / %d pending ops / %d active rules / %d governed approvals / %d gated ops / %d complete approval chains / %d approval asset chains", approvalEvidence, approvalAssets, pendingApprovalOps, activeApprovalRules, approvalGraphLinks.RuleApprovals, approvalGraphLinks.ApprovalOperations, approvalGraphLinks.CompleteApprovalChains, approvalGraphLinks.CompleteApprovalAssetChains), approvalEvidence > 0 || approvalAssets > 0 || pendingApprovalOps > 0 || activeApprovalRules > 0 || approvalGraphLinks.RuleApprovals > 0 || approvalGraphLinks.ApprovalOperations > 0 || approvalGraphLinks.CompleteApprovalAssetChains > 0),
		readinessItem("context", "Generate AI-readable context from graph", "Create an agent task or AI runtime after syncing the canonical asset ledger.", contextEvidence > 0 && contextGenerations > 0 && graphEvidence > 0 && contextGraphLinks.CompleteContextTaskAssets > 0, fmt.Sprintf("%d context assets / %d context generations / %d complete context tasks / %d context asset tasks / %d runtime links / %d context tool links / %d graph nodes / %d graph edges", contextEvidence, contextGenerations, contextGraphLinks.CompleteContextTasks, contextGraphLinks.CompleteContextTaskAssets, contextGraphLinks.TaskRuntimes, contextGraphLinks.TaskContextToolCalls, graphNodes, graphEdges), contextEvidence > 0 || contextGenerations > 0 || graphEvidence > 0 || contextGraphLinks.TaskRuntimes > 0 || contextGraphLinks.TaskContextToolCalls > 0 || contextGraphLinks.CompleteContextTaskAssets > 0),
	}

	counts := map[string]int{"ready": 0, "partial": 0, "missing": 0}
	for _, row := range rows {
		counts[row.Status]++
	}
	return map[string]any{
		"ready":   counts["ready"],
		"partial": counts["partial"],
		"missing": counts["missing"],
		"total":   len(rows),
		"items":   rows,
	}
}

func countOperationRowsWithLogs(rows []map[string]any, operationAssetIDs map[string]bool) int {
	count := 0
	for _, row := range rows {
		if intFromAPI(row["log_count"]) > 0 && operationAssetIDs[operationRowAssetID(row)] {
			count++
		}
	}
	return count
}

func assetIDsByType(rows []map[string]any, typ string) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["asset_type"]) != typ {
			continue
		}
		if assetID := canonicalAssetGraphID(row, typ); assetID != "" {
			ids[assetID] = true
		}
	}
	return ids
}

func assetIDsByTypeMetadata(rows []map[string]any, typ, key, value string) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		metadata := mapFromAPI(row["metadata"])
		if fmt.Sprint(row["asset_type"]) != typ || !metadataValueEqual(metadata[key], value) {
			continue
		}
		if assetID := canonicalAssetGraphID(row, typ); assetID != "" {
			ids[assetID] = true
		}
	}
	return ids
}

func tagRunAssetIDsByOperation(rows []map[string]any) map[string]map[string]bool {
	ids := map[string]map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["asset_type"]) != "repo_tag_run" {
			continue
		}
		assetID := canonicalAssetGraphID(row, "repo_tag_run")
		if assetID == "" {
			continue
		}
		metadata := mapFromAPI(row["metadata"])
		operationID := cleanOperationAssetID(fmt.Sprint(metadata["operation_run_id"]))
		if operationID == "" {
			continue
		}
		if ids[operationID] == nil {
			ids[operationID] = map[string]bool{}
		}
		ids[operationID][assetID] = true
	}
	return ids
}

func cleanOperationAssetID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "<nil>" {
		return ""
	}
	if strings.HasPrefix(value, "operation_run:") {
		return value
	}
	return "operation_run:" + value
}

func assetIDsByGraphType(rows []map[string]any, assetType, graphType string) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["asset_type"]) != assetType {
			continue
		}
		if assetID := canonicalAssetGraphID(row, graphType); assetID != "" {
			ids[assetID] = true
		}
	}
	return ids
}

func canonicalAssetGraphID(row map[string]any, typ string) string {
	sourceID := strings.TrimSpace(fmt.Sprint(row["source_id"]))
	if sourceID != "" && sourceID != "<nil>" {
		return typ + ":" + sourceID
	}
	for _, key := range []string{"asset_id", "id"} {
		raw, ok := row[key].(string)
		if !ok {
			continue
		}
		value := strings.TrimSpace(raw)
		if value == "" || value == "<nil>" {
			continue
		}
		if strings.HasPrefix(value, typ+":") {
			return value
		}
		if !strings.Contains(value, ":") {
			return typ + ":" + value
		}
	}
	return ""
}

func operationRowAssetID(row map[string]any) string {
	for _, key := range []string{"id", "asset_id"} {
		value := strings.TrimSpace(fmt.Sprint(row[key]))
		if value == "" || value == "<nil>" {
			continue
		}
		if strings.HasPrefix(value, "operation_run:") {
			return value
		}
		return "operation_run:" + value
	}
	return ""
}

func operationIDsByType(rows []map[string]any, typ string) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["operation_type"]) != typ {
			continue
		}
		if assetID := operationRowAssetID(row); assetID != "" {
			ids[assetID] = true
		}
	}
	return ids
}

func operationIDsByStatus(rows []map[string]any, status string) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["status"]) != status {
			continue
		}
		if assetID := operationRowAssetID(row); assetID != "" {
			ids[assetID] = true
		}
	}
	return ids
}
