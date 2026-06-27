package app

import (
	"database/sql"
	"time"

	"github.com/lib/pq"
)

type Project struct {
	ID          string    `db:"id" json:"id"`
	Name        string    `db:"name" json:"name"`
	Slug        string    `db:"slug" json:"slug"`
	Description string    `db:"description" json:"description"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
}

type ProjectGitRepository struct {
	ID            string    `db:"id" json:"id"`
	ProjectID     string    `db:"project_id" json:"project_id"`
	Name          string    `db:"name" json:"name"`
	RepoKey       string    `db:"repo_key" json:"repo_key"`
	DisplayName   string    `db:"display_name" json:"display_name"`
	RepoRole      string    `db:"repo_role" json:"repo_role"`
	Status        string    `db:"status" json:"status"`
	Description   string    `db:"description" json:"description"`
	DefaultBranch string    `db:"default_branch" json:"default_branch"`
	CreatedAt     time.Time `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time `db:"updated_at" json:"updated_at"`
}

type GitRemote struct {
	ID                     string         `db:"id" json:"id"`
	ProjectGitRepositoryID string         `db:"project_git_repository_id" json:"project_git_repository_id"`
	Name                   string         `db:"name" json:"name"`
	Kind                   string         `db:"kind" json:"kind"`
	RemoteKey              string         `db:"remote_key" json:"remote_key"`
	ProviderType           string         `db:"provider_type" json:"provider_type"`
	SourceProviderID       sql.NullString `db:"source_provider_id" json:"source_provider_id"`
	SourceAccountID        sql.NullString `db:"source_account_id" json:"source_account_id"`
	CredentialID           sql.NullString `db:"credential_id" json:"credential_id"`
	RemoteURL              string         `db:"remote_url" json:"remote_url"`
	WebURL                 string         `db:"web_url" json:"web_url"`
	RemoteRole             string         `db:"remote_role" json:"remote_role"`
	IsPrimary              bool           `db:"is_primary" json:"is_primary"`
	SyncEnabled            bool           `db:"sync_enabled" json:"sync_enabled"`
	Protected              bool           `db:"protected" json:"protected"`
	LatestSHA              string         `db:"latest_sha" json:"latest_sha"`
	LastSyncStatus         string         `db:"last_sync_status" json:"last_sync_status"`
	URLs                   JSONValue      `db:"urls" json:"urls"`
	DefaultBranch          string         `db:"default_branch" json:"default_branch"`
	Metadata               JSONValue      `db:"metadata" json:"metadata"`
	CreatedAt              time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt              time.Time      `db:"updated_at" json:"updated_at"`
}

type ProviderAccount struct {
	ID           string         `db:"id" json:"id"`
	Name         string         `db:"name" json:"name"`
	ProviderType string         `db:"provider_type" json:"provider_type"`
	APIBaseURL   string         `db:"api_base_url" json:"api_base_url"`
	WebBaseURL   string         `db:"web_base_url" json:"web_base_url"`
	TokenEnv     string         `db:"token_env" json:"-"`
	DefaultOwner string         `db:"default_owner" json:"default_owner"`
	Visibility   string         `db:"visibility" json:"visibility"`
	CredentialID sql.NullString `db:"credential_id" json:"credential_id"`
	Enabled      bool           `db:"enabled" json:"enabled"`
	Metadata     JSONValue      `db:"metadata" json:"metadata"`
	CreatedAt    time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time      `db:"updated_at" json:"updated_at"`
}

type OperationRun struct {
	ID            string         `db:"id" json:"id"`
	ProjectID     sql.NullString `db:"project_id" json:"project_id"`
	GitRemoteID   sql.NullString `db:"git_remote_id" json:"git_remote_id"`
	OperationType string         `db:"operation_type" json:"operation_type"`
	Status        string         `db:"status" json:"status"`
	Title         string         `db:"title" json:"title"`
	Input         JSONValue      `db:"input" json:"input"`
	Result        JSONValue      `db:"result" json:"result"`
	Error         string         `db:"error" json:"error"`
	StartedAt     sql.NullTime   `db:"started_at" json:"started_at"`
	FinishedAt    sql.NullTime   `db:"finished_at" json:"finished_at"`
	CreatedAt     time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time      `db:"updated_at" json:"updated_at"`
}

