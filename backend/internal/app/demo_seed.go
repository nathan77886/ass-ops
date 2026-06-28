package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

type DemoSeedResult struct {
	ProjectID         string `json:"project_id"`
	RepositoryID      string `json:"repository_id"`
	SourceRemote      string `json:"source_remote_id"`
	TargetRemote      string `json:"target_remote_id"`
	RepoSyncAsset     string `json:"repo_sync_asset_id"`
	WebhookConnection string `json:"webhook_connection_id"`
	SSHMachineID      string `json:"ssh_machine_id"`
	ArgoID            string `json:"argo_connection_id"`
	AIRuntimeID       string `json:"ai_runtime_id"`
	AgentTaskID       string `json:"agent_task_id"`
}

type demoSeedDefaults struct {
	ProjectSlug     string
	RepositoryKey   string
	SourceRemoteKey string
	TargetRemoteKey string
	SSHHost         string
	RepoSyncEnabled bool
}

type demoRemoteSeed struct {
	Name         string
	Kind         string
	RemoteKey    string
	ProviderType string
	RemoteURL    string
	WebURL       string
	RemoteRole   string
	IsPrimary    bool
}

func defaultDemoSeedDefaults() demoSeedDefaults {
	return demoSeedDefaults{
		ProjectSlug:     "assops-demo",
		RepositoryKey:   "demo-service",
		SourceRemoteKey: "gitea",
		TargetRemoteKey: "github",
		SSHHost:         "192.0.2.10",
		RepoSyncEnabled: false,
	}
}

func (s *Store) SeedDemoData(ctx context.Context, cfg Config) (*DemoSeedResult, error) {
	if err := s.SeedAdmin(ctx, cfg); err != nil {
		return nil, err
	}
	admin, err := s.UserByEmail(ctx, cfg.AdminEmail)
	if err != nil {
		return nil, fmt.Errorf("loading seed admin: %w", err)
	}
	if s.Gorm == nil {
		return nil, fmt.Errorf("gorm database is not configured")
	}

	defaults := defaultDemoSeedDefaults()
	var result DemoSeedResult
	if err := s.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		projectID, err := upsertDemoProject(ctx, tx)
		if err != nil {
			return err
		}
		if err := upsertDemoProjectMember(ctx, tx, projectID, admin.ID); err != nil {
			return err
		}
		repositoryID, err := upsertDemoRepository(ctx, tx, projectID)
		if err != nil {
			return err
		}
		sourceRemoteID, err := upsertDemoRemote(ctx, tx, repositoryID, demoRemoteSeed{
			Name: "Demo Gitea Origin", Kind: "gitea", RemoteKey: defaults.SourceRemoteKey,
			ProviderType: "gitea", RemoteURL: "ssh://git@gitea.example.com/demo/demo-service.git",
			WebURL: "https://gitea.example.com/demo/demo-service", RemoteRole: "source", IsPrimary: true,
		})
		if err != nil {
			return err
		}
		targetRemoteID, err := upsertDemoRemote(ctx, tx, repositoryID, demoRemoteSeed{
			Name: "Demo GitHub Mirror", Kind: "github", RemoteKey: defaults.TargetRemoteKey,
			ProviderType: "github", RemoteURL: "git@github.com:example/demo-service.git",
			WebURL: "https://github.com/example/demo-service", RemoteRole: "mirror", IsPrimary: false,
		})
		if err != nil {
			return err
		}
		repoSyncAssetID, err := upsertDemoRepoSyncAsset(ctx, tx, defaults, projectID, repositoryID, sourceRemoteID, targetRemoteID)
		if err != nil {
			return err
		}
		webhookConnectionID, err := upsertDemoWebhookConnection(ctx, tx, cfg, projectID, sourceRemoteID)
		if err != nil {
			return err
		}
		sshMachineID, err := upsertDemoSSHMachine(ctx, tx, defaults, projectID)
		if err != nil {
			return err
		}
		argoID, err := upsertDemoArgoConnection(ctx, tx, projectID)
		if err != nil {
			return err
		}
		aiRuntimeID, err := upsertDemoAIRuntime(ctx, tx, projectID)
		if err != nil {
			return err
		}
		agentTaskID, err := upsertDemoAgentTask(ctx, tx, projectID, admin.ID)
		if err != nil {
			return err
		}
		if err := upsertDemoOperationalHistory(ctx, tx, projectID, repositoryID, sourceRemoteID, targetRemoteID, repoSyncAssetID, webhookConnectionID, argoID, admin.ID); err != nil {
			return err
		}
		result = DemoSeedResult{ProjectID: projectID, RepositoryID: repositoryID, SourceRemote: sourceRemoteID, TargetRemote: targetRemoteID, RepoSyncAsset: repoSyncAssetID, WebhookConnection: webhookConnectionID, SSHMachineID: sshMachineID, ArgoID: argoID, AIRuntimeID: aiRuntimeID, AgentTaskID: agentTaskID}
		return nil
	}); err != nil {
		return nil, err
	}
	if _, err := s.SyncCanonicalAssets(ctx); err != nil {
		return nil, fmt.Errorf("syncing canonical assets after demo seed: %w", err)
	}
	if err := s.seedDemoManualAssetRelation(ctx, result.ProjectID, result.RepositoryID, result.RepoSyncAsset); err != nil {
		return nil, err
	}
	return &result, nil
}

