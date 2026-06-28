package app

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

var ErrNotFound = errors.New("not found")

var approvalWebhookHTTPClient = &http.Client{Timeout: 5 * time.Second}

var errAgentPlanNotApproved = errors.New("agent task requires an approved plan before execution")

var errProjectVersionRefreshAlreadyQueued = errors.New("project version refresh is already queued or running")

var errProjectTemplateRunNotRetryable = errors.New("project template run is not retryable")

var errAssetRelationNotManual = errors.New("asset relation is not manual")

var errRepoSyncAssetDisabled = errors.New("repo sync asset is disabled")

var errRepoSyncAssetArchived = errors.New("repo sync asset is archived")

var errApprovalNotPending = errors.New("approval is not pending")

var providerReviewLiveExecutionLocks sync.Map

const (
	contextDirMode  os.FileMode = 0o750
	contextFileMode os.FileMode = 0o600
)

type Server struct {
	cfg            Config
	store          *Store
	log            *slog.Logger
	webhookLimiter *webhookRateLimiter
}

func NewServer(cfg Config, store *Store, log *slog.Logger) *Server {
	return &Server{cfg: cfg, store: store, log: log, webhookLimiter: newWebhookRateLimiter(60, time.Minute)}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(cors)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, HealthPayload("gateway"))
	})
	r.Post("/api/auth/login", s.login)
	r.Post("/api/webhooks/gitea/{id}", s.receiveGiteaWebhook)
	r.Post("/api/webhooks/github/{id}", s.receiveGitHubWebhook)

	r.Group(func(r chi.Router) {
		r.Use(s.auth)
		r.Get("/api/auth/me", s.me)
		r.Post("/api/projects", s.createProject)
		r.Get("/api/projects", s.listProjects)
		r.Post("/api/demo-readiness-data", s.ensureDemoReadinessData)
		r.Post("/api/demo-readiness-snapshot", s.recordDemoReadinessSnapshot)
		r.Get("/api/provider-accounts", s.listProviderAccounts)
		r.Post("/api/provider-accounts", s.createProviderAccount)
		r.Get("/api/connection-credentials", s.listGlobalConnectionCredentials)
		r.Post("/api/connection-credentials", s.createGlobalConnectionCredential)
		r.Post("/api/provider-accounts/execute-token-rotation-plan", s.executeProviderAccountTokenRotationPlan)
		r.Get("/api/provider-accounts/{id}", s.getProviderAccount)
		r.Patch("/api/provider-accounts/{id}", s.updateProviderAccount)
		r.Post("/api/provider-accounts/{id}/check", s.checkProviderAccount)
		r.Post("/api/provider-accounts/{id}/rotate-token-env", s.rotateProviderAccountTokenEnv)
		r.Get("/api/projects/{id}", s.getProject)
		r.Patch("/api/projects/{id}", s.updateProject)
		r.Delete("/api/projects/{id}", s.deleteProject)
		r.Get("/api/projects/{id}/versions", s.listProjectVersions)
		r.Post("/api/projects/{id}/versions", s.createProjectVersion)
		r.Get("/api/project-versions/{id}", s.getProjectVersion)
		r.Get("/api/project-versions/{id}/validation", s.getProjectVersionValidation)
		r.Post("/api/project-versions/{id}/refresh", s.refreshProjectVersionProviders)
		r.Post("/api/project-versions/{id}/validation-rerun", s.requestProjectVersionValidationRerun)
		r.Post("/api/project-versions/{id}/validation-snapshot", s.recordProjectVersionValidationSnapshot)
		r.Post("/api/project-versions/{id}/validation-rerun-snapshot", s.recordProjectVersionValidationRerunSnapshot)
		r.Post("/api/project-versions/{id}/pin-config-commit", s.pinProjectVersionConfigCommit)
		r.Get("/api/asset-graph-views", s.listAssetGraphViews)
		r.Post("/api/asset-graph-views", s.createAssetGraphView)
		r.Patch("/api/asset-graph-views/{id}", s.updateAssetGraphView)
		r.Delete("/api/asset-graph-views/{id}", s.deleteAssetGraphView)
		r.Get("/api/assets", s.listAssets)
		r.Get("/api/assets/graph", s.listAssetGraph)
		r.Post("/api/asset-relations", s.createAssetRelation)
		r.Delete("/api/asset-relations/{id}", s.deleteAssetRelation)
		r.Get("/api/assets/{id}/relations", s.listAssetRelations)
		r.Get("/api/assets/{id}/status-snapshots", s.listAssetStatusSnapshots)
		r.Get("/api/assets/{id}/dependencies", s.listAssetDependencies)
		r.Get("/api/projects/{id}/assets", s.listProjectAssets)
		r.Post("/api/projects/{id}/git-repositories", s.createGitRepository)
		r.Get("/api/projects/{id}/git-repositories", s.listGitRepositories)
		r.Get("/api/git-repositories/{id}", s.getGitRepository)
		r.Patch("/api/git-repositories/{id}", s.updateGitRepository)
		r.Get("/api/git-repositories/{id}/config-scaffold", s.getConfigRepositoryScaffold)
		r.Post("/api/git-repositories/{id}/config-scaffold/refresh-refs", s.refreshConfigRepositoryRefs)
		r.Post("/api/git-repositories/{id}/config-scaffold/ref-refresh-snapshot", s.recordConfigRepositoryRefRefreshSnapshot)
		r.Post("/api/git-repositories/{id}/config-scaffold/request-git-workflow", s.requestConfigRepositoryGitWorkflow)
		r.Post("/api/git-repositories/{id}/config-scaffold/promotion-snapshot", s.recordConfigRepositoryGitWorkflowPromotionSnapshot)
		r.Post("/api/git-repositories/{id}/sync", s.createRepositorySync)
		r.Post("/api/git-repositories/{id}/tags", s.createRepositoryTag)
		r.Get("/api/git-repositories/{id}/repo-sync-assets", s.listRepoSyncAssets)
		r.Post("/api/git-repositories/{id}/repo-sync-assets", s.createRepoSyncAsset)
		r.Post("/api/git-repositories/{id}/remotes", s.createGitRemote)
		r.Get("/api/git-repositories/{id}/remotes", s.listGitRemotes)
		r.Get("/api/git-remotes/{id}", s.getGitRemote)
		r.Put("/api/git-remotes/{id}", s.updateGitRemote)
		r.Patch("/api/git-remotes/{id}", s.updateGitRemote)
		r.Get("/api/git-remotes/{id}/github-actions", s.listGitHubActions)
		r.Get("/api/git-remotes/{id}/github-labels", s.listGitHubLabels)
		r.Post("/api/git-remotes/{id}/sync", s.createRemoteOperation("repo.sync"))
		r.Post("/api/git-remotes/{id}/tag", s.createRemoteOperation("repo.tag"))
		r.Post("/api/git-remotes/{id}/github-actions/sync", s.createRemoteOperation("github.actions.sync"))
		r.Post("/api/git-remotes/{id}/github-labels/sync", s.createRemoteOperation("github.labels.sync"))
		r.Get("/api/repo-sync-runs", s.listRepoSyncRuns)
		r.Post("/api/repo-sync-runs/{id}/rerun", s.rerunRepoSyncRun)
		r.Get("/api/repo-sync-assets/{id}", s.getRepoSyncAsset)
		r.Patch("/api/repo-sync-assets/{id}", s.updateRepoSyncAsset)
		r.Post("/api/repo-sync-assets/{id}/archive", s.archiveRepoSyncAsset)
		r.Post("/api/repo-sync-assets/{id}/restore", s.restoreRepoSyncAsset)
		r.Post("/api/repo-sync-assets/{id}/run", s.runRepoSyncAsset)
		r.Get("/api/repo-tag-runs", s.listRepoTagRuns)
		r.Post("/api/repo-tag-runs/{id}/live-lookup", s.createRepoTagRunLiveLookup)
		r.Post("/api/repo-tag-runs/{id}/actions-refresh", s.createRepoTagRunActionsRefresh)
		r.Post("/api/repo-tag-runs/{id}/result-snapshot", s.recordRepoTagRunResultSnapshot)
		r.Post("/api/repo-tag-runs/{id}/actions-refresh-snapshot", s.recordRepoTagRunActionsRefreshSnapshot)
		r.Get("/api/operation-approvals", s.listOperationApprovals)
		r.Get("/api/operation-approvals/summary", s.getOperationApprovalSummary)
		r.Get("/api/operation-approvals/reminder-candidates", s.listOperationApprovalReminderCandidates)
		r.Get("/api/operation-approval-rules", s.listOperationApprovalRules)
		r.Post("/api/operation-approval-rules", s.createOperationApprovalRule)
		r.Patch("/api/operation-approval-rules/{id}", s.updateOperationApprovalRule)
		r.Get("/api/operation-approval-rules/{id}/audits", s.listOperationApprovalRuleAudits)
		r.Get("/api/operation-approval-views", s.listOperationApprovalViews)
		r.Post("/api/operation-approval-views", s.createOperationApprovalView)
		r.Patch("/api/operation-approval-views/{id}", s.updateOperationApprovalView)
		r.Delete("/api/operation-approval-views/{id}", s.deleteOperationApprovalView)
		r.Get("/api/operation-approvals/{id}", s.getOperationApproval)
		r.Post("/api/operation-approvals/{id}/approve", s.approveOperationApproval)
		r.Post("/api/operation-approvals/{id}/reject", s.rejectOperationApproval)
		r.Post("/api/operation-approvals/{id}/remind", s.remindOperationApproval)
		r.Post("/api/operation-approvals/{id}/provider-review-arming-snapshot", s.recordProviderReviewMutationArmingSnapshot)
		r.Post("/api/operation-approvals/{id}/provider-review-current-live-readiness-snapshot", s.recordProviderReviewCurrentAttemptLiveReadinessSnapshot)
		r.Post("/api/operation-approvals/{id}/provider-review-current-live-launch-plan", s.providerReviewCurrentAttemptLiveExecutionLaunchPlan)
		r.Post("/api/operation-approvals/{id}/provider-review-current-live-execution-gate", s.providerReviewCurrentLiveExecutionGate)
		r.Post("/api/operation-approvals/{id}/delegations", s.createOperationApprovalDelegation)
		r.Post("/api/operation-approvals/{id}/delegations/{delegationID}/revoke", s.revokeOperationApprovalDelegation)
		r.Post("/api/provider-review-attempts/{id}/claim", s.claimProviderReviewAttempt)
		r.Post("/api/provider-review-attempts/{id}/local-result", s.recordProviderReviewAttemptLocalResult)
		r.Post("/api/provider-review-attempts/{id}/snapshot", s.recordProviderReviewAttemptSnapshot)
		r.Post("/api/provider-review-attempts/{id}/idempotency-snapshot", s.recordProviderReviewAttemptIdempotencySnapshot)
		r.Post("/api/provider-review-attempts/{id}/credential-snapshot", s.recordProviderReviewAttemptCredentialSnapshot)
		r.Post("/api/provider-review-attempts/{id}/branch-policy-snapshot", s.recordProviderReviewAttemptBranchPolicySnapshot)
		r.Post("/api/provider-review-attempts/{id}/execution-lock-snapshot", s.recordProviderReviewAttemptExecutionLockSnapshot)
		r.Post("/api/provider-review-attempts/{id}/runtime-snapshot", s.recordProviderReviewAttemptRuntimeSnapshot)
		r.Post("/api/provider-review-attempts/{id}/adapter-rehearsal-snapshot", s.recordProviderReviewAttemptAdapterRehearsalSnapshot)
		r.Post("/api/provider-review-attempts/{id}/adapter-blueprint-snapshot", s.recordProviderReviewAttemptAdapterBlueprintSnapshot)
		r.Post("/api/provider-review-attempts/{id}/live-adapter-contract-snapshot", s.recordProviderReviewAttemptLiveAdapterContractSnapshot)
		r.Post("/api/provider-review-attempts/{id}/invocation-snapshot", s.recordProviderReviewAttemptInvocationSnapshot)
		r.Post("/api/provider-review-attempts/{id}/request-materialization-snapshot", s.recordProviderReviewAttemptRequestMaterializationSnapshot)
		r.Post("/api/provider-review-attempts/{id}/request-validation-snapshot", s.recordProviderReviewAttemptRequestValidationSnapshot)
		r.Post("/api/provider-review-attempts/{id}/request-envelope-snapshot", s.recordProviderReviewAttemptRequestEnvelopeSnapshot)
		r.Post("/api/provider-review-attempts/{id}/activation-snapshot", s.recordProviderReviewAttemptActivationSnapshot)
		r.Post("/api/provider-review-attempts/{id}/transport-snapshot", s.recordProviderReviewAttemptTransportSnapshot)
		r.Post("/api/provider-review-attempts/{id}/send-snapshot", s.recordProviderReviewAttemptSendSnapshot)
		r.Post("/api/provider-review-attempts/{id}/retry-backoff-snapshot", s.recordProviderReviewAttemptRetryBackoffSnapshot)
		r.Post("/api/provider-review-attempts/{id}/response-snapshot", s.recordProviderReviewAttemptResponseSnapshot)
		r.Post("/api/provider-review-attempts/{id}/result-recording-snapshot", s.recordProviderReviewAttemptResultRecordingSnapshot)
		r.Post("/api/provider-review-attempts/{id}/provider-call-boundary-snapshot", s.recordProviderReviewAttemptProviderCallBoundarySnapshot)
		r.Post("/api/provider-review-attempts/{id}/transaction-snapshot", s.recordProviderReviewAttemptTransactionSnapshot)
		r.Post("/api/provider-review-attempts/{id}/live-execution-readiness-snapshot", s.recordProviderReviewAttemptLiveExecutionReadinessSnapshot)
		r.Post("/api/provider-review-attempts/{id}/live-execution-guard-snapshot", s.recordProviderReviewAttemptLiveExecutionGuardSnapshot)
		r.Post("/api/provider-review-attempts/{id}/live-execution-preflight", s.providerReviewAttemptLiveExecutionPreflight)
		r.Post("/api/provider-review-attempts/{id}/live-execution-launch-plan", s.providerReviewAttemptLiveExecutionLaunchPlan)
		r.Post("/api/provider-review-attempts/{id}/execute-live", s.executeProviderReviewAttemptLive)
		r.Post("/api/provider-review-attempts/{id}/cleanup-live", s.cleanupProviderReviewAttemptLive)
		r.Get("/api/operations", s.listOperations)
		r.Get("/api/worker-queue/summary", s.getWorkerQueueSummary)
		r.Get("/api/operations/{id}", s.getOperation)
		r.Get("/api/operations/{id}/logs", s.getOperationLogs)
		r.Get("/api/operations/{id}/logs/stream", s.streamOperationLogs)
		r.Post("/api/operations/{id}/cancel", s.cancelOperation)
		r.Post("/api/worker-nodes/test-job", s.createNodeTestJob)
		r.Get("/api/ai-runtimes", s.listAIRuntimes)
		r.Post("/api/ai-runtimes", s.createAIRuntime)
		r.Post("/api/ai-runtimes/{id}/verify", s.verifyAIRuntime)
		r.Post("/api/projects/{id}/agent/tasks", s.createAgentTask)
		r.Get("/api/projects/{id}/agent/tasks", s.listAgentTasks)
		r.Post("/api/projects/{id}/connection-credentials", s.createConnectionCredential)
		r.Get("/api/projects/{id}/connection-credentials", s.listConnectionCredentials)
		r.Get("/api/agent/tasks/{id}", s.getAgentTask)
		r.Get("/api/agent/tasks/{id}/tool-calls", s.listAgentTaskToolCalls)
		r.Post("/api/agent/tasks/{id}/tool-audit-snapshot", s.recordAgentToolAuditSnapshot)
		r.Post("/api/agent/tasks/{id}/tool-arming-snapshot", s.recordAgentToolArmingSnapshot)
		r.Post("/api/agent/tasks/{id}/code-audit-snapshot", s.recordAgentCodeAuditSnapshot)
		r.Post("/api/agent/tasks/{id}/generate-plan", s.generatePlan)
		r.Post("/api/agent/tasks/{id}/approve-plan", s.approvePlan)
		r.Post("/api/agent/tasks/{id}/execute", s.executePlan)
		r.Post("/api/projects/{id}/argo/connections", s.createArgoConnection)
		r.Get("/api/projects/{id}/argo/connections", s.listArgoConnections)
		r.Patch("/api/argo/connections/{id}", s.updateArgoConnection)
		r.Delete("/api/argo/connections/{id}", s.deleteArgoConnection)
		r.Post("/api/argo/connections/{id}/apps/sync", s.syncArgoApps)
		r.Get("/api/projects/{id}/argo/apps", s.listArgoApps)
		r.Post("/api/projects/{id}/kubernetes/environments", s.createKubernetesEnvironment)
		r.Get("/api/projects/{id}/kubernetes/environments", s.listKubernetesEnvironments)
		r.Post("/api/kubernetes/environments/{id}/argo/import-preview", s.previewArgoImportFromKubernetesEnvironment)
		r.Post("/api/kubernetes/environments/{id}/argo/import", s.importArgoFromKubernetesEnvironment)
		r.Get("/api/projects/{id}/deployment-targets", s.listDeploymentTargets)
		r.Post("/api/deployment-targets/{id}/execution-gate", s.deploymentTargetExecutionGate)
		r.Post("/api/deployment-targets/{id}/pods", s.listDeploymentTargetPods)
		r.Get("/api/projects/{id}/deployment-records", s.listDeploymentRecords)
		r.Get("/api/projects/{id}/rollback-points", s.listRollbackPoints)
		r.Post("/api/rollback-points/{id}/execution-gate", s.rollbackPointExecutionGate)
		r.Post("/api/projects/{id}/argo/pod-log-query-preview", s.previewArgoPodLogQuery)
		r.Post("/api/projects/{id}/argo/pod-logs", s.requestArgoPodLogRetrieval)
		r.Post("/api/projects/{id}/argo/pod-restarts", s.requestArgoPodRestart)
		r.Post("/api/projects/{id}/argo/pod-log-audit-snapshot", s.recordArgoPodLogAuditSnapshot)
		r.Post("/api/projects/{id}/webhook-connections", s.createWebhookConnection)
		r.Get("/api/projects/{id}/webhook-connections", s.listWebhookConnections)
		r.Get("/api/projects/{id}/webhook-events", s.listWebhookEvents)
		r.Post("/api/webhook-connections/{id}/threshold-decision-audit", s.recordWebhookThresholdDecisionAudit)
		r.Post("/api/webhook-connections/{id}/threshold-configuration", s.applyWebhookThresholdConfiguration)
		r.Post("/api/webhook-connections/{id}/provider-callback-rehearsal-snapshot", s.recordWebhookProviderCallbackRehearsalSnapshot)
		r.Post("/api/webhook-connections/{id}/rotate-secret", s.rotateWebhookConnectionSecret)
		r.Post("/api/webhook-events/{id}/replay", s.replayWebhookEvent)
		r.Post("/api/projects/{id}/ssh-machines", s.createSSHMachine)
		r.Get("/api/projects/{id}/ssh-machines", s.listSSHMachines)
		r.Patch("/api/ssh-machines/{id}", s.updateSSHMachine)
		r.Delete("/api/ssh-machines/{id}", s.deleteSSHMachine)
		r.Post("/api/ssh-machines/{id}/kubernetes/import-preview", s.previewKubernetesImportFromSSHMachine)
		r.Post("/api/ssh-machines/{id}/kubernetes/import", s.importKubernetesFromSSHMachine)
		r.Get("/api/ssh-machines/{id}/rehearsal", s.getSSHMachineRehearsal)
		r.Post("/api/ssh-machines/{id}/target-environment-proof", s.recordSSHMachineTargetEnvironmentProof)
		r.Post("/api/ssh-machines/{id}/rehearsal-snapshot", s.recordSSHMachineRehearsalSnapshot)
		r.Post("/api/ssh-machines/{id}/verify", s.verifySSHMachine)
		r.Post("/api/ssh-machines/{id}/commands", s.createSSHCommand)
		r.Get("/api/ssh-command-runs", s.listSSHCommandRuns)
		r.Post("/api/projects/{id}/context/generate", s.generateContext)
	})

	r.Post("/api/worker-nodes/register", s.registerNode)
	r.Post("/api/worker-nodes/heartbeat", s.nodeHeartbeat)
	r.Post("/api/worker-nodes/jobs/claim", s.claimJob)
	r.Post("/api/worker-nodes/jobs/{id}/logs", s.nodeJobLog)
	r.Post("/api/worker-nodes/jobs/{id}/complete", s.nodeJobComplete)
	r.Post("/api/worker-nodes/jobs/{id}/fail", s.nodeJobFail)
	return r
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "authorization, content-type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type webhookRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]webhookRateBucket
}

type webhookRateBucket struct {
	count     int
	resetAt   time.Time
	updatedAt time.Time
}

func newWebhookRateLimiter(limit int, window time.Duration) *webhookRateLimiter {
	return &webhookRateLimiter{limit: limit, window: window, buckets: map[string]webhookRateBucket{}}
}
