package main

import (
	"fmt"
	"strings"
)

func countGitHubActionGraphLinks(graph map[string]any, projectAssetIDs, repositoryAssetIDs, remoteAssetIDs, actionAssetIDs, tagRunAssetIDs map[string]bool, tagRunAssetIDsByOperation map[string]map[string]bool, tagOperationIDs map[string]bool) githubActionGraphLinkCounts {
	counts := githubActionGraphLinkCounts{}
	repositoryProjects := map[string]map[string]bool{}
	remoteRepositories := map[string]map[string]bool{}
	remoteActionRuns := map[string]map[string]bool{}
	taggedRemoteOps := map[string]map[string]bool{}
	tagActionRuns := map[string]map[string]bool{}
	addRepositoryProject := func(repositoryID, projectID string) {
		if repositoryProjects[repositoryID] == nil {
			repositoryProjects[repositoryID] = map[string]bool{}
		}
		repositoryProjects[repositoryID][projectID] = true
	}
	addRemoteRepository := func(remoteID, repositoryID string) {
		if remoteRepositories[remoteID] == nil {
			remoteRepositories[remoteID] = map[string]bool{}
		}
		remoteRepositories[remoteID][repositoryID] = true
	}
	addRemoteActionRun := func(remoteID, actionID string) {
		if remoteActionRuns[remoteID] == nil {
			remoteActionRuns[remoteID] = map[string]bool{}
		}
		remoteActionRuns[remoteID][actionID] = true
	}
	addTaggedRemoteOp := func(remoteID, operationID string) {
		if taggedRemoteOps[remoteID] == nil {
			taggedRemoteOps[remoteID] = map[string]bool{}
		}
		taggedRemoteOps[remoteID][operationID] = true
	}
	addTagActionRun := func(tagRunID, actionID string) {
		if tagActionRuns[tagRunID] == nil {
			tagActionRuns[tagRunID] = map[string]bool{}
		}
		tagActionRuns[tagRunID][actionID] = true
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "owns":
			if strings.HasPrefix(from, "project:") && strings.HasPrefix(to, "repository:") {
				counts.ProjectRepositories++
				addRepositoryProject(to, from)
			}
		case "has_remote":
			if strings.HasPrefix(from, "repository:") && strings.HasPrefix(to, "git_remote:") {
				counts.RepositoryRemotes++
				addRemoteRepository(to, from)
			}
		case "triggered_by":
			if strings.HasPrefix(from, "git_remote:") && strings.HasPrefix(to, "github_action_run:") {
				counts.RemoteActionRuns++
				addRemoteActionRun(from, to)
			}
		case "tagged_remote":
			metadata := mapFromAPI(edge["metadata"])
			status := strings.ToLower(strings.TrimSpace(fmt.Sprint(metadata["status"])))
			if strings.HasPrefix(from, "operation_run:") && strings.HasPrefix(to, "git_remote:") && (status == "completed" || status == "succeeded" || status == "success") {
				counts.TaggedRemotes++
				addTaggedRemoteOp(to, from)
			}
		case "matched_action_run":
			if strings.HasPrefix(from, "repo_tag_run:") && strings.HasPrefix(to, "github_action_run:") {
				counts.TagActionRunLinks++
				addTagActionRun(from, to)
			}
		}
	}
	projectLinkedActionRuns := map[string]bool{}
	canonicalProjectLinkedActionRuns := map[string]bool{}
	canonicalTaggedTagRunAssets := map[string]bool{}
	for remoteID, actionRuns := range remoteActionRuns {
		hasProjectRepository := false
		for repositoryID := range remoteRepositories[remoteID] {
			if len(repositoryProjects[repositoryID]) > 0 {
				hasProjectRepository = true
				break
			}
		}
		if hasProjectRepository {
			counts.CompleteActionRuns += len(actionRuns)
			for actionID := range actionRuns {
				projectLinkedActionRuns[actionID] = true
				if hasCanonicalProjectRemote(remoteID, remoteRepositories, repositoryProjects, projectAssetIDs, repositoryAssetIDs) && remoteAssetIDs[remoteID] && actionAssetIDs[actionID] {
					counts.CompleteActionAssets++
					canonicalProjectLinkedActionRuns[actionID] = true
				}
			}
		}
	}
	for remoteID, operations := range taggedRemoteOps {
		hasProjectRepository := false
		for repositoryID := range remoteRepositories[remoteID] {
			if len(repositoryProjects[repositoryID]) > 0 {
				hasProjectRepository = true
				break
			}
		}
		if hasProjectRepository {
			counts.CompleteTaggedRemotes += len(operations)
			if hasCanonicalProjectRemote(remoteID, remoteRepositories, repositoryProjects, projectAssetIDs, repositoryAssetIDs) && remoteAssetIDs[remoteID] {
				for operationID := range operations {
					if tagOperationIDs[operationID] && hasAnyKnownID(tagRunAssetIDsByOperation[operationID], tagRunAssetIDs) {
						counts.CompleteTaggedRemoteAssets++
						for tagRunID := range tagRunAssetIDsByOperation[operationID] {
							if tagRunAssetIDs[tagRunID] {
								canonicalTaggedTagRunAssets[tagRunID] = true
							}
						}
					}
				}
			}
		}
	}
	for tagRunID, actionRuns := range tagActionRuns {
		linked := false
		linkedAsset := false
		for actionID := range actionRuns {
			if projectLinkedActionRuns[actionID] {
				linked = true
				if canonicalTaggedTagRunAssets[tagRunID] && actionAssetIDs[actionID] && canonicalProjectLinkedActionRuns[actionID] {
					linkedAsset = true
				}
			}
		}
		if linked {
			counts.LinkedTagRuns++
		}
		if linkedAsset {
			counts.LinkedTagRunAssets++
		}
	}
	return counts
}

