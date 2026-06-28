package app

import (
	"database/sql"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"time"
)

func (m *GormRepoSyncRun) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormRepoTagRun struct {
	ID                     string         `gorm:"type:uuid;primaryKey" json:"id"`
	OperationRunID         string         `gorm:"type:uuid;not null;index" json:"operation_run_id"`
	GitRemoteID            string         `gorm:"type:uuid;not null;index" json:"git_remote_id"`
	ProjectID              sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	ProjectGitRepositoryID sql.NullString `gorm:"type:uuid;index" json:"project_git_repository_id"`
	TargetRemoteID         sql.NullString `gorm:"type:uuid;index" json:"target_remote_id"`
	TagName                string         `gorm:"not null;default:''" json:"tag_name"`
	TargetSHA              string         `gorm:"not null;default:''" json:"target_sha"`
	TagMessage             string         `gorm:"not null;default:''" json:"tag_message"`
	ActorUserID            sql.NullString `gorm:"type:uuid;index" json:"actor_user_id"`
	Status                 string         `gorm:"not null;default:'queued';index" json:"status"`
	Stdout                 string         `gorm:"not null;default:''" json:"stdout"`
	Stderr                 string         `gorm:"not null;default:''" json:"stderr"`
	ErrorMessage           string         `gorm:"not null;default:''" json:"error_message"`
	StartedAt              sql.NullTime   `json:"started_at"`
	FinishedAt             sql.NullTime   `json:"finished_at"`
	CreatedAt              time.Time      `json:"created_at"`
}

func (GormRepoTagRun) TableName() string { return "repo_tag_runs" }

func (m *GormRepoTagRun) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormGitHubActionRun struct {
	ID             string         `gorm:"type:uuid;primaryKey" json:"id"`
	OperationRunID sql.NullString `gorm:"type:uuid;index" json:"operation_run_id"`
	GitRemoteID    string         `gorm:"type:uuid;not null;index" json:"git_remote_id"`
	ExternalRunID  string         `gorm:"not null;default:'';index" json:"external_run_id"`
	WorkflowName   string         `gorm:"not null;default:''" json:"workflow_name"`
	RunID          string         `gorm:"not null;default:''" json:"run_id"`
	Branch         string         `gorm:"not null;default:''" json:"branch"`
	CommitSHA      string         `gorm:"not null;default:''" json:"commit_sha"`
	Status         string         `gorm:"not null;default:'queued';index" json:"status"`
	Conclusion     string         `gorm:"not null;default:''" json:"conclusion"`
	HTMLURL        string         `gorm:"not null;default:''" json:"html_url"`
	Metadata       JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
	StartedAt      sql.NullTime   `json:"started_at"`
	UpdatedAt      sql.NullTime   `json:"updated_at"`
	SyncedAt       sql.NullTime   `json:"synced_at"`
	CreatedAt      time.Time      `json:"created_at"`
}

func (GormGitHubActionRun) TableName() string { return "github_action_runs" }

func (m *GormGitHubActionRun) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormAgentTask struct {
	GormBase
	ProjectID string         `gorm:"type:uuid;not null;index" json:"project_id"`
	Title     string         `gorm:"not null" json:"title"`
	Prompt    string         `gorm:"not null" json:"prompt"`
	Status    string         `gorm:"not null;default:'draft';index" json:"status"`
	CreatedBy sql.NullString `gorm:"type:uuid;index" json:"created_by"`
}

func (GormAgentTask) TableName() string { return "agent_tasks" }

type GormAgentPlan struct {
	ID          string       `gorm:"type:uuid;primaryKey" json:"id"`
	AgentTaskID string       `gorm:"type:uuid;not null;index" json:"agent_task_id"`
	Status      string       `gorm:"not null;default:'generated';index" json:"status"`
	Content     string       `gorm:"not null" json:"content"`
	CreatedAt   time.Time    `json:"created_at"`
	ApprovedAt  sql.NullTime `json:"approved_at"`
}

func (GormAgentPlan) TableName() string { return "agent_plans" }

