package app

import (
	"database/sql"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"time"
)

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
