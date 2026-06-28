package app

import (
	"context"
	"fmt"
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