func (m *GormAgentPlan) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormAgentToolCall struct {
	ID             string         `gorm:"type:uuid;primaryKey" json:"id"`
	AgentTaskID    string         `gorm:"type:uuid;not null;index" json:"agent_task_id"`
	OperationRunID sql.NullString `gorm:"type:uuid;index" json:"operation_run_id"`
	ProjectID      sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	ToolName       string         `gorm:"not null" json:"tool_name"`
	Input          JSONValue      `gorm:"type:jsonb;not null" json:"input"`
	Output         JSONValue      `gorm:"type:jsonb;not null" json:"output"`
	Status         string         `gorm:"not null;default:'queued';index" json:"status"`
	StartedAt      sql.NullTime   `json:"started_at"`
	FinishedAt     sql.NullTime   `json:"finished_at"`
	ErrorMessage   string         `gorm:"not null;default:''" json:"error_message"`
	Metadata       JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
	CreatedAt      time.Time      `json:"created_at"`
}

func (GormAgentToolCall) TableName() string { return "agent_tool_calls" }

func (m *GormAgentToolCall) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormAgentContextSnapshot struct {
	ID              string         `gorm:"type:uuid;primaryKey" json:"id"`
	ProjectID       string         `gorm:"type:uuid;not null;index" json:"project_id"`
	AgentTaskID     sql.NullString `gorm:"type:uuid;index" json:"agent_task_id"`
	SummaryMarkdown string         `gorm:"not null" json:"summary_markdown"`
	ContextJSON     JSONValue      `gorm:"type:jsonb;not null" json:"context_json"`
	ToolManifest    JSONValue      `gorm:"type:jsonb;not null" json:"tool_manifest"`
	CreatedAt       time.Time      `json:"created_at"`
}

func (GormAgentContextSnapshot) TableName() string { return "agent_context_snapshots" }

func (m *GormAgentContextSnapshot) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormAgentToolToken struct {
	ID          string         `gorm:"type:uuid;primaryKey" json:"id"`
	AgentTaskID sql.NullString `gorm:"type:uuid;index" json:"agent_task_id"`
	TokenHash   string         `gorm:"not null" json:"-"`
	Scopes      pq.StringArray `gorm:"type:text[]" json:"scopes"`
	ExpiresAt   sql.NullTime   `json:"expires_at"`
	CreatedAt   time.Time      `json:"created_at"`
}

func (GormAgentToolToken) TableName() string { return "agent_tool_tokens" }

func (m *GormAgentToolToken) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormArgoApp struct {
	GormBase
	ProjectID          string         `gorm:"type:uuid;not null;index" json:"project_id"`
	ArgoConnectionID   sql.NullString `gorm:"type:uuid;index:idx_argo_apps_conn_name,unique" json:"argo_connection_id"`
	DeploymentTargetID sql.NullString `gorm:"type:uuid;index" json:"deployment_target_id"`
	Name               string         `gorm:"not null;index:idx_argo_apps_conn_name,unique" json:"name"`
	Namespace          string         `gorm:"not null;default:''" json:"namespace"`
	Status             string         `gorm:"not null;default:'unknown';index" json:"status"`
	Metadata           JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
	SyncedAt           sql.NullTime   `json:"synced_at"`
}

func (GormArgoApp) TableName() string { return "argo_apps" }

type GormSSHCommandRun struct {
	ID             string         `gorm:"type:uuid;primaryKey" json:"id"`
	OperationRunID sql.NullString `gorm:"type:uuid;index" json:"operation_run_id"`
	SSHMachineID   sql.NullString `gorm:"type:uuid;index" json:"ssh_machine_id"`
	ProjectID      sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	ActorUserID    sql.NullString `gorm:"type:uuid;index" json:"actor_user_id"`
	Command        string         `gorm:"not null" json:"command"`
	Status         string         `gorm:"not null;default:'queued';index" json:"status"`
	ExitCode       sql.NullInt64  `json:"exit_code"`
	Stdout         string         `gorm:"not null;default:''" json:"stdout"`
	Stderr         string         `gorm:"not null;default:''" json:"stderr"`
	ErrorMessage   string         `gorm:"not null;default:''" json:"error_message"`
	StartedAt      sql.NullTime   `json:"started_at"`
	FinishedAt     sql.NullTime   `json:"finished_at"`
	CreatedAt      time.Time      `json:"created_at"`
}