func upsertDemoProject(ctx context.Context, tx *gorm.DB) (string, error) {
	project := GormProject{Name: "ASSOPS Demo", Slug: "assops-demo", Description: "Demo project seeded by assops-tool db seed-demo."}
	if err := tx.WithContext(ctx).Where(&GormProject{Slug: project.Slug}).Assign(project).FirstOrCreate(&project).Error; err != nil {
		return "", fmt.Errorf("upserting demo project: %w", err)
	}
	return project.ID, nil
}

func upsertDemoProjectMember(ctx context.Context, tx *gorm.DB, projectID, userID string) error {
	member := GormProjectMember{ProjectID: projectID, UserID: userID, Role: "owner"}
	if err := tx.WithContext(ctx).Where(&GormProjectMember{ProjectID: projectID, UserID: userID}).Assign(member).FirstOrCreate(&member).Error; err != nil {
		return fmt.Errorf("upserting demo project membership: %w", err)
	}
	return nil
}

func upsertDemoRepository(ctx context.Context, tx *gorm.DB, projectID string) (string, error) {
	repo := GormProjectGitRepository{ProjectID: projectID, Name: "Demo Service", RepoKey: "demo-service", DisplayName: "Demo Service", RepoRole: "service", Status: "active", Description: "Demo repository pair for source-to-mirror workflows.", DefaultBranch: "main"}
	if err := tx.WithContext(ctx).Where(&GormProjectGitRepository{ProjectID: projectID, RepoKey: repo.RepoKey}).Assign(repo).FirstOrCreate(&repo).Error; err != nil {
		return "", fmt.Errorf("upserting demo repository: %w", err)
	}
	return repo.ID, nil
}

func upsertDemoRemote(ctx context.Context, tx *gorm.DB, repositoryID string, seed demoRemoteSeed) (string, error) {
	remote := GormGitRemote{
		ProjectGitRepositoryID: repositoryID, Name: seed.Name, Kind: seed.Kind, RemoteKey: seed.RemoteKey,
		ProviderType: seed.ProviderType, RemoteURL: seed.RemoteURL, WebURL: seed.WebURL, RemoteRole: seed.RemoteRole,
		IsPrimary: seed.IsPrimary, SyncEnabled: true, Protected: false, URLs: JSONValue{Data: []string{seed.RemoteURL}},
		DefaultBranch: "main", Metadata: JSONValue{Data: map[string]any{"source": "demo_seed"}},
	}
	if err := tx.WithContext(ctx).Where(&GormGitRemote{ProjectGitRepositoryID: repositoryID, Name: seed.Name}).Assign(remote).FirstOrCreate(&remote).Error; err != nil {
		return "", fmt.Errorf("upserting demo remote %q: %w", seed.Name, err)
	}
	return remote.ID, nil
}

