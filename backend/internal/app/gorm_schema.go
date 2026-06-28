package app

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type GormBase struct {
	ID        string    `gorm:"type:uuid;primaryKey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (m *GormBase) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormUser struct {
	GormBase
	Email        string `gorm:"not null;uniqueIndex:users_email_key" json:"email"`
	Name         string `gorm:"not null;default:''" json:"name"`
	PasswordHash string `gorm:"not null;default:''" json:"-"`
	Role         string `gorm:"not null;default:'operator';index" json:"role"`
}

func (GormUser) TableName() string { return "users" }

type GormProject struct {
	GormBase
	Name        string `gorm:"not null" json:"name"`
	Slug        string `gorm:"not null;uniqueIndex:projects_slug_key" json:"slug"`
	Description string `gorm:"not null;default:''" json:"description"`
}

func (GormProject) TableName() string { return "projects" }

type GormProjectMember struct {
	ID        string    `gorm:"type:uuid;primaryKey" json:"id"`
	ProjectID string    `gorm:"type:uuid;not null;uniqueIndex:project_members_project_user_key" json:"project_id"`
	UserID    string    `gorm:"type:uuid;not null;uniqueIndex:project_members_project_user_key" json:"user_id"`
	Role      string    `gorm:"not null;default:'viewer'" json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (GormProjectMember) TableName() string { return "project_members" }

func (m *GormProjectMember) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormProjectGitRepository struct {
	GormBase
	ProjectID     string `gorm:"type:uuid;not null;uniqueIndex:project_git_repositories_project_repo_key_key" json:"project_id"`
	Name          string `gorm:"not null" json:"name"`
	RepoKey       string `gorm:"not null;default:'';uniqueIndex:project_git_repositories_project_repo_key_key" json:"repo_key"`
	DisplayName   string `gorm:"not null;default:''" json:"display_name"`
	RepoRole      string `gorm:"not null;default:'service'" json:"repo_role"`
	Status        string `gorm:"not null;default:'active';index" json:"status"`
	Description   string `gorm:"not null;default:''" json:"description"`
	DefaultBranch string `gorm:"not null;default:'main'" json:"default_branch"`
}

func (GormProjectGitRepository) TableName() string { return "project_git_repositories" }

type GormConnectionCredential struct {
	GormBase
	ProjectID        sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	Name             string         `gorm:"not null" json:"name"`
	Kind             string         `gorm:"not null;index" json:"kind"`
	SecretCiphertext string         `gorm:"not null;default:''" json:"-"`
	PublicValue      string         `gorm:"not null;default:''" json:"public_value"`
	Metadata         JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormConnectionCredential) TableName() string { return "connection_credentials" }

type GormProviderAccount struct {
	GormBase
	Name         string         `gorm:"not null;uniqueIndex:provider_accounts_name_key" json:"name"`
	ProviderType string         `gorm:"not null;index" json:"provider_type"`
	APIBaseURL   string         `gorm:"not null;default:''" json:"api_base_url"`
	WebBaseURL   string         `gorm:"not null;default:''" json:"web_base_url"`
	TokenEnv     string         `gorm:"not null;default:''" json:"-"`
	CredentialID sql.NullString `gorm:"type:uuid;index" json:"credential_id"`
	DefaultOwner string         `gorm:"not null;default:''" json:"default_owner"`
	Visibility   string         `gorm:"not null;default:'private'" json:"visibility"`
	Enabled      bool           `gorm:"not null;default:true" json:"enabled"`
	Metadata     JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormProviderAccount) TableName() string { return "provider_accounts" }

type GormGitRemote struct {
	GormBase
	ProjectGitRepositoryID string         `gorm:"type:uuid;not null;uniqueIndex:git_remotes_repo_name_key" json:"project_git_repository_id"`
	Name                   string         `gorm:"not null;uniqueIndex:git_remotes_repo_name_key" json:"name"`
	Kind                   string         `gorm:"not null;default:'git'" json:"kind"`
	RemoteKey              string         `gorm:"not null;default:'';index" json:"remote_key"`
	ProviderType           string         `gorm:"not null;default:'git';index" json:"provider_type"`
	SourceProviderID       sql.NullString `gorm:"type:uuid;index" json:"source_provider_id"`
	SourceAccountID        sql.NullString `gorm:"type:uuid;index" json:"source_account_id"`
	CredentialID           sql.NullString `gorm:"type:uuid;index" json:"credential_id"`
	RemoteURL              string         `gorm:"not null;default:''" json:"remote_url"`
	WebURL                 string         `gorm:"not null;default:''" json:"web_url"`
	RemoteRole             string         `gorm:"not null;default:'source';index" json:"remote_role"`
	IsPrimary              bool           `gorm:"not null;default:false" json:"is_primary"`
	SyncEnabled            bool           `gorm:"not null;default:false" json:"sync_enabled"`
	Protected              bool           `gorm:"not null;default:false" json:"protected"`
	LatestSHA              string         `gorm:"not null;default:''" json:"latest_sha"`
	LastSyncStatus         string         `gorm:"not null;default:'never'" json:"last_sync_status"`
	URLs                   JSONValue      `gorm:"type:jsonb;not null" json:"urls"`
	DefaultBranch          string         `gorm:"not null;default:'main'" json:"default_branch"`
	Metadata               JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormGitRemote) TableName() string { return "git_remotes" }

type GormAIRuntime struct {
	GormBase
	ProjectID    sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	Name         string         `gorm:"not null" json:"name"`
	RuntimeType  string         `gorm:"not null;default:'codex-cli';index" json:"runtime_type"`
	CodexBinary  string         `gorm:"not null;default:'codex'" json:"codex_binary"`
	ProviderType string         `gorm:"not null;default:'';index" json:"provider_type"`
	APIBaseURL   string         `gorm:"not null;default:''" json:"api_base_url"`
	CredentialID sql.NullString `gorm:"type:uuid;index" json:"credential_id"`
	Model        string         `gorm:"not null;default:''" json:"model"`
	Config       JSONValue      `gorm:"type:jsonb;not null" json:"config"`
	Status       string         `gorm:"not null;default:'unknown';index" json:"status"`
}

func (GormAIRuntime) TableName() string { return "ai_runtimes" }

type GormArgoConnection struct {
	GormBase
	ProjectID      string         `gorm:"type:uuid;not null;index" json:"project_id"`
	Name           string         `gorm:"not null" json:"name"`
	ServerURL      string         `gorm:"not null;default:''" json:"server_url"`
	AuthType       string         `gorm:"not null;default:'token'" json:"auth_type"`
	CredentialID   sql.NullString `gorm:"type:uuid;index" json:"credential_id"`
	Config         JSONValue      `gorm:"type:jsonb;not null" json:"config"`
	LastSyncStatus string         `gorm:"not null;default:'never'" json:"last_sync_status"`
	LastSyncError  string         `gorm:"not null;default:''" json:"last_sync_error"`
}

func (GormArgoConnection) TableName() string { return "argo_connections" }

type GormSSHMachine struct {
	GormBase
	ProjectID    string         `gorm:"type:uuid;not null;index" json:"project_id"`
	Name         string         `gorm:"not null" json:"name"`
	Host         string         `gorm:"not null;default:''" json:"host"`
	Port         int            `gorm:"not null;default:22" json:"port"`
	Username     string         `gorm:"not null;default:''" json:"username"`
	AuthType     string         `gorm:"not null;default:'key'" json:"auth_type"`
	CredentialID sql.NullString `gorm:"type:uuid;index" json:"credential_id"`
	Metadata     JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormSSHMachine) TableName() string { return "ssh_machines" }

type GormOperationRun struct {
	GormBase
	ProjectID     sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	GitRemoteID   sql.NullString `gorm:"type:uuid;index" json:"git_remote_id"`
	OperationType string         `gorm:"not null;index" json:"operation_type"`
	Status        string         `gorm:"not null;default:'queued';index" json:"status"`
	Title         string         `gorm:"not null;default:''" json:"title"`
	Input         JSONValue      `gorm:"type:jsonb;not null" json:"input"`
	Result        JSONValue      `gorm:"type:jsonb;not null" json:"result"`
	Error         string         `gorm:"not null;default:''" json:"error"`
	StartedAt     sql.NullTime   `json:"started_at"`
	FinishedAt    sql.NullTime   `json:"finished_at"`
}

func (GormOperationRun) TableName() string { return "operation_runs" }

type GormWorkerNode struct {
	GormBase
	Name            string         `gorm:"not null;uniqueIndex:worker_nodes_name_key" json:"name"`
	Kind            string         `gorm:"not null;default:'generic';index" json:"kind"`
	Capabilities    pq.StringArray `gorm:"type:text[]" json:"capabilities"`
	Status          string         `gorm:"not null;default:'unknown';index" json:"status"`
	LastHeartbeatAt time.Time      `json:"last_heartbeat_at"`
	Metadata        JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormWorkerNode) TableName() string { return "worker_nodes" }

type GormRepoSyncPolicy struct {
	ID          string    `gorm:"type:uuid;primaryKey" json:"id"`
	GitRemoteID string    `gorm:"type:uuid;not null;index" json:"git_remote_id"`
	Enabled     bool      `gorm:"not null;default:false" json:"enabled"`
	Schedule    string    `gorm:"not null;default:''" json:"schedule"`
	Config      JSONValue `gorm:"type:jsonb;not null" json:"config"`
	CreatedAt   time.Time `json:"created_at"`
}

func (GormRepoSyncPolicy) TableName() string { return "repo_sync_policies" }

func (m *GormRepoSyncPolicy) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormProjectVersion struct {
	ID        string    `gorm:"type:uuid;primaryKey" json:"id"`
	ProjectID string    `gorm:"type:uuid;not null;uniqueIndex:idx_project_versions_project_version" json:"project_id"`
	Version   string    `gorm:"not null;uniqueIndex:idx_project_versions_project_version" json:"version"`
	Source    string    `gorm:"not null;default:'manual'" json:"source"`
	Metadata  JSONValue `gorm:"type:jsonb;not null" json:"metadata"`
	CreatedAt time.Time `json:"created_at"`
}

func (GormProjectVersion) TableName() string { return "project_versions" }

func (m *GormProjectVersion) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormWorkerNodeToken struct {
	ID           string       `gorm:"type:uuid;primaryKey" json:"id"`
	WorkerNodeID string       `gorm:"type:uuid;not null;index" json:"worker_node_id"`
	TokenHash    string       `gorm:"not null" json:"-"`
	Name         string       `gorm:"not null;default:'default'" json:"name"`
	LastUsedAt   sql.NullTime `json:"last_used_at"`
	CreatedAt    time.Time    `json:"created_at"`
}

func (GormWorkerNodeToken) TableName() string { return "worker_node_tokens" }

func (m *GormWorkerNodeToken) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormWorkerJob struct {
	GormBase
	OperationRunID       sql.NullString `gorm:"type:uuid;index" json:"operation_run_id"`
	ToolName             string         `gorm:"not null" json:"tool_name"`
	Status               string         `gorm:"not null;default:'queued';index" json:"status"`
	Payload              JSONValue      `gorm:"type:jsonb;not null" json:"payload"`
	Result               JSONValue      `gorm:"type:jsonb;not null" json:"result"`
	Error                string         `gorm:"not null;default:''" json:"error"`
	RequiredCapabilities pq.StringArray `gorm:"type:text[]" json:"required_capabilities"`
	PreferredNodeKind    string         `gorm:"not null;default:''" json:"preferred_node_kind"`
	AssignedWorkerNodeID sql.NullString `gorm:"type:uuid;index" json:"assigned_worker_node_id"`
	ClaimedAt            sql.NullTime   `json:"claimed_at"`
	StartedAt            sql.NullTime   `json:"started_at"`
	FinishedAt           sql.NullTime   `json:"finished_at"`
}

func (GormWorkerJob) TableName() string { return "worker_jobs" }

type GormOperationLog struct {
	ID             string         `gorm:"type:uuid;primaryKey" json:"id"`
	OperationRunID sql.NullString `gorm:"type:uuid;index" json:"operation_run_id"`
	WorkerJobID    sql.NullString `gorm:"type:uuid;index" json:"worker_job_id"`
	Level          string         `gorm:"not null;default:'info'" json:"level"`
	Message        string         `gorm:"not null" json:"message"`
	Fields         JSONValue      `gorm:"type:jsonb;not null" json:"fields"`
	CreatedAt      time.Time      `json:"created_at"`
}

func (GormOperationLog) TableName() string { return "operation_logs" }

func (m *GormOperationLog) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormRepoSyncRun struct {
	ID                     string         `gorm:"type:uuid;primaryKey" json:"id"`
	OperationRunID         string         `gorm:"type:uuid;not null;index" json:"operation_run_id"`
	GitRemoteID            string         `gorm:"type:uuid;not null;index" json:"git_remote_id"`
	ProjectID              sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	ProjectGitRepositoryID sql.NullString `gorm:"type:uuid;index" json:"project_git_repository_id"`
	RepoSyncAssetID        sql.NullString `gorm:"type:uuid;index" json:"repo_sync_asset_id"`
	SourceRemoteID         sql.NullString `gorm:"type:uuid;index" json:"source_remote_id"`
	TargetRemoteID         sql.NullString `gorm:"type:uuid;index" json:"target_remote_id"`
	Ref                    string         `gorm:"not null;default:''" json:"ref"`
	BeforeSHA              string         `gorm:"not null;default:''" json:"before_sha"`
	AfterSHA               string         `gorm:"not null;default:''" json:"after_sha"`
	ActorUserID            sql.NullString `gorm:"type:uuid;index" json:"actor_user_id"`
	Status                 string         `gorm:"not null;default:'queued';index" json:"status"`
	Stdout                 string         `gorm:"not null;default:''" json:"stdout"`
	Stderr                 string         `gorm:"not null;default:''" json:"stderr"`
	ErrorMessage           string         `gorm:"not null;default:''" json:"error_message"`
	StartedAt              sql.NullTime   `json:"started_at"`
	FinishedAt             sql.NullTime   `json:"finished_at"`
	CreatedAt              time.Time      `json:"created_at"`
}

func (GormRepoSyncRun) TableName() string { return "repo_sync_runs" }

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
	ArgoConnectionID   sql.NullString `gorm:"type:uuid;uniqueIndex:idx_argo_apps_conn_name" json:"argo_connection_id"`
	DeploymentTargetID sql.NullString `gorm:"type:uuid;index" json:"deployment_target_id"`
	Name               string         `gorm:"not null;uniqueIndex:idx_argo_apps_conn_name" json:"name"`
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
	ProjectID        string         `gorm:"type:uuid;not null;uniqueIndex:deployment_targets_project_scope_key" json:"project_id"`
	Name             string         `gorm:"not null" json:"name"`
	Environment      string         `gorm:"not null;default:'default';uniqueIndex:deployment_targets_project_scope_key" json:"environment"`
	ClusterName      string         `gorm:"not null;default:'';uniqueIndex:deployment_targets_project_scope_key" json:"cluster_name"`
	Namespace        string         `gorm:"not null;default:'';uniqueIndex:deployment_targets_project_scope_key" json:"namespace"`
	Source           string         `gorm:"not null;default:'argocd'" json:"source"`
	ArgoConnectionID sql.NullString `gorm:"type:uuid;index" json:"argo_connection_id"`
	Status           string         `gorm:"not null;default:'unknown';index" json:"status"`
	Metadata         JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormDeploymentTarget) TableName() string { return "deployment_targets" }