func (GormSSHCommandRun) TableName() string { return "ssh_command_runs" }

func (m *GormSSHCommandRun) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormDeploymentTarget struct {
	GormBase
	ProjectID        string         `gorm:"type:uuid;not null;index:deployment_targets_project_scope_key,unique" json:"project_id"`
	Name             string         `gorm:"not null" json:"name"`
	Environment      string         `gorm:"not null;default:'default';index:deployment_targets_project_scope_key,unique" json:"environment"`
	ClusterName      string         `gorm:"not null;default:'';index:deployment_targets_project_scope_key,unique" json:"cluster_name"`
	Namespace        string         `gorm:"not null;default:'';index:deployment_targets_project_scope_key,unique" json:"namespace"`
	Source           string         `gorm:"not null;default:'argocd'" json:"source"`
	ArgoConnectionID sql.NullString `gorm:"type:uuid;index" json:"argo_connection_id"`
	Status           string         `gorm:"not null;default:'unknown';index" json:"status"`
	Metadata         JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormDeploymentTarget) TableName() string { return "deployment_targets" }

type GormDeploymentRecord struct {
	GormBase
	ProjectID          string         `gorm:"type:uuid;not null;index:deployment_records_identity_key,unique" json:"project_id"`
	DeploymentTargetID sql.NullString `gorm:"type:uuid;index" json:"deployment_target_id"`
	ArgoConnectionID   sql.NullString `gorm:"type:uuid;index" json:"argo_connection_id"`
	ArgoAppID          sql.NullString `gorm:"type:uuid;index" json:"argo_app_id"`
	Name               string         `gorm:"not null;index:deployment_records_identity_key,unique" json:"name"`
	Environment        string         `gorm:"not null;default:'';index:deployment_records_identity_key,unique" json:"environment"`
	Namespace          string         `gorm:"not null;default:'';index:deployment_records_identity_key,unique" json:"namespace"`
	ClusterName        string         `gorm:"not null;default:'';index:deployment_records_identity_key,unique" json:"cluster_name"`
	Source             string         `gorm:"not null;default:'argocd';index:deployment_records_identity_key,unique" json:"source"`
	Status             string         `gorm:"not null;default:'unknown';index" json:"status"`
	Revision           string         `gorm:"not null;default:''" json:"revision"`
	ImageRefs          JSONValue      `gorm:"type:jsonb;not null" json:"image_refs"`
	Metadata           JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
	ObservedAt         time.Time      `gorm:"not null;index" json:"observed_at"`
}

func (GormDeploymentRecord) TableName() string { return "deployment_records" }

type GormRollbackPoint struct {
	ID                 string         `gorm:"type:uuid;primaryKey" json:"id"`
	ProjectID          string         `gorm:"type:uuid;not null;index:rollback_points_identity_key,unique" json:"project_id"`
	DeploymentRecordID sql.NullString `gorm:"type:uuid;index" json:"deployment_record_id"`
	DeploymentTargetID sql.NullString `gorm:"type:uuid;index" json:"deployment_target_id"`
	Name               string         `gorm:"not null;index:rollback_points_identity_key,unique" json:"name"`
	Environment        string         `gorm:"not null;default:'';index:rollback_points_identity_key,unique" json:"environment"`
	Revision           string         `gorm:"not null;default:'';index:rollback_points_identity_key,unique" json:"revision"`
	ImageRefs          JSONValue      `gorm:"type:jsonb;not null" json:"image_refs"`
	Source             string         `gorm:"not null;default:'argocd';index:rollback_points_identity_key,unique" json:"source"`
	Status             string         `gorm:"not null;default:'available';index" json:"status"`
	Metadata           JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
	CapturedAt         time.Time      `gorm:"not null;index" json:"captured_at"`
	CreatedAt          time.Time      `json:"created_at"`
}

func (GormRollbackPoint) TableName() string { return "rollback_points" }
