package app

import (
	"database/sql"
	"time"

	"github.com/lib/pq"
)

type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ProjectGitRepository struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Name          string    `json:"name"`
	RepoKey       string    `json:"repo_key"`
	DisplayName   string    `json:"display_name"`
	RepoRole      string    `json:"repo_role"`
	Status        string    `json:"status"`
	Description   string    `json:"description"`
	DefaultBranch string    `json:"default_branch"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type GitRemote struct {
	ID                     string         `json:"id"`
	ProjectGitRepositoryID string         `json:"project_git_repository_id"`
	Name                   string         `json:"name"`
	Kind                   string         `json:"kind"`
	RemoteKey              string         `json:"remote_key"`
	ProviderType           string         `json:"provider_type"`
	SourceProviderID       sql.NullString `json:"source_provider_id"`
	SourceAccountID        sql.NullString `json:"source_account_id"`
	CredentialID           sql.NullString `json:"credential_id"`
	RemoteURL              string         `json:"remote_url"`
	WebURL                 string         `json:"web_url"`
	RemoteRole             string         `json:"remote_role"`
	IsPrimary              bool           `json:"is_primary"`
	SyncEnabled            bool           `json:"sync_enabled"`
	Protected              bool           `json:"protected"`
	LatestSHA              string         `json:"latest_sha"`
	LastSyncStatus         string         `json:"last_sync_status"`
	URLs                   JSONValue      `json:"urls"`
	DefaultBranch          string         `json:"default_branch"`
	Metadata               JSONValue      `json:"metadata"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
}

type ProviderAccount struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	ProviderType string         `json:"provider_type"`
	APIBaseURL   string         `json:"api_base_url"`
	WebBaseURL   string         `json:"web_base_url"`
	TokenEnv     string         `json:"-"`
	DefaultOwner string         `json:"default_owner"`
	Visibility   string         `json:"visibility"`
	CredentialID sql.NullString `json:"credential_id"`
	Enabled      bool           `json:"enabled"`
	Metadata     JSONValue      `json:"metadata"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type OperationRun struct {
	ID            string         `json:"id"`
	ProjectID     sql.NullString `json:"project_id"`
	GitRemoteID   sql.NullString `json:"git_remote_id"`
	OperationType string         `json:"operation_type"`
	Status        string         `json:"status"`
	Title         string         `json:"title"`
	Input         JSONValue      `json:"input"`
	Result        JSONValue      `json:"result"`
	Error         string         `json:"error"`
	StartedAt     sql.NullTime   `json:"started_at"`
	FinishedAt    sql.NullTime   `json:"finished_at"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type WorkerNode struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Kind            string         `json:"kind"`
	Capabilities    pq.StringArray `json:"capabilities"`
	Status          string         `json:"status"`
	LastHeartbeatAt time.Time      `json:"last_heartbeat_at"`
	Metadata        JSONValue      `json:"metadata"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type WorkerJob struct {
	ID                   string         `json:"id"`
	OperationRunID       sql.NullString `json:"operation_run_id"`
	ToolName             string         `json:"tool_name"`
	Status               string         `json:"status"`
	Payload              JSONValue      `json:"payload"`
	Result               JSONValue      `json:"result"`
	Error                string         `json:"error"`
	RequiredCapabilities pq.StringArray `json:"required_capabilities"`
	PreferredNodeKind    string         `json:"preferred_node_kind"`
	AssignedWorkerNodeID sql.NullString `json:"assigned_worker_node_id"`
	ClaimedAt            sql.NullTime   `json:"claimed_at"`
	StartedAt            sql.NullTime   `json:"started_at"`
	FinishedAt           sql.NullTime   `json:"finished_at"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

type OperationLog struct {
	ID             string         `json:"id"`
	OperationRunID sql.NullString `json:"operation_run_id"`
	WorkerJobID    sql.NullString `json:"worker_job_id"`
	Level          string         `json:"level"`
	Message        string         `json:"message"`
	Fields         JSONValue      `json:"fields"`
	CreatedAt      time.Time      `json:"created_at"`
}

type RepoSyncRun struct {
	ID                     string         `json:"id"`
	OperationRunID         string         `json:"operation_run_id"`
	GitRemoteID            string         `json:"git_remote_id"`
	ProjectID              sql.NullString `json:"project_id"`
	ProjectGitRepositoryID sql.NullString `json:"project_git_repository_id"`
	SourceRemoteID         sql.NullString `json:"source_remote_id"`
	TargetRemoteID         sql.NullString `json:"target_remote_id"`
	Ref                    string         `json:"ref"`
	BeforeSHA              string         `json:"before_sha"`
	AfterSHA               string         `json:"after_sha"`
	ActorUserID            sql.NullString `json:"actor_user_id"`
	Status                 string         `json:"status"`
	Stdout                 string         `json:"stdout"`
	Stderr                 string         `json:"stderr"`
	ErrorMessage           string         `json:"error_message"`
	StartedAt              sql.NullTime   `json:"started_at"`
	FinishedAt             sql.NullTime   `json:"finished_at"`
	CreatedAt              time.Time      `json:"created_at"`
}