type GormDeploymentRecord struct {
	GormBase
	ProjectID          string         `gorm:"type:uuid;not null;uniqueIndex:deployment_records_identity_key" json:"project_id"`
	DeploymentTargetID sql.NullString `gorm:"type:uuid;index" json:"deployment_target_id"`
	ArgoConnectionID   sql.NullString `gorm:"type:uuid;index" json:"argo_connection_id"`
	ArgoAppID          sql.NullString `gorm:"type:uuid;index" json:"argo_app_id"`
	Name               string         `gorm:"not null;uniqueIndex:deployment_records_identity_key" json:"name"`
	Environment        string         `gorm:"not null;default:'';uniqueIndex:deployment_records_identity_key" json:"environment"`
	Namespace          string         `gorm:"not null;default:'';uniqueIndex:deployment_records_identity_key" json:"namespace"`
	ClusterName        string         `gorm:"not null;default:'';uniqueIndex:deployment_records_identity_key" json:"cluster_name"`
	Source             string         `gorm:"not null;default:'argocd';uniqueIndex:deployment_records_identity_key" json:"source"`
	Status             string         `gorm:"not null;default:'unknown';index" json:"status"`
	Revision           string         `gorm:"not null;default:''" json:"revision"`
	ImageRefs          JSONValue      `gorm:"type:jsonb;not null" json:"image_refs"`
	Metadata           JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
	ObservedAt         time.Time      `gorm:"not null;index" json:"observed_at"`
}