type WorkerNode struct {
	ID              string         `db:"id" json:"id"`
	Name            string         `db:"name" json:"name"`
	Kind            string         `db:"kind" json:"kind"`
	Capabilities    pq.StringArray `db:"capabilities" json:"capabilities"`
	Status          string         `db:"status" json:"status"`
	LastHeartbeatAt time.Time      `db:"last_heartbeat_at" json:"last_heartbeat_at"`
	Metadata        JSONValue      `db:"metadata" json:"metadata"`
	CreatedAt       time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time      `db:"updated_at" json:"updated_at"`
}

type WorkerJob struct {
	ID                   string         `db:"id" json:"id"`
	OperationRunID       sql.NullString `db:"operation_run_id" json:"operation_run_id"`
	ToolName             string         `db:"tool_name" json:"tool_name"`
	Status               string         `db:"status" json:"status"`
	Payload              JSONValue      `db:"payload" json:"payload"`
	Result               JSONValue      `db:"result" json:"result"`
	Error                string         `db:"error" json:"error"`
	RequiredCapabilities pq.StringArray `db:"required_capabilities" json:"required_capabilities"`
	PreferredNodeKind    string         `db:"preferred_node_kind" json:"preferred_node_kind"`
	AssignedWorkerNodeID sql.NullString `db:"assigned_worker_node_id" json:"assigned_worker_node_id"`
	ClaimedAt            sql.NullTime   `db:"claimed_at" json:"claimed_at"`
	StartedAt            sql.NullTime   `db:"started_at" json:"started_at"`
	FinishedAt           sql.NullTime   `db:"finished_at" json:"finished_at"`
	CreatedAt            time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt            time.Time      `db:"updated_at" json:"updated_at"`
}

type OperationLog struct {
	ID             string         `db:"id" json:"id"`
	OperationRunID sql.NullString `db:"operation_run_id" json:"operation_run_id"`
	WorkerJobID    sql.NullString `db:"worker_job_id" json:"worker_job_id"`
	Level          string         `db:"level" json:"level"`
	Message        string         `db:"message" json:"message"`
	Fields         JSONValue      `db:"fields" json:"fields"`
	CreatedAt      time.Time      `db:"created_at" json:"created_at"`
}

type RepoSyncRun struct {
	ID                     string         `db:"id" json:"id"`
	OperationRunID         string         `db:"operation_run_id" json:"operation_run_id"`
	GitRemoteID            string         `db:"git_remote_id" json:"git_remote_id"`
	ProjectID              sql.NullString `db:"project_id" json:"project_id"`
	ProjectGitRepositoryID sql.NullString `db:"project_git_repository_id" json:"project_git_repository_id"`
	SourceRemoteID         sql.NullString `db:"source_remote_id" json:"source_remote_id"`
	TargetRemoteID         sql.NullString `db:"target_remote_id" json:"target_remote_id"`
	Ref                    string         `db:"ref" json:"ref"`
	BeforeSHA              string         `db:"before_sha" json:"before_sha"`
	AfterSHA               string         `db:"after_sha" json:"after_sha"`
	ActorUserID            sql.NullString `db:"actor_user_id" json:"actor_user_id"`
	Status                 string         `db:"status" json:"status"`
	Stdout                 string         `db:"stdout" json:"stdout"`
	Stderr                 string         `db:"stderr" json:"stderr"`
	ErrorMessage           string         `db:"error_message" json:"error_message"`
	StartedAt              sql.NullTime   `db:"started_at" json:"started_at"`
	FinishedAt             sql.NullTime   `db:"finished_at" json:"finished_at"`
	CreatedAt              time.Time      `db:"created_at" json:"created_at"`
}

