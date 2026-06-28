package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func countSSHGraphLinks(graph map[string]any, commandAssetIDs, machineAssetIDs, operationIDs, verifyOperationIDs, runOperationIDs map[string]bool) sshGraphLinkCounts {
	counts := sshGraphLinkCounts{}
	type commandLinks struct {
		operations map[string]bool
		machines   map[string]bool
	}
	byCommand := map[string]*commandLinks{}
	commandEntry := func(assetID string) *commandLinks {
		entry := byCommand[assetID]
		if entry == nil {
			entry = &commandLinks{operations: map[string]bool{}, machines: map[string]bool{}}
			byCommand[assetID] = entry
		}
		return entry
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "ran_ssh_command":
			if strings.HasPrefix(from, "operation_run:") && strings.HasPrefix(to, "ssh_command_run:") {
				counts.OperationCommands++
				commandEntry(to).operations[from] = true
			}
		case "executed_on":
			if strings.HasPrefix(from, "ssh_command_run:") && strings.HasPrefix(to, "ssh_machine:") {
				counts.CommandMachines++
				commandEntry(from).machines[to] = true
			}
		}
	}
	for commandID, entry := range byCommand {
		if len(entry.operations) > 0 && len(entry.machines) > 0 {
			counts.CompleteCommands++
			if commandAssetIDs[commandID] && hasAnyKnownID(entry.operations, operationIDs) && hasAnyKnownID(entry.machines, machineAssetIDs) {
				counts.CompleteCommandAssets++
				if hasAnyKnownID(entry.operations, verifyOperationIDs) {
					counts.CompleteVerifyCommandAssets++
				}
				if hasAnyKnownID(entry.operations, runOperationIDs) {
					counts.CompleteRunCommandAssets++
				}
			}
		}
	}
	return counts
}

type argoGraphLinkCounts struct {
	ConnectionApps    int
	AppTargets        int
	CompleteApps      int
	CompleteAppAssets int
}

func countArgoGraphLinks(graph map[string]any, connectionAssetIDs, appAssetIDs, targetAssetIDs, syncOperationIDs map[string]bool) argoGraphLinkCounts {
	counts := argoGraphLinkCounts{}
	type appLinks struct {
		connections map[string]bool
		targets     map[string]bool
	}
	syncedConnections := map[string]map[string]bool{}
	byApp := map[string]*appLinks{}
	appEntry := func(assetID string) *appLinks {
		entry := byApp[assetID]
		if entry == nil {
			entry = &appLinks{connections: map[string]bool{}, targets: map[string]bool{}}
			byApp[assetID] = entry
		}
		return entry
	}
	addSyncedConnection := func(connectionID, operationID string) {
		if syncedConnections[connectionID] == nil {
			syncedConnections[connectionID] = map[string]bool{}
		}
		syncedConnections[connectionID][operationID] = true
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "manages":
			if strings.HasPrefix(from, "argo_connection:") && strings.HasPrefix(to, "argo_app:") {
				counts.ConnectionApps++
				appEntry(to).connections[from] = true
			}
		case "deployed_to":
			if strings.HasPrefix(from, "argo_app:") && strings.HasPrefix(to, "deployment_target:") {
				counts.AppTargets++
				appEntry(from).targets[to] = true
			}
		case "synced_argo_connection":
			if strings.HasPrefix(from, "operation_run:") && strings.HasPrefix(to, "argo_connection:") {
				addSyncedConnection(to, from)
			}
		}
	}
	for appID, entry := range byApp {
		if len(entry.targets) > 0 && hasSyncedConnection(entry.connections, syncedConnections) {
			counts.CompleteApps++
			if hasCanonicalArgoAppChain(appID, entry.connections, entry.targets, syncedConnections, connectionAssetIDs, appAssetIDs, targetAssetIDs, syncOperationIDs) {
				counts.CompleteAppAssets++
			}
		}
	}
	return counts
}

func hasSyncedConnection(connections map[string]bool, syncedConnections map[string]map[string]bool) bool {
	for connectionID := range connections {
		if len(syncedConnections[connectionID]) > 0 {
			return true
		}
	}
	return false
}

func hasCanonicalArgoAppChain(appID string, connections, targets map[string]bool, syncedConnections map[string]map[string]bool, connectionAssetIDs, appAssetIDs, targetAssetIDs, syncOperationIDs map[string]bool) bool {
	if !appAssetIDs[appID] {
		return false
	}
	for connectionID := range connections {
		if !connectionAssetIDs[connectionID] {
			continue
		}
		if !hasCanonicalSyncedOperation(syncedConnections[connectionID], syncOperationIDs) {
			continue
		}
		for targetID := range targets {
			if targetAssetIDs[targetID] {
				return true
			}
		}
	}
	return false
}

func hasCanonicalSyncedOperation(operationIDs, syncOperationIDs map[string]bool) bool {
	for operationID := range operationIDs {
		if syncOperationIDs[operationID] {
			return true
		}
	}
	return false
}

type approvalGraphLinkCounts struct {
	RuleApprovals               int
	ApprovalOperations          int
	CompleteApprovalChains      int
	CompleteApprovalAssetChains int
}

func countApprovalGraphLinks(graph map[string]any, activeRuleIDs, approvalAssetIDs, operationAssetIDs, pendingOperationIDs map[string]bool) approvalGraphLinkCounts {
	counts := approvalGraphLinkCounts{}
	type approvalLinks struct {
		rules      map[string]bool
		operations map[string]bool
	}
	byApproval := map[string]*approvalLinks{}
	approvalEntry := func(assetID string) *approvalLinks {
		entry := byApproval[assetID]
		if entry == nil {
			entry = &approvalLinks{rules: map[string]bool{}, operations: map[string]bool{}}
			byApproval[assetID] = entry
		}
		return entry
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "governs":
			if activeRuleIDs[from] && approvalAssetIDs[to] {
				counts.RuleApprovals++
				approvalEntry(to).rules[from] = true
			}
		case "gates_operation":
			if strings.HasPrefix(from, "operation_approval:") && strings.HasPrefix(to, "operation_run:") {
				counts.ApprovalOperations++
				approvalEntry(from).operations[to] = true
			}
		}
	}
	for approvalID, entry := range byApproval {
		if len(entry.rules) > 0 && len(entry.operations) > 0 {
			counts.CompleteApprovalChains++
			// operation_run asset_inventory.source_id is emitted from operations.id,
			// matching the operation_run:<id> graph edges used for pending operations.
			if approvalAssetIDs[approvalID] && hasAnyIDInBoth(entry.operations, operationAssetIDs, pendingOperationIDs) {
				counts.CompleteApprovalAssetChains++
			}
		}
	}
	return counts
}

func intFromAPI(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		i, _ := typed.Int64()
		return int(i)
	default:
		return 0
	}
}

func getAPI(base, token, path string) error {
	payload, err := getAPIBytes(base, token, path)
	if err != nil {
		return err
	}
	fmt.Println(string(payload))
	return nil
}

func getAPIJSON(base, token, path string) (map[string]any, error) {
	body, err := getAPIBytes(base, token, path)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decoding %s response: %w", path, err)
	}
	return out, nil
}

func getAPIBytes(base, token, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("gateway returned %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func printJSON(value any) error {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(bytes))
	return nil
}