func (GormDeploymentRecord) TableName() string { return "deployment_records" }

type GormRollbackPoint struct {
	ID                 string         `gorm:"type:uuid;primaryKey" json:"id"`
	ProjectID          string         `gorm:"type:uuid;not null;uniqueIndex:rollback_points_identity_key" json:"project_id"`
	DeploymentRecordID sql.NullString `gorm:"type:uuid;index" json:"deployment_record_id"`
	DeploymentTargetID sql.NullString `gorm:"type:uuid;index" json:"deployment_target_id"`
	Name               string         `gorm:"not null;uniqueIndex:rollback_points_identity_key" json:"name"`
	Environment        string         `gorm:"not null;default:'';uniqueIndex:rollback_points_identity_key" json:"environment"`
	Revision           string         `gorm:"not null;default:'';uniqueIndex:rollback_points_identity_key" json:"revision"`
	ImageRefs          JSONValue      `gorm:"type:jsonb;not null" json:"image_refs"`
	Source             string         `gorm:"not null;default:'argocd';uniqueIndex:rollback_points_identity_key" json:"source"`
	Status             string         `gorm:"not null;default:'available';index" json:"status"`
	Metadata           JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
	CapturedAt         time.Time      `gorm:"not null;index" json:"captured_at"`
	CreatedAt          time.Time      `json:"created_at"`
}