func upsertDemoRepoSyncAsset(ctx context.Context, tx *gorm.DB, defaults demoSeedDefaults, projectID, repositoryID, sourceRemoteID, targetRemoteID string) (string, error) {
	asset := GormRepoSyncAsset{
		ProjectID: projectID, ProjectGitRepositoryID: repositoryID, Name: "Demo Gitea to GitHub mirror",
		SourceRemoteID: sourceRemoteID, TargetRemoteID: targetRemoteID, TriggerMode: "manual_or_webhook",
		SyncMode: "selected_refs", Transport: "ssh", Driver: "projectops_worker_git_ssh",
		Refs: JSONValue{Data: map[string]any{"branches": []string{"main"}, "tags": []string{}}}, Enabled: defaults.RepoSyncEnabled,
		Metadata: JSONValue{Data: map[string]any{"source": "demo_seed", "note": "disabled by default to avoid accidental sync"}},
	}
	if err := tx.WithContext(ctx).Where(&GormRepoSyncAsset{ProjectGitRepositoryID: repositoryID, Name: asset.Name}).Assign(asset).FirstOrCreate(&asset).Error; err != nil {
		return "", fmt.Errorf("upserting demo repo sync asset: %w", err)
	}
	return asset.ID, nil
}

func upsertDemoWebhookConnection(ctx context.Context, tx *gorm.DB, cfg Config, projectID, sourceRemoteID string) (string, error) {
	secretCiphertext, err := (&Server{cfg: cfg}).encryptWebhookSecret("demo-webhook-secret")
	if err != nil {
		return "", fmt.Errorf("encrypting demo webhook secret: %w", err)
	}
	conn := GormWebhookConnection{ProjectID: projectID, Provider: "gitea", Name: "Demo Gitea push webhook", SourceRemoteID: validNullString(sourceRemoteID), SecretCiphertext: secretCiphertext, Enabled: true, EventTypes: JSONValue{Data: []string{"push"}}, LastDeliveryStatus: "verified", Metadata: JSONValue{Data: map[string]any{"source": "demo_seed", "secret_configured": true}}}
	if err := tx.WithContext(ctx).Where(&GormWebhookConnection{ProjectID: projectID, Name: conn.Name}).Assign(conn).FirstOrCreate(&conn).Error; err != nil {
		return "", fmt.Errorf("upserting demo webhook connection: %w", err)
	}
	return conn.ID, nil
}

func upsertDemoSSHMachine(ctx context.Context, tx *gorm.DB, defaults demoSeedDefaults, projectID string) (string, error) {
	machine := GormSSHMachine{ProjectID: projectID, Name: "Demo Deploy Host", Host: defaults.SSHHost, Port: 22, Username: "deploy", AuthType: "key", Metadata: JSONValue{Data: map[string]any{"source": "demo_seed", "environment": "demo"}}}
	if err := tx.WithContext(ctx).Where(&GormSSHMachine{ProjectID: projectID, Name: machine.Name}).Assign(machine).FirstOrCreate(&machine).Error; err != nil {
		return "", fmt.Errorf("upserting demo SSH machine: %w", err)
	}
	return machine.ID, nil
}

func upsertDemoArgoConnection(ctx context.Context, tx *gorm.DB, projectID string) (string, error) {
	conn := GormArgoConnection{ProjectID: projectID, Name: "Demo Argo CD", ServerURL: "https://argocd.example.com", AuthType: "token", Config: JSONValue{Data: map[string]any{"source": "demo_seed", "project": "demo"}}}
	if err := tx.WithContext(ctx).Where(&GormArgoConnection{ProjectID: projectID, Name: conn.Name}).Assign(conn).FirstOrCreate(&conn).Error; err != nil {
		return "", fmt.Errorf("upserting demo Argo connection: %w", err)
	}
	return conn.ID, nil
}

func upsertDemoAIRuntime(ctx context.Context, tx *gorm.DB, projectID string) (string, error) {
	runtime := GormAIRuntime{ProjectID: validNullString(projectID), Name: "Demo Codex Runtime", RuntimeType: "codex-cli", CodexBinary: "codex", Model: "", Config: JSONValue{Data: map[string]any{"source": "demo_seed", "mode": "read_only_first"}}, Status: "unknown"}
	if err := tx.WithContext(ctx).Where(&GormAIRuntime{ProjectID: validNullString(projectID), Name: runtime.Name}).Assign(runtime).FirstOrCreate(&runtime).Error; err != nil {
		return "", fmt.Errorf("upserting demo AI runtime: %w", err)
	}
	return runtime.ID, nil
}

func upsertDemoAgentTask(ctx context.Context, tx *gorm.DB, projectID, adminID string) (string, error) {
	task := GormAgentTask{ProjectID: projectID, Title: "Review demo operations", Prompt: demoAgentPrompt(), CreatedBy: validNullString(adminID), Status: "draft"}
	if err := tx.WithContext(ctx).Where(&GormAgentTask{ProjectID: projectID, Title: task.Title}).Assign(task).FirstOrCreate(&task).Error; err != nil {
		return "", fmt.Errorf("upserting demo agent task: %w", err)
	}
	return task.ID, nil
}

