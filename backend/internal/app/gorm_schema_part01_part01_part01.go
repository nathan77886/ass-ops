package app

import (
	"database/sql"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"time"
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
	Email        string `gorm:"not null;unique" json:"email"`
	Name         string `gorm:"not null;default:''" json:"name"`
	PasswordHash string `gorm:"not null;default:''" json:"-"`
	Role         string `gorm:"not null;default:'operator';index" json:"role"`
}

func (GormUser) TableName() string { return "users" }

type GormProject struct {
	GormBase
	Name        string `gorm:"not null" json:"name"`
	Slug        string `gorm:"not null;unique" json:"slug"`
	Description string `gorm:"not null;default:''" json:"description"`
}

func (GormProject) TableName() string { return "projects" }

type GormProjectMember struct {
	ID        string    `gorm:"type:uuid;primaryKey" json:"id"`
	ProjectID string    `gorm:"type:uuid;not null;index:project_members_project_user_key,unique" json:"project_id"`
	UserID    string    `gorm:"type:uuid;not null;index:project_members_project_user_key,unique" json:"user_id"`
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
	ProjectID     string `gorm:"type:uuid;not null;index:project_git_repositories_project_repo_key_key,unique" json:"project_id"`
	Name          string `gorm:"not null" json:"name"`
	RepoKey       string `gorm:"not null;default:'';index:project_git_repositories_project_repo_key_key,unique" json:"repo_key"`
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
	Name         string         `gorm:"not null;unique" json:"name"`
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
	ProjectGitRepositoryID string         `gorm:"type:uuid;not null;index:git_remotes_repo_name_key,unique" json:"project_git_repository_id"`
	Name                   string         `gorm:"not null;index:git_remotes_repo_name_key,unique" json:"name"`
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
	Name            string         `gorm:"not null;unique" json:"name"`
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
	ProjectID string    `gorm:"type:uuid;not null;index:idx_project_versions_project_version,unique" json:"project_id"`
	Version   string    `gorm:"not null;index:idx_project_versions_project_version,unique" json:"version"`
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
