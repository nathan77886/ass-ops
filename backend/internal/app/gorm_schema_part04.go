package app

import (
	"database/sql"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"time"
)

type GormWebhookThresholdDecisionAudit struct {
	ID                   string         `gorm:"type:uuid;primaryKey" json:"id"`
	ProjectID            string         `gorm:"type:uuid;not null;index" json:"project_id"`
	WebhookConnectionID  string         `gorm:"type:uuid;not null;index:webhook_threshold_decision_audits_once,unique" json:"webhook_connection_id"`
	Provider             string         `gorm:"not null;default:''" json:"provider"`
	ThresholdReviewState string         `gorm:"not null;default:''" json:"threshold_review_state"`
	DecisionState        string         `gorm:"not null;default:'';index:webhook_threshold_decision_audits_once,unique" json:"decision_state"`
	OperatorDecision     string         `gorm:"not null;default:''" json:"operator_decision"`
	EvidenceWindow       string         `gorm:"not null;default:'7d';index:webhook_threshold_decision_audits_once,unique" json:"evidence_window"`
	Evidence             JSONValue      `gorm:"type:jsonb;not null" json:"evidence"`
	CreatedBy            sql.NullString `gorm:"type:uuid;index" json:"created_by"`
	CreatedAt            time.Time      `json:"created_at"`
}

func (GormWebhookThresholdDecisionAudit) TableName() string {
	return "webhook_threshold_decision_audits"
}

func (m *GormWebhookThresholdDecisionAudit) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormWebhookThresholdConfiguration struct {
	ID                  string         `gorm:"type:uuid;primaryKey" json:"id"`
	ProjectID           string         `gorm:"type:uuid;not null;index" json:"project_id"`
	WebhookConnectionID string         `gorm:"type:uuid;not null;index:webhook_threshold_configurations_once,unique" json:"webhook_connection_id"`
	Provider            string         `gorm:"not null;default:''" json:"provider"`
	ThresholdKey        string         `gorm:"not null;index:webhook_threshold_configurations_once,unique" json:"threshold_key"`
	WarningAt           int            `gorm:"not null" json:"warning_at"`
	DangerAt            int            `gorm:"not null" json:"danger_at"`
	Unit                string         `gorm:"not null;default:''" json:"unit"`
	EvidenceWindow      string         `gorm:"not null;default:'7d';index:webhook_threshold_configurations_once,unique" json:"evidence_window"`
	SourceAuditID       sql.NullString `gorm:"type:uuid;index" json:"source_audit_id"`
	Evidence            JSONValue      `gorm:"type:jsonb;not null" json:"evidence"`
	AppliedBy           sql.NullString `gorm:"type:uuid;index" json:"applied_by"`
	AppliedAt           time.Time      `gorm:"not null;index" json:"applied_at"`
}

func (GormWebhookThresholdConfiguration) TableName() string {
	return "webhook_threshold_configurations"
}

func (m *GormWebhookThresholdConfiguration) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormGitHubActionArtifact struct {
	ID                 string       `gorm:"type:uuid;primaryKey" json:"id"`
	GitRemoteID        string       `gorm:"type:uuid;not null;index" json:"git_remote_id"`
	GitHubActionRunID  string       `gorm:"column:github_action_run_id;type:uuid;not null;index:github_action_artifacts_run_external_key,unique" json:"github_action_run_id"`
	ExternalArtifactID string       `gorm:"not null;default:'';index:github_action_artifacts_run_external_key,unique" json:"external_artifact_id"`
	Name               string       `gorm:"not null;default:'';index" json:"name"`
	SizeInBytes        int64        `gorm:"not null;default:0" json:"size_in_bytes"`
	Expired            bool         `gorm:"not null;default:false" json:"expired"`
	Metadata           JSONValue    `gorm:"type:jsonb;not null" json:"metadata"`
	CreatedAt          sql.NullTime `json:"created_at"`
	UpdatedAt          sql.NullTime `json:"updated_at"`
	ExpiresAt          sql.NullTime `json:"expires_at"`
	SyncedAt           time.Time    `gorm:"not null;index" json:"synced_at"`
}

func (GormGitHubActionArtifact) TableName() string { return "github_action_artifacts" }