func upsertDemoOperationalHistory(ctx context.Context, tx *gorm.DB, projectID, repositoryID, sourceRemoteID, targetRemoteID, repoSyncAssetID, webhookConnectionID, argoID, adminID string) error {
	completedOpID, err := upsertDemoOperationRun(ctx, tx, projectID, targetRemoteID, "repo.sync", "Demo completed mirror sync", "completed", "", map[string]any{"ref": "refs/heads/main"}, map[string]any{"after_sha": "0123456789abcdef0123456789abcdef01234567"}, -72)
	if err != nil {
		return err
	}
	if err := upsertDemoRepoSyncRun(ctx, tx, completedOpID, projectID, repositoryID, sourceRemoteID, targetRemoteID, repoSyncAssetID, adminID, "refs/heads/main", "completed", "", -72); err != nil {
		return err
	}
	failedOpID, err := upsertDemoOperationRun(ctx, tx, projectID, targetRemoteID, "repo.sync", "Demo failed mirror sync", "failed", "target remote rejected non-fast-forward update", map[string]any{"ref": "refs/heads/release"}, map[string]any{"retryable": true}, -30)
	if err != nil {
		return err
	}
	if err := upsertDemoRepoSyncRun(ctx, tx, failedOpID, projectID, repositoryID, sourceRemoteID, targetRemoteID, repoSyncAssetID, adminID, "refs/heads/release", "failed", "target remote rejected non-fast-forward update", -30); err != nil {
		return err
	}
	githubActionRunID, err := upsertDemoGitHubActionRun(ctx, tx, completedOpID, targetRemoteID)
	if err != nil {
		return err
	}
	if err := upsertDemoGitHubActionArtifact(ctx, tx, githubActionRunID, targetRemoteID); err != nil {
		return err
	}
	if err := upsertDemoGitHubRepositoryLabels(ctx, tx, completedOpID, targetRemoteID); err != nil {
		return err
	}
	tagOpID, err := upsertDemoOperationRun(ctx, tx, projectID, targetRemoteID, "repo.tag", "Demo release tag", "completed", "", map[string]any{"tag_name": "v0.1.0", "target_sha": "0123456789abcdef0123456789abcdef01234567"}, map[string]any{"remote_tag_found": true, "matched_sha_present": true}, -68)
	if err != nil {
		return err
	}
	if err := upsertDemoRepoTagRun(ctx, tx, tagOpID, projectID, repositoryID, targetRemoteID, adminID); err != nil {
		return err
	}
	if err := upsertDemoWebhookEvents(ctx, tx, webhookConnectionID, projectID, repoSyncAssetID, completedOpID); err != nil {
		return err
	}
	if _, err := upsertDemoOperationRun(ctx, tx, projectID, "", "argo.apps.sync", "Demo Argo app sync", "completed", "", map[string]any{"argo_connection_id": argoID}, map[string]any{"synced_apps": 1, "deployment_targets": 1}, -69); err != nil {
		return err
	}
	if err := upsertDemoKubernetesEnvironment(ctx, tx, projectID); err != nil {
		return err
	}
	if err := upsertDemoDeploymentReadModel(ctx, tx, projectID, argoID); err != nil {
		return err
	}
	if err := upsertDemoApproval(ctx, tx, projectID, repoSyncAssetID, adminID); err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Model(&GormRepoSyncAsset{}).Where(&GormRepoSyncAsset{GormBase: GormBase{ID: repoSyncAssetID}}).Updates(map[string]any{"last_sync_status": "failed", "last_sync_run_id": validNullString(failedOpID), "last_synced_at": validNullTime(demoSeedTime(-30))}).Error; err != nil {
		return fmt.Errorf("updating demo repo sync asset status: %w", err)
	}
	return nil
}