type RepoTagRun struct {
	ID                     string         `json:"id"`
	OperationRunID         string         `json:"operation_run_id"`
	GitRemoteID            string         `json:"git_remote_id"`
	ProjectID              sql.NullString `json:"project_id"`
	ProjectGitRepositoryID sql.NullString `json:"project_git_repository_id"`
	TargetRemoteID         sql.NullString `json:"target_remote_id"`
	TagName                string         `json:"tag_name"`
	TargetSHA              string         `json:"target_sha"`
	TagMessage             string         `json:"tag_message"`
	ActorUserID            sql.NullString `json:"actor_user_id"`
	Status                 string         `json:"status"`
	Stdout                 string         `json:"stdout"`
	Stderr                 string         `json:"stderr"`
	ErrorMessage           string         `json:"error_message"`
	StartedAt              sql.NullTime   `json:"started_at"`
	FinishedAt             sql.NullTime   `json:"finished_at"`
	CreatedAt              time.Time      `json:"created_at"`
}

type GitHubActionRun struct {
	ID             string         `json:"id"`
	OperationRunID sql.NullString `json:"operation_run_id"`
	GitRemoteID    string         `json:"git_remote_id"`
	ExternalRunID  string         `json:"external_run_id"`
	WorkflowName   string         `json:"workflow_name"`
	RunID          string         `json:"run_id"`
	Branch         string         `json:"branch"`
	CommitSHA      string         `json:"commit_sha"`
	Status         string         `json:"status"`
	Conclusion     string         `json:"conclusion"`
	HTMLURL        string         `json:"html_url"`
	Metadata       JSONValue      `json:"metadata"`
	StartedAt      sql.NullTime   `json:"started_at"`
	UpdatedAt      sql.NullTime   `json:"updated_at"`
	SyncedAt       sql.NullTime   `json:"synced_at"`
	CreatedAt      time.Time      `json:"created_at"`
}

type AIRuntime struct {
	ID           string         `json:"id"`
	ProjectID    sql.NullString `json:"project_id"`
	Name         string         `json:"name"`
	RuntimeType  string         `json:"runtime_type"`
	CodexBinary  string         `json:"codex_binary"`
	ProviderType string         `json:"provider_type"`
	APIBaseURL   string         `json:"api_base_url"`
	CredentialID sql.NullString `json:"credential_id"`
	Model        string         `json:"model"`
	Config       JSONValue      `json:"config"`
	Status       string         `json:"status"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type AgentTask struct {
	ID        string         `json:"id"`
	ProjectID string         `json:"project_id"`
	Title     string         `json:"title"`
	Prompt    string         `json:"prompt"`
	Status    string         `json:"status"`
	CreatedBy sql.NullString `json:"created_by"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type AgentPlan struct {
	ID          string       `json:"id"`
	AgentTaskID string       `json:"agent_task_id"`
	Status      string       `json:"status"`
	Content     string       `json:"content"`
	CreatedAt   time.Time    `json:"created_at"`
	ApprovedAt  sql.NullTime `json:"approved_at"`
}

type ArgoConnection struct {
	ID             string         `json:"id"`
	ProjectID      string         `json:"project_id"`
	Name           string         `json:"name"`
	ServerURL      string         `json:"server_url"`
	AuthType       string         `json:"auth_type"`
	CredentialID   sql.NullString `json:"credential_id"`
	Config         JSONValue      `json:"config"`
	LastSyncStatus string         `json:"last_sync_status"`
	LastSyncError  string         `json:"last_sync_error"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type ArgoApp struct {
	ID                 string         `json:"id"`
	ProjectID          string         `json:"project_id"`
	ArgoConnectionID   sql.NullString `json:"argo_connection_id"`
	DeploymentTargetID sql.NullString `json:"deployment_target_id"`
	Name               string         `json:"name"`
	Namespace          string         `json:"namespace"`
	Status             string         `json:"status"`
	Metadata           JSONValue      `json:"metadata"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	SyncedAt           sql.NullTime   `json:"synced_at"`
}

type DeploymentTarget struct {
	ID               string         `json:"id"`
	ProjectID        string         `json:"project_id"`
	Name             string         `json:"name"`
	Environment      string         `json:"environment"`
	ClusterName      string         `json:"cluster_name"`
	Namespace        string         `json:"namespace"`
	Source           string         `json:"source"`
	ArgoConnectionID sql.NullString `json:"argo_connection_id"`
	Status           string         `json:"status"`
	Metadata         JSONValue      `json:"metadata"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type SSHMachine struct {
	ID           string         `json:"id"`
	ProjectID    string         `json:"project_id"`
	Name         string         `json:"name"`
	Host         string         `json:"host"`
	Port         int            `json:"port"`
	Username     string         `json:"username"`
	AuthType     string         `json:"auth_type"`
	CredentialID sql.NullString `json:"credential_id"`
	Metadata     JSONValue      `json:"metadata"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type SSHCommandRun struct {
	ID             string         `json:"id"`
	OperationRunID sql.NullString `json:"operation_run_id"`
	SSHMachineID   sql.NullString `json:"ssh_machine_id"`
	ProjectID      sql.NullString `json:"project_id"`
	Command        string         `json:"command"`
	Status         string         `json:"status"`
	ExitCode       sql.NullInt64  `json:"exit_code"`
	Stdout         string         `json:"stdout"`
	Stderr         string         `json:"stderr"`
	ErrorMessage   string         `json:"error_message"`
	ActorUserID    sql.NullString `json:"actor_user_id"`
	StartedAt      sql.NullTime   `json:"started_at"`
	FinishedAt     sql.NullTime   `json:"finished_at"`
	CreatedAt      time.Time      `json:"created_at"`
}