func (m *GormGitHubActionArtifact) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormKubernetesEnvironment struct {
	GormBase
	ProjectID                  string    `gorm:"type:uuid;not null;index:kubernetes_environments_scope_key,unique" json:"project_id"`
	Name                       string    `gorm:"not null" json:"name"`
	Environment                string    `gorm:"not null;default:'';index:kubernetes_environments_scope_key,unique" json:"environment"`
	ClusterName                string    `gorm:"not null;default:'';index:kubernetes_environments_scope_key,unique" json:"cluster_name"`
	Namespace                  string    `gorm:"not null;default:'';index:kubernetes_environments_scope_key,unique" json:"namespace"`
	KubeconfigSecretRef        string    `gorm:"not null;default:''" json:"kubeconfig_secret_ref"`
	KubeconfigSecretCiphertext string    `gorm:"not null;default:''" json:"-"`
	ServiceAccount             string    `gorm:"not null;default:''" json:"service_account"`
	TokenSubjectReviewStatus   string    `gorm:"not null;default:'not_reviewed'" json:"token_subject_review_status"`
	RBACReadLogsStatus         string    `gorm:"not null;default:'not_reviewed'" json:"rbac_read_logs_status"`
	PodRestartStatus           string    `gorm:"not null;default:'not_reviewed'" json:"pod_restart_status"`
	Status                     string    `gorm:"not null;default:'metadata_only';index" json:"status"`
	Metadata                   JSONValue `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormKubernetesEnvironment) TableName() string { return "kubernetes_environments" }

type GormGitHubRepositoryLabel struct {
	ID              string         `gorm:"type:uuid;primaryKey" json:"id"`
	OperationRunID  sql.NullString `gorm:"type:uuid;index" json:"operation_run_id"`
	GitRemoteID     string         `gorm:"type:uuid;not null;index" json:"git_remote_id"`
	ExternalLabelID string         `gorm:"not null;default:'';index" json:"external_label_id"`
	NodeID          string         `gorm:"not null;default:''" json:"node_id"`
	Name            string         `gorm:"not null;default:'';index" json:"name"`
	Color           string         `gorm:"not null;default:''" json:"color"`
	Description     string         `gorm:"not null;default:''" json:"description"`
	IsDefault       bool           `gorm:"not null;default:false" json:"is_default"`
	SyncedAt        time.Time      `gorm:"not null;index" json:"synced_at"`
	CreatedAt       time.Time      `json:"created_at"`
}

func (GormGitHubRepositoryLabel) TableName() string { return "github_repository_labels" }

func (m *GormGitHubRepositoryLabel) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

func gormSchemaModels() []any {
	return []any{
		&GormUser{},
		&GormProject{},
		&GormProjectMember{},
		&GormProjectGitRepository{},
		&GormConnectionCredential{},
		&GormProviderAccount{},
		&GormGitRemote{},
		&GormRepoSyncPolicy{},
		&GormProjectVersion{},
		&GormOperationRun{},
		&GormWorkerNode{},
		&GormWorkerNodeToken{},
		&GormWorkerJob{},
		&GormOperationLog{},
		&GormRepoSyncRun{},
		&GormRepoTagRun{},
		&GormGitHubActionRun{},
		&GormAIRuntime{},
		&GormAgentTask{},
		&GormAgentPlan{},
		&GormAgentToolCall{},
		&GormAgentContextSnapshot{},
		&GormAgentToolToken{},
		&GormArgoConnection{},
		&GormArgoApp{},
		&GormSSHMachine{},
		&GormSSHCommandRun{},
		&GormDeploymentTarget{},
		&GormDeploymentRecord{},
		&GormRollbackPoint{},
		&GormAsset{},
		&GormAssetRelation{},
		&GormAssetStatusSnapshot{},
		&GormProjectTemplate{},
		&GormProjectTemplateRun{},
		&GormProjectTemplateFile{},
		&GormRepoSyncAsset{},
		&GormWebhookConnection{},
		&GormWebhookEvent{},
		&GormOperationApproval{},
		&GormOperationApprovalRule{},
		&GormOperationApprovalDecision{},
		&GormOperationApprovalDelegation{},
		&GormOperationApprovalRuleAudit{},
		&GormOperationApprovalView{},
		&GormAssetGraphView{},
		&GormProviderReviewAttempt{},
		&GormWebhookThresholdDecisionAudit{},
		&GormWebhookThresholdConfiguration{},
		&GormGitHubActionArtifact{},
		&GormKubernetesEnvironment{},
		&GormGitHubRepositoryLabel{},
	}
}

func gormSchemaModelsForAutoMigrate(db *gorm.DB) ([]any, error) {
	if db == nil {
		return nil, nil
	}
	for _, model := range gormSchemaModels() {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(model); err != nil {
			return nil, err
		}
	}
	return gormSchemaModels(), nil
}