func upsertDemoOperationRun(ctx context.Context, tx *gorm.DB, projectID, remoteID, operationType, title, status, errorMessage string, input, result map[string]any, hoursAgo int) (string, error) {
	started := demoSeedTime(hoursAgo)
	run := GormOperationRun{ProjectID: validNullString(projectID), GitRemoteID: validNullString(remoteID), OperationType: operationType, Status: status, Title: title, Input: JSONValue{Data: input}, Result: JSONValue{Data: result}, Error: errorMessage, StartedAt: validNullTime(started), FinishedAt: validNullTime(started.Add(4 * time.Minute))}
	if err := tx.WithContext(ctx).Where(&GormOperationRun{ProjectID: validNullString(projectID), OperationType: operationType, Title: title}).Assign(run).FirstOrCreate(&run).Error; err != nil {
		return "", fmt.Errorf("upserting demo operation %q: %w", title, err)
	}
	return run.ID, nil
}

func upsertDemoRepoSyncRun(ctx context.Context, tx *gorm.DB, operationRunID, projectID, repositoryID, sourceRemoteID, targetRemoteID, repoSyncAssetID, adminID, ref, status, errorMessage string, hoursAgo int) error {
	if err := tx.WithContext(ctx).Where(&GormRepoSyncRun{OperationRunID: operationRunID}).Delete(&GormRepoSyncRun{}).Error; err != nil {
		return fmt.Errorf("deleting existing demo repo sync run: %w", err)
	}
	started := demoSeedTime(hoursAgo)
	run := GormRepoSyncRun{OperationRunID: operationRunID, GitRemoteID: targetRemoteID, ProjectID: validNullString(projectID), ProjectGitRepositoryID: validNullString(repositoryID), RepoSyncAssetID: validNullString(repoSyncAssetID), SourceRemoteID: validNullString(sourceRemoteID), TargetRemoteID: validNullString(targetRemoteID), Ref: ref, BeforeSHA: "0000000000000000000000000000000000000000", AfterSHA: "0123456789abcdef0123456789abcdef01234567", ActorUserID: validNullString(adminID), Status: status, Stdout: demoRepoSyncStdout(status), ErrorMessage: errorMessage, StartedAt: validNullTime(started), FinishedAt: validNullTime(started.Add(4 * time.Minute)), CreatedAt: started}
	if err := tx.WithContext(ctx).Create(&run).Error; err != nil {
		return fmt.Errorf("inserting demo repo sync run: %w", err)
	}
	return nil
}

func upsertDemoGitHubActionRun(ctx context.Context, tx *gorm.DB, operationRunID, targetRemoteID string) (string, error) {
	started := demoSeedTime(-70)
	run := GormGitHubActionRun{OperationRunID: validNullString(operationRunID), GitRemoteID: targetRemoteID, ExternalRunID: "100200300", RunID: "100200300", WorkflowName: "CI", Branch: "main", CommitSHA: "0123456789abcdef0123456789abcdef01234567", Status: "completed", Conclusion: "success", HTMLURL: "https://github.com/example/demo-service/actions/runs/100200300", Metadata: JSONValue{Data: map[string]any{"source": "demo_seed"}}, StartedAt: validNullTime(started), UpdatedAt: validNullTime(demoSeedTime(-69)), SyncedAt: validNullTime(demoSeedTime(-69)), CreatedAt: started}
	if err := tx.WithContext(ctx).Where(&GormGitHubActionRun{GitRemoteID: targetRemoteID, ExternalRunID: run.ExternalRunID}).Assign(run).FirstOrCreate(&run).Error; err != nil {
		return "", fmt.Errorf("upserting demo GitHub Action run: %w", err)
	}
	return run.ID, nil
}

func upsertDemoGitHubActionArtifact(ctx context.Context, tx *gorm.DB, githubActionRunID, targetRemoteID string) error {
	artifact := GormGitHubActionArtifact{GitRemoteID: targetRemoteID, GitHubActionRunID: githubActionRunID, ExternalArtifactID: "artifact-demo-build", Name: "demo-service-linux-amd64.tar.gz", SizeInBytes: 1048576, Expired: false, Metadata: JSONValue{Data: map[string]any{"source": "demo_seed"}}, CreatedAt: validNullTime(demoSeedTime(-69)), UpdatedAt: validNullTime(demoSeedTime(-69)), ExpiresAt: validNullTime(time.Now().Add(21 * 24 * time.Hour)), SyncedAt: demoSeedTime(-69)}
	if err := tx.WithContext(ctx).Where(&GormGitHubActionArtifact{GitHubActionRunID: githubActionRunID, ExternalArtifactID: artifact.ExternalArtifactID}).Assign(artifact).FirstOrCreate(&artifact).Error; err != nil {
		return fmt.Errorf("upserting demo GitHub Action artifact: %w", err)
	}
	return nil
}

