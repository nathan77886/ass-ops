package main

import (
	"fmt"
	"strings"
)

func countRepositoryGraphLinks(graph map[string]any, projectAssetIDs, repositoryAssetIDs, remoteAssetIDs map[string]bool) repositoryGraphLinkCounts {
	counts := repositoryGraphLinkCounts{}
	type repositoryLinks struct {
		projects map[string]bool
		remotes  map[string]bool
	}
	byRepository := map[string]*repositoryLinks{}
	repositoryEntry := func(assetID string) *repositoryLinks {
		entry := byRepository[assetID]
		if entry == nil {
			entry = &repositoryLinks{projects: map[string]bool{}, remotes: map[string]bool{}}
			byRepository[assetID] = entry
		}
		return entry
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "owns":
			if strings.HasPrefix(from, "project:") && strings.HasPrefix(to, "repository:") {
				counts.ProjectRepository++
				repositoryEntry(to).projects[from] = true
			}
		case "has_remote":
			if strings.HasPrefix(from, "repository:") && strings.HasPrefix(to, "git_remote:") {
				counts.RepositoryRemotes++
				repositoryEntry(from).remotes[to] = true
			}
		}
	}
	for repositoryID, entry := range byRepository {
		if len(entry.projects) > 0 && len(entry.remotes) >= 2 {
			counts.CompleteRepos++
			if hasAnyKnownID(entry.projects, projectAssetIDs) && repositoryAssetIDs[repositoryID] && countMatchingAssets(entry.remotes, remoteAssetIDs) >= 2 {
				counts.CompleteRepoAssets++
			}
		}
	}
	return counts
}

func countMatchingAssets(ids, knownIDs map[string]bool) int {
	count := 0
	for id := range ids {
		if knownIDs[id] {
			count++
		}
	}
	return count
}

type repoSyncGraphLinkCounts struct {
	RepositorySync     int
	SourceRemotes      int
	TargetRemotes      int
	CompleteSyncs      int
	CompleteSyncAssets int
}

func countRepoSyncGraphLinks(graph map[string]any, repositoryAssetIDs, repoSyncAssetIDs, remoteAssetIDs map[string]bool) repoSyncGraphLinkCounts {
	counts := repoSyncGraphLinkCounts{}
	type syncLinks struct {
		repositories map[string]bool
		sources      map[string]bool
		targets      map[string]bool
	}
	bySync := map[string]*syncLinks{}
	syncEntry := func(assetID string) *syncLinks {
		entry := bySync[assetID]
		if entry == nil {
			entry = &syncLinks{repositories: map[string]bool{}, sources: map[string]bool{}, targets: map[string]bool{}}
			bySync[assetID] = entry
		}
		return entry
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "has_sync":
			if strings.HasPrefix(from, "repository:") && strings.HasPrefix(to, "repo_sync:") {
				counts.RepositorySync++
				syncEntry(to).repositories[from] = true
			}
		case "synced_from":
			if strings.HasPrefix(from, "repo_sync:") && strings.HasPrefix(to, "git_remote:") {
				counts.SourceRemotes++
				syncEntry(from).sources[to] = true
			}
		case "mirrors_to":
			if strings.HasPrefix(from, "repo_sync:") && strings.HasPrefix(to, "git_remote:") {
				counts.TargetRemotes++
				syncEntry(from).targets[to] = true
			}
		}
	}
	for syncID, entry := range bySync {
		if len(entry.repositories) > 0 && hasDistinctSourceTarget(entry.sources, entry.targets) {
			counts.CompleteSyncs++
			if repoSyncAssetIDs[syncID] && hasAnyKnownID(entry.repositories, repositoryAssetIDs) && hasDistinctKnownSourceTarget(entry.sources, entry.targets, remoteAssetIDs) {
				counts.CompleteSyncAssets++
			}
		}
	}
	return counts
}

func hasDistinctSourceTarget(sources, targets map[string]bool) bool {
	for source := range sources {
		for target := range targets {
			if source != target {
				return true
			}
		}
	}
	return false
}

func hasDistinctKnownSourceTarget(sources, targets, knownIDs map[string]bool) bool {
	for source := range sources {
		if !knownIDs[source] {
			continue
		}
		for target := range targets {
			if source != target && knownIDs[target] {
				return true
			}
		}
	}
	return false
}

type githubActionGraphLinkCounts struct {
	ProjectRepositories        int
	RepositoryRemotes          int
	RemoteActionRuns           int
	TaggedRemotes              int
	TagActionRunLinks          int
	CompleteActionRuns         int
	CompleteActionAssets       int
	CompleteTaggedRemotes      int
	CompleteTaggedRemoteAssets int
	LinkedTagRuns              int
	LinkedTagRunAssets         int
}