type RepoTagRun struct {
	ID                     string         `db:"id" json:"id"`
	OperationRunID         string         `db:"operation_run_id" json:"operation_run_id"`
	GitRemoteID            string         `db:"git_remote_id" json:"git_remote_id"`
	ProjectID              sql.NullString `db:"project_id" json:"project_id"`
	ProjectGitRepositoryID sql.NullString `db:"project_git_repository_id" json:"project_git_repository_id"`
	TargetRemoteID         sql.NullString `db:"target_remote_id" json:"target_remote_id"`
	TagName                string         `db:"tag_name" json:"tag_name"`
	TargetSHA              string         `db:"target_sha" json:"target_sha"`
	TagMessage             string         `db:"tag_message" json:"tag_message"`
	ActorUserID            sql.NullString `db:"actor_user_id" json:"actor_user_id"`
	Status                 string         `db:"status" json:"status"`
	Stdout                 string         `db:"stdout" json:"stdout"`
	Stderr                 string         `db:"stderr" json:"stderr"`
	ErrorMessage           string         `db:"error_message" json:"error_message"`
	StartedAt              sql.NullTime   `db:"started_at" json:"started_at"`
	FinishedAt             sql.NullTime   `db:"finished_at" json:"finished_at"`
	CreatedAt              time.Time      `db:"created_at" json:"created_at"`
}

type GitHubActionRun struct {
	ID             string         `db:"id" json:"id"`
	OperationRunID sql.NullString `db:"operation_run_id" json:"operation_run_id"`
	GitRemoteID    string         `db:"git_remote_id" json:"git_remote_id"`
	ExternalRunID  string         `db:"external_run_id" json:"external_run_id"`
	WorkflowName   string         `db:"workflow_name" json:"workflow_name"`
	RunID          string         `db:"run_id" json:"run_id"`
	Branch         string         `db:"branch" json:"branch"`
	CommitSHA      string         `db:"commit_sha" json:"commit_sha"`
	Status         string         `db:"status" json:"status"`
	Conclusion     string         `db:"conclusion" json:"conclusion"`
	HTMLURL        string         `db:"html_url" json:"html_url"`
	Metadata       JSONValue      `db:"metadata" json:"metadata"`
	StartedAt      sql.NullTime   `db:"started_at" json:"started_at"`
	UpdatedAt      sql.NullTime   `db:"updated_at" json:"updated_at"`
	SyncedAt       sql.NullTime   `db:"synced_at" json:"synced_at"`
	CreatedAt      time.Time      `db:"created_at" json:"created_at"`
}

type AIRuntime struct {
	ID           string         `db:"id" json:"id"`
	ProjectID    sql.NullString `db:"project_id" json:"project_id"`
	Name         string         `db:"name" json:"name"`
	RuntimeType  string         `db:"runtime_type" json:"runtime_type"`
	CodexBinary  string         `db:"codex_binary" json:"codex_binary"`
	ProviderType string         `db:"provider_type" json:"provider_type"`
	APIBaseURL   string         `db:"api_base_url" json:"api_base_url"`
	CredentialID sql.NullString `db:"credential_id" json:"credential_id"`
	Model        string         `db:"model" json:"model"`
	Config       JSONValue      `db:"config" json:"config"`
	Status       string         `db:"status" json:"status"`
	CreatedAt    time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time      `db:"updated_at" json:"updated_at"`
}

type AgentTask struct {
	ID        string         `db:"id" json:"id"`
	ProjectID string         `db:"project_id" json:"project_id"`
	Title     string         `db:"title" json:"title"`
	Prompt    string         `db:"prompt" json:"prompt"`
	Status    string         `db:"status" json:"status"`
	CreatedBy sql.NullString `db:"created_by" json:"created_by"`
	CreatedAt time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt time.Time      `db:"updated_at" json:"updated_at"`
}

type AgentPlan struct {
	ID          string       `db:"id" json:"id"`
	AgentTaskID string       `db:"agent_task_id" json:"agent_task_id"`
	Status      string       `db:"status" json:"status"`
	Content     string       `db:"content" json:"content"`
	CreatedAt   time.Time    `db:"created_at" json:"created_at"`
	ApprovedAt  sql.NullTime `db:"approved_at" json:"approved_at"`
}