func upsertDemoGitHubRepositoryLabels(ctx context.Context, tx *gorm.DB, operationRunID, targetRemoteID string) error {
	for _, label := range []GormGitHubRepositoryLabel{
		{OperationRunID: validNullString(operationRunID), GitRemoteID: targetRemoteID, ExternalLabelID: "label-demo-bug", NodeID: "LA_demo_bug", Name: "bug", Color: "d73a4a", Description: "Something is not working", IsDefault: true, SyncedAt: demoSeedTime(-69), CreatedAt: demoSeedTime(-69)},
		{OperationRunID: validNullString(operationRunID), GitRemoteID: targetRemoteID, ExternalLabelID: "label-demo-deploy", NodeID: "LA_demo_deploy", Name: "deploy", Color: "0e8a16", Description: "Deployment or release work", IsDefault: false, SyncedAt: demoSeedTime(-69), CreatedAt: demoSeedTime(-69)},
	} {
		if err := upsertDemoGitHubRepositoryLabel(ctx, tx, label); err != nil {
			return fmt.Errorf("upserting demo GitHub repository label %q: %w", label.Name, err)
		}
	}
	return nil
}

func upsertDemoGitHubRepositoryLabel(ctx context.Context, tx *gorm.DB, label GormGitHubRepositoryLabel) error {
	var existing []GormGitHubRepositoryLabel
	if err := tx.WithContext(ctx).Where(&GormGitHubRepositoryLabel{GitRemoteID: label.GitRemoteID}).Find(&existing).Error; err != nil {
		return err
	}
	for _, row := range existing {
		if strings.EqualFold(row.Name, label.Name) {
			label.ID = row.ID
			label.CreatedAt = row.CreatedAt
			return tx.WithContext(ctx).Save(&label).Error
		}
	}
	return tx.WithContext(ctx).Create(&label).Error
}

func upsertDemoRepoTagRun(ctx context.Context, tx *gorm.DB, operationRunID, projectID, repositoryID, targetRemoteID, adminID string) error {
	if err := tx.WithContext(ctx).Where(&GormRepoTagRun{OperationRunID: operationRunID}).Delete(&GormRepoTagRun{}).Error; err != nil {
		return fmt.Errorf("deleting existing demo repo tag run: %w", err)
	}
	started := demoSeedTime(-68)
	run := GormRepoTagRun{OperationRunID: operationRunID, GitRemoteID: targetRemoteID, ProjectID: validNullString(projectID), ProjectGitRepositoryID: validNullString(repositoryID), TargetRemoteID: validNullString(targetRemoteID), TagName: "v0.1.0", TargetSHA: "0123456789abcdef0123456789abcdef01234567", TagMessage: "Demo release tag", ActorUserID: validNullString(adminID), Status: "completed", Stdout: "created demo tag v0.1.0", StartedAt: validNullTime(started), FinishedAt: validNullTime(started.Add(4 * time.Minute)), CreatedAt: started}
	if err := tx.WithContext(ctx).Create(&run).Error; err != nil {
		return fmt.Errorf("inserting demo repo tag run: %w", err)
	}
	return nil
}

func upsertDemoWebhookEvents(ctx context.Context, tx *gorm.DB, webhookConnectionID, projectID, repoSyncAssetID, operationRunID string) error {
	for _, event := range []struct {
		deliveryID, status, err string
		hoursAgo                int
	}{{"demo-delivery-main", "processed", "", -71}, {"demo-delivery-ignored", "ignored", "push ref refs/heads/docs did not match enabled RepoSyncAsset refs", -18}} {
		received := demoSeedTime(event.hoursAgo)
		row := GormWebhookEvent{WebhookConnectionID: validNullString(webhookConnectionID), ProjectID: validNullString(projectID), Provider: "gitea", EventType: "push", DeliveryID: event.deliveryID, SignatureValid: true, MatchedRepoSyncAssetID: validNullString(repoSyncAssetID), OperationRunID: validNullString(operationRunID), Status: event.status, ErrorMessage: event.err, Payload: JSONValue{Data: map[string]any{"ref": "refs/heads/main", "repository": map[string]any{"full_name": "demo/demo-service"}}}, Result: JSONValue{Data: map[string]any{"source": "demo_seed", "status": event.status}}, ReceivedAt: received, ProcessedAt: validNullTime(received.Add(time.Minute))}
		if err := tx.WithContext(ctx).Where(&GormWebhookEvent{WebhookConnectionID: validNullString(webhookConnectionID), DeliveryID: event.deliveryID, SignatureValid: true}).Assign(row).FirstOrCreate(&row).Error; err != nil {
			return fmt.Errorf("upserting demo webhook event %q: %w", event.deliveryID, err)
		}
	}
	return nil
}

