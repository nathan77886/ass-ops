package app

import (
	"context"
	"fmt"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"strings"
	"time"
)

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
