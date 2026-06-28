package app

import (
	"database/sql"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"time"
)

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
	Slug        string    `gorm:"not null;unique" json:"slug"`
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
	OperationRunID    sql.NullString `gorm:"type:uuid;unique" json:"operation_run_id"`
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
	ProjectTemplateRunID   sql.NullString `gorm:"type:uuid;index:project_template_files_run_path_key,unique" json:"project_template_run_id"`
	ProjectTemplateID      sql.NullString `gorm:"type:uuid;index" json:"project_template_id"`
	ProjectID              sql.NullString `gorm:"type:uuid;index" json:"project_id"`
	ProjectGitRepositoryID sql.NullString `gorm:"type:uuid;index" json:"project_git_repository_id"`
	Path                   string         `gorm:"not null;index:project_template_files_run_path_key,unique" json:"path"`
	Kind                   string         `gorm:"not null;default:'text'" json:"kind"`
	Content                string         `gorm:"not null;default:''" json:"content"`
	Status                 string         `gorm:"not null;default:'planned';index" json:"status"`
	Metadata               JSONValue      `gorm:"type:jsonb;not null" json:"metadata"`
}

func (GormProjectTemplateFile) TableName() string { return "project_template_files" }

type GormRepoSyncAsset struct {
	GormBase
	ProjectID              string         `gorm:"type:uuid;not null;index" json:"project_id"`
	ProjectGitRepositoryID string         `gorm:"type:uuid;not null;index:repo_sync_assets_repo_name_key,unique" json:"project_git_repository_id"`
	Name                   string         `gorm:"not null;index:repo_sync_assets_repo_name_key,unique" json:"name"`
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
	ResourceType          string         `gorm:"not null;default:'';index:operation_approval_rules_resource_action_key,unique" json:"resource_type"`
	Action                string         `gorm:"not null;index:operation_approval_rules_resource_action_key,unique" json:"action"`
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
	OperationApprovalID string         `gorm:"type:uuid;not null;index:operation_approval_decisions_approval_user_key,unique" json:"operation_approval_id"`
	UserID              sql.NullString `gorm:"type:uuid;index:operation_approval_decisions_approval_user_key,unique" json:"user_id"`
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
	OperationApprovalID string         `gorm:"type:uuid;not null;index:operation_approval_delegations_approval_user_key,unique" json:"operation_approval_id"`
	FromUserID          sql.NullString `gorm:"type:uuid;index" json:"from_user_id"`
	ToUserID            string         `gorm:"type:uuid;not null;index:operation_approval_delegations_approval_user_key,unique" json:"to_user_id"`
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
	UserID  string    `gorm:"type:uuid;not null;index:operation_approval_views_user_name_key,unique" json:"user_id"`
	Name    string    `gorm:"not null;index:operation_approval_views_user_name_key,unique" json:"name"`
	Filters JSONValue `gorm:"type:jsonb;not null" json:"filters"`
}

func (GormOperationApprovalView) TableName() string { return "operation_approval_views" }

type GormAssetGraphView struct {
	GormBase
	UserID  string    `gorm:"type:uuid;not null;index:asset_graph_views_user_name_key,unique" json:"user_id"`
	Name    string    `gorm:"not null;index:asset_graph_views_user_name_key,unique" json:"name"`
	Filters JSONValue `gorm:"type:jsonb;not null" json:"filters"`
}

func (GormAssetGraphView) TableName() string { return "asset_graph_views" }

type GormProviderReviewAttempt struct {
	GormBase
	OperationApprovalID            string         `gorm:"type:uuid;not null;index:provider_review_attempts_approval_operation_key,unique" json:"operation_approval_id"`
	ProjectTemplateRunID           sql.NullString `gorm:"type:uuid;index" json:"project_template_run_id"`
	ProviderType                   string         `gorm:"not null;default:'';index" json:"provider_type"`
	ReviewKind                     string         `gorm:"not null;default:''" json:"review_kind"`
	OperationName                  string         `gorm:"not null;index:provider_review_attempts_approval_operation_key,unique" json:"operation_name"`
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