func upsertDemoKubernetesEnvironment(ctx context.Context, tx *gorm.DB, projectID string) error {
	env := GormKubernetesEnvironment{ProjectID: projectID, Name: "Demo Kubernetes Environment", Environment: "staging", ClusterName: "demo-cluster", Namespace: "demo", KubeconfigSecretRef: "demo/assops-reader.yaml", ServiceAccount: "system:serviceaccount:demo:assops-reader", TokenSubjectReviewStatus: "reviewed", RBACReadLogsStatus: "reviewed", PodRestartStatus: "reviewed", Status: "ready", Metadata: JSONValue{Data: map[string]any{"source": "demo_seed", "scope": "namespace_readonly"}}}
	if err := tx.WithContext(ctx).Where(&GormKubernetesEnvironment{ProjectID: projectID, Environment: env.Environment, ClusterName: env.ClusterName, Namespace: env.Namespace}).Assign(env).FirstOrCreate(&env).Error; err != nil {
		return fmt.Errorf("upserting demo Kubernetes environment: %w", err)
	}
	return nil
}

func upsertDemoDeploymentReadModel(ctx context.Context, tx *gorm.DB, projectID, argoID string) error {
	target := GormDeploymentTarget{ProjectID: projectID, Name: "demo-cluster/demo", Environment: "staging", ClusterName: "demo-cluster", Namespace: "demo", Source: "argocd", ArgoConnectionID: validNullString(argoID), Status: "Synced", Metadata: JSONValue{Data: map[string]any{"source": "demo_seed"}}}
	if err := tx.WithContext(ctx).Where(&GormDeploymentTarget{ProjectID: projectID, Environment: target.Environment, ClusterName: target.ClusterName, Namespace: target.Namespace}).Assign(target).FirstOrCreate(&target).Error; err != nil {
		return fmt.Errorf("upserting demo deployment target: %w", err)
	}
	app := GormArgoApp{ProjectID: projectID, ArgoConnectionID: validNullString(argoID), DeploymentTargetID: validNullString(target.ID), Name: "demo-service", Namespace: "demo", Status: "Synced", Metadata: JSONValue{Data: map[string]any{"source": "demo_seed"}}}
	if err := tx.WithContext(ctx).Where(&GormArgoApp{ArgoConnectionID: validNullString(argoID), Name: app.Name}).Assign(app).FirstOrCreate(&app).Error; err != nil {
		return fmt.Errorf("upserting demo Argo app: %w", err)
	}
	record := GormDeploymentRecord{ProjectID: projectID, DeploymentTargetID: validNullString(target.ID), ArgoConnectionID: validNullString(argoID), ArgoAppID: validNullString(app.ID), Name: "demo-service", Environment: "staging", Namespace: "demo", ClusterName: "demo-cluster", Source: "argocd", Status: "Synced", Revision: "0123456789abcdef0123456789abcdef01234567", ImageRefs: JSONValue{Data: []string{"ghcr.io/example/demo-service:demo"}}, Metadata: JSONValue{Data: map[string]any{"source": "demo_seed"}}, ObservedAt: demoSeedTime(-69)}
	if err := tx.WithContext(ctx).Where(&GormDeploymentRecord{ProjectID: projectID, Source: record.Source, Name: record.Name, Environment: record.Environment, Namespace: record.Namespace, ClusterName: record.ClusterName}).Assign(record).FirstOrCreate(&record).Error; err != nil {
		return fmt.Errorf("upserting demo deployment record: %w", err)
	}
	rollback := GormRollbackPoint{ProjectID: projectID, DeploymentRecordID: validNullString(record.ID), DeploymentTargetID: validNullString(target.ID), Name: "demo-service", Environment: "staging", Revision: record.Revision, ImageRefs: record.ImageRefs, Source: "argocd", Status: "available", Metadata: JSONValue{Data: map[string]any{"source": "demo_seed"}}, CapturedAt: demoSeedTime(-69)}
	if err := tx.WithContext(ctx).Where(&GormRollbackPoint{ProjectID: projectID, Source: rollback.Source, Name: rollback.Name, Environment: rollback.Environment, Revision: rollback.Revision}).Assign(rollback).FirstOrCreate(&rollback).Error; err != nil {
		return fmt.Errorf("upserting demo rollback point: %w", err)
	}
	return nil
}

