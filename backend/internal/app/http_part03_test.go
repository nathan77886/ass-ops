package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeleteProjectHandlerRemovesScopedRows(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(
		t,
		store,
		&GormUser{},
		&GormProject{},
		&GormProjectMember{},
		&GormProjectGitRepository{},
		&GormConnectionCredential{},
		&GormProviderAccount{},
		&GormGitRemote{},
		&GormRepoSyncPolicy{},
		&GormProjectVersion{},
		&GormArgoConnection{},
		&GormArgoApp{},
		&GormSSHMachine{},
		&GormAIRuntime{},
		&GormAgentTask{},
		&GormAgentPlan{},
		&GormAgentToolCall{},
		&GormAgentContextSnapshot{},
		&GormAgentToolToken{},
		&GormWorkerNode{},
		&GormAsset{},
		&GormAssetRelation{},
		&sqliteAssetStatusSnapshotFixture{},
		&GormOperationRun{},
		&GormWorkerJob{},
		&GormOperationLog{},
		&GormRepoSyncRun{},
		&GormRepoTagRun{},
		&GormGitHubActionRun{},
		&GormGitHubActionArtifact{},
		&GormGitHubRepositoryLabel{},
		&GormSSHCommandRun{},
		&GormDeploymentTarget{},
		&GormDeploymentRecord{},
		&GormRollbackPoint{},
		&GormProjectTemplateRun{},
		&GormProjectTemplateFile{},
		&GormRepoSyncAsset{},
		&GormWebhookConnection{},
		&GormWebhookEvent{},
		&GormOperationApproval{},
		&GormOperationApprovalDecision{},
		&GormOperationApprovalDelegation{},
		&GormProviderReviewAttempt{},
		&GormWebhookThresholdDecisionAudit{},
		&GormWebhookThresholdConfiguration{},
		&GormKubernetesEnvironment{},
	)
	server := &Server{store: store}
	admin := &User{ID: "user-admin", Email: "admin@example.test", Role: "admin"}
	mustCreate := func(value any) {
		t.Helper()
		if err := store.Gorm.Create(value).Error; err != nil {
			t.Fatalf("create %T: %v", value, err)
		}
	}
	if err := store.Gorm.Create(&GormUser{GormBase: GormBase{ID: admin.ID}, Email: admin.Email, Role: admin.Role}).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
	project := GormProject{Name: "Trash", Slug: "trash"}
	mustCreate(&project)
	mustCreate(&GormProjectMember{ProjectID: project.ID, UserID: admin.ID, Role: "owner"})
	credential := GormConnectionCredential{ProjectID: validNullString(project.ID), Name: "ssh-key", Kind: "ssh_key", SecretCiphertext: "encrypted", Metadata: JSONValue{Data: map[string]any{}}}
	mustCreate(&credential)
	mustCreate(&GormProviderAccount{Name: "account", ProviderType: "github", CredentialID: validNullString(credential.ID), Metadata: JSONValue{Data: map[string]any{}}})
	repo := GormProjectGitRepository{ProjectID: project.ID, Name: "service", RepoKey: "service", DisplayName: "Service", RepoRole: "service"}
	mustCreate(&repo)
	remote := GormGitRemote{ProjectGitRepositoryID: repo.ID, Name: "origin", RemoteKey: "origin", ProviderType: "github", RemoteURL: "git@example.test/repo.git", URLs: JSONValue{Data: []any{}}, Metadata: JSONValue{Data: map[string]any{}}}
	mustCreate(&remote)
	mustCreate(&GormRepoSyncPolicy{GitRemoteID: remote.ID, Config: JSONValue{Data: map[string]any{}}})
	mustCreate(&GormProjectVersion{ProjectID: project.ID, Version: "v1", Metadata: JSONValue{Data: map[string]any{}}})
	asset := GormAsset{ProjectID: validNullString(project.ID), AssetType: "project", SourceTable: "projects", SourceID: validNullString(project.ID), Name: project.Name, Metadata: JSONValue{Data: map[string]any{}}}
	mustCreate(&asset)
	repoAsset := GormAsset{ProjectID: validNullString(project.ID), AssetType: "repository", SourceTable: "project_git_repositories", SourceID: validNullString(repo.ID), Name: repo.Name, Metadata: JSONValue{Data: map[string]any{}}}
	mustCreate(&repoAsset)
	mustCreate(&GormAssetRelation{ProjectID: validNullString(project.ID), FromAssetID: asset.ID, ToAssetID: repoAsset.ID, RelationType: "owns", Metadata: JSONValue{Data: map[string]any{}}})
	mustCreate(&GormAssetStatusSnapshot{AssetID: asset.ID, Status: "active", Raw: JSONValue{Data: map[string]any{}}, CollectedAt: time.Now().UTC()})
	op := GormOperationRun{ProjectID: validNullString(project.ID), OperationType: "ssh.verify", Status: "completed", Title: "verify", Input: JSONValue{Data: map[string]any{}}, Result: JSONValue{Data: map[string]any{}}}
	mustCreate(&op)
	job := GormWorkerJob{OperationRunID: validNullString(op.ID), ToolName: "ssh.verify", Status: "completed", Payload: JSONValue{Data: map[string]any{}}, Result: JSONValue{Data: map[string]any{}}}
	mustCreate(&job)
	mustCreate(&GormOperationLog{OperationRunID: validNullString(op.ID), WorkerJobID: validNullString(job.ID), Message: "done", Fields: JSONValue{Data: map[string]any{}}})
	mustCreate(&GormRepoSyncRun{OperationRunID: op.ID, GitRemoteID: remote.ID, ProjectID: validNullString(project.ID), ProjectGitRepositoryID: validNullString(repo.ID), Ref: "main", Status: "completed"})
	mustCreate(&GormRepoTagRun{OperationRunID: op.ID, GitRemoteID: remote.ID, ProjectID: validNullString(project.ID), ProjectGitRepositoryID: validNullString(repo.ID), TagName: "v1", Status: "completed"})
	actionRun := GormGitHubActionRun{OperationRunID: validNullString(op.ID), GitRemoteID: remote.ID, ExternalRunID: "run-1", Metadata: JSONValue{Data: map[string]any{}}}
	mustCreate(&actionRun)
	mustCreate(&GormGitHubActionArtifact{GitRemoteID: remote.ID, GitHubActionRunID: actionRun.ID, ExternalArtifactID: "artifact-1", SyncedAt: time.Now().UTC(), Metadata: JSONValue{Data: map[string]any{}}})
	mustCreate(&GormGitHubRepositoryLabel{OperationRunID: validNullString(op.ID), GitRemoteID: remote.ID, ExternalLabelID: "label-1"})
	mustCreate(&GormSSHCommandRun{OperationRunID: validNullString(op.ID), ProjectID: validNullString(project.ID), Command: "true", Status: "completed"})
	approval := GormOperationApproval{ProjectID: validNullString(project.ID), OperationRunID: validNullString(op.ID), ResourceType: "project", ResourceID: project.ID, Action: "delete", Title: "delete", RequestPayload: JSONValue{Data: map[string]any{}}, Status: "pending"}
	mustCreate(&approval)
	mustCreate(&GormOperationApprovalDecision{OperationApprovalID: approval.ID, UserID: validNullString(admin.ID), Decision: "approved"})
	mustCreate(&GormOperationApprovalDelegation{OperationApprovalID: approval.ID, FromUserID: validNullString(admin.ID), ToUserID: admin.ID})
	templateRun := GormProjectTemplateRun{OperationRunID: validNullString(op.ID), ProjectID: validNullString(project.ID), ProjectName: project.Name, ProjectSlug: project.Slug, Input: JSONValue{Data: map[string]any{}}, Steps: JSONValue{Data: []any{}}, Result: JSONValue{Data: map[string]any{}}}
	mustCreate(&templateRun)
	mustCreate(&GormProjectTemplateFile{ProjectTemplateRunID: validNullString(templateRun.ID), ProjectID: validNullString(project.ID), ProjectGitRepositoryID: validNullString(repo.ID), Path: "README.md", Metadata: JSONValue{Data: map[string]any{}}})
	mustCreate(&GormProviderReviewAttempt{OperationApprovalID: approval.ID, ProjectTemplateRunID: validNullString(templateRun.ID), OperationName: "create_branch_ref", IdempotencyKeyMaterial: JSONValue{Data: map[string]any{}}, RequestSummary: JSONValue{Data: map[string]any{}}, ResponseDiagnostics: JSONValue{Data: map[string]any{}}})
	agentTask := GormAgentTask{ProjectID: project.ID, Title: "task", Prompt: "prompt"}
	mustCreate(&agentTask)
	mustCreate(&GormAgentPlan{AgentTaskID: agentTask.ID, Content: "plan"})
	mustCreate(&GormAgentToolCall{AgentTaskID: agentTask.ID, ProjectID: validNullString(project.ID), OperationRunID: validNullString(op.ID), ToolName: "tool", Input: JSONValue{Data: map[string]any{}}, Output: JSONValue{Data: map[string]any{}}, Metadata: JSONValue{Data: map[string]any{}}})
	mustCreate(&GormAgentContextSnapshot{ProjectID: project.ID, AgentTaskID: validNullString(agentTask.ID), SummaryMarkdown: "summary", ContextJSON: JSONValue{Data: map[string]any{}}, ToolManifest: JSONValue{Data: map[string]any{}}})
	mustCreate(&GormAgentToolToken{AgentTaskID: validNullString(agentTask.ID), TokenHash: "hash"})
	argo := GormArgoConnection{ProjectID: project.ID, Name: "argo", Config: JSONValue{Data: map[string]any{}}}
	mustCreate(&argo)
	target := GormDeploymentTarget{ProjectID: project.ID, Name: "target", Environment: "test", ClusterName: "cluster", Namespace: "ns", Metadata: JSONValue{Data: map[string]any{}}, ArgoConnectionID: validNullString(argo.ID)}
	mustCreate(&target)
	argoApp := GormArgoApp{ProjectID: project.ID, ArgoConnectionID: validNullString(argo.ID), DeploymentTargetID: validNullString(target.ID), Name: "app", Metadata: JSONValue{Data: map[string]any{}}}
	mustCreate(&argoApp)
	record := GormDeploymentRecord{ProjectID: project.ID, DeploymentTargetID: validNullString(target.ID), ArgoConnectionID: validNullString(argo.ID), ArgoAppID: validNullString(argoApp.ID), Name: "app", Environment: "test", Namespace: "ns", ClusterName: "cluster", ImageRefs: JSONValue{Data: []any{}}, Metadata: JSONValue{Data: map[string]any{}}, ObservedAt: time.Now().UTC()}
	mustCreate(&record)
	mustCreate(&GormRollbackPoint{ProjectID: project.ID, DeploymentRecordID: validNullString(record.ID), DeploymentTargetID: validNullString(target.ID), Name: "rollback", Environment: "test", Revision: "rev", ImageRefs: JSONValue{Data: []any{}}, Metadata: JSONValue{Data: map[string]any{}}, CapturedAt: time.Now().UTC()})
	mustCreate(&GormKubernetesEnvironment{ProjectID: project.ID, Name: "kube", Environment: "test", ClusterName: "cluster", Namespace: "ns", Metadata: JSONValue{Data: map[string]any{}}})
	mustCreate(&GormSSHMachine{ProjectID: project.ID, Name: "ssh", CredentialID: validNullString(credential.ID), Metadata: JSONValue{Data: map[string]any{}}})
	mustCreate(&GormAIRuntime{ProjectID: validNullString(project.ID), Name: "runtime", Config: JSONValue{Data: map[string]any{}}})
	syncAsset := GormRepoSyncAsset{ProjectID: project.ID, ProjectGitRepositoryID: repo.ID, Name: "sync", SourceRemoteID: remote.ID, TargetRemoteID: remote.ID, Refs: JSONValue{Data: map[string]any{}}, Metadata: JSONValue{Data: map[string]any{}}}
	mustCreate(&syncAsset)
	webhook := GormWebhookConnection{ProjectID: project.ID, Name: "hook", SourceRemoteID: validNullString(remote.ID), EventTypes: JSONValue{Data: []any{}}, Metadata: JSONValue{Data: map[string]any{}}}
	mustCreate(&webhook)
	mustCreate(&GormWebhookEvent{WebhookConnectionID: validNullString(webhook.ID), ProjectID: validNullString(project.ID), MatchedRepoSyncAssetID: validNullString(syncAsset.ID), OperationRunID: validNullString(op.ID), DeliveryID: "delivery-1", Payload: JSONValue{Data: map[string]any{}}, Result: JSONValue{Data: map[string]any{}}, ReceivedAt: time.Now().UTC()})
	audit := GormWebhookThresholdDecisionAudit{ProjectID: project.ID, WebhookConnectionID: webhook.ID, DecisionState: "reviewed", EvidenceWindow: "7d", Evidence: JSONValue{Data: map[string]any{}}}
	mustCreate(&audit)
	mustCreate(&GormWebhookThresholdConfiguration{ProjectID: project.ID, WebhookConnectionID: webhook.ID, ThresholdKey: "runs", WarningAt: 1, DangerAt: 2, EvidenceWindow: "7d", SourceAuditID: validNullString(audit.ID), Evidence: JSONValue{Data: map[string]any{}}, AppliedAt: time.Now().UTC()})

	req := withRouteParam(httptest.NewRequest(http.MethodDelete, "/api/projects/"+project.ID, nil), "id", project.ID)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey{}, admin))
	rr := httptest.NewRecorder()
	server.deleteProject(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete project status = %d, body: %s", rr.Code, rr.Body.String())
	}
	for name, model := range map[string]any{
		"agent_context_snapshots":           &GormAgentContextSnapshot{},
		"agent_plans":                       &GormAgentPlan{},
		"agent_tasks":                       &GormAgentTask{},
		"agent_tool_calls":                  &GormAgentToolCall{},
		"agent_tool_tokens":                 &GormAgentToolToken{},
		"ai_runtimes":                       &GormAIRuntime{},
		"argo_apps":                         &GormArgoApp{},
		"argo_connections":                  &GormArgoConnection{},
		"asset_relations":                   &GormAssetRelation{},
		"asset_status_snapshots":            &GormAssetStatusSnapshot{},
		"assets":                            &GormAsset{},
		"connection_credentials":            &GormConnectionCredential{},
		"deployment_records":                &GormDeploymentRecord{},
		"deployment_targets":                &GormDeploymentTarget{},
		"git_remotes":                       &GormGitRemote{},
		"github_action_artifacts":           &GormGitHubActionArtifact{},
		"github_action_runs":                &GormGitHubActionRun{},
		"github_repository_labels":          &GormGitHubRepositoryLabel{},
		"kubernetes_environments":           &GormKubernetesEnvironment{},
		"operation_approval_decisions":      &GormOperationApprovalDecision{},
		"operation_approval_delegations":    &GormOperationApprovalDelegation{},
		"operation_approvals":               &GormOperationApproval{},
		"operation_logs":                    &GormOperationLog{},
		"operation_runs":                    &GormOperationRun{},
		"project_git_repositories":          &GormProjectGitRepository{},
		"project_members":                   &GormProjectMember{},
		"project_template_files":            &GormProjectTemplateFile{},
		"project_template_runs":             &GormProjectTemplateRun{},
		"project_versions":                  &GormProjectVersion{},
		"projects":                          &GormProject{},
		"provider_review_attempts":          &GormProviderReviewAttempt{},
		"repo_sync_assets":                  &GormRepoSyncAsset{},
		"repo_sync_policies":                &GormRepoSyncPolicy{},
		"repo_sync_runs":                    &GormRepoSyncRun{},
		"repo_tag_runs":                     &GormRepoTagRun{},
		"rollback_points":                   &GormRollbackPoint{},
		"ssh_command_runs":                  &GormSSHCommandRun{},
		"ssh_machines":                      &GormSSHMachine{},
		"webhook_connections":               &GormWebhookConnection{},
		"webhook_events":                    &GormWebhookEvent{},
		"webhook_threshold_configurations":  &GormWebhookThresholdConfiguration{},
		"webhook_threshold_decision_audits": &GormWebhookThresholdDecisionAudit{},
		"worker_jobs":                       &GormWorkerJob{},
	} {
		var count int64
		if err := store.Gorm.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", name, count)
		}
	}
	var providerAccount GormProviderAccount
	if err := store.Gorm.Where(&GormProviderAccount{Name: "account"}).First(&providerAccount).Error; err != nil {
		t.Fatalf("load provider account: %v", err)
	}
	if providerAccount.CredentialID.Valid {
		t.Fatalf("provider account credential id still linked: %#v", providerAccount.CredentialID)
	}
}