type ArgoConnection struct {
	ID             string         `db:"id" json:"id"`
	ProjectID      string         `db:"project_id" json:"project_id"`
	Name           string         `db:"name" json:"name"`
	ServerURL      string         `db:"server_url" json:"server_url"`
	AuthType       string         `db:"auth_type" json:"auth_type"`
	CredentialID   sql.NullString `db:"credential_id" json:"credential_id"`
	Config         JSONValue      `db:"config" json:"config"`
	LastSyncStatus string         `db:"last_sync_status" json:"last_sync_status"`
	LastSyncError  string         `db:"last_sync_error" json:"last_sync_error"`
	CreatedAt      time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time      `db:"updated_at" json:"updated_at"`
}

type ArgoApp struct {
	ID                 string         `db:"id" json:"id"`
	ProjectID          string         `db:"project_id" json:"project_id"`
	ArgoConnectionID   sql.NullString `db:"argo_connection_id" json:"argo_connection_id"`
	DeploymentTargetID sql.NullString `db:"deployment_target_id" json:"deployment_target_id"`
	Name               string         `db:"name" json:"name"`
	Namespace          string         `db:"namespace" json:"namespace"`
	Status             string         `db:"status" json:"status"`
	Metadata           JSONValue      `db:"metadata" json:"metadata"`
	CreatedAt          time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at" json:"updated_at"`
	SyncedAt           sql.NullTime   `db:"synced_at" json:"synced_at"`
}

type DeploymentTarget struct {
	ID               string         `db:"id" json:"id"`
	ProjectID        string         `db:"project_id" json:"project_id"`
	Name             string         `db:"name" json:"name"`
	Environment      string         `db:"environment" json:"environment"`
	ClusterName      string         `db:"cluster_name" json:"cluster_name"`
	Namespace        string         `db:"namespace" json:"namespace"`
	Source           string         `db:"source" json:"source"`
	ArgoConnectionID sql.NullString `db:"argo_connection_id" json:"argo_connection_id"`
	Status           string         `db:"status" json:"status"`
	Metadata         JSONValue      `db:"metadata" json:"metadata"`
	CreatedAt        time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time      `db:"updated_at" json:"updated_at"`
}

type SSHMachine struct {
	ID           string         `db:"id" json:"id"`
	ProjectID    string         `db:"project_id" json:"project_id"`
	Name         string         `db:"name" json:"name"`
	Host         string         `db:"host" json:"host"`
	Port         int            `db:"port" json:"port"`
	Username     string         `db:"username" json:"username"`
	AuthType     string         `db:"auth_type" json:"auth_type"`
	CredentialID sql.NullString `db:"credential_id" json:"credential_id"`
	Metadata     JSONValue      `db:"metadata" json:"metadata"`
	CreatedAt    time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time      `db:"updated_at" json:"updated_at"`
}

type SSHCommandRun struct {
	ID             string         `db:"id" json:"id"`
	OperationRunID sql.NullString `db:"operation_run_id" json:"operation_run_id"`
	SSHMachineID   sql.NullString `db:"ssh_machine_id" json:"ssh_machine_id"`
	ProjectID      sql.NullString `db:"project_id" json:"project_id"`
	Command        string         `db:"command" json:"command"`
	Status         string         `db:"status" json:"status"`
	ExitCode       sql.NullInt64  `db:"exit_code" json:"exit_code"`
	Stdout         string         `db:"stdout" json:"stdout"`
	Stderr         string         `db:"stderr" json:"stderr"`
	ErrorMessage   string         `db:"error_message" json:"error_message"`
	ActorUserID    sql.NullString `db:"actor_user_id" json:"actor_user_id"`
	StartedAt      sql.NullTime   `db:"started_at" json:"started_at"`
	FinishedAt     sql.NullTime   `db:"finished_at" json:"finished_at"`
	CreatedAt      time.Time      `db:"created_at" json:"created_at"`
}