func (GormRollbackPoint) TableName() string { return "rollback_points" }

func (m *GormRollbackPoint) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormAsset struct {
	GormBase
	ProjectID   sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	AssetType   string         `gorm:"not null;uniqueIndex:assets_source_key" json:"asset_type"`
	SourceTable string         `gorm:"not null;default:'';uniqueIndex:assets_source_key" json:"source_table"`
	SourceID    sql.NullString `gorm:"type:uuid;uniqueIndex:assets_source_key" json:"source_id"`
	Name        string         `gorm:"not null" json:"name"`
	DisplayName string         `gorm:"not null;default:''" json:"display_name"`
	Description string         `gorm:"not null;default:''" json:"description"`
	Source      string         `gorm:"not null;default:'local'" json:"source"`
	ExternalID  string         `gorm:"not null;default:''" json:"external_id"`
	Status      string         `gorm:"not null;default:'unknown';index" json:"status"`
	RiskLevel   string         `gorm:"not null;default:'normal';index" json:"risk_level"`
	OwnerUserID sql.NullString `gorm:"type:uuid;index" json:"owner_user_id"`
	Metadata    JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormAsset) TableName() string { return "assets" }

type GormAssetRelation struct {
	ID           string         `gorm:"type:uuid;primaryKey" json:"id"`
	ProjectID    sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	FromAssetID  string         `gorm:"type:uuid;not null;uniqueIndex:asset_relations_unique_relation" json:"from_asset_id"`
	ToAssetID    string         `gorm:"type:uuid;not null;uniqueIndex:asset_relations_unique_relation" json:"to_asset_id"`
	RelationType string         `gorm:"not null;uniqueIndex:asset_relations_unique_relation" json:"relation_type"`
	Metadata     JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
	CreatedAt    time.Time      `json:"created_at"`
}

func (GormAssetRelation) TableName() string { return "asset_relations" }

