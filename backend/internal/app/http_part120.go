package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Server) finishNodeJob(w http.ResponseWriter, r *http.Request, status string) {
	node, ok := s.authenticateNode(w, r)
	if !ok {
		return
	}
	var req struct {
		Result map[string]any `json:"result"`
		Error  string         `json:"error"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var job map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var jobModel GormWorkerJob
		if err := tx.Where(&GormWorkerJob{GormBase: GormBase{ID: chi.URLParam(r, "id")}, AssignedWorkerNodeID: validNullString(cleanOptionalID(fmt.Sprint(node["id"])))}).First(&jobModel).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		jobModel.Status = status
		jobModel.Result = JSONValue{Data: req.Result}
		jobModel.Error = req.Error
		jobModel.FinishedAt = validNullTime(time.Now())
		if err := tx.Save(&jobModel).Error; err != nil {
			return err
		}
		opStatus := "completed"
		if status == "failed" {
			opStatus = "failed"
		}
		opID := cleanOptionalID(jobModel.OperationRunID.String)
		if opID != "" {
			updates := map[string]any{"status": opStatus, "result": JSONValue{Data: req.Result}, "error": req.Error, "finished_at": validNullTime(time.Now())}
			if err := tx.Model(&GormOperationRun{}).Where(&GormOperationRun{GormBase: GormBase{ID: opID}}).Updates(updates).Error; err != nil {
				return err
			}
		}
		job = workerJobMap(jobModel)
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) authenticateNode(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		var req struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		token = req.Token
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing node token")
		return nil, false
	}
	var tokenModel GormWorkerNodeToken
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormWorkerNodeToken{TokenHash: tokenHash(token)}).First(&tokenModel).Error; err != nil {
		writeError(w, http.StatusUnauthorized, "invalid node token")
		return nil, false
	}
	var nodeModel GormWorkerNode
	if err := s.store.Gorm.WithContext(r.Context()).First(&nodeModel, &GormWorkerNode{GormBase: GormBase{ID: tokenModel.WorkerNodeID}}).Error; err != nil {
		writeError(w, http.StatusUnauthorized, "invalid node token")
		return nil, false
	}
	_ = s.store.Gorm.WithContext(r.Context()).Model(&GormWorkerNodeToken{}).
		Where(&GormWorkerNodeToken{ID: tokenModel.ID}).
		Updates(map[string]any{"last_used_at": validNullTime(time.Now())}).Error
	return workerNodeMap(nodeModel), true
}

func (s *Server) generateContext(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project", ID: projectID, ProjectID: projectID}, "context.generate") {
		return
	}
	files, snapshot, err := s.BuildContextFiles(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "snapshot": snapshot})
}

func (s *Server) BuildContextFiles(ctx context.Context, projectID string) (map[string]string, map[string]any, error) {
	db := s.store.Gorm.WithContext(ctx)
	var projectModel GormProject
	if err := db.First(&projectModel, &GormProject{GormBase: GormBase{ID: projectID}}).Error; err != nil {
		return nil, nil, gormNotFoundAsErrNotFound(err)
	}
	project := projectMap(projectModel)
	var repoModels []GormProjectGitRepository
	if err := db.Where(&GormProjectGitRepository{ProjectID: projectID}).Order(gormOrderAsc("name")).Find(&repoModels).Error; err != nil {
		return nil, nil, err
	}
	repos := gitRepositoryMaps(repoModels)
	repoIDs := make([]string, 0, len(repoModels))
	for _, repo := range repoModels {
		repoIDs = append(repoIDs, repo.ID)
	}
	var remoteModels []GormGitRemote
	if len(repoIDs) > 0 {
		if err := db.Where(gormField("project_git_repository_id", repoIDs)).Order(gormOrderAsc("name")).Find(&remoteModels).Error; err != nil {
			return nil, nil, err
		}
	}
	remoteCredentials, err := s.connectionCredentialsForGitRemotes(ctx, remoteModels)
	if err != nil {
		return nil, nil, err
	}
	remotes := gitRemoteMaps(remoteModels, remoteCredentials, projectID)
	remoteIDs := make([]string, 0, len(remoteModels))
	for _, remote := range remoteModels {
		remoteIDs = append(remoteIDs, remote.ID)
	}
	var operationModels []GormOperationRun
	if err := db.Where(&GormOperationRun{ProjectID: validNullString(projectID)}).Order(gormOrderDesc("created_at")).Limit(20).Find(&operationModels).Error; err != nil {
		return nil, nil, err
	}
	operations := make([]map[string]any, 0, len(operationModels))
	for _, operation := range operationModels {
		item := operationRunMap(operation)
		delete(item, "input")
		delete(item, "result")
		delete(item, "started_at")
		delete(item, "finished_at")
		operations = append(operations, item)
	}
	var approvalModels []GormOperationApproval
	if err := db.Where(&GormOperationApproval{ProjectID: validNullString(projectID)}).Order(gormOrderDesc("created_at")).Limit(20).Find(&approvalModels).Error; err != nil {
		return nil, nil, err
	}
	approvals := contextOperationApprovalMaps(approvalModels)
	var deploymentTargetModels []GormDeploymentTarget
	if err := db.Where(&GormDeploymentTarget{ProjectID: projectID}).Order(gormOrderDesc("updated_at")).Limit(20).Find(&deploymentTargetModels).Error; err != nil {
		return nil, nil, err
	}
	deploymentTargets := contextDeploymentTargetMaps(deploymentTargetModels)
	enrichDeploymentTargetsWithExecutionReadiness(deploymentTargets)
	var deploymentRecordModels []GormDeploymentRecord
	if err := db.Where(&GormDeploymentRecord{ProjectID: projectID}).Order(gormOrderDesc("observed_at")).Limit(20).Find(&deploymentRecordModels).Error; err != nil {
		return nil, nil, err
	}
	deploymentRecords := contextDeploymentRecordMaps(deploymentRecordModels)
	rollbackPoints, err := contextRollbackPointMaps(ctx, s.store.Gorm, projectID, 20)
	if err != nil {
		return nil, nil, err
	}
	sanitizeContextRowsMetadata(rollbackPoints)
	var sshMachineModels []GormSSHMachine
	if err := db.Where(&GormSSHMachine{ProjectID: projectID}).Order(gormOrderDesc("updated_at")).Limit(20).Find(&sshMachineModels).Error; err != nil {
		return nil, nil, err
	}
	sshCredentials, err := s.connectionCredentialsForSSHMachine(ctx, sshMachineModels)
	if err != nil {
		return nil, nil, err
	}
	sshMachines := sshMachineMaps(sshMachineModels, sshCredentials)
	var githubRunModels []GormGitHubActionRun
	if len(remoteIDs) > 0 {
		if err := db.Where(gormField("git_remote_id", remoteIDs)).Order(gormOrderDesc("created_at")).Limit(20).Find(&githubRunModels).Error; err != nil {
			return nil, nil, err
		}
	}
	githubRuns := make([]map[string]any, 0, len(githubRunModels))
	for _, run := range githubRunModels {
		item := gitHubActionRunMap(run)
		delete(item, "operation_run_id")
		delete(item, "external_run_id")
		delete(item, "metadata")
		delete(item, "created_at")
		githubRuns = append(githubRuns, item)
	}
	var assetModels []GormAsset
	if err := db.Where(&GormAsset{ProjectID: validNullString(projectID)}).Order(gormOrder(gormOrderColumn("asset_type", false), gormOrderColumn("name", false))).Limit(200).Find(&assetModels).Error; err != nil {
		return nil, nil, err
	}
	assets := assetMaps(assetModels)
	assetIndex := map[string]GormAsset{}
	assetIDs := make([]string, 0, len(assetModels))
	for _, asset := range assetModels {
		assetIndex[asset.ID] = asset
		assetIDs = append(assetIDs, asset.ID)
	}
	assetRelations, err := contextAssetRelationMaps(ctx, s.store.Gorm, projectID, assetIndex, 300)
	if err != nil {
		return nil, nil, err
	}
	assetStatusSnapshots, err := contextAssetStatusSnapshotMaps(ctx, s.store.Gorm, assetIndex, assetIDs, 100)
	if err != nil {
		return nil, nil, err
	}
	tools := []map[string]any{
		{"name": "repo.sync", "description": "sync repository adapter"},
		{"name": "repo.tag", "description": "tag repository adapter"},
		{"name": "github.actions.sync", "description": "GitHub Actions query adapter"},
		{"name": "ssh.verify", "description": "SSH machine connectivity check"},
		{"name": "ssh.exec", "description": "SSH command adapter"},
	}
	rollbackGuardrail := rollbackGuardrailSummary(rollbackPoints)
	contextJSON := map[string]any{
		"project":            project,
		"repositories":       repos,
		"remotes":            remotes,
		"operations":         operations,
		"approvals":          approvals,
		"deployment_targets": deploymentTargets,
		"deployment_records": deploymentRecords,
		"rollback_points":    rollbackPoints,
		"rollback_guardrail": rollbackGuardrail,
		"ssh_machines":       sshMachines,
		"github_action_runs": githubRuns,
		"asset_graph": map[string]any{
			"assets":               assets,
			"relations":            assetRelations,
			"status_snapshots":     assetStatusSnapshots,
			"asset_type_counts":    countByStringField(assets, "asset_type"),
			"relation_type_counts": countByStringField(assetRelations, "relation_type"),
			"health_counts":        countByStringField(assetStatusSnapshots, "health"),
		},
	}
	manifest := map[string]any{"tools": tools}
	deploymentExecutionSummary := formatCountMap(countNestedStringField(deploymentTargets, "deployment_execution_readiness", "status"))
	if deploymentExecutionSummary == "" {
		deploymentExecutionSummary = "none"
	}
	brief := fmt.Sprintf("# ASSOPS Context\n\nProject: %s\n\nRepositories: %d\nRemotes: %d\nRecent operations: %d\nApprovals: %d\nDeployment targets: %d\nDeployment execution readiness: %s\nRollback points: %d\nRollback execution: %s\nSSH machines: %d\nGitHub Actions runs: %d\nAsset graph assets: %d\nAsset graph relations: %d\nAsset status snapshots: %d\n", project["name"], len(repos), len(remotes), len(operations), len(approvals), len(deploymentTargets), deploymentExecutionSummary, len(rollbackPoints), rollbackGuardrail["execution_mode"], len(sshMachines), len(githubRuns), len(assets), len(assetRelations), len(assetStatusSnapshots))
	base := filepath.Join(s.cfg.ContextDir, projectID)
	if err := os.MkdirAll(base, contextDirMode); err != nil {
		return nil, nil, err
	}
	files := map[string]string{
		"ASSOPS_CONTEXT.md":   filepath.Join(base, "ASSOPS_CONTEXT.md"),
		"assops-context.json": filepath.Join(base, "assops-context.json"),
		"tool-manifest.json":  filepath.Join(base, "tool-manifest.json"),
	}
	if err := os.WriteFile(files["ASSOPS_CONTEXT.md"], []byte(brief), contextFileMode); err != nil {
		return nil, nil, err
	}
	if err := writeJSONFile(files["assops-context.json"], contextJSON); err != nil {
		return nil, nil, err
	}
	if err := writeJSONFile(files["tool-manifest.json"], manifest); err != nil {
		return nil, nil, err
	}
	snapshotModel := GormAgentContextSnapshot{ProjectID: projectID, SummaryMarkdown: brief, ContextJSON: JSONValue{Data: contextJSON}, ToolManifest: JSONValue{Data: manifest}}
	if err := db.Create(&snapshotModel).Error; err != nil {
		return nil, nil, err
	}
	return files, agentContextSnapshotMap(snapshotModel), nil
}

func agentContextSnapshotMap(snapshot GormAgentContextSnapshot) map[string]any {
	return map[string]any{"id": snapshot.ID, "project_id": snapshot.ProjectID, "agent_task_id": nullableStringValue(snapshot.AgentTaskID), "summary_markdown": snapshot.SummaryMarkdown, "context_json": mapFromAny(snapshot.ContextJSON.Data), "tool_manifest": mapFromAny(snapshot.ToolManifest.Data), "created_at": snapshot.CreatedAt}
}

func contextOperationApprovalMaps(approvals []GormOperationApproval) []map[string]any {
	items := make([]map[string]any, 0, len(approvals))
	for _, approval := range approvals {
		items = append(items, map[string]any{"id": approval.ID, "resource_type": approval.ResourceType, "resource_id": approval.ResourceID, "action": approval.Action, "title": approval.Title, "status": approval.Status, "expires_at": nullableTimeAny(approval.ExpiresAt), "created_at": approval.CreatedAt, "updated_at": approval.UpdatedAt})
	}
	return items
}