func upsertDemoApproval(ctx context.Context, tx *gorm.DB, projectID, repoSyncAssetID, adminID string) error {
	approval := GormOperationApproval{ProjectID: validNullString(projectID), ResourceType: "repo_sync_asset", ResourceID: repoSyncAssetID, Action: "repo.sync", Title: "Demo pending mirror sync approval", RequestPayload: JSONValue{Data: map[string]any{"source": "demo_seed", "repo_sync_asset_id": repoSyncAssetID}}, Status: "pending", RequiredApproverRoles: pq.StringArray{"admin", "owner"}, RequiredApprovalCount: 2, NotificationChannels: pq.StringArray{"ui"}, NotificationStatus: "pending", RequestedBy: validNullString(adminID), DecidedBy: validNullString(adminID), DecisionReason: "seeded first approval; waiting for another approver", ExpiresAt: validNullTime(time.Now().Add(24 * time.Hour))}
	if err := tx.WithContext(ctx).Where(&GormOperationApproval{ResourceType: approval.ResourceType, ResourceID: repoSyncAssetID, Action: approval.Action}).Assign(approval).FirstOrCreate(&approval).Error; err != nil {
		return fmt.Errorf("upserting demo approval: %w", err)
	}
	decision := GormOperationApprovalDecision{OperationApprovalID: approval.ID, UserID: validNullString(adminID), Decision: "approved", Reason: "Seeded first approval for demo multi-approver progress", DecidedAt: time.Now()}
	if err := tx.WithContext(ctx).Where(&GormOperationApprovalDecision{OperationApprovalID: approval.ID, UserID: validNullString(adminID)}).Assign(decision).FirstOrCreate(&decision).Error; err != nil {
		return fmt.Errorf("upserting demo approval decision: %w", err)
	}
	return nil
}

func (s *Store) seedDemoManualAssetRelation(ctx context.Context, projectID, repositoryID, repoSyncAssetID string) error {
	if s.Gorm == nil {
		return fmt.Errorf("gorm database is not configured")
	}
	var fromAsset GormAsset
	if err := s.Gorm.WithContext(ctx).Where(&GormAsset{AssetType: "repository", SourceTable: "project_git_repositories", SourceID: validNullString(repositoryID)}).First(&fromAsset).Error; err != nil {
		return fmt.Errorf("loading demo repository asset: %w", err)
	}
	var toAsset GormAsset
	if err := s.Gorm.WithContext(ctx).Where(&GormAsset{AssetType: "repo_sync", SourceTable: "repo_sync_assets", SourceID: validNullString(repoSyncAssetID)}).First(&toAsset).Error; err != nil {
		return fmt.Errorf("loading demo repo sync asset: %w", err)
	}
	relation := GormAssetRelation{ProjectID: validNullString(projectID), FromAssetID: fromAsset.ID, ToAssetID: toAsset.ID, RelationType: "observes", Metadata: JSONValue{Data: map[string]any{"source": "manual", "demo_seed": true, "note": "operator-curated demo relation"}}}
	if err := s.Gorm.WithContext(ctx).Where(&GormAssetRelation{FromAssetID: fromAsset.ID, ToAssetID: toAsset.ID, RelationType: relation.RelationType}).Assign(relation).FirstOrCreate(&relation).Error; err != nil {
		return fmt.Errorf("upserting demo manual asset relation: %w", err)
	}
	return nil
}

func demoSeedTime(hoursAgo int) time.Time { return time.Now().Add(time.Duration(hoursAgo) * time.Hour) }

func demoRepoSyncStdout(status string) string {
	if status == "completed" {
		return "Fetched refs/heads/main from Demo Gitea Origin and pushed to Demo GitHub Mirror."
	}
	return "Fetched refs/heads/release from Demo Gitea Origin; push was rejected by target policy."
}

func demoAgentPrompt() string {
	return "Summarize project assets, repository sync state, deployment posture, SSH access, and approvals. Do not mutate anything."
}