func (m *GormAssetRelation) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormAssetStatusSnapshot struct {
	ID          string    `gorm:"type:uuid;primaryKey" json:"id"`
	AssetID     string    `gorm:"type:uuid;not null;index" json:"asset_id"`
	Status      string    `gorm:"not null;index" json:"status"`
	Health      string    `gorm:"not null;default:''" json:"health"`
	Summary     string    `gorm:"not null;default:''" json:"summary"`
	Raw         JSONValue `gorm:"type:jsonb;not null" json:"raw"`
	CollectedAt time.Time `gorm:"not null;default:now();index" json:"collected_at"`
}

func (GormAssetStatusSnapshot) TableName() string { return "asset_status_snapshots" }

func (m *GormAssetStatusSnapshot) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormProjectTemplate struct {
	GormBase
	Slug        string    `gorm:"not null;uniqueIndex:project_templates_slug_key" json:"slug"`
	Name        string    `gorm:"not null" json:"name"`
	Description string    `gorm:"not null;default:''" json:"description"`
	Version     string    `gorm:"not null;default:'v0.1'" json:"version"`
	Status      string    `gorm:"not null;default:'active';index" json:"status"`
	Defaults    JSONValue `gorm:"type:jsonb;not null" json:"defaults"`
	Steps       JSONValue `gorm:"type:jsonb;not null" json:"steps"`
	Metadata    JSONValue `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormProjectTemplate) TableName() string { return "project_templates" }

type GormProjectTemplateRun struct {
	GormBase
	OperationRunID    sql.NullString `gorm:"type:uuid;uniqueIndex" json:"operation_run_id"`
	ProjectTemplateID sql.NullString `gorm:"type:uuid;index" json:"project_template_id"`
	ProjectID         sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	RequestedBy       sql.NullString `gorm:"type:uuid;index" json:"requested_by"`
	Status            string         `gorm:"not null;default:'queued';index" json:"status"`
	ProjectName       string         `gorm:"not null" json:"project_name"`
	ProjectSlug       string         `gorm:"not null" json:"project_slug"`
	Input             JSONValue      `gorm:"type:jsonb;not null" json:"input"`
	Steps             JSONValue      `gorm:"type:jsonb;not null" json:"steps"`
	Result            JSONValue      `gorm:"type:jsonb;not null" json:"result"`
	ErrorMessage      string         `gorm:"not null;default:''" json:"error_message"`
	StartedAt         sql.NullTime   `json:"started_at"`
	FinishedAt        sql.NullTime   `json:"finished_at"`
}

func (GormProjectTemplateRun) TableName() string { return "project_template_runs" }

type GormProjectTemplateFile struct {
	GormBase
	ProjectTemplateRunID   sql.NullString `gorm:"type:uuid;uniqueIndex:project_template_files_run_path_key" json:"project_template_run_id"`
	ProjectTemplateID      sql.NullString `gorm:"type:uuid;index" json:"project_template_id"`
	ProjectID              sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	ProjectGitRepositoryID sql.NullString `gorm:"type:uuid;index" json:"project_git_repository_id"`
	Path                   string         `gorm:"not null;uniqueIndex:project_template_files_run_path_key" json:"path"`
	Kind                   string         `gorm:"not null;default:'text'" json:"kind"`
	Content                string         `gorm:"not null;default:''" json:"content"`
	Status                 string         `gorm:"not null;default:'planned';index" json:"status"`
	Metadata               JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormProjectTemplateFile) TableName() string { return "project_template_files" }

type GormRepoSyncAsset struct {
	GormBase
	ProjectID              string         `gorm:"type:uuid;not null;index" json:"project_id"`
	ProjectGitRepositoryID string         `gorm:"type:uuid;not null;uniqueIndex:repo_sync_assets_repo_name_key" json:"project_git_repository_id"`
	Name                   string         `gorm:"not null;uniqueIndex:repo_sync_assets_repo_name_key" json:"name"`
	SourceRemoteID         string         `gorm:"type:uuid;not null;index" json:"source_remote_id"`
	TargetRemoteID         string         `gorm:"type:uuid;not null;index" json:"target_remote_id"`
	TriggerMode            string         `gorm:"not null;default:'manual'" json:"trigger_mode"`
	SyncMode               string         `gorm:"not null;default:'selected_refs'" json:"sync_mode"`
	Transport              string         `gorm:"not null;default:'ssh'" json:"transport"`
	Driver                 string         `gorm:"not null;default:'projectops_worker_git_ssh'" json:"driver"`
	Refs                   JSONValue      `gorm:"type:jsonb;not null" json:"refs"`
	Enabled                bool           `gorm:"not null;default:true;index" json:"enabled"`
	LastSyncStatus         string         `gorm:"not null;default:'never';index" json:"last_sync_status"`
	LastSyncRunID          sql.NullString `gorm:"type:uuid;index" json:"last_sync_run_id"`
	LastSyncedAt           sql.NullTime   `json:"last_synced_at"`
	Metadata               JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
	ArchivedAt             sql.NullTime   `gorm:"index" json:"archived_at"`
}

func (GormRepoSyncAsset) TableName() string { return "repo_sync_assets" }

type GormWebhookConnection struct {
	GormBase
	ProjectID          string         `gorm:"type:uuid;not null;index" json:"project_id"`
	Provider           string         `gorm:"not null;default:'gitea';index" json:"provider"`
	Name               string         `gorm:"not null" json:"name"`
	SourceRemoteID     sql.NullString `gorm:"type:uuid;index" json:"source_remote_id"`
	SecretCiphertext   string         `gorm:"not null;default:''" json:"-"`
	Enabled            bool           `gorm:"not null;default:true;index" json:"enabled"`
	EventTypes         JSONValue      `gorm:"type:jsonb;not null" json:"event_types"`
	LastDeliveryStatus string         `gorm:"not null;default:'never'" json:"last_delivery_status"`
	LastDeliveryError  string         `gorm:"not null;default:''" json:"last_delivery_error"`
	Metadata           JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormWebhookConnection) TableName() string { return "webhook_connections" }

type GormWebhookEvent struct {
	ID                     string         `gorm:"type:uuid;primaryKey" json:"id"`
	WebhookConnectionID    sql.NullString `gorm:"type:uuid;index" json:"webhook_connection_id"`
	ProjectID              sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	Provider               string         `gorm:"not null;default:'gitea'" json:"provider"`
	EventType              string         `gorm:"not null;default:''" json:"event_type"`
	DeliveryID             string         `gorm:"not null;default:'';index" json:"delivery_id"`
	SignatureValid         bool           `gorm:"not null;default:false;index" json:"signature_valid"`
	MatchedRepoSyncAssetID sql.NullString `gorm:"type:uuid;index" json:"matched_repo_sync_asset_id"`
	OperationRunID         sql.NullString `gorm:"type:uuid;index" json:"operation_run_id"`
	Status                 string         `gorm:"not null;default:'received';index" json:"status"`
	ErrorMessage           string         `gorm:"not null;default:''" json:"error_message"`
	Payload                JSONValue      `gorm:"type:jsonb;not null" json:"payload"`
	Result                 JSONValue      `gorm:"type:jsonb;not null" json:"result"`
	ReceivedAt             time.Time      `gorm:"not null;index" json:"received_at"`
	ProcessedAt            sql.NullTime   `json:"processed_at"`
}

func (GormWebhookEvent) TableName() string { return "webhook_events" }

func (m *GormWebhookEvent) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormOperationApproval struct {
	GormBase
	ProjectID             sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	OperationRunID        sql.NullString `gorm:"type:uuid;index" json:"operation_run_id"`
	ApprovalRuleID        sql.NullString `gorm:"type:uuid;index" json:"approval_rule_id"`
	ResourceType          string         `gorm:"not null;default:'';index" json:"resource_type"`
	ResourceID            string         `gorm:"not null;default:'';index" json:"resource_id"`
	Action                string         `gorm:"not null;index" json:"action"`
	Title                 string         `gorm:"not null" json:"title"`
	RequestPayload        JSONValue      `gorm:"type:jsonb;not null" json:"request_payload"`
	Status                string         `gorm:"not null;default:'pending';index" json:"status"`
	RequiredApproverRoles pq.StringArray `gorm:"type:text[]" json:"required_approver_roles"`
	RequiredApprovalCount int            `gorm:"not null;default:1" json:"required_approval_count"`
	NotificationChannels  pq.StringArray `gorm:"type:text[]" json:"notification_channels"`
	NotificationStatus    string         `gorm:"not null;default:'pending'" json:"notification_status"`
	NotificationLastError string         `gorm:"not null;default:''" json:"notification_last_error"`
	ReminderCount         int            `gorm:"not null;default:0" json:"reminder_count"`
	LastReminderAt        sql.NullTime   `json:"last_reminder_at"`
	EscalationPolicy      JSONValue      `gorm:"type:jsonb;not null;default:'{}'" json:"escalation_policy"`
	EscalatedAt           sql.NullTime   `json:"escalated_at"`
	RequestedBy           sql.NullString `gorm:"type:uuid;index" json:"requested_by"`
	DecidedBy             sql.NullString `gorm:"type:uuid;index" json:"decided_by"`
	DecisionReason        string         `gorm:"not null;default:''" json:"decision_reason"`
	DecidedAt             sql.NullTime   `json:"decided_at"`
	ExpiresAt             sql.NullTime   `gorm:"index" json:"expires_at"`
	ExpiredAt             sql.NullTime   `json:"expired_at"`
}

func (GormOperationApproval) TableName() string { return "operation_approvals" }

type GormOperationApprovalRule struct {
	GormBase
	ResourceType          string         `gorm:"not null;default:'';uniqueIndex:operation_approval_rules_resource_action_key" json:"resource_type"`
	Action                string         `gorm:"not null;uniqueIndex:operation_approval_rules_resource_action_key" json:"action"`
	RequiredApproverRoles pq.StringArray `gorm:"type:text[]" json:"required_approver_roles"`
	RequiredApprovalCount int            `gorm:"not null;default:1" json:"required_approval_count"`
	ExpiresAfterMinutes   int            `gorm:"not null;default:1440" json:"expires_after_minutes"`
	NotificationChannels  pq.StringArray `gorm:"type:text[]" json:"notification_channels"`
	EscalationPolicy      JSONValue      `gorm:"type:jsonb;not null;default:'{}'" json:"escalation_policy"`
	Priority              int            `gorm:"not null;default:100;index" json:"priority"`
	Enabled               bool           `gorm:"not null;default:true;index" json:"enabled"`
	Metadata              JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormOperationApprovalRule) TableName() string { return "operation_approval_rules" }

type GormOperationApprovalDecision struct {
	ID                  string         `gorm:"type:uuid;primaryKey" json:"id"`
	OperationApprovalID string         `gorm:"type:uuid;not null;uniqueIndex:operation_approval_decisions_approval_user_key" json:"operation_approval_id"`
	UserID              sql.NullString `gorm:"type:uuid;uniqueIndex:operation_approval_decisions_approval_user_key" json:"user_id"`
	Decision            string         `gorm:"not null" json:"decision"`
	Reason              string         `gorm:"not null;default:''" json:"reason"`
	DecidedAt           time.Time      `gorm:"not null;index" json:"decided_at"`
}

func (GormOperationApprovalDecision) TableName() string { return "operation_approval_decisions" }

func (m *GormOperationApprovalDecision) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormOperationApprovalDelegation struct {
	ID                  string         `gorm:"type:uuid;primaryKey" json:"id"`
	OperationApprovalID string         `gorm:"type:uuid;not null;uniqueIndex:operation_approval_delegations_approval_user_key" json:"operation_approval_id"`
	FromUserID          sql.NullString `gorm:"type:uuid;index" json:"from_user_id"`
	ToUserID            string         `gorm:"type:uuid;not null;uniqueIndex:operation_approval_delegations_approval_user_key" json:"to_user_id"`
	Reason              string         `gorm:"not null;default:''" json:"reason"`
	RevokedAt           sql.NullTime   `json:"revoked_at"`
	CreatedAt           time.Time      `json:"created_at"`
}

func (GormOperationApprovalDelegation) TableName() string { return "operation_approval_delegations" }

func (m *GormOperationApprovalDelegation) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormOperationApprovalRuleAudit struct {
	ID                      string         `gorm:"type:uuid;primaryKey" json:"id"`
	OperationApprovalRuleID sql.NullString `gorm:"type:uuid;index" json:"operation_approval_rule_id"`
	ActorUserID             sql.NullString `gorm:"type:uuid;index" json:"actor_user_id"`
	Action                  string         `gorm:"not null" json:"action"`
	Before                  JSONValue      `gorm:"column:before_state;type:jsonb;not null" json:"before_state"`
	After                   JSONValue      `gorm:"column:after_state;type:jsonb;not null" json:"after_state"`
	CreatedAt               time.Time      `json:"created_at"`
}

func (GormOperationApprovalRuleAudit) TableName() string { return "operation_approval_rule_audits" }

func (m *GormOperationApprovalRuleAudit) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

type GormOperationApprovalView struct {
	GormBase
	UserID  string    `gorm:"type:uuid;not null;uniqueIndex:operation_approval_views_user_name_key" json:"user_id"`
	Name    string    `gorm:"not null;uniqueIndex:operation_approval_views_user_name_key" json:"name"`
	Filters JSONValue `gorm:"type:jsonb;not null" json:"filters"`
}

func (GormOperationApprovalView) TableName() string { return "operation_approval_views" }

type GormAssetGraphView struct {
	GormBase
	UserID  string    `gorm:"type:uuid;not null;uniqueIndex:asset_graph_views_user_name_key" json:"user_id"`
	Name    string    `gorm:"not null;uniqueIndex:asset_graph_views_user_name_key" json:"name"`
	Filters JSONValue `gorm:"type:jsonb;not null" json:"filters"`
}

func (GormAssetGraphView) TableName() string { return "asset_graph_views" }

type GormProviderReviewAttempt struct {
	GormBase
	OperationApprovalID            string         `gorm:"type:uuid;not null;uniqueIndex:provider_review_attempts_approval_operation_key" json:"operation_approval_id"`
	ProjectTemplateRunID           sql.NullString `gorm:"type:uuid;index" json:"project_template_run_id"`
	ProviderType                   string         `gorm:"not null;default:'';index" json:"provider_type"`
	ReviewKind                     string         `gorm:"not null;default:''" json:"review_kind"`
	OperationName                  string         `gorm:"not null;uniqueIndex:provider_review_attempts_approval_operation_key" json:"operation_name"`
	EndpointKey                    string         `gorm:"not null;default:''" json:"endpoint_key"`
	Status                         string         `gorm:"not null;default:'planned';index" json:"status"`
	ReplayCheck                    string         `gorm:"not null;default:''" json:"replay_check"`
	ConflictPolicy                 string         `gorm:"not null;default:''" json:"conflict_policy"`
	RetryPolicy                    string         `gorm:"not null;default:''" json:"retry_policy"`
	IdempotencyKeyKind             string         `gorm:"not null;default:'operation_scope_hash'" json:"idempotency_key_kind"`
	IdempotencyKeyHash             string         `gorm:"not null;default:''" json:"idempotency_key_hash"`
	IdempotencyKeyMaterial         JSONValue      `gorm:"type:jsonb;not null" json:"idempotency_key_material"`
	RequestSummary                 JSONValue      `gorm:"type:jsonb;not null" json:"request_summary"`
	ResponseDiagnostics            JSONValue      `gorm:"type:jsonb;not null" json:"response_diagnostics"`
	ProviderAPICallMade            bool           `gorm:"not null;default:false;index" json:"provider_api_call_made"`
	ProviderAPIMutation            string         `gorm:"not null;default:'disabled'" json:"provider_api_mutation"`
	ExternalCallMade               bool           `gorm:"not null;default:false" json:"external_call_made"`
	OperationOrder                 int            `gorm:"not null;default:0;index" json:"operation_order"`
	DependsOnOperation             string         `gorm:"not null;default:''" json:"depends_on_operation"`
	DependencyStatus               string         `gorm:"not null;default:'independent'" json:"dependency_status"`
	ClaimedByUserID                sql.NullString `gorm:"type:uuid;index" json:"claimed_by_user_id"`
	ClaimedAt                      sql.NullTime   `gorm:"index" json:"claimed_at"`
	ProviderStatusClass            string         `gorm:"not null;default:'';index" json:"provider_status_class"`
	ProviderReviewURL              string         `gorm:"not null;default:''" json:"provider_review_url"`
	ExecutedAt                     sql.NullTime   `gorm:"index" json:"executed_at"`
	LiveExecutionPhase             string         `gorm:"not null;default:''" json:"live_execution_phase"`
	LiveExecutionRetryable         bool           `gorm:"not null;default:false" json:"live_execution_retryable"`
	LiveExecutionManualCleanupHint string         `gorm:"not null;default:''" json:"live_execution_manual_cleanup_hint"`
	CleanupAttempted               bool           `gorm:"not null;default:false" json:"cleanup_attempted"`
	CleanupSucceeded               bool           `gorm:"not null;default:false" json:"cleanup_succeeded"`
	CleanupRequired                bool           `gorm:"not null;default:false;index" json:"cleanup_required"`
}

func (GormProviderReviewAttempt) TableName() string { return "provider_review_attempts" }

type GormWebhookThresholdDecisionAudit struct {
	ID                   string         `gorm:"type:uuid;primaryKey" json:"id"`
	ProjectID            string         `gorm:"type:uuid;not null;index" json:"project_id"`
	WebhookConnectionID  string         `gorm:"type:uuid;not null;uniqueIndex:webhook_threshold_decision_audits_once" json:"webhook_connection_id"`
	Provider             string         `gorm:"not null;default:''" json:"provider"`
	ThresholdReviewState string         `gorm:"not null;default:''" json:"threshold_review_state"`
	DecisionState        string         `gorm:"not null;default:'';uniqueIndex:webhook_threshold_decision_audits_once" json:"decision_state"`
	OperatorDecision     string         `gorm:"not null;default:''" json:"operator_decision"`
	EvidenceWindow       string         `gorm:"not null;default:'7d';uniqueIndex:webhook_threshold_decision_audits_once" json:"evidence_window"`
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
	WebhookConnectionID string         `gorm:"type:uuid;not null;uniqueIndex:webhook_threshold_configurations_once" json:"webhook_connection_id"`
	Provider            string         `gorm:"not null;default:''" json:"provider"`
	ThresholdKey        string         `gorm:"not null;uniqueIndex:webhook_threshold_configurations_once" json:"threshold_key"`
	WarningAt           int            `gorm:"not null" json:"warning_at"`
	DangerAt            int            `gorm:"not null" json:"danger_at"`
	Unit                string         `gorm:"not null;default:''" json:"unit"`
	EvidenceWindow      string         `gorm:"not null;default:'7d';uniqueIndex:webhook_threshold_configurations_once" json:"evidence_window"`
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
	GitHubActionRunID  string       `gorm:"column:github_action_run_id;type:uuid;not null;uniqueIndex:github_action_artifacts_run_external_key" json:"github_action_run_id"`
	ExternalArtifactID string       `gorm:"not null;default:'';uniqueIndex:github_action_artifacts_run_external_key" json:"external_artifact_id"`
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
	ProjectID                string    `gorm:"type:uuid;not null;uniqueIndex:kubernetes_environments_scope_key" json:"project_id"`
	Name                     string    `gorm:"not null" json:"name"`
	Environment              string    `gorm:"not null;default:'';uniqueIndex:kubernetes_environments_scope_key" json:"environment"`
	ClusterName              string    `gorm:"not null;default:'';uniqueIndex:kubernetes_environments_scope_key" json:"cluster_name"`
	Namespace                string    `gorm:"not null;default:'';uniqueIndex:kubernetes_environments_scope_key" json:"namespace"`
	KubeconfigSecretRef      string    `gorm:"not null;default:''" json:"kubeconfig_secret_ref"`
	ServiceAccount           string    `gorm:"not null;default:''" json:"service_account"`
	TokenSubjectReviewStatus string    `gorm:"not null;default:'not_reviewed'" json:"token_subject_review_status"`
	RBACReadLogsStatus       string    `gorm:"not null;default:'not_reviewed'" json:"rbac_read_logs_status"`
	PodRestartStatus         string    `gorm:"not null;default:'not_reviewed'" json:"pod_restart_status"`
	Status                   string    `gorm:"not null;default:'metadata_only';index" json:"status"`
	Metadata                 JSONValue `gorm:"type:jsonb;not null" json:"metadata"`
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