func hasCanonicalProjectRemote(remoteID string, remoteRepositories, repositoryProjects map[string]map[string]bool, projectAssetIDs, repositoryAssetIDs map[string]bool) bool {
	for repositoryID := range remoteRepositories[remoteID] {
		if !repositoryAssetIDs[repositoryID] {
			continue
		}
		if hasAnyKnownID(repositoryProjects[repositoryID], projectAssetIDs) {
			return true
		}
	}
	return false
}

type webhookSyncGraphLinkCounts struct {
	ConnectionEvents    int
	EventRepoSyncs      int
	EventOperations     int
	CompleteChains      int
	CompleteChainAssets int
}

func countWebhookSyncGraphLinks(graph map[string]any, connectionAssetIDs, eventAssetIDs, repoSyncAssetIDs, syncOperationIDs map[string]bool) webhookSyncGraphLinkCounts {
	counts := webhookSyncGraphLinkCounts{}
	type eventLinks struct {
		connections map[string]bool
		repoSyncs   map[string]bool
		operations  map[string]bool
	}
	operationRepoSyncs := map[string]map[string]bool{}
	byEvent := map[string]*eventLinks{}
	eventEntry := func(assetID string) *eventLinks {
		entry := byEvent[assetID]
		if entry == nil {
			entry = &eventLinks{connections: map[string]bool{}, repoSyncs: map[string]bool{}, operations: map[string]bool{}}
			byEvent[assetID] = entry
		}
		return entry
	}
	addOperationRepoSync := func(operationID, repoSyncID string) {
		if operationRepoSyncs[operationID] == nil {
			operationRepoSyncs[operationID] = map[string]bool{}
		}
		operationRepoSyncs[operationID][repoSyncID] = true
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "received_webhook_event":
			if strings.HasPrefix(from, "webhook_connection:") && strings.HasPrefix(to, "webhook_event:") {
				counts.ConnectionEvents++
				eventEntry(to).connections[from] = true
			}
		case "matched_repo_sync":
			if strings.HasPrefix(from, "webhook_event:") && strings.HasPrefix(to, "repo_sync:") {
				counts.EventRepoSyncs++
				eventEntry(from).repoSyncs[to] = true
			}
		case "triggered_operation":
			// Ignore legacy webhook_connection -> operation_run compatibility edges.
			if strings.HasPrefix(from, "webhook_event:") && strings.HasPrefix(to, "operation_run:") {
				counts.EventOperations++
				eventEntry(from).operations[to] = true
			}
		case "ran_repo_sync":
			if strings.HasPrefix(from, "operation_run:") && strings.HasPrefix(to, "repo_sync:") {
				addOperationRepoSync(from, to)
			}
		}
	}
	for eventID, entry := range byEvent {
		if len(entry.connections) > 0 && hasOperationRepoSyncChain(entry.repoSyncs, entry.operations, operationRepoSyncs) {
			counts.CompleteChains++
			if eventAssetIDs[eventID] &&
				hasKnownWebhookConnection(entry.connections, connectionAssetIDs) &&
				hasCanonicalOperationRepoSyncChain(entry.repoSyncs, entry.operations, operationRepoSyncs, repoSyncAssetIDs, syncOperationIDs) {
				counts.CompleteChainAssets++
			}
		}
	}
	return counts
}

func hasKnownWebhookConnection(connections, knownIDs map[string]bool) bool {
	for connectionID := range connections {
		if knownIDs[connectionID] {
			return true
		}
	}
	return false
}

func hasOperationRepoSyncChain(repoSyncs, operations map[string]bool, operationRepoSyncs map[string]map[string]bool) bool {
	for operationID := range operations {
		for repoSyncID := range repoSyncs {
			if operationRepoSyncs[operationID][repoSyncID] {
				return true
			}
		}
	}
	return false
}

func hasCanonicalOperationRepoSyncChain(repoSyncs, operations map[string]bool, operationRepoSyncs map[string]map[string]bool, repoSyncAssetIDs, syncOperationIDs map[string]bool) bool {
	for operationID := range operations {
		if !syncOperationIDs[operationID] {
			continue
		}
		for repoSyncID := range repoSyncs {
			if repoSyncAssetIDs[repoSyncID] && operationRepoSyncs[operationID][repoSyncID] {
				return true
			}
		}
	}
	return false
}

type sshGraphLinkCounts struct {
	OperationCommands           int
	CommandMachines             int
	CompleteCommands            int
	CompleteCommandAssets       int
	CompleteVerifyCommandAssets int
	CompleteRunCommandAssets    int
}
