import React, { useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  ApiOutlined,
  AppstoreOutlined,
  BranchesOutlined,
  CloudServerOutlined,
  CodeOutlined,
  DashboardOutlined,
  DeploymentUnitOutlined,
  PlayCircleOutlined,
  RobotOutlined,
  SettingOutlined
} from '@ant-design/icons';
import {
  Alert,
  Button,
  Card,
  Checkbox,
  ConfigProvider,
  Form,
  Input,
  Layout,
  List,
  Menu,
  Modal,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
  message
} from 'antd';
import './styles.css';

const { Header, Sider, Content } = Layout;
const API = import.meta.env.VITE_API_BASE || '';

type AnyRow = Record<string, any>;

function authToken() {
  return localStorage.getItem('assops_token') || '';
}

async function api(path: string, init: RequestInit = {}) {
  const headers: Record<string, string> = { 'content-type': 'application/json' };
  const token = authToken();
  if (token) headers.authorization = `Bearer ${token}`;
  const res = await fetch(`${API}${path}`, { ...init, headers: { ...headers, ...(init.headers || {}) } });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

function streamURL(path: string) {
  const token = authToken();
  const sep = path.includes('?') ? '&' : '?';
  return `${API}${path}${sep}token=${encodeURIComponent(token)}`;
}

function safeProviderAuthSchemeLabel(value: unknown) {
  switch (value) {
    case 'bearer_token':
      return 'bearer token';
    case 'token':
      return 'token';
    default:
      return 'redacted auth';
  }
}

function safeProviderEndpointTemplateLabel(value: unknown) {
  switch (value) {
    case 'github_git_refs_path_template':
    case 'gitea_git_refs_path_template':
      return 'git refs template';
    case 'github_repository_contents_path_template':
    case 'gitea_repository_contents_path_template':
      return 'repository contents template';
    case 'github_pull_request_path_template':
      return 'pull request template';
    case 'gitea_merge_request_path_template':
      return 'merge request template';
    default:
      return 'redacted endpoint template';
  }
}

function Login({ onLogin }: { onLogin: () => void }) {
  const [loading, setLoading] = useState(false);
  return (
    <div className="loginPage">
      <div className="loginPanel">
        <Typography.Title level={1}>ASSOPS</Typography.Title>
        <Typography.Paragraph>Operations cockpit for projects, workers, remotes, and AI runtime context.</Typography.Paragraph>
        <Form
          layout="vertical"
          initialValues={{ email: 'admin@assops.local', password: 'admin1234' }}
          onFinish={async (values) => {
            setLoading(true);
            try {
              const data = await api('/api/auth/login', { method: 'POST', body: JSON.stringify(values) });
              localStorage.setItem('assops_token', data.token);
              onLogin();
            } catch (error: any) {
              message.error(error.message);
            } finally {
              setLoading(false);
            }
          }}
        >
          <Form.Item name="email" label="Email" rules={[{ required: true }]}>
            <Input autoComplete="username" />
          </Form.Item>
          <Form.Item name="password" label="Password" rules={[{ required: true }]}>
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={loading} block>Sign in</Button>
        </Form>
      </div>
    </div>
  );
}

function useLoad<T>(loader: () => Promise<T>, deps: React.DependencyList) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError('');
    loader().then((next) => {
      if (!alive) return;
      setData(next);
    }).catch((err) => {
      if (!alive) return;
      setError(err instanceof Error ? err.message : String(err || 'Request failed'));
    }).finally(() => alive && setLoading(false));
    return () => { alive = false; };
  }, [...deps, tick]);
  return { data, error, loading, reload: () => setTick((x) => x + 1) };
}

function useDebouncedValue<T>(value: T, delay = 300) {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(value), delay);
    return () => window.clearTimeout(timer);
  }, [value, delay]);
  return debounced;
}

function useOperationLogStream(operationID?: string) {
  const [logs, setLogs] = useState<AnyRow[]>([]);
  const [status, setStatus] = useState('');
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    setLogs([]);
    setStatus('');
    setError('');
    setConnected(false);
    if (!operationID) return;
    const source = new EventSource(streamURL(`/api/operations/${operationID}/logs/stream`));
    source.onopen = () => {
      setConnected(true);
      setError('');
    };
    source.addEventListener('log', (event) => {
      const row = JSON.parse(event.data);
      setLogs((current) => current.some((item) => item.id === row.id) ? current : [...current, row]);
    });
    source.addEventListener('operation_status', (event) => {
      const payload = JSON.parse(event.data);
      setStatus(payload.status || '');
      setConnected(false);
      source.close();
    });
    source.addEventListener('stream_error', (event) => {
      const payload = JSON.parse((event as MessageEvent).data);
      setError(payload.message || 'Log stream failed');
      setConnected(false);
      source.close();
    });
    source.onerror = () => {
      setConnected(false);
      setError('Log stream disconnected');
    };
    return () => source.close();
  }, [operationID]);
  return { logs, status, connected, error };
}

function operationLogStreamTag(stream: ReturnType<typeof useOperationLogStream>) {
  if (stream.connected) return { color: 'green', label: 'live' };
  if (stream.error) return { color: 'red', label: 'error' };
  if (stream.status) return { color: operationStatusColor(stream.status), label: stream.status };
  return { color: 'default', label: 'waiting' };
}

function emptyOperationLogMessage(stream: ReturnType<typeof useOperationLogStream>) {
  if (stream.connected) return 'Waiting for operation logs';
  if (String(stream.status || '').toLowerCase() === 'completed') return 'Operation completed with no logs';
  return 'No operation logs received';
}

function operationStatusColor(status: any) {
  switch (String(status || '').toLowerCase()) {
    case 'completed':
      return 'green';
    case 'failed':
    case 'canceled':
    case 'cancelled':
      return 'red';
    case 'running':
    case 'queued':
      return 'blue';
    default:
      return 'default';
  }
}

function configWorkflowAuditEvidenceColor(status: any) {
  switch (String(status || '').toLowerCase()) {
    case 'recorded':
      return 'green';
    case 'waiting_for_worker':
      return 'blue';
    case 'failed':
    case 'mixed_failed':
      return 'red';
    case 'canceled':
    case 'cancelled':
      return 'orange';
    default:
      return 'default';
  }
}

function callbackEvidenceColor(status: any) {
  switch (String(status || '').toLowerCase()) {
    case 'recorded':
    case 'observed':
      return 'green';
    case 'failed':
      return 'red';
    case 'ignored':
      return 'gold';
    default:
      return 'default';
  }
}

function thresholdVolumeColor(plan: any, volume: any) {
  if (plan?.threshold_review_ready) return 'green';
  if (volume?.webhook_failure_volume_observed) return 'red';
  if (volume?.local_volume_observed) return 'gold';
  return 'default';
}

function tagResultEvidenceColor(status: any) {
  switch (String(status || '').toLowerCase()) {
    case 'recorded':
      return 'green';
    case 'failed':
      return 'red';
    case 'waiting_for_worker':
      return 'blue';
    default:
      return 'default';
  }
}

function rowOptions(rows: AnyRow[] = [], labelKey = 'name') {
  return rows.map((row) => ({ value: row.id, label: row[labelKey] || row.name || row.id }));
}

function useSelectedRow(rows: AnyRow[] = []) {
  const [selectedID, setSelectedID] = useState<string>();
  useEffect(() => {
    setSelectedID((previousID) => {
      if (!rows.length) return undefined;
      if (!previousID || !rows.some((row) => row.id === previousID)) return rows[0].id;
      return previousID;
    });
  }, [rows]);
  return { selectedID, setSelectedID, selected: rows.find((row) => row.id === selectedID) };
}

function EntitySelect({ label, rows, value, onChange }: { label: string; rows: AnyRow[]; value?: string; onChange: (value: string) => void }) {
  return (
    <Space direction="vertical" size={4} className="selector">
      <Typography.Text type="secondary">{label}</Typography.Text>
      <Select value={value} onChange={onChange} options={rowOptions(rows)} placeholder={label} disabled={!rows.length} />
    </Space>
  );
}

function urlsText(row: AnyRow) {
  if (Array.isArray(row.urls)) return row.urls.join(', ');
  if (row.remote_url) return row.remote_url;
  return '';
}

function secondsText(value: any) {
  const seconds = Number(value || 0);
  if (!Number.isFinite(seconds) || seconds <= 0) return '-';
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)}s`;
  return `${(seconds / 60).toFixed(1)}m`;
}

function shortText(value: any, limit = 72) {
  const text = String(value || '').trim();
  if (!text) return '-';
  return text.length > limit ? `${text.slice(0, limit - 1)}...` : text;
}

function signalSeverityColor(value: any) {
  switch (String(value || '').toLowerCase()) {
    case 'danger':
      return 'red';
    case 'warning':
      return 'gold';
    default:
      return 'green';
  }
}

const githubActionSuccessStates = ['success', 'completed'];
const githubActionFailureStates = ['failure', 'failed', 'cancelled', 'timed_out', 'action_required', 'startup_failure'];
const githubActionActiveStates = ['in_progress', 'queued', 'requested', 'waiting', 'pending'];

function githubActionState(row: AnyRow) {
  return String(row.conclusion || row.status || '').toLowerCase();
}

function githubActionStatusColor(row: AnyRow) {
  const value = githubActionState(row);
  if (githubActionSuccessStates.includes(value)) return 'green';
  if (githubActionFailureStates.includes(value)) return value === 'action_required' ? 'orange' : 'red';
  if (githubActionActiveStates.includes(value)) return 'blue';
  return 'default';
}

function githubActionRemoteDescription(sourceRemote: AnyRow | undefined, repo: AnyRow | undefined, project: AnyRow | undefined, summary: ReturnType<typeof githubActionsSummary>) {
  if (!sourceRemote) return 'Select a GitHub remote to tie pipeline state back to this repository.';
  const provider = String(sourceRemote.provider_type || sourceRemote.kind || '').toLowerCase();
  const remoteName = sourceRemote.name || sourceRemote.remote_key || sourceRemote.id;
  if (provider !== 'github') return `Remote ${remoteName} is a ${provider || 'non-GitHub'} remote, so GitHub Actions are unavailable for this selection.`;
  const failureNote = summary.failures > 0 ? ` ${summary.failures} problem run${summary.failures === 1 ? '' : 's'} in this batch.` : '';
  return `Remote ${remoteName} is attached to ${repo?.display_name || repo?.name || 'the selected repository'} in ${project?.name || 'the selected project'}. Latest status: ${summary.latestStatus}.${failureNote}`;
}

function githubActionsSummary(rows: AnyRow[]) {
  const failures = rows.filter((row) => githubActionFailureStates.includes(githubActionState(row))).length;
  const successes = rows.filter((row) => githubActionSuccessStates.includes(githubActionState(row))).length;
  const active = rows.filter((row) => githubActionActiveStates.includes(String(row.status || '').toLowerCase())).length;
  const latest = rows[0];
  return {
    total: rows.length,
    successes,
    failures,
    active,
    latestLabel: latest ? `${latest.workflow_name || 'GitHub Actions'} on ${latest.branch || 'unknown branch'}` : 'No GitHub Actions runs synced',
    latestStatus: latest ? String(latest.conclusion || latest.status || 'unknown') : 'none'
  };
}

function templateProvisionSummary(row: AnyRow) {
  const details = row.result?.details || {};
  const reconciliation = details.repository_reconciliation || {};
  if (row.result?.repository_provisioned) return { color: 'green', label: 'provisioned', detail: '' };
  if (row.status === 'provisioning') return { color: 'blue', label: 'provisioning', detail: '' };
  if (reconciliation.kind === 'existing_repository') return { color: 'gold', label: 'needs reconcile', detail: 'existing repository' };
  if (reconciliation.kind === 'protected_branch') return { color: 'gold', label: 'guarded', detail: 'protected branch' };
  if (reconciliation.kind === 'missing_token') return { color: 'red', label: 'token', detail: 'not configured' };
  if (details.starter_push_skipped && details.repository_exists) return { color: 'gold', label: 'push skipped', detail: 'repository exists' };
  if (details.starter_push_skipped) return { color: 'gold', label: 'push skipped', detail: shortText(row.result?.repository_provision_reason || details.reason, 44) };
  if (details.provider_status) return { color: 'red', label: `HTTP ${details.provider_status}`, detail: shortText(details.provider_error, 44) };
  if (details.provider_error) return { color: 'red', label: 'error', detail: shortText(details.provider_error, 44) };
  if (row.result?.repository_provision_reason) return { color: 'gold', label: 'needs reconcile', detail: shortText(row.result.repository_provision_reason, 44) };
  return { color: 'default', label: 'pending', detail: '' };
}

type TemplateProvisionGuidance = {
  status: string;
  color: string;
  title: string;
  detail: string;
  next: string;
  reviewStatus: string;
  reviewExecution: string;
  reviewPlanMode: string;
  reviewKind: string;
  sourceBranch: string;
  targetBranch: string;
  approvalAction: string;
  executionRequestStatus: string;
  executionRequestResource: string;
  guardrailMode: string;
  guardrailReasons: string[];
  guardrailGates: AnyRow[];
  apiPlanStatus: string;
  apiPlanMode: string;
  apiPlanFileCount: number;
  apiPlanOperations: AnyRow[];
  reviewSteps: AnyRow[];
};

function templateGuidance(value: Omit<TemplateProvisionGuidance, 'reviewStatus' | 'reviewExecution' | 'reviewPlanMode' | 'reviewKind' | 'sourceBranch' | 'targetBranch' | 'approvalAction' | 'executionRequestStatus' | 'executionRequestResource' | 'guardrailMode' | 'guardrailReasons' | 'guardrailGates' | 'apiPlanStatus' | 'apiPlanMode' | 'apiPlanFileCount' | 'apiPlanOperations' | 'reviewSteps'> & Partial<Pick<TemplateProvisionGuidance, 'reviewStatus' | 'reviewExecution' | 'reviewPlanMode' | 'reviewKind' | 'sourceBranch' | 'targetBranch' | 'approvalAction' | 'executionRequestStatus' | 'executionRequestResource' | 'guardrailMode' | 'guardrailReasons' | 'guardrailGates' | 'apiPlanStatus' | 'apiPlanMode' | 'apiPlanFileCount' | 'apiPlanOperations' | 'reviewSteps'>>): TemplateProvisionGuidance {
  return {
    reviewStatus: '',
    reviewExecution: '',
    reviewPlanMode: '',
    reviewKind: '',
    sourceBranch: '',
    targetBranch: '',
    approvalAction: '',
    executionRequestStatus: '',
    executionRequestResource: '',
    guardrailMode: '',
    guardrailReasons: [],
    guardrailGates: [],
    apiPlanStatus: '',
    apiPlanMode: '',
    apiPlanFileCount: 0,
    apiPlanOperations: [],
    reviewSteps: [],
    ...value
  };
}

function templateProvisionGuidance(row: AnyRow): TemplateProvisionGuidance {
  const details = row.result?.details || {};
  const reconciliation = details.repository_reconciliation || {};
  if (row.result?.repository_provisioned) {
    return templateGuidance({
      status: 'ready',
      color: 'green',
      title: 'Repository provisioned',
      detail: 'Starter files were pushed and the repository metadata is linked to this template run.',
      next: 'Continue with RepoSync or deployment wiring.'
    });
  }
  if (row.status === 'provisioning' || row.status === 'running' || row.status === 'queued') {
    return templateGuidance({
      status: 'waiting',
      color: 'blue',
      title: 'Provisioning in progress',
      detail: 'The worker is still reconciling repository provisioning for this template run.',
      next: 'Wait for the run to finish before retrying.'
    });
  }
  if (reconciliation.kind) {
    const branchStrategy = reconciliation.branch_strategy || {};
    const providerReview = reconciliation.provider_review_readiness || {};
    const executionPlan = providerReview.execution_plan || {};
    const executionRequest = executionPlan.execution_request || {};
    const executionGuardrail = executionPlan.execution_guardrail || {};
    const apiRequestPlan = executionPlan.provider_api_request_plan || {};
    const branchStrategyReady = reconciliation.kind === 'protected_branch' && branchStrategy.strategy_status === 'planned';
    const titles: Record<string, string> = {
      existing_repository: 'Existing repository needs reconciliation',
      protected_branch: branchStrategyReady ? 'Protected branch strategy ready' : 'Protected branch guard is active',
      missing_token: 'Provider token is not configured'
    };
    return templateGuidance({
      status: reconciliation.kind === 'missing_token' ? 'token' : branchStrategyReady ? 'branch strategy' : 'manual reconcile',
      color: reconciliation.kind === 'missing_token' ? 'red' : 'gold',
      title: titles[String(reconciliation.kind)] || 'Repository needs reconciliation',
      detail: String((branchStrategyReady ? reconciliation.action_required : branchStrategy.message) || reconciliation.action_required || details.reason || row.result?.repository_provision_reason || 'Repository provisioning needs operator review.'),
      next: String(providerReview.message || reconciliation.retry_after || 'Retry after the missing provider condition is fixed.'),
      reviewStatus: String(providerReview.status || ''),
      reviewExecution: providerReview.execution_enabled === true ? 'enabled' : 'disabled',
      reviewPlanMode: String(executionPlan.mode || ''),
      reviewKind: String(executionPlan.review_kind || ''),
      sourceBranch: String(executionPlan.source_branch || ''),
      targetBranch: String(executionPlan.target_branch || ''),
      approvalAction: String(executionPlan.approval_action || ''),
      executionRequestStatus: String(executionRequest.status || ''),
      executionRequestResource: String(executionRequest.resource_type || ''),
      guardrailMode: String(executionGuardrail.execution_mode || ''),
      guardrailReasons: Array.isArray(executionGuardrail.blocked_reasons) ? executionGuardrail.blocked_reasons.map((item: unknown) => String(item)) : [],
      guardrailGates: Array.isArray(executionGuardrail.gates) ? executionGuardrail.gates : [],
      apiPlanStatus: String(apiRequestPlan.status || ''),
      apiPlanMode: String(apiRequestPlan.mode || ''),
      apiPlanFileCount: Number(apiRequestPlan.file_count || 0),
      apiPlanOperations: Array.isArray(apiRequestPlan.operations) ? apiRequestPlan.operations : [],
      reviewSteps: Array.isArray(executionPlan.steps) ? executionPlan.steps : []
    });
  }
  if (details.repository_exists && details.starter_push_skipped) {
    return templateGuidance({
      status: 'manual reconcile',
      color: 'gold',
      title: 'Existing repository needs reconciliation',
      detail: 'Starter files were skipped because the external repository already exists.',
      next: 'Review the repository contents, then set allow_existing_repository_push only when it is safe to write starter files.'
    });
  }
  if (details.starter_push_skipped) {
    return templateGuidance({
      status: 'manual reconcile',
      color: 'gold',
      title: 'Protected branch guard is active',
      detail: String(row.result?.repository_provision_reason || details.reason || 'Starter files were skipped by a template remote protection guard.'),
      next: 'Configure a provider-specific branch strategy or set allow_protected_branch_push only after branch protection rules are reviewed.'
    });
  }
  if (details.token_configured === false) {
    return templateGuidance({
      status: 'token',
      color: 'red',
      title: 'Provider token is not configured',
      detail: 'The selected provider account token environment variable is missing at runtime.',
      next: 'Rotate the provider account to a configured token env, run Check, then retry provisioning.'
    });
  }
  if (details.provider_status || details.provider_error) {
    return templateGuidance({
      status: 'provider',
      color: 'red',
      title: details.provider_status ? `Provider returned HTTP ${details.provider_status}` : 'Provider API error',
      detail: shortText(details.provider_error || row.result?.repository_provision_reason, 96),
      next: 'Use the provider account Check action and provider diagnostics before retrying.'
    });
  }
  if (row.result?.repository_provision_reason) {
    return templateGuidance({
      status: 'review',
      color: 'gold',
      title: 'Repository needs review',
      detail: shortText(row.result.repository_provision_reason, 96),
      next: 'Review the template remote metadata and retry after the missing condition is fixed.'
    });
  }
  return templateGuidance({
    status: 'pending',
    color: 'default',
    title: 'Provisioning not attempted',
    detail: 'No repository provisioning result has been recorded for this run yet.',
    next: 'Start or retry the template run when the provider account and template remotes are ready.'
  });
}

function providerTokenRotationSummary(row: AnyRow) {
  const rotation = row.token_rotation_status || {};
  const status = String(rotation.status || 'unknown');
  const colors: Record<string, string> = {
    fresh: 'green',
    soon: 'gold',
    due: 'red',
    missing: 'red',
    unknown: 'default'
  };
  const daysUntilDue = Number(rotation.days_until_due);
  const dueText = Number.isFinite(daysUntilDue) && status !== 'due' ? `${daysUntilDue}d left` : '';
  const lastText = rotation.last_rotated_at ? `since ${String(rotation.last_rotated_at).slice(0, 10)}` : '';
  return {
    color: colors[status] || 'default',
    label: status,
    detail: [dueText, lastText].filter(Boolean).join(' · ')
  };
}

function providerTokenRotationSummaryTags(summary: AnyRow = {}) {
  return [
    { key: 'due', label: `${summary.due || 0} due`, color: (summary.due || 0) > 0 ? 'red' : 'default' },
    { key: 'soon', label: `${summary.soon || 0} soon`, color: (summary.soon || 0) > 0 ? 'gold' : 'default' },
    { key: 'missing', label: `${summary.missing || 0} missing`, color: (summary.missing || 0) > 0 ? 'red' : 'default' },
    { key: 'unknown', label: `${summary.unknown || 0} unknown`, color: (summary.unknown || 0) > 0 ? 'orange' : 'default' },
    { key: 'fresh', label: `${summary.fresh || 0} fresh`, color: 'green' }
  ];
}

function providerAutoRotationPlanTags(plan: AnyRow = {}) {
  return [
    { key: 'ready', label: `${plan.ready || 0} auto-ready`, color: (plan.ready || 0) > 0 ? 'green' : 'default' },
    { key: 'blocked', label: `${plan.blocked || 0} auto-blocked`, color: (plan.blocked || 0) > 0 ? 'red' : 'default' },
    { key: 'not_needed', label: `${plan.not_needed || 0} not needed`, color: 'default' },
    { key: 'mode', label: plan.mode || 'dry_run', color: plan.automation_enabled ? 'green' : 'blue' }
  ];
}

function providerAutoRotationPlanByID(plan: AnyRow = {}) {
  const rows = Array.isArray(plan.items) ? plan.items : [];
  return rows.reduce<Record<string, AnyRow>>((acc, row) => {
    if (row.provider_account_id) acc[String(row.provider_account_id)] = row;
    return acc;
  }, {});
}

function providerAutoRotationStatus(row: AnyRow, planByID: Record<string, AnyRow>) {
  const plan = planByID[String(row.id)] || {};
  const status = String(plan.status || 'unknown');
  const colors: Record<string, string> = {
    ready: 'green',
    blocked: 'red',
    not_needed: 'default',
    unknown: 'default'
  };
  return {
    color: colors[status] || 'default',
    label: status,
    candidate: String(plan.masked_candidate_env || ''),
    next: String(plan.next_action || plan.blocked_reason || '')
  };
}

function countByField(rows: AnyRow[] = [], field: string) {
  return rows.reduce<Record<string, number>>((acc, row) => {
    const key = String(row[field] || '').trim();
    if (key) acc[key] = (acc[key] || 0) + 1;
    return acc;
  }, {});
}

function countOperationRowsWithLogs(rows: AnyRow[] = []) {
  return rows.filter((row) => Number(row.log_count || 0) > 0).length;
}

function countContextGenerationEvidence(assets: AnyRow[] = []) {
  return assets.filter((row) =>
    String(row.asset_type || '') === 'agent_tool_call' &&
    String(row.metadata?.tool_name || '') === 'context.generate' &&
    ['queued', 'completed'].includes(String(row.status || '').toLowerCase())
  ).length;
}

function apiAssetGraphID(row: AnyRow = {}) {
  for (const key of ['asset_id', 'id']) {
    if (typeof row[key] !== 'string') continue;
    const value = row[key].trim();
    if (value) return value;
  }
  const type = String(row.asset_type ?? '').trim();
  const sourceID = String(row.source_id ?? '').trim();
  return type && sourceID ? `${type}:${sourceID}` : '';
}

function countContextGraphLinks(assets: AnyRow[] = [], graph: AnyRow = {}) {
  const contextToolCalls = new Set(
    assets
      .filter((row) =>
        String(row.asset_type ?? '') === 'agent_tool_call' &&
        String(row.metadata?.tool_name ?? '') === 'context.generate' &&
        ['queued', 'completed'].includes(String(row.status ?? '').toLowerCase())
      )
      .map(apiAssetGraphID)
      .filter((assetID) => assetID.startsWith('agent_tool_call:'))
  );
  const byTask: Record<string, { runtime?: boolean; contextTool?: boolean }> = {};
  const taskEntry = (assetID: string) => {
    byTask[assetID] ??= {};
    return byTask[assetID];
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type ?? '');
    const from = String(edge.from_asset_id ?? '');
    const to = String(edge.to_asset_id ?? '');
    if (relation === 'uses_runtime' && from.startsWith('agent_task:') && to.startsWith('ai_runtime:')) {
      nextCounts.taskRuntimes += 1;
      taskEntry(from).runtime = true;
    }
    if (relation === 'records_tool_call' && from.startsWith('agent_task:') && contextToolCalls.has(to)) {
      nextCounts.taskContextToolCalls += 1;
      taskEntry(from).contextTool = true;
    }
    return nextCounts;
  }, { taskRuntimes: 0, taskContextToolCalls: 0, completeContextTasks: 0 });
  counts.completeContextTasks = Object.values(byTask).filter((entry) => entry.runtime && entry.contextTool).length;
  return counts;
}

function countRowsByTypeStatus(rows: AnyRow[] = [], type: string, status: string) {
  return rows.filter((row) => String(row.asset_type || '') === type && String(row.status || '') === status).length;
}

function countRowsByTypeMetadata(rows: AnyRow[] = [], type: string, key: string, value: string) {
  return rows.filter((row) => String(row.asset_type || '') === type && String(row.metadata?.[key] || '') === value).length;
}

function graphItems(graph: AnyRow = {}, key: string) {
  return Array.isArray(graph[key]) ? graph[key] : [];
}

function countGraphNodesByPrefix(graph: AnyRow = {}, prefix: string) {
  return graphItems(graph, 'nodes').filter((node: AnyRow) => String(node.id ?? '').startsWith(prefix)).length;
}

function countRepositoryGraphLinks(graph: AnyRow = {}) {
  const byRepository: Record<string, { project?: boolean; remotes: Record<string, boolean> }> = {};
  const repositoryEntry = (assetID: string) => {
    byRepository[assetID] ??= { remotes: {} };
    return byRepository[assetID];
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type ?? '');
    const from = String(edge.from_asset_id ?? '');
    const to = String(edge.to_asset_id ?? '');
    if (relation === 'owns' && from.startsWith('project:') && to.startsWith('repository:')) {
      nextCounts.projectRepository += 1;
      repositoryEntry(to).project = true;
    }
    if (relation === 'has_remote' && from.startsWith('repository:') && to.startsWith('git_remote:')) {
      nextCounts.repositoryRemotes += 1;
      repositoryEntry(from).remotes[to] = true;
    }
    return nextCounts;
  }, { projectRepository: 0, repositoryRemotes: 0, completeRepos: 0 });
  counts.completeRepos = Object.values(byRepository).filter((entry) => entry.project && Object.keys(entry.remotes).length >= 2).length;
  return counts;
}

function countRepoSyncGraphLinks(graph: AnyRow = {}) {
  const bySync: Record<string, { repository?: boolean; source?: boolean; target?: boolean }> = {};
  const syncEntry = (assetID: string) => {
    bySync[assetID] ||= {};
    return bySync[assetID];
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type || '');
    const from = String(edge.from_asset_id || '');
    const to = String(edge.to_asset_id || '');
    if (relation === 'has_sync' && from.startsWith('repository:') && to.startsWith('repo_sync:')) {
      nextCounts.repositorySync += 1;
      syncEntry(to).repository = true;
    }
    if (relation === 'synced_from' && from.startsWith('repo_sync:') && to.startsWith('git_remote:')) {
      nextCounts.sourceRemotes += 1;
      syncEntry(from).source = true;
    }
    if (relation === 'mirrors_to' && from.startsWith('repo_sync:') && to.startsWith('git_remote:')) {
      nextCounts.targetRemotes += 1;
      syncEntry(from).target = true;
    }
    return nextCounts;
  }, { repositorySync: 0, sourceRemotes: 0, targetRemotes: 0, completeSyncs: 0 });
  counts.completeSyncs = Object.values(bySync).filter((entry) => entry.repository && entry.source && entry.target).length;
  return counts;
}

function countGitHubActionGraphLinks(graph: AnyRow = {}) {
  const repositoriesWithProject: Record<string, boolean> = {};
  const remoteRepositories: Record<string, Record<string, boolean>> = {};
  const remoteActionRuns: Record<string, Record<string, boolean>> = {};
  const taggedRemoteOps: Record<string, Record<string, boolean>> = {};
  const addRemoteRepository = (remoteID: string, repositoryID: string) => {
    remoteRepositories[remoteID] ??= {};
    remoteRepositories[remoteID][repositoryID] = true;
  };
  const addRemoteActionRun = (remoteID: string, actionID: string) => {
    remoteActionRuns[remoteID] ??= {};
    remoteActionRuns[remoteID][actionID] = true;
  };
  const addTaggedRemoteOp = (remoteID: string, operationID: string) => {
    taggedRemoteOps[remoteID] ??= {};
    taggedRemoteOps[remoteID][operationID] = true;
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type || '');
    const from = String(edge.from_asset_id || '');
    const to = String(edge.to_asset_id || '');
    if (relation === 'owns' && from.startsWith('project:') && to.startsWith('repository:')) {
      nextCounts.projectRepositories += 1;
      repositoriesWithProject[to] = true;
    }
    if (relation === 'has_remote' && from.startsWith('repository:') && to.startsWith('git_remote:')) {
      nextCounts.repositoryRemotes += 1;
      addRemoteRepository(to, from);
    }
    if (relation === 'triggered_by' && from.startsWith('git_remote:') && to.startsWith('github_action_run:')) {
      nextCounts.remoteActionRuns += 1;
      addRemoteActionRun(from, to);
    }
    const tagStatus = String(edge.metadata?.status || '').trim().toLowerCase();
    if (relation === 'tagged_remote' && from.startsWith('operation_run:') && to.startsWith('git_remote:') && ['completed', 'succeeded', 'success'].includes(tagStatus)) {
      nextCounts.taggedRemotes += 1;
      addTaggedRemoteOp(to, from);
    }
    return nextCounts;
  }, { projectRepositories: 0, repositoryRemotes: 0, remoteActionRuns: 0, taggedRemotes: 0, completeActionRuns: 0, completeTaggedRemotes: 0 });
  counts.completeActionRuns = Object.entries(remoteActionRuns).reduce((total, [remoteID, actionRuns]) => {
    const hasProjectRepository = Object.keys(remoteRepositories[remoteID] || {}).some((repositoryID) => repositoriesWithProject[repositoryID]);
    return hasProjectRepository ? total + Object.keys(actionRuns).length : total;
  }, 0);
  counts.completeTaggedRemotes = Object.entries(taggedRemoteOps).reduce((total, [remoteID, operations]) => {
    const hasProjectRepository = Object.keys(remoteRepositories[remoteID] || {}).some((repositoryID) => repositoriesWithProject[repositoryID]);
    return hasProjectRepository ? total + Object.keys(operations).length : total;
  }, 0);
  return counts;
}

function countWebhookSyncGraphLinks(graph: AnyRow = {}) {
  const byEvent: Record<string, { connection?: boolean; repoSync?: boolean; operation?: boolean }> = {};
  const eventEntry = (assetID: string) => {
    byEvent[assetID] ??= {};
    return byEvent[assetID];
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type || '');
    const from = String(edge.from_asset_id || '');
    const to = String(edge.to_asset_id || '');
    if (relation === 'received_webhook_event' && from.startsWith('webhook_connection:') && to.startsWith('webhook_event:')) {
      nextCounts.connectionEvents += 1;
      eventEntry(to).connection = true;
    }
    if (relation === 'matched_repo_sync' && from.startsWith('webhook_event:') && to.startsWith('repo_sync:')) {
      nextCounts.eventRepoSyncs += 1;
      eventEntry(from).repoSync = true;
    }
    // Ignore legacy webhook_connection -> operation_run compatibility edges.
    if (relation === 'triggered_operation' && from.startsWith('webhook_event:') && to.startsWith('operation_run:')) {
      nextCounts.eventOperations += 1;
      eventEntry(from).operation = true;
    }
    return nextCounts;
  }, { connectionEvents: 0, eventRepoSyncs: 0, eventOperations: 0, completeChains: 0 });
  counts.completeChains = Object.values(byEvent).filter((entry) => entry.connection && entry.repoSync && entry.operation).length;
  return counts;
}

function countSSHGraphLinks(graph: AnyRow = {}) {
  const byCommand: Record<string, { operation?: boolean; machine?: boolean }> = {};
  const commandEntry = (assetID: string) => {
    byCommand[assetID] ||= {};
    return byCommand[assetID];
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type || '');
    const from = String(edge.from_asset_id || '');
    const to = String(edge.to_asset_id || '');
    if (relation === 'ran_ssh_command' && from.startsWith('operation_run:') && to.startsWith('ssh_command_run:')) {
      nextCounts.operationCommands += 1;
      commandEntry(to).operation = true;
    }
    if (relation === 'executed_on' && from.startsWith('ssh_command_run:') && to.startsWith('ssh_machine:')) {
      nextCounts.commandMachines += 1;
      commandEntry(from).machine = true;
    }
    return nextCounts;
  }, { operationCommands: 0, commandMachines: 0, completeCommands: 0 });
  counts.completeCommands = Object.values(byCommand).filter((entry) => entry.operation && entry.machine).length;
  return counts;
}

function countArgoGraphLinks(graph: AnyRow = {}) {
  const byApp: Record<string, { connection?: boolean; target?: boolean }> = {};
  const appEntry = (assetID: string) => {
    byApp[assetID] ||= {};
    return byApp[assetID];
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type || '');
    const from = String(edge.from_asset_id || '');
    const to = String(edge.to_asset_id || '');
    if (relation === 'manages' && from.startsWith('argo_connection:') && to.startsWith('argo_app:')) {
      nextCounts.connectionApps += 1;
      appEntry(to).connection = true;
    }
    if (relation === 'deployed_to' && from.startsWith('argo_app:') && to.startsWith('deployment_target:')) {
      nextCounts.appTargets += 1;
      appEntry(from).target = true;
    }
    return nextCounts;
  }, { connectionApps: 0, appTargets: 0, completeApps: 0 });
  counts.completeApps = Object.values(byApp).filter((entry) => entry.connection && entry.target).length;
  return counts;
}

function countApprovalGraphLinks(graph: AnyRow = {}) {
  return graphItems(graph, 'edges').reduce((counts, edge: AnyRow) => {
    const relation = String(edge.relation_type || '');
    const from = String(edge.from_asset_id || '');
    const to = String(edge.to_asset_id || '');
    if (relation === 'governs' && from.startsWith('operation_approval_rule:') && to.startsWith('operation_approval:')) {
      counts.ruleApprovals += 1;
    }
    // Pending approvals can be ready before a gated operation exists; keep this as display/future execution evidence.
    if (relation === 'gates_operation' && from.startsWith('operation_approval:') && to.startsWith('operation_run:')) {
      counts.approvalOperations += 1;
    }
    return counts;
  }, { ruleApprovals: 0, approvalOperations: 0 });
}

function graphPayloadAvailable(graph: AnyRow | null) {
  if (!graph) return false;
  return Object.prototype.hasOwnProperty.call(graph, 'nodes') || Object.prototype.hasOwnProperty.call(graph, 'edges');
}

function readinessState(done: boolean, evidence: React.ReactNode, hasPartialEvidence?: boolean) {
  if (done) return { status: 'ready', color: 'green', evidence };
  if (hasPartialEvidence ?? Boolean(evidence)) return { status: 'partial', color: 'gold', evidence };
  return { status: 'missing', color: 'red', evidence };
}

function demoPlanStateColor(state: string) {
  return state === 'observed' ? 'green' : state === 'blocked' ? 'red' : 'gold';
}

function demoDataRehearsalPlan(status: string, evidenceCounts: AnyRow, requiredEvidence: string[]) {
  const planState = status === 'ready' ? 'observed' : status === 'missing' ? 'blocked' : 'planned';
  const environmentPlan = demoDataEnvironmentEvidencePlan(status, evidenceCounts, requiredEvidence);
  const graphPlan = demoDataGraphProofPlan(status, evidenceCounts, requiredEvidence);
  const environmentProof = demoDataEnvironmentProof(status, evidenceCounts, requiredEvidence);
  const resultPlan = demoDataResultRecordingPlan(status, evidenceCounts, requiredEvidence);
  return {
    mode: 'first_version_demo_data_rehearsal_plan',
    plan_state: planState,
    readiness_status: status,
    execution_enabled: false,
    external_call_made: false,
    demo_seed_written: false,
    project_created: false,
    repository_created: false,
    git_remote_created: false,
    asset_graph_written: false,
    contains_remote_url: false,
    contains_credentials: false,
    required_evidence: requiredEvidence,
    evidence_counts: evidenceCounts,
    environment_evidence_plan: environmentPlan,
    environment_demo_proof: environmentProof,
    graph_proof_plan: graphPlan,
    result_recording_plan: resultPlan,
    disabled_backends: ['project_create', 'repository_create', 'git_remote_create', 'demo_seed_write', 'asset_graph_write'],
    suppressed_fields: ['remote_url', 'git_credentials', 'provider_token', 'repository_secret', 'webhook_secret'],
    blocked_reasons: status === 'ready' ? [] : ['live_demo_graph_evidence_incomplete'],
    message: 'Demo data rehearsal is audit-only; create project/repository/remote evidence in the live environment, then sync the canonical asset graph.'
  };
}

// Keep this proof contract in sync with backend/cmd/assops-tool demoDataEnvironmentProof.
function demoDataEnvironmentProof(status: string, evidenceCounts: AnyRow = {}, requiredEvidence: string[] = []) {
  const checks = demoDataEvidenceChecks(evidenceCounts);
  const missing = missingDemoDataEvidence(checks, requiredEvidence);
  const proofState = status === 'missing' ? 'blocked' : missing.length ? 'partial' : 'observed';
  const liveEnvironmentDataObserved = status === 'missing' ? false : missing.length === 0;
  const multiRemoteObserved = Number(evidenceCounts.repository_assets || 0) > 0 &&
    Number(evidenceCounts.git_remote_assets || 0) >= 2 &&
    Number(evidenceCounts.project_repository_links || 0) > 0 &&
    Number(evidenceCounts.complete_repository_paths || 0) > 0 &&
    Number(evidenceCounts.repository_remote_links || 0) >= 2 &&
    proofState !== 'blocked';
  return {
    mode: 'first_version_demo_environment_proof',
    proof_state: proofState,
    proof_ready: missing.length === 0 && status === 'ready',
    proof_source: 'canonical_asset_graph_counts',
    live_environment_data_observed: liveEnvironmentDataObserved,
    complete_repository_multi_remote_path_observed: multiRemoteObserved,
    required_evidence: requiredEvidence,
    missing_evidence: missing,
    evidence_counts: evidenceCounts,
    external_call_made: false,
    demo_seed_written: false,
    project_created: false,
    repository_created: false,
    git_remote_created: false,
    asset_graph_written: false,
    contains_remote_url: false,
    contains_credentials: false,
    suppressed_fields: ['project_asset_id', 'repository_asset_id', 'source_remote_asset_id', 'mirror_remote_asset_id', 'remote_url', 'git_credentials', 'provider_token', 'repository_secret', 'webhook_secret']
  };
}

function demoDataEvidenceChecks(evidenceCounts: AnyRow = {}) {
  return {
    project_asset: Number(evidenceCounts.project_assets || 0) > 0,
    project_graph_node: Number(evidenceCounts.project_graph_nodes || 0) > 0,
    repository_asset: Number(evidenceCounts.repository_assets || 0) > 0,
    two_git_remote_assets: Number(evidenceCounts.git_remote_assets || 0) >= 2,
    project_to_repository_graph_link: Number(evidenceCounts.project_repository_links || 0) > 0,
    repository_to_two_remotes_graph_path: Number(evidenceCounts.complete_repository_paths || 0) > 0
  };
}

function missingDemoDataEvidence(checks: AnyRow, requiredEvidence: string[] = []) {
  return requiredEvidence.filter((key) => !checks[key]);
}

function demoDataEnvironmentEvidencePlan(status: string, evidenceCounts: AnyRow, requiredEvidence: string[]) {
  return {
    mode: 'first_version_demo_environment_evidence_plan',
    evidence_state: status === 'ready' ? 'observed' : status === 'missing' ? 'blocked' : 'planned',
    evidence_ready: false,
    evidence_ready_reason: 'demo_environment_execution_disabled',
    metadata_ready: status === 'ready',
    execution_enabled: false,
    demo_seed_written: false,
    project_created: false,
    repository_created: false,
    git_remote_created: false,
    external_call_made: false,
    contains_remote_url: false,
    contains_credentials: false,
    required_evidence: requiredEvidence,
    evidence_counts: evidenceCounts,
    required_environment_fields: ['project_asset', 'project_graph_node', 'repository_asset', 'two_git_remote_assets', 'project_repository_graph_link', 'repository_to_two_remotes_graph_path'],
    suppressed_fields: ['remote_url', 'git_credentials', 'provider_token', 'repository_secret', 'webhook_secret'],
    blocked_reasons: status === 'ready' ? ['demo_seed_execution_disabled', 'live_environment_not_recorded'] : ['demo_seed_execution_disabled', 'live_environment_not_recorded', 'required_graph_evidence_missing'],
    message: 'Demo environment evidence is observed only; this plan does not create demo project, repository, or remote rows.'
  };
}

function demoDataGraphProofPlan(status: string, evidenceCounts: AnyRow, requiredEvidence: string[]) {
  return {
    mode: 'first_version_demo_graph_proof_plan',
    proof_state: status === 'ready' ? 'observed' : status === 'missing' ? 'blocked' : 'planned',
    proof_ready: false,
    proof_ready_reason: 'demo_graph_proof_execution_disabled',
    metadata_ready: status === 'ready',
    asset_graph_written: false,
    asset_sync_triggered: false,
    graph_query_performed: false,
    external_call_made: false,
    required_evidence: requiredEvidence,
    evidence_counts: evidenceCounts,
    required_graph_paths: ['project:*', 'project:* -> repository:*', 'repository:* -> git_remote:*', 'repository:* -> second git_remote:*'],
    suppressed_fields: ['remote_url', 'git_credentials', 'provider_token', 'repository_secret', 'webhook_secret'],
    blocked_reasons: status === 'ready' ? ['asset_graph_write_disabled'] : ['asset_graph_write_disabled', 'graph_proof_incomplete'],
    message: 'Demo graph proof is read-only; future execution must sync canonical assets and prove one repository has at least two remotes.'
  };
}

function demoDataResultRecordingPlan(status: string, evidenceCounts: AnyRow = {}, requiredEvidence: string[] = []) {
  // Result recording stays blocked even when graph evidence is observed; it only
  // becomes meaningful after a future live demo-data execution writes a result.
  const checks = demoDataEvidenceChecks(evidenceCounts);
  const missing = missingDemoDataEvidence(checks, requiredEvidence);
  const readinessSnapshotReady = status === 'ready' && missing.length === 0;
  const graphSnapshotReady = readinessSnapshotReady;
  const disabledBackends = readinessSnapshotReady
    ? ['demo_result_write', 'asset_graph_snapshot_write', 'operation_log_write']
    : ['demo_result_write', 'readiness_snapshot_write', 'asset_graph_snapshot_write', 'operation_log_write'];
  const preflight = {
    mode: 'first_version_demo_data_result_recording_preflight',
    readiness_status: status,
    readiness_snapshot_ready_for_review: readinessSnapshotReady,
    asset_graph_snapshot_ready_for_review: graphSnapshotReady,
    snapshot_contract_ready: readinessSnapshotReady && graphSnapshotReady,
    snapshot_write_enabled: readinessSnapshotReady,
    asset_graph_write_enabled: false,
    operation_log_write_enabled: false,
    external_call_made: false,
    contains_remote_url: false,
    contains_credentials: false,
    required_evidence: requiredEvidence,
    missing_required_evidence: missing,
    evidence_counts: evidenceCounts,
    required_snapshot_fields: ['project_asset_id', 'repository_asset_id', 'source_remote_asset_id', 'mirror_remote_asset_id', 'graph_proof_status', 'readiness_status', 'evidence_counts', 'missing_required_evidence'],
    suppressed_fields: ['remote_url', 'git_credentials', 'provider_token', 'repository_secret', 'webhook_secret', 'raw_graph_payload', 'operation_log_body'],
    disabled_backends: disabledBackends,
    blocked_reasons: readinessSnapshotReady ? ['demo_result_write_disabled', 'asset_graph_snapshot_write_disabled'] : ['demo_result_write_disabled', 'readiness_snapshot_write_disabled', 'asset_graph_snapshot_write_disabled', 'required_demo_evidence_missing'],
    message: readinessSnapshotReady ? 'Local readiness snapshot recording is available; demo result and operation-log writes remain disabled.' : 'Demo result recording preflight is review metadata only until required graph evidence is observed.'
  };
  return {
    mode: 'first_version_demo_data_result_recording_plan',
    result_recording_state: readinessSnapshotReady ? 'snapshot_ready' : 'blocked',
    result_recording_ready: readinessSnapshotReady,
    result_recording_ready_reason: readinessSnapshotReady ? 'local_readiness_snapshot_recording_ready' : 'demo_data_execution_not_performed',
    recording_enabled: readinessSnapshotReady,
    result_written: false,
    operation_log_written: false,
    readiness_snapshot_written: false,
    asset_graph_snapshot_written: false,
    raw_remote_url_recorded: false,
    raw_credentials_recorded: false,
    required_result_fields: ['project_asset_id', 'repository_asset_id', 'source_remote_asset_id', 'mirror_remote_asset_id', 'graph_proof_status', 'readiness_status'],
    result_recording_preflight: preflight,
    suppressed_fields: ['remote_url', 'git_credentials', 'provider_token', 'repository_secret', 'webhook_secret', 'raw_graph_payload', 'operation_log_body'],
    blocked_reasons: readinessSnapshotReady ? ['demo_data_execution_not_performed', 'asset_graph_snapshot_not_recorded'] : ['demo_data_execution_not_performed', 'readiness_snapshot_not_recorded', 'asset_graph_snapshot_not_recorded'],
    message: readinessSnapshotReady ? 'Local readiness snapshot recording can persist sanitized graph evidence; live demo result recording remains disabled.' : 'Demo data result recording is disabled until a live environment run creates and proves the graph-backed demo evidence.'
  };
}

function firstVersionReadinessRows(assets: AnyRow[] = [], operations: AnyRow[] = [], approvalSummary: AnyRow = {}, graph: AnyRow = {}) {
  const assetCounts = countByField(assets, 'asset_type');
  const operationCounts = countByField(operations, 'operation_type');
  const syncTriggered = (operationCounts['repo.sync'] || 0) + (operationCounts['repo.sync_remote'] || 0);
  const giteaWebhooks = countRowsByTypeMetadata(assets, 'webhook_connection', 'provider', 'gitea');
  const giteaWebhookEvents = countRowsByTypeMetadata(assets, 'webhook_event', 'provider', 'gitea');
  const sshVerifyRuns = operationCounts['ssh.verify'] || 0;
  const sshCommandRuns = (operationCounts['ssh.exec'] || 0) + (operationCounts['ssh.command'] || 0);
  const approvalEvidence = Number(approvalSummary.total || 0);
  const pendingApprovalOps = operations.filter((row) => String(row.status || '') === 'pending_approval').length;
  const approvalAssets = assetCounts.operation_approval || 0;
  const activeApprovalRules = countRowsByTypeStatus(assets, 'operation_approval_rule', 'active');
  const operationAssets = assetCounts.operation_run || 0;
  const listedOperationRuns = operations.length;
  const operationLogs = countOperationRowsWithLogs(operations);
  const contextEvidence = (assetCounts.agent_task || 0) + (assetCounts.ai_runtime || 0);
  const contextGenerations = countContextGenerationEvidence(assets);
  const repositoryGraphLinks = countRepositoryGraphLinks(graph);
  const repoSyncGraphLinks = countRepoSyncGraphLinks(graph);
  const webhookSyncGraphLinks = countWebhookSyncGraphLinks(graph);
  const githubActionLinks = countGitHubActionGraphLinks(graph);
  const repoTagRuns = (operationCounts['repo.tag'] || 0) + (operationCounts['repo.create_tag'] || 0);
  const sshGraphLinks = countSSHGraphLinks(graph);
  const argoGraphLinks = countArgoGraphLinks(graph);
  const approvalGraphLinks = countApprovalGraphLinks(graph);
  const contextGraphLinks = countContextGraphLinks(assets, graph);
  const argoEvidence = (assetCounts.argo_connection || 0) + (assetCounts.argo_app || 0) + (assetCounts.deployment_target || 0) + (operationCounts['argo.apps.sync'] || 0) + argoGraphLinks.connectionApps + argoGraphLinks.appTargets;
  const graphNodes = graphItems(graph, 'nodes').length;
  const graphEdges = graphItems(graph, 'edges').length;
  const graphEvidence = graphNodes + graphEdges;
  const projectGraphNodes = countGraphNodesByPrefix(graph, 'project:');
  const projectState = readinessState((assetCounts.project || 0) > 0 && projectGraphNodes > 0, `${assetCounts.project || 0} project assets / ${projectGraphNodes} project graph nodes`, (assetCounts.project || 0) > 0 || projectGraphNodes > 0);
  const repositoryState = readinessState((assetCounts.repository || 0) > 0 && (assetCounts.git_remote || 0) >= 2 && repositoryGraphLinks.completeRepos > 0, `${assetCounts.repository || 0} repos / ${assetCounts.git_remote || 0} remotes / ${repositoryGraphLinks.completeRepos} complete repos / ${repositoryGraphLinks.projectRepository} project links / ${repositoryGraphLinks.repositoryRemotes} remote links`, (assetCounts.repository || 0) > 0 || (assetCounts.git_remote || 0) > 0 || repositoryGraphLinks.projectRepository > 0 || repositoryGraphLinks.repositoryRemotes > 0);
  return [
    {
      key: 'project',
      label: 'Create/import project asset',
      next: 'Create a project or run the demo seed.',
      ...projectState,
      demo_data_rehearsal_plan: demoDataRehearsalPlan(projectState.status, { project_assets: assetCounts.project || 0, project_graph_nodes: projectGraphNodes }, ['project_asset', 'project_graph_node'])
    },
    {
      key: 'repositories',
      label: 'Attach source and mirror repositories',
      next: 'Add repository metadata and at least two Git remotes.',
      ...repositoryState,
      demo_data_rehearsal_plan: demoDataRehearsalPlan(repositoryState.status, { repository_assets: assetCounts.repository || 0, git_remote_assets: assetCounts.git_remote || 0, complete_repository_paths: repositoryGraphLinks.completeRepos, project_repository_links: repositoryGraphLinks.projectRepository, repository_remote_links: repositoryGraphLinks.repositoryRemotes }, ['repository_asset', 'two_git_remote_assets', 'project_to_repository_graph_link', 'repository_to_two_remotes_graph_path'])
    },
    {
      key: 'repo_sync',
      label: 'Define RepoSyncAsset',
      next: 'Create a RepoSyncAsset between source and mirror remotes.',
      ...readinessState((assetCounts.repo_sync || 0) > 0 && repoSyncGraphLinks.completeSyncs > 0, `${assetCounts.repo_sync || 0} repo syncs / ${repoSyncGraphLinks.completeSyncs} complete syncs / ${repoSyncGraphLinks.repositorySync} repository links / ${repoSyncGraphLinks.sourceRemotes} source links / ${repoSyncGraphLinks.targetRemotes} target links`, (assetCounts.repo_sync || 0) > 0 || repoSyncGraphLinks.repositorySync > 0 || repoSyncGraphLinks.sourceRemotes > 0 || repoSyncGraphLinks.targetRemotes > 0)
    },
    {
      key: 'sync_trigger',
      label: 'Trigger sync manually and from webhook',
      next: 'Run a manual sync and receive or replay a Gitea webhook event.',
      ...readinessState(syncTriggered > 0 && giteaWebhooks > 0 && giteaWebhookEvents > 0 && webhookSyncGraphLinks.completeChains > 0, `${syncTriggered} sync ops / ${giteaWebhooks} Gitea webhooks / ${giteaWebhookEvents} Gitea events / ${webhookSyncGraphLinks.completeChains} complete webhook chains`, syncTriggered > 0 || giteaWebhooks > 0 || giteaWebhookEvents > 0 || webhookSyncGraphLinks.connectionEvents > 0 || webhookSyncGraphLinks.eventRepoSyncs > 0 || webhookSyncGraphLinks.eventOperations > 0)
    },
    {
      key: 'github_actions',
      label: 'See GitHub tags and Actions state',
      next: 'Create a repository tag and sync GitHub Actions for the mirror remote or receive workflow_run webhooks.',
      ...readinessState((assetCounts.pipeline_run || 0) > 0 && githubActionLinks.completeActionRuns > 0 && repoTagRuns > 0 && githubActionLinks.completeTaggedRemotes > 0, `${assetCounts.pipeline_run || 0} pipeline runs / ${githubActionLinks.completeActionRuns} complete action chains / ${repoTagRuns} tag ops / ${githubActionLinks.completeTaggedRemotes} complete tag links / ${githubActionLinks.projectRepositories} project links / ${githubActionLinks.repositoryRemotes} remote links / ${githubActionLinks.remoteActionRuns} action links / ${githubActionLinks.taggedRemotes} tag links`, (assetCounts.pipeline_run || 0) > 0 || repoTagRuns > 0 || githubActionLinks.projectRepositories > 0 || githubActionLinks.repositoryRemotes > 0 || githubActionLinks.remoteActionRuns > 0 || githubActionLinks.taggedRemotes > 0)
    },
    {
      key: 'ssh',
      label: 'Register SSH machines and audited commands',
      next: 'Verify an SSH machine, then run an approval-gated command.',
      ...readinessState((assetCounts.host || 0) > 0 && sshVerifyRuns > 0 && sshCommandRuns > 0 && sshGraphLinks.completeCommands >= 2, `${assetCounts.host || 0} hosts / ${sshVerifyRuns} verify ops / ${sshCommandRuns} command ops / ${assetCounts.ssh_command_run || 0} command assets / ${sshGraphLinks.completeCommands} complete audit chains`, (assetCounts.host || 0) > 0 || sshVerifyRuns > 0 || sshCommandRuns > 0 || (assetCounts.ssh_command_run || 0) > 0 || sshGraphLinks.operationCommands > 0 || sshGraphLinks.commandMachines > 0)
    },
    {
      key: 'argo',
      label: 'Sync Argo apps to deployment targets',
      next: 'Create an Argo connection, sync apps, and inspect deployment targets.',
      ...readinessState((assetCounts.argo_connection || 0) > 0 && (assetCounts.argo_app || 0) > 0 && (assetCounts.deployment_target || 0) > 0 && (operationCounts['argo.apps.sync'] || 0) > 0 && argoGraphLinks.completeApps > 0, `${assetCounts.deployment_target || 0} targets / ${assetCounts.argo_connection || 0} Argo connections / ${assetCounts.argo_app || 0} apps / ${operationCounts['argo.apps.sync'] || 0} sync ops / ${argoGraphLinks.completeApps} complete app links`, argoEvidence > 0)
    },
    {
      key: 'operations',
      label: 'View operation history and logs',
      next: 'Run any controlled operation and inspect its logs.',
      ...readinessState(operationAssets > 0 && operationLogs > 0, `${operationAssets} operation assets / ${listedOperationRuns} listed runs / ${operationLogs} with logs`, operationAssets > 0 || listedOperationRuns > 0 || operationLogs > 0)
    },
    {
      key: 'approval',
      label: 'Enforce approval for high-risk operations',
      next: 'Queue a high-risk action that creates an approval request.',
      ...readinessState(approvalAssets > 0 && activeApprovalRules > 0 && approvalGraphLinks.ruleApprovals > 0, `${approvalEvidence} approvals / ${approvalAssets} approval assets / ${pendingApprovalOps} pending ops / ${activeApprovalRules} active rules / ${approvalGraphLinks.ruleApprovals} governed approvals`, approvalEvidence > 0 || approvalAssets > 0 || pendingApprovalOps > 0 || activeApprovalRules > 0 || approvalGraphLinks.ruleApprovals > 0)
    },
    {
      key: 'context',
      label: 'Generate AI-readable context from graph',
      next: 'Create an agent task or AI runtime after syncing the canonical asset ledger.',
      ...readinessState(contextEvidence > 0 && contextGenerations > 0 && graphEvidence > 0 && contextGraphLinks.completeContextTasks > 0, `${contextEvidence} context assets / ${contextGenerations} context generations / ${contextGraphLinks.completeContextTasks} complete context tasks / ${contextGraphLinks.taskRuntimes} runtime links / ${contextGraphLinks.taskContextToolCalls} context tool links / ${graphNodes} graph nodes / ${graphEdges} graph edges`, contextEvidence > 0 || contextGenerations > 0 || graphEvidence > 0 || contextGraphLinks.taskRuntimes > 0 || contextGraphLinks.taskContextToolCalls > 0)
    }
  ];
}

function cleanedList(values: string[] = []) {
  return values.map((value) => value.trim()).filter(Boolean);
}

function JSONBlock({ value }: { value: any }) {
  return <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{JSON.stringify(value || {}, null, 2)}</pre>;
}

function sanitizedConfigPinResult(result: AnyRow = {}) {
  const keys = [
    'mode',
    'project_version_id',
    'repository_id',
    'remote_id',
    'dry_run',
    'pin_state',
    'metadata_changed',
    'project_version_metadata_written',
    'config_commit_sha_written',
    'config_commit_sha_present',
    'external_call_made',
    'git_fetch_performed',
    'git_push_performed',
    'provider_api_called',
    'operation_log_written',
    'commit_sha_included',
    'remote_url_included',
    'secret_included',
    'message'
  ];
  return keys.reduce((out: AnyRow, key) => {
    if (Object.prototype.hasOwnProperty.call(result, key)) out[key] = result[key];
    return out;
  }, {});
}

function sanitizedValidationSnapshotResult(result: AnyRow = {}) {
  const keys = [
    'mode',
    'recording_state',
    'recording_ready',
    'recording_enabled',
    'recording_trigger',
    'auto_record_terminal_required',
    'auto_record_terminal_satisfied',
    'dry_run',
    'project_version_id',
    'project_version_asset_observed',
    'snapshots_written',
    'snapshots_skipped_as_duplicate',
    'validation_snapshot_written',
    'asset_status_snapshot_written',
    'operation_log_written',
    'external_call_made',
    'rows_affected_warning',
    'rows_affected_unknown',
    'missing_evidence',
    'message'
  ];
  return keys.reduce((out: AnyRow, key) => {
    if (Object.prototype.hasOwnProperty.call(result, key)) out[key] = result[key];
    return out;
  }, {});
}

function parseJSONField(value?: string) {
  const text = (value || '').trim();
  if (!text) return {};
  return JSON.parse(text);
}

function projectVersionRepositoryItems(values: AnyRow, repos: AnyRow[] = [], remotes: AnyRow[] = []) {
  const rows = Array.isArray(values.repositories) ? values.repositories : [];
  return rows.map((row: AnyRow) => {
    const repo = repos.find((item) => item.id === row.repository_id);
    const remote = remotes.find((item) => item.id === row.remote_id);
    if (!repo || !remote || remote.repository_id !== repo.id) return null;
    const item: AnyRow = {
      repo_id: repo.id,
      repo_key: repo.repo_key || repo.name,
      repo_role: repo.repo_role || 'code',
      remote_id: remote.id,
      remote_key: remote.remote_key || remote.name,
      remote_role: remote.remote_role || 'mirror',
      provider_type: remote.provider_type || remote.kind || 'git'
    };
    for (const key of ['tag', 'commit_sha', 'config_commit_sha', 'github_action_run_id', 'argo_revision']) {
      const value = String(row[key] || '').trim();
      if (value) item[key] = value;
    }
    return item;
  }).filter(Boolean);
}

function projectVersionMetadata(values: AnyRow, repos: AnyRow[] = [], remotes: AnyRow[] = []) {
  let metadata: AnyRow;
  try {
    metadata = parseJSONField(values.metadata_json);
  } catch {
    throw new Error('Extra metadata JSON must be valid JSON');
  }
  const repositories = projectVersionRepositoryItems(values, repos, remotes);
  if (repositories.length > 0) {
    metadata.repositories = repositories;
  }
  return metadata;
}

function templateParametersWithProviderAccounts(values: AnyRow, providerAccounts: AnyRow[] = []) {
  const parameters = values.parameters || parseJSONField(values.parameters_json);
  const remotes: AnyRow[] = Array.isArray(parameters.remotes) ? [...parameters.remotes] : [];
  const ensureRemote = (remoteKey: string, providerType: string, accountID?: string) => {
    if (!accountID) return;
    const account = providerAccounts.find((row) => row.id === accountID);
    const index = remotes.findIndex((row) => row.remote_key === remoteKey || row.provider_type === providerType);
    const next = {
      ...(index >= 0 ? remotes[index] : {}),
      remote_key: index >= 0 ? remotes[index].remote_key || remoteKey : remoteKey,
      provider_type: index >= 0 ? remotes[index].provider_type || providerType : providerType,
      provider_account_id: accountID,
      provider_account_name: account?.name
    };
    if (index >= 0) remotes[index] = next;
    else remotes.push(next);
  };
  ensureRemote('gitea', 'gitea', values.gitea_provider_account_id);
  ensureRemote('github', 'github', values.github_provider_account_id);
  return remotes.length ? { ...parameters, remotes } : parameters;
}

function assetLabelFromID(id = '') {
  const parts = String(id).split(':');
  return parts.length > 1 ? parts.slice(1).join(':') : id;
}

function graphLabel(value: any, limit = 16) {
  const text = String(value || '').trim();
  if (text.length <= limit) return text;
  return `${text.slice(0, Math.max(1, limit - 1))}...`;
}

function buildAssetGraph(asset: AnyRow | undefined, assets: AnyRow[] = [], relations: AnyRow[] = []) {
  if (!asset) return { nodes: [], edges: [] };
  const byID = new Map<string, AnyRow>(assets.map((row) => [row.id, row]));
  const nodeMap = new Map<string, AnyRow>();
  const addNode = (id: string, column: 'from' | 'center' | 'to') => {
    if (!id) return;
    const row = byID.get(id);
    const existing = nodeMap.get(id);
    if (existing) {
      if (existing.column !== column) existing.column = 'center';
      return;
    }
    nodeMap.set(id, {
      id,
      column,
      label: row?.display_name || row?.name || id,
      asset_type: row?.asset_type || 'unknown',
      status: row?.status || '',
      external: !row
    });
  };
  addNode(asset.id, 'center');
  relations.forEach((relation) => {
    addNode(relation.from_asset_id, relation.from_asset_id === asset.id ? 'center' : 'from');
    addNode(relation.to_asset_id, relation.to_asset_id === asset.id ? 'center' : 'to');
  });
  const edges = relations.map((relation) => {
    return {
      id: relation.id,
      from: relation.from_asset_id,
      to: relation.to_asset_id,
      relation_type: relation.relation_type
    };
  });
  const nodes = Array.from(nodeMap.values());
  const columns: Record<string, AnyRow[]> = { from: [], center: [], to: [] };
  nodes.forEach((node) => columns[node.column || 'to'].push(node));
  const positioned = nodes.map((node) => {
    const column = node.column || 'to';
    const peers = columns[column];
    const index = peers.findIndex((item) => item.id === node.id);
    const x = column === 'from' ? 140 : column === 'center' ? 400 : 660;
    const y = 70 + index * 88;
    return { ...node, x, y };
  });
  const height = Math.max(220, 140 + Math.max(columns.from.length, columns.center.length, columns.to.length) * 88);
  return { nodes: positioned, edges, height };
}

function buildAssetSearchGraph(nodes: AnyRow[] = [], relations: AnyRow[] = []) {
  const columnForType = (type: string) => {
    if (['project', 'project_template', 'provider_account', 'node_agent'].includes(type)) return 'from';
    if (['repository', 'git_remote', 'repo_sync', 'webhook_connection', 'host', 'ai_runtime'].includes(type)) return 'center';
    return 'to';
  };
  const columns: Record<string, AnyRow[]> = { from: [], center: [], to: [] };
  const positioned = nodes.map((row) => {
    const column = columnForType(String(row.asset_type || ''));
    const node = {
      id: row.id,
      column,
      label: row.display_name || row.name || row.id,
      asset_type: row.asset_type || 'unknown',
      status: row.status || '',
      relation_count: row.relation_count || 0,
      external: false
    };
    columns[column].push(node);
    return node;
  });
  const laidOut = positioned.map((node) => {
    const peers = columns[node.column];
    const index = peers.findIndex((item) => item.id === node.id);
    const x = node.column === 'from' ? 140 : node.column === 'center' ? 400 : 660;
    const y = 70 + index * 88;
    return { ...node, x, y };
  });
  const edges = relations.map((relation) => ({
    id: relation.id,
    from: relation.from_asset_id,
    to: relation.to_asset_id,
    relation_type: relation.relation_type
  }));
  const height = Math.max(220, 140 + Math.max(columns.from.length, columns.center.length, columns.to.length) * 88);
  return { nodes: laidOut, edges, height };
}

function assetGraphRankingSummary(nodes: AnyRow[] = [], edges: AnyRow[] = [], truncated = false) {
  const ranked = [...nodes].sort((a, b) =>
    Number(b.graph_rank || 0) - Number(a.graph_rank || 0)
      || Number(b.relation_count || 0) - Number(a.relation_count || 0)
  );
  const top = ranked[0];
  return {
    nodesLabel: `${nodes.length} ranked nodes${truncated ? ' (truncated)' : ''}`,
    edges: edges.length,
    topLabel: top ? `${top.display_name || top.name || top.id} (${top.relation_count || 0} links)` : 'No ranked assets'
  };
}

function assetDependencyPath(assetID: string, direction: 'downstream' | 'upstream', projectID?: string) {
  const params = new URLSearchParams({ direction, depth: '4' });
  if (projectID) params.set('project_id', projectID);
  return `/api/assets/${encodeURIComponent(assetID)}/dependencies?${params.toString()}`;
}

function graphNodeColor(type: string) {
  const palette: Record<string, string> = {
    project: '#2563eb',
    repository: '#0891b2',
    git_remote: '#16a34a',
    repo_sync: '#7c3aed',
    webhook_connection: '#ea580c',
    pipeline_run: '#475569',
    host: '#be123c',
    argo_connection: '#0d9488',
    deployment_target: '#4f46e5',
    deployment_record: '#9333ea',
    rollback_point: '#ca8a04',
    argo_app: '#0284c7',
    ai_runtime: '#db2777',
    node_agent: '#64748b'
  };
  return palette[type] || '#475569';
}

function Dashboard() {
  const ops = useLoad(() => api('/api/operations'), []);
  const assets = useLoad(() => api('/api/assets'), []);
  const graph = useLoad(() => api('/api/assets/graph'), []);
  const approvalSummary = useLoad(() => api('/api/operation-approvals/summary'), []);
  const readinessRows = firstVersionReadinessRows(assets.data?.items || [], ops.data?.items || [], approvalSummary.data || {}, graph.data || {});
  const readinessCounts = countByField(readinessRows, 'status');
  const graphWarning = graph.error ? `Asset graph unavailable: ${graph.error}` : graph.data && !graphPayloadAvailable(graph.data) ? 'Asset graph response missing nodes or edges' : '';
  const [demoDataLoading, setDemoDataLoading] = useState(false);
  const [demoDataResult, setDemoDataResult] = useState<AnyRow>();
  const [demoSnapshotLoading, setDemoSnapshotLoading] = useState(false);
  const [demoSnapshotResult, setDemoSnapshotResult] = useState<AnyRow>();
  async function ensureDemoReadinessData() {
    setDemoDataLoading(true);
    try {
      const result = await api('/api/demo-readiness-data', { method: 'POST', body: '{}' });
      setDemoDataResult(result);
      if (result.readiness_snapshot_written) {
        setDemoSnapshotResult(result.readiness_snapshot);
      }
      message.success(result.project_created || result.repository_created || result.git_remote_created ? 'Demo readiness data prepared' : 'Demo readiness data already prepared');
      assets.reload();
      graph.reload();
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setDemoDataLoading(false);
    }
  }
  async function recordDemoReadinessSnapshot() {
    setDemoSnapshotLoading(true);
    try {
      const result = await api('/api/demo-readiness-snapshot', { method: 'POST', body: '{}' });
      setDemoSnapshotResult(result);
      message.success(result.readiness_snapshot_written ? 'Demo readiness snapshot recorded' : 'Demo readiness snapshot already current');
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setDemoSnapshotLoading(false);
    }
  }
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>Dashboard</Typography.Title>
      <div className="metricGrid">
        <Card><Typography.Text type="secondary">Gateway</Typography.Text><Typography.Title level={3}>Online</Typography.Title></Card>
        <Card><Typography.Text type="secondary">Recent operations</Typography.Text><Typography.Title level={3}>{ops.data?.items?.length || 0}</Typography.Title></Card>
        <Card><Typography.Text type="secondary">Ready checks</Typography.Text><Typography.Title level={3}>{readinessCounts.ready || 0}/{readinessRows.length}</Typography.Title></Card>
        <Card><Typography.Text type="secondary">Needs evidence</Typography.Text><Typography.Title level={3}>{(readinessCounts.partial || 0) + (readinessCounts.missing || 0)}</Typography.Title></Card>
      </div>
      {graphWarning ? <Alert showIcon closable type="warning" message={graphWarning} action={<Button size="small" onClick={graph.reload}>Retry</Button>} /> : null}
      <Card
        title="First-Version Readiness"
        extra={<Space size={8} wrap>
          {demoDataResult ? <Tag color={demoDataResult.asset_graph_written ? 'green' : 'default'}>demo data {demoDataResult.recording_state || 'unknown'}</Tag> : null}
          {demoDataResult ? <Tag>{demoDataResult.git_remote_count || 0} remotes</Tag> : null}
          {demoDataResult ? <Tag>{demoDataResult.git_remote_url_written ? 'remote URL written' : 'no remote URL'}</Tag> : null}
          {demoDataResult ? <Tag>{demoDataResult.provider_api_called ? 'provider called' : 'no provider call'}</Tag> : null}
          {demoSnapshotResult ? <Tag color={demoSnapshotResult.readiness_snapshot_written ? 'green' : demoSnapshotResult.recording_state === 'blocked' ? 'red' : 'default'}>snapshot {demoSnapshotResult.recording_state || 'unknown'}</Tag> : null}
          {demoSnapshotResult ? <Tag>{demoSnapshotResult.asset_graph_snapshot_written ? 'asset status written' : 'no asset status write'}</Tag> : null}
          <Button size="small" onClick={ensureDemoReadinessData} loading={demoDataLoading}>Ensure demo data</Button>
          <Button size="small" onClick={recordDemoReadinessSnapshot} loading={demoSnapshotLoading}>Record demo snapshot</Button>
        </Space>}
      >
        <Table<AnyRow>
          rowKey="key"
          dataSource={readinessRows}
          pagination={false}
          size="small"
          columns={[
            { title: 'Status', render: (_, row) => <Tag color={row.color}>{row.status}</Tag> },
            { title: 'Demo proof', dataIndex: 'label' },
            { title: 'Evidence', render: (_, row) => <Typography.Text>{String(row.evidence)}</Typography.Text> },
            { title: 'Demo plan', render: (_, row) => {
              const plan = row.demo_data_rehearsal_plan;
              if (!plan) return null;
              const environmentProof = plan.environment_demo_proof || {};
              const resultPreflight = plan.result_recording_plan?.result_recording_preflight || {};
              return <Space size={4} wrap>
                <Tag color={demoPlanStateColor(plan.plan_state)}>{plan.plan_state}</Tag>
                {plan.environment_evidence_plan ? <Tag color={demoPlanStateColor(plan.environment_evidence_plan.evidence_state)}>env {plan.environment_evidence_plan.evidence_state || 'blocked'}</Tag> : null}
                {environmentProof.proof_state ? <Tag color={demoPlanStateColor(environmentProof.proof_state)}>proof {environmentProof.proof_state}</Tag> : null}
                {environmentProof.complete_repository_multi_remote_path_observed ? <Tag color="green">multi-remote path</Tag> : null}
                {plan.graph_proof_plan ? <Tag color={demoPlanStateColor(plan.graph_proof_plan.proof_state)}>graph {plan.graph_proof_plan.proof_state || 'blocked'}</Tag> : null}
                <Tag>{plan.demo_seed_written ? 'seed written' : 'no seed write'}</Tag>
                <Tag>{plan.asset_graph_written ? 'graph written' : 'no graph write'}</Tag>
                {plan.result_recording_plan ? <Tag color={demoPlanStateColor(plan.result_recording_plan.result_recording_state)}>{plan.result_recording_plan.result_recording_state || 'blocked'} result</Tag> : null}
                {resultPreflight.mode ? <Tag color={resultPreflight.snapshot_contract_ready ? 'green' : 'gold'}>{resultPreflight.snapshot_contract_ready ? 'snapshot review' : 'snapshot blocked'}</Tag> : null}
                {resultPreflight.mode ? <Tag>{resultPreflight.snapshot_write_enabled ? 'snapshot write' : 'no snapshot write'}</Tag> : null}
              </Space>;
            } },
            { title: 'Next', dataIndex: 'next' }
          ]}
        />
      </Card>
      <Operations embedded />
    </Space>
  );
}

function Projects() {
	const projects = useLoad(() => api('/api/projects'), []);
	const templates = useLoad(() => api('/api/project-templates'), []);
	const templateRuns = useLoad(() => api('/api/project-template-runs'), []);
	const [open, setOpen] = useState(false);
	const [templateOpen, setTemplateOpen] = useState(false);
	const [templateDetailOpen, setTemplateDetailOpen] = useState(false);
	const [requestingReviewID, setRequestingReviewID] = useState('');
	const [selectedTemplate, setSelectedTemplate] = useState<AnyRow>();
	async function createFromTemplate(values: AnyRow) {
		if (!selectedTemplate) return;
    const parameters = values.parameters || parseJSONField(values.parameters_json);
		await api(`/api/project-templates/${selectedTemplate.id}/create-project`, {
			method: 'POST',
			body: JSON.stringify({
				name: values.name,
				slug: values.slug,
				description: values.description,
				parameters
			})
		});
		message.success('Template operation queued');
		templateRuns.reload();
	}
	async function retryTemplateProvision(row: AnyRow) {
		try {
			await api(`/api/project-template-runs/${row.id}/retry-provision`, { method: 'POST', body: '{}' });
			message.success('Template provision retry queued');
			templateRuns.reload();
		} catch (error: any) {
			message.error(error.message || 'Could not retry template provision');
		}
  }
	async function requestProviderReviewExecution(row: AnyRow) {
		setRequestingReviewID(row.id);
		try {
			await api(`/api/project-template-runs/${row.id}/request-provider-review-execution`, { method: 'POST', body: '{}' });
			message.success('Provider review execution approval requested');
			templateRuns.reload();
		} catch (error: any) {
			message.error(error.message || 'Could not request provider review execution');
		} finally {
			setRequestingReviewID('');
		}
  }
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="Projects" onCreate={() => setOpen(true)} />
      <Table rowKey="id" dataSource={projects.data?.items || []} pagination={false} columns={[
        { title: 'Name', dataIndex: 'name' },
        { title: 'Slug', dataIndex: 'slug' },
        { title: 'Description', dataIndex: 'description' },
        { title: 'Created', dataIndex: 'created_at' }
      ]} />
      <Typography.Title level={3}>Project Templates</Typography.Title>
      <Table<AnyRow> rowKey="id" dataSource={templates.data?.items || []} pagination={false} columns={[
        { title: 'Name', dataIndex: 'name' },
        { title: 'Slug', dataIndex: 'slug' },
        { title: 'Version', dataIndex: 'version' },
        { title: 'Status', render: (_, row) => <Tag color={row.status === 'active' ? 'green' : 'blue'}>{row.status}</Tag> },
        { title: 'Steps', render: (_, row) => Array.isArray(row.steps) ? row.steps.length : 0 },
        { title: 'Updated', dataIndex: 'updated_at' },
        { title: 'Action', render: (_, row) => <Space><Button size="small" onClick={() => { setSelectedTemplate(row); setTemplateDetailOpen(true); }}>Details</Button><Button size="small" onClick={() => { setSelectedTemplate(row); setTemplateOpen(true); }}>Use</Button></Space> }
      ]} />
      <Typography.Title level={3}>Template Runs</Typography.Title>
      <Table<AnyRow>
        rowKey="id"
        dataSource={templateRuns.data?.items || []}
        pagination={{ pageSize: 6 }}
        expandable={{
          expandedRowRender: (row) => <Tabs items={[
            { key: 'result', label: 'Result', children: <JSONBlock value={row.result} /> },
            { key: 'steps', label: 'Steps', children: <JSONBlock value={row.steps} /> },
            { key: 'reconcile', label: 'Reconcile', children: templateProvisionGuidanceView(row) }
          ]} />
        }}
        columns={[
          { title: 'Project', dataIndex: 'project_name' },
          { title: 'Template', dataIndex: 'template_name' },
          { title: 'Status', render: (_, row) => <Tag color={row.status === 'completed' ? 'green' : row.status === 'failed' ? 'red' : row.status === 'running' || row.status === 'provisioning' ? 'blue' : 'gold'}>{row.status}</Tag> },
          { title: 'Repository', render: (_, row) => row.result?.repository_id ? <Tag color="green">created</Tag> : <Tag>planned</Tag> },
          { title: 'Provision', render: (_, row) => templateProvisionStatus(row) },
          { title: 'Reconcile', render: (_, row) => templateProvisionGuidanceView(row, true) },
          { title: 'RepoSync', render: (_, row) => row.result?.repo_sync_asset_id ? <Tag color="green">created</Tag> : <Tag>planned</Tag> },
          { title: 'Files', render: (_, row) => Array.isArray(row.result?.template_file_ids) ? <Tag color="green">{row.result.template_file_ids.length}</Tag> : <Tag>planned</Tag> },
          { title: 'Steps', render: (_, row) => Array.isArray(row.steps) ? `${row.steps.filter((step: AnyRow) => step.status === 'completed').length}/${row.steps.length}` : '-' },
          { title: 'Error', render: (_, row) => templateRunErrorText(row) },
          { title: 'Created', dataIndex: 'created_at' },
          { title: 'Action', render: (_, row) => {
            const guidance = templateProvisionGuidance(row);
            return (
              <Space>
                {canRetryTemplateProvision(row) ? <Button size="small" title={templateProvisionRetryTitle(row)} onClick={() => retryTemplateProvision(row)}>Retry provision</Button> : null}
                {guidance.executionRequestStatus === 'approval_ready' ? <Button size="small" loading={requestingReviewID === row.id} disabled={Boolean(requestingReviewID)} onClick={() => requestProviderReviewExecution(row)}>Request review</Button> : null}
                {!canRetryTemplateProvision(row) && guidance.executionRequestStatus !== 'approval_ready' ? '-' : null}
              </Space>
            );
          } }
        ]}
      />
      <CreateModal title="Create project" open={open} setOpen={setOpen} fields={['name', 'slug', 'description']} onSubmit={(v) => api('/api/projects', { method: 'POST', body: JSON.stringify(v) }).then(projects.reload)} />
      <TemplateDetailModal template={selectedTemplate} open={templateDetailOpen} setOpen={setTemplateDetailOpen} />
      <TemplateUseModal template={selectedTemplate} open={templateOpen} setOpen={setTemplateOpen} onSubmit={createFromTemplate} />
    </Space>
  );
}

function templateProvisionStatus(row: AnyRow) {
  const summary = templateProvisionSummary(row);
  return (
    <Space size={4} wrap>
      <Tag color={summary.color}>{summary.label}</Tag>
      {summary.detail ? <Typography.Text type="secondary">{summary.detail}</Typography.Text> : null}
    </Space>
  );
}

function templateProvisionGuidanceView(row: AnyRow, compact = false) {
  const guidance = templateProvisionGuidance(row);
  if (compact) {
    return (
      <Space direction="vertical" size={2}>
        <Space size={4} wrap>
          <Tag color={guidance.color}>{guidance.status}</Tag>
          {guidance.reviewStatus ? <Tag>{guidance.reviewStatus}</Tag> : null}
          {guidance.reviewPlanMode ? <Tag color="blue">{guidance.reviewPlanMode}</Tag> : null}
        </Space>
        <Typography.Text type="secondary">{shortText(guidance.next, 96)}</Typography.Text>
      </Space>
    );
  }
  return (
    <Alert
      showIcon
      type={guidance.color === 'red' ? 'error' : guidance.color === 'green' ? 'success' : guidance.color === 'gold' ? 'warning' : 'info'}
      message={guidance.title}
      description={<Space direction="vertical" size={4}>
        <Typography.Text>{guidance.detail}</Typography.Text>
        {guidance.reviewStatus ? <Space size={4} wrap><Tag>{guidance.reviewStatus}</Tag><Tag>provider execution: {guidance.reviewExecution}</Tag></Space> : null}
        {guidance.reviewPlanMode ? <TemplateProviderReviewPlan guidance={guidance} /> : null}
        <Typography.Text strong>{guidance.next}</Typography.Text>
      </Space>}
    />
  );
}

function TemplateProviderReviewPlan({ guidance }: { guidance: TemplateProvisionGuidance }) {
  const requestColor = guidance.executionRequestStatus === 'approval_ready' ? 'green' : guidance.executionRequestStatus === 'blocked' ? 'gold' : 'default';
  const apiPlanColor = guidance.apiPlanStatus === 'ready' ? 'green' : guidance.apiPlanStatus === 'blocked' ? 'gold' : 'default';
  return (
    <Space direction="vertical" size={4}>
      <Space size={4} wrap>
        <Tag color="blue">{guidance.reviewPlanMode}</Tag>
        {guidance.reviewKind ? <Tag>{guidance.reviewKind}</Tag> : null}
        {guidance.approvalAction ? <Tag>{guidance.approvalAction}</Tag> : null}
        {guidance.guardrailMode ? <Tag color="gold">guardrail {guidance.guardrailMode.replaceAll('_', ' ')}</Tag> : null}
        {guidance.executionRequestStatus ? <Tag color={requestColor}>request {guidance.executionRequestStatus.replaceAll('_', ' ')}</Tag> : null}
        {guidance.apiPlanStatus ? <Tag color={apiPlanColor}>api plan {guidance.apiPlanStatus}</Tag> : null}
      </Space>
      {guidance.sourceBranch || guidance.targetBranch ? (
        <Typography.Text type="secondary">{guidance.sourceBranch || '-'} -&gt; {guidance.targetBranch || '-'}</Typography.Text>
      ) : null}
      {guidance.executionRequestResource ? (
        <Typography.Text type="secondary">Resource: {guidance.executionRequestResource}</Typography.Text>
      ) : null}
      {guidance.guardrailGates.length ? (
        <Space size={4} wrap>
          {guidance.guardrailGates.map((gate, index) => (
            <Tag key={`${gate.gate || 'gate'}-${index}`} color={gate.status === 'ready' ? 'green' : 'gold'}>
              {String(gate.gate || 'gate')}: {String(gate.status || 'unknown')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {guidance.guardrailReasons.length ? (
        <Typography.Text type="secondary">{shortText(`Blocked: ${guidance.guardrailReasons.join(', ')}`, 120)}</Typography.Text>
      ) : null}
      {guidance.apiPlanOperations.length ? (
        <Space size={4} wrap>
          {guidance.apiPlanMode ? <Tag>{guidance.apiPlanMode.replaceAll('_', ' ')}</Tag> : null}
          <Tag>files {guidance.apiPlanFileCount}</Tag>
          {guidance.apiPlanOperations.map((operation, index) => (
            <Tag key={String(operation.endpoint_key || operation.name || `api-${index}`)} color={operation.api_call === true ? 'red' : 'default'}>
              {String(operation.endpoint_key || operation.name || 'provider.api')}: {String(operation.payload_shape || operation.method || 'redacted')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {guidance.reviewSteps.length ? (
        <Space size={4} wrap>
          {guidance.reviewSteps.map((step, index) => (
            <Tag key={`${step.name || 'step'}-${index}`} color={step.api_call === true ? 'red' : 'default'}>
              {String(step.name || 'step')}: {String(step.status || 'planned')}
            </Tag>
          ))}
        </Space>
      ) : null}
    </Space>
  );
}

function ProviderReviewApprovalAudit({ value, persistedAttemptLedger }: { value?: AnyRow; persistedAttemptLedger?: AnyRow }) {
  if (!value || value.kind !== 'project_template_provider_review_execute') return null;
  const request = value.execution_request || {};
  const guardrail = value.execution_guardrail || {};
  const credential = value.credential_strategy || {};
  const starter = value.starter_file_payload || {};
  const apiPlan = value.provider_api_request_plan || {};
  const reconciliation = value.provider_review_reconciliation || {};
  const result = value.approval_result || {};
  const targetSummary = value.provider_review_target_summary?.status ? value.provider_review_target_summary : (result.provider_review_target_summary || {});
  const adapterContract = reconciliation.adapter_contract || {};
  const gates = Array.isArray(guardrail.gates) ? guardrail.gates : [];
  const files = Array.isArray(starter.files) ? starter.files : [];
  const operations = Array.isArray(apiPlan.operations) ? apiPlan.operations : [];
  const targetOperations = Array.isArray(targetSummary.operations) ? targetSummary.operations : [];
  const targetSummarySafe = targetSummary.payload_redacted !== false &&
    targetSummary.contains_token !== true &&
    targetSummary.contains_provider_url !== true &&
    targetSummary.contains_repository_ref !== true &&
    targetSummary.contains_file_content !== true;
  const reconciliationGates = Array.isArray(reconciliation.gates) ? reconciliation.gates : [];
  const reconciliationOperations = Array.isArray(reconciliation.operations) ? reconciliation.operations : [];
  const adapterRehearsal = reconciliation.adapter_rehearsal || {};
  const mutationArmingPlan = reconciliation.mutation_arming_plan || {};
  const adapterRehearsalOperations = Array.isArray(adapterRehearsal.operations) ? adapterRehearsal.operations : [];
  const adapterOperations = Array.isArray(adapterContract.operations) ? adapterContract.operations : [];
  const requestEnvelopes = Array.isArray(reconciliation.request_envelopes)
    ? reconciliation.request_envelopes
    : (Array.isArray(adapterContract.request_envelopes) ? adapterContract.request_envelopes : []);
  const responseDiagnostics = reconciliation.response_diagnostics || adapterContract.response_diagnostics || {};
  const responseDiagnosticOperations = Array.isArray(responseDiagnostics.operations) ? responseDiagnostics.operations : [];
  const executionBlueprint = reconciliation.execution_blueprint || adapterContract.execution_blueprint || {};
  const executionBlueprintOperations = Array.isArray(executionBlueprint.operations) ? executionBlueprint.operations : [];
  const idempotencyPlan = reconciliation.idempotency_plan || adapterContract.idempotency_plan || {};
  const idempotencyOperations = Array.isArray(idempotencyPlan.operations) ? idempotencyPlan.operations : [];
  const attemptLedger = result.provider_review_attempt_ledger?.status ? result.provider_review_attempt_ledger : (persistedAttemptLedger || {});
  const attemptOrchestration = attemptLedger.orchestration || {};
  const attemptDependencyChainPlan = attemptOrchestration.dependency_chain_plan || {};
  const attemptDependencyOperations = Array.isArray(attemptDependencyChainPlan.ordered_operations) ? attemptDependencyChainPlan.ordered_operations : [];
  const attemptExecutionCandidate = attemptOrchestration.execution_candidate || {};
  const attemptAdapterContract = attemptExecutionCandidate.adapter_contract || {};
  const attemptAdapterContractMode = typeof attemptAdapterContract.mode === 'string' ? attemptAdapterContract.mode.replaceAll('_', ' ') : 'redacted attempt adapter contract';
  const attemptClaimPlan = attemptExecutionCandidate.claim_plan || {};
  const attemptClaimPlanMode = typeof attemptClaimPlan.mode === 'string' ? attemptClaimPlan.mode.replaceAll('_', ' ') : 'redacted attempt execution claim plan';
  const attemptDispatchPlan = attemptExecutionCandidate.dispatch_plan || {};
  const attemptDispatchPlanMode = typeof attemptDispatchPlan.mode === 'string' ? attemptDispatchPlan.mode.replaceAll('_', ' ') : 'redacted attempt adapter dispatch plan';
  const attemptRequestPlan = attemptDispatchPlan.request_materialization_plan || {};
  const attemptRequestPlanMode = typeof attemptRequestPlan.mode === 'string' ? attemptRequestPlan.mode.replaceAll('_', ' ') : 'redacted attempt adapter request materialization plan';
  const attemptBranchPolicyPlan = attemptDispatchPlan.branch_policy_plan || {};
  const attemptBranchPolicyPlanMode = typeof attemptBranchPolicyPlan.mode === 'string' ? attemptBranchPolicyPlan.mode.replaceAll('_', ' ') : 'redacted attempt branch policy plan';
  const attemptTransportPlan = attemptDispatchPlan.transport_plan || {};
  const attemptTransportPlanMode = typeof attemptTransportPlan.mode === 'string' ? attemptTransportPlan.mode.replaceAll('_', ' ') : 'redacted attempt adapter transport plan';
  const attemptResponsePlan = attemptDispatchPlan.response_plan || {};
  const attemptResponsePlanMode = typeof attemptResponsePlan.mode === 'string' ? attemptResponsePlan.mode.replaceAll('_', ' ') : 'redacted attempt adapter response plan';
  const attemptResultRecordingPlan = attemptResponsePlan.result_recording_plan || {};
  const attemptCredentialPlan = attemptDispatchPlan.credential_binding_plan || {};
  const attemptCredentialPlanMode = typeof attemptCredentialPlan.mode === 'string' ? attemptCredentialPlan.mode.replaceAll('_', ' ') : 'redacted attempt adapter credential binding plan';
  const attemptRuntimePlan = attemptDispatchPlan.adapter_runtime_plan || {};
  const attemptRuntimePlanMode = typeof attemptRuntimePlan.mode === 'string' ? attemptRuntimePlan.mode.replaceAll('_', ' ') : 'redacted attempt adapter runtime plan';
  const attemptRuntimeRequestBuilderPlan = attemptRuntimePlan.request_builder_plan || {};
  const attemptRuntimeProviderClientPlan = attemptRuntimePlan.provider_client_plan || {};
  const attemptRuntimeExecuteMethodPlan = attemptRuntimePlan.execute_method_plan || {};
  const attemptRuntimeResponseHandlerPlan = attemptRuntimePlan.response_handler_plan || {};
  const attemptTransactionPlan = attemptDispatchPlan.transaction_plan || {};
  const attemptTransactionPlanMode = typeof attemptTransactionPlan.mode === 'string' ? attemptTransactionPlan.mode.replaceAll('_', ' ') : 'redacted attempt adapter transaction plan';
  const attemptTransactionProviderCallBoundaryPlan = attemptTransactionPlan.provider_call_boundary_plan || {};
  const attemptInvocationPlan = attemptDispatchPlan.invocation_plan || {};
  const attemptInvocationPlanMode = typeof attemptInvocationPlan.mode === 'string' ? attemptInvocationPlan.mode.replaceAll('_', ' ') : 'redacted attempt adapter invocation plan';
  const attemptInvocationExecutionLockPlan = attemptInvocationPlan.execution_lock_plan || {};
  const attemptInvocationActivationPlan = attemptInvocationPlan.adapter_activation_plan || {};
  const attemptInvocationLiveAdapterPlan = attemptInvocationActivationPlan.live_adapter_plan || {};
  const attemptInvocationLiveAdapterContractPlan = attemptInvocationLiveAdapterPlan.contract_plan || {};
  const attemptInvocationLiveAdapterContractInputs = Array.isArray(attemptInvocationLiveAdapterContractPlan.contract_input_fields)
    ? attemptInvocationLiveAdapterContractPlan.contract_input_fields
    : [];
  const attemptInvocationLiveAdapterContractOutputs = Array.isArray(attemptInvocationLiveAdapterContractPlan.contract_output_fields)
    ? attemptInvocationLiveAdapterContractPlan.contract_output_fields
    : [];
  const attemptInvocationLiveAdapterContractErrors = Array.isArray(attemptInvocationLiveAdapterContractPlan.contract_error_classes)
    ? attemptInvocationLiveAdapterContractPlan.contract_error_classes
    : [];
  const attemptInvocationLiveAdapterContractPersistedFields = Array.isArray(attemptInvocationLiveAdapterContractPlan.contract_persisted_fields)
    ? attemptInvocationLiveAdapterContractPlan.contract_persisted_fields
    : [];
  const attemptInvocationLiveAdapterContractSuppressedFields = Array.isArray(attemptInvocationLiveAdapterContractPlan.contract_suppressed_fields)
    ? attemptInvocationLiveAdapterContractPlan.contract_suppressed_fields
    : [];
  const attemptInvocationProviderSendPlan = attemptInvocationPlan.provider_send_plan || {};
  const attemptInvocationRetryBackoffPlan = attemptInvocationProviderSendPlan.retry_backoff_plan || {};
  const attemptInvocationSequence = Array.isArray(attemptInvocationPlan.invocation_sequence) ? attemptInvocationPlan.invocation_sequence : [];
  const attemptExecutionCandidateGates = Array.isArray(attemptExecutionCandidate.gates) ? attemptExecutionCandidate.gates : [];
  const attemptOperations = Array.isArray(attemptLedger.operations) ? attemptLedger.operations : [];
  return (
    <Space direction="vertical" size={8} className="full">
      <Space size={4} wrap>
        <Tag color="blue">{String(value.kind)}</Tag>
        <Tag>{String(request.provider_type || value.provider_type || 'provider')}</Tag>
        <Tag>{String(request.review_kind || 'review')}</Tag>
        <Tag color="green">redacted</Tag>
        <Tag color={value.provider_api_call_made === true ? 'red' : 'default'}>{value.provider_api_call_made === true ? 'api called' : 'no api call'}</Tag>
        <Tag>{String(value.provider_api_mutation || 'disabled')}</Tag>
      </Space>
      <Typography.Text type="secondary">Run: {String(value.project_template_run_id || '-')}</Typography.Text>
      {credential.mode ? (
        <Space size={4} wrap>
          <Tag>credential {String(credential.mode).replaceAll('_', ' ')}</Tag>
          <Tag color={credential.token_env_configured === true ? 'green' : 'gold'}>{credential.token_env_configured === true ? 'token env configured' : 'token env missing'}</Tag>
          <Tag color={credential.token_env_present === true ? 'green' : 'gold'}>{credential.token_env_present === true ? 'runtime token present' : 'runtime token missing'}</Tag>
          <Tag>{credential.token_stored === true ? 'token stored' : 'token not stored'}</Tag>
        </Space>
      ) : null}
      <Space size={4} wrap>
        <Tag color="gold">guardrail {String(guardrail.execution_mode || 'disabled').replaceAll('_', ' ')}</Tag>
        {Array.isArray(guardrail.blocked_reasons) && guardrail.blocked_reasons.map((reason: unknown) => (
          <Tag key={String(reason)}>{String(reason)}</Tag>
        ))}
      </Space>
      {gates.length ? (
        <Space size={4} wrap>
          {gates.map((gate: AnyRow, index: number) => (
            <Tag key={String(gate.gate || `gate-${index}`)} color={gate.status === 'ready' ? 'green' : 'gold'}>
              {String(gate.gate || 'gate')}: {String(gate.status || 'unknown')}
            </Tag>
          ))}
        </Space>
      ) : null}
      <Space size={4} wrap>
        <Tag color={starter.status === 'ready' ? 'green' : 'gold'}>starter {String(starter.status || 'unknown')}</Tag>
        <Tag>files {Number(starter.file_count || 0)}</Tag>
        <Tag>{starter.content_included === true ? 'content included' : 'content redacted'}</Tag>
      </Space>
      {files.length ? (
        <Space size={4} wrap>
          {files.map((file: AnyRow, index: number) => (
            <Tag key={String(file.id || file.path || `file-${index}`)}>{String(file.path || 'file')}: {String(file.status || file.kind || 'planned')}</Tag>
          ))}
        </Space>
      ) : null}
      <Space size={4} wrap>
        <Tag color={apiPlan.status === 'ready' ? 'green' : 'gold'}>api plan {String(apiPlan.status || 'unknown')}</Tag>
        <Tag>{String(apiPlan.mode || 'redacted_request_plan').replaceAll('_', ' ')}</Tag>
        <Tag>files {Number(apiPlan.file_count || 0)}</Tag>
      </Space>
      {operations.length ? (
        <Space size={4} wrap>
          {operations.map((operation: AnyRow, index: number) => (
            <Tag key={String(operation.endpoint_key || operation.name || `operation-${index}`)} color={operation.api_call === true ? 'red' : 'default'}>
              {String(operation.endpoint_key || operation.name || 'provider.api')}: {String(operation.payload_shape || operation.method || 'redacted')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {targetSummary.status ? (
        <Space size={4} wrap>
          <Tag color={targetSummary.status === 'adapter_blocked' || targetSummary.status === 'mutation_blocked' ? 'blue' : targetSummary.status === 'ready' ? 'green' : 'gold'}>target {String(targetSummary.status).replaceAll('_', ' ')}</Tag>
          <Tag>{String(targetSummary.mode || 'redacted_execution_target_summary').replaceAll('_', ' ')}</Tag>
          {!targetSummarySafe ? <Tag color="red">target not redacted</Tag> : null}
          <Tag>{targetSummary.branch_refs_ready === true ? 'branches ready' : 'branches blocked'}</Tag>
          <Tag>{targetSummary.starter_file_payload_ready === true ? 'starter ready' : 'starter blocked'}</Tag>
          <Tag>files {Number(targetSummary.file_count || 0)}</Tag>
          <Tag color={targetSummary.adapter_mutation_currently_off === true ? 'blue' : 'red'}>{targetSummary.adapter_mutation_currently_off === true ? 'mutation off' : 'mutation armed'}</Tag>
          <Tag>adapter {String(targetSummary.adapter_status || 'missing')}</Tag>
        </Space>
      ) : null}
      {targetSummarySafe && targetOperations.length ? (
        <Space size={4} wrap>
          {targetOperations.map((operation: AnyRow, index: number) => (
            <Tag key={String(operation.endpoint_key || operation.name || `target-operation-${index}`)}>
              {String(operation.endpoint_key || operation.name || 'provider.api')}: {String(operation.status || 'planned')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {reconciliation.status ? (
        <Space size={4} wrap>
          <Tag color={reconciliation.status === 'ready' ? 'green' : 'gold'}>reconcile {String(reconciliation.status)}</Tag>
          <Tag>adapter {String(reconciliation.adapter_status || 'unknown')}</Tag>
          <Tag color={reconciliation.external_call_made === true ? 'red' : 'default'}>{reconciliation.external_call_made === true ? 'external call made' : 'no external call'}</Tag>
          <Tag>{String(reconciliation.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {adapterRehearsal.status ? (
        <Space size={4} wrap>
          <Tag color={adapterRehearsal.status === 'ready' ? 'green' : 'gold'}>rehearsal {String(adapterRehearsal.status)}</Tag>
          <Tag>ready {Number(adapterRehearsal.ready_operation_count || 0)}</Tag>
          <Tag>blocked {Number(adapterRehearsal.blocked_operation_count || 0)}</Tag>
          <Tag color={adapterRehearsal.mutation_arming_candidate === true ? 'green' : 'blue'}>
            {adapterRehearsal.mutation_arming_candidate === true ? 'arming candidate' : 'arming blocked'}
          </Tag>
          <Tag>{adapterRehearsal.provider_api_call_made === true ? 'api called' : 'no api call'}</Tag>
        </Space>
      ) : null}
      {adapterRehearsalOperations.length ? (
        <Space size={4} wrap>
          {adapterRehearsalOperations.map((operation: AnyRow, index: number) => (
            <Tag key={String(operation.endpoint_key || operation.name || `rehearsal-operation-${index}`)} color={operation.status === 'ready' ? 'green' : 'gold'}>
              {String(operation.endpoint_key || operation.name || 'provider.api')}: {String(operation.status || 'blocked')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {mutationArmingPlan.status ? (
        <Space size={4} wrap>
          <Tag color={mutationArmingPlan.status === 'ready_to_arm' ? 'green' : 'gold'}>arming {String(mutationArmingPlan.status).replaceAll('_', ' ')}</Tag>
          <Tag>{mutationArmingPlan.execution_enabled_config === true ? 'execution config ready' : 'execution config blocked'}</Tag>
          <Tag>{mutationArmingPlan.adapter_rehearsal_ready === true ? 'rehearsal ready' : 'rehearsal blocked'}</Tag>
          <Tag>{mutationArmingPlan.mutation_armed_config === true ? 'arming config requested' : 'arming config off'}</Tag>
          <Tag color={mutationArmingPlan.adapter_mutation_currently_off === true ? 'blue' : 'red'}>{mutationArmingPlan.adapter_mutation_currently_off === true ? 'mutation off' : 'mutation armed'}</Tag>
          <Tag>{String(mutationArmingPlan.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {adapterContract.status ? (
        <Space size={4} wrap>
          <Tag>audit-only</Tag>
          <Tag color={adapterContract.status === 'planned' ? 'blue' : 'gold'}>contract {String(adapterContract.status)}</Tag>
          <Tag>version {String(adapterContract.contract_version || 'unknown')}</Tag>
          <Tag>adapter {String(adapterContract.adapter_status || 'missing')}</Tag>
        </Space>
      ) : null}
      {adapterOperations.length ? (
        <Space size={4} wrap>
          {adapterOperations.map((operation: AnyRow, index: number) => (
            <Tag key={String(operation.endpoint_key || operation.name || `adapter-operation-${index}`)}>
              {String(operation.endpoint_key || operation.name || 'provider.api')}: {String(operation.required_capability || operation.execution_status || 'blocked')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {requestEnvelopes.length ? (
        <Space size={4} wrap>
          {requestEnvelopes.map((envelope: AnyRow, index: number) => (
            <Tag key={String(envelope.endpoint_key || envelope.name || `request-envelope-${index}`)}>
              {String(envelope.endpoint_key || envelope.name || 'provider.api')}: {String(envelope.execution_status || 'blocked')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {requestEnvelopes.length ? (
        <Space size={4} wrap>
          {requestEnvelopes.flatMap((envelope: AnyRow, envelopeIndex: number) => {
            const readiness = Array.isArray(envelope.readiness) ? envelope.readiness : [];
            return readiness.map((item: AnyRow, readinessIndex: number) => (
              <Tag
                key={`${String(envelope.endpoint_key || envelope.name || envelopeIndex)}-${String(item.evidence || readinessIndex)}`}
                color={item.status === 'ready' ? 'green' : 'gold'}
              >
                {String(item.evidence || 'evidence')}: {String(item.status || 'unknown')}
              </Tag>
            ));
          })}
        </Space>
      ) : null}
      {executionBlueprint.status ? (
        <Space size={4} wrap>
          <Tag color={executionBlueprint.status === 'ready_for_adapter_implementation' ? 'green' : 'gold'}>blueprint {String(executionBlueprint.status).replaceAll('_', ' ')}</Tag>
          <Tag>{String(executionBlueprint.mode || 'redacted_adapter_execution_blueprint').replaceAll('_', ' ')}</Tag>
          <Tag>{executionBlueprint.live_adapter_implemented === true ? 'live adapter ready' : 'adapter implementation required'}</Tag>
          <Tag>{executionBlueprint.requires_idempotency_ledger === true ? 'ledger required' : 'ledger missing'}</Tag>
          <Tag color={executionBlueprint.adapter_mutation_currently_off === true ? 'blue' : 'gold'}>{executionBlueprint.adapter_mutation_currently_off === true ? 'mutation off' : 'mutation armed'}</Tag>
        </Space>
      ) : null}
      {executionBlueprintOperations.length ? (
        <Space size={4} wrap>
          {executionBlueprintOperations.map((operation: AnyRow, index: number) => (
            <Tag key={String(operation.endpoint_key || operation.name || `execution-blueprint-${index}`)} color={operation.execution_status === 'ready_for_adapter_implementation' ? 'green' : 'gold'}>
              {String(operation.endpoint_key || operation.name || 'provider.api')}: {String(operation.payload_builder || operation.execution_status || 'blocked')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {responseDiagnostics.status ? (
        <Space size={4} wrap>
          <Tag color={responseDiagnostics.status === 'ready' ? 'green' : 'gold'}>response diagnostics {String(responseDiagnostics.status)}</Tag>
          <Tag>{String(responseDiagnostics.mode || 'redacted_response_diagnostics').replaceAll('_', ' ')}</Tag>
          <Tag>{responseDiagnostics.response_body_included === true ? 'body included' : 'body redacted'}</Tag>
          <Tag>{responseDiagnostics.headers_included === true ? 'headers included' : 'headers redacted'}</Tag>
        </Space>
      ) : null}
      {responseDiagnosticOperations.length ? (
        <Space size={4} wrap>
          {responseDiagnosticOperations.map((operation: AnyRow, index: number) => (
            <Tag key={String(operation.endpoint_key || operation.name || `response-diagnostic-${index}`)}>
              {String(operation.endpoint_key || operation.name || 'provider.api')}: {String(operation.status || 'pending')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {idempotencyPlan.status ? (
        <Space size={4} wrap>
          <Tag color={idempotencyPlan.status === 'ready' ? 'green' : 'blue'}>idempotency {String(idempotencyPlan.status)}</Tag>
          <Tag>{String(idempotencyPlan.mode || 'redacted_idempotency_plan').replaceAll('_', ' ')}</Tag>
          <Tag>{idempotencyPlan.requires_persisted_attempt === true ? 'persisted attempt required' : 'no persisted attempt'}</Tag>
          <Tag>{idempotencyPlan.idempotency_key_included === true ? 'key included' : 'key redacted'}</Tag>
        </Space>
      ) : null}
      {idempotencyOperations.length ? (
        <Space size={4} wrap>
          {idempotencyOperations.map((operation: AnyRow, index: number) => (
            <Tag key={String(operation.endpoint_key || operation.name || `idempotency-operation-${index}`)}>
              {String(operation.endpoint_key || operation.name || 'provider.api')}: {String(operation.replay_check || operation.conflict_policy || 'planned')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {reconciliationGates.length ? (
        <Space size={4} wrap>
          {reconciliationGates.map((gate: AnyRow, index: number) => (
            <Tag key={String(gate.gate || `reconcile-gate-${index}`)} color={gate.status === 'ready' ? 'green' : 'gold'}>
              {String(gate.gate || 'gate')}: {String(gate.status || 'unknown')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {reconciliationOperations.length ? (
        <Space size={4} wrap>
          {reconciliationOperations.map((operation: AnyRow, index: number) => (
            <Tag key={String(operation.endpoint_key || operation.name || `reconcile-operation-${index}`)} color={operation.status === 'ready' ? 'green' : 'default'}>
              {String(operation.endpoint_key || operation.name || 'provider.api')}: {String(operation.status || 'blocked')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {result && result.execution_enabled !== undefined ? (
        <Space size={4} wrap>
          <Tag color={result.execution_enabled === true ? 'red' : 'default'}>{result.execution_enabled === true ? 'execution enabled' : 'execution disabled'}</Tag>
          <Tag color={result.provider_api_call_made === true ? 'red' : 'default'}>{result.provider_api_call_made === true ? 'result api called' : 'result no api call'}</Tag>
          <Tag>{String(result.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {attemptLedger.status ? (
        <Space size={4} wrap>
          <Tag color={attemptLedger.status === 'recorded' ? 'green' : 'gold'}>attempt ledger {String(attemptLedger.status)}</Tag>
          <Tag>attempts {Number(attemptLedger.attempt_count || 0)}</Tag>
          <Tag>{attemptLedger.idempotency_key_included === true ? 'key included' : 'key redacted'}</Tag>
        </Space>
      ) : null}
      {attemptOrchestration.status && attemptOrchestration.status !== 'not_recorded' ? (
        <Space size={4} wrap>
          <Tag color={attemptOrchestration.dependency_chain_status === 'blocked' ? 'red' : attemptOrchestration.dependency_chain_status === 'ready' ? 'green' : attemptOrchestration.dependency_chain_status === 'waiting_for_dependency' ? 'gold' : 'default'}>
            orchestration {String(attemptOrchestration.dependency_chain_status || attemptOrchestration.status).replaceAll('_', ' ')}
          </Tag>
          <Tag>next {String(attemptOrchestration.next_operation || '-')}</Tag>
          <Tag>ready {Number(attemptOrchestration.ready_count || 0)}</Tag>
          <Tag>waiting {Number(attemptOrchestration.waiting_count || 0)}</Tag>
          <Tag>blocked {Number(attemptOrchestration.blocked_count || 0)}</Tag>
          <Tag>completed {Number(attemptOrchestration.completed_count || 0)}</Tag>
        </Space>
      ) : null}
      {attemptDependencyChainPlan.mode ? (
        <Space size={4} wrap>
          <Tag color={attemptDependencyChainPlan.status === 'blocked' ? 'red' : attemptDependencyChainPlan.status === 'ready' ? 'green' : 'gold'}>
            chain {String(attemptDependencyChainPlan.status || 'not_recorded').replaceAll('_', ' ')}
          </Tag>
          <Tag>next {String(attemptDependencyChainPlan.next_operation || '-')}</Tag>
          <Tag>{attemptDependencyChainPlan.chain_ready_for_next_attempt === true ? 'next claim ready' : 'next claim blocked'}</Tag>
          <Tag>ops {Number(attemptDependencyChainPlan.operation_count || 0)}</Tag>
          <Tag>{String(attemptDependencyChainPlan.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {attemptDependencyOperations.length ? (
        <Space size={4} wrap>
          {attemptDependencyOperations.map((operation: AnyRow, index: number) => (
            <Tag key={String(operation.name || operation.endpoint_key || `dependency-operation-${index}`)}>
              {String(operation.name || 'operation')}: {String(operation.dependency_status || 'independent').replaceAll('_', ' ')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {attemptExecutionCandidate.mode ? (
        <Space size={4} wrap>
          <Tag color={attemptExecutionCandidate.status === 'ready' ? 'green' : attemptExecutionCandidate.status === 'blocked' ? 'red' : 'gold'}>candidate {String(attemptExecutionCandidate.status || 'blocked')}</Tag>
          <Tag>next {String(attemptExecutionCandidate.next_operation || '-')}</Tag>
          <Tag>{String(attemptExecutionCandidate.endpoint_key || 'provider.api')}</Tag>
          <Tag>{String(attemptExecutionCandidate.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {attemptAdapterContract.mode ? (
        <Space size={4} wrap>
          <Tag>{attemptAdapterContractMode}</Tag>
          <Tag>{String(attemptAdapterContract.payload_builder || 'build_redacted_provider_request')}</Tag>
          <Tag>{String(attemptAdapterContract.response_handler || 'handle_provider_response')}</Tag>
          <Tag>response {String(attemptAdapterContract.response_status || 'pending')}</Tag>
          <Tag color={attemptAdapterContract.adapter_call_state === 'blocked' ? 'red' : 'gold'}>
            adapter {String(attemptAdapterContract.adapter_call_state || 'blocked')}
          </Tag>
        </Space>
      ) : null}
      {attemptClaimPlan.mode ? (
        <Space size={4} wrap>
          <Tag>{attemptClaimPlanMode}</Tag>
          <Tag color={attemptClaimPlan.claim_state === 'blocked' ? 'red' : 'gold'}>
            claim {String(attemptClaimPlan.claim_state || 'blocked')}
          </Tag>
          <Tag color={attemptClaimPlan.claim_metadata_ready === true ? 'green' : 'gold'}>
            metadata {attemptClaimPlan.claim_metadata_ready === true ? 'ready' : 'blocked'}
          </Tag>
          <Tag>
            {String(attemptClaimPlan.claim_status_from || 'planned')} -&gt; {String(attemptClaimPlan.claim_status_to || 'running')}
          </Tag>
          <Tag color={attemptClaimPlan.dependency_ready === true ? 'green' : 'gold'}>
            dependency {String(attemptClaimPlan.dependency_status || 'unknown').replaceAll('_', ' ')}
          </Tag>
          <Tag>{String(attemptClaimPlan.replay_check || 'redacted replay')}</Tag>
          <Tag>{String(attemptClaimPlan.conflict_policy || 'redacted conflict')}</Tag>
          <Tag>{String(attemptClaimPlan.retry_policy || 'redacted retry')}</Tag>
        </Space>
      ) : null}
      {attemptDispatchPlan.mode ? (
        <Space size={4} wrap>
          <Tag>{attemptDispatchPlanMode}</Tag>
          <Tag color={attemptDispatchPlan.dispatch_state === 'blocked' ? 'red' : 'gold'}>
            dispatch {String(attemptDispatchPlan.dispatch_state || 'blocked')}
          </Tag>
          <Tag>{String(attemptDispatchPlan.dispatch_ready_reason || 'provider_api_adapter_dispatch_not_armed')}</Tag>
          <Tag color={attemptDispatchPlan.dispatch_metadata_ready === true ? 'green' : 'gold'}>
            metadata {attemptDispatchPlan.dispatch_metadata_ready === true ? 'ready' : 'blocked'}
          </Tag>
          <Tag>{String(attemptDispatchPlan.adapter_kind || 'redacted adapter')}</Tag>
          <Tag>{String(attemptDispatchPlan.method || 'redacted method')}</Tag>
          <Tag>{String(attemptDispatchPlan.payload_shape || 'redacted payload')}</Tag>
          <Tag>{String(attemptDispatchPlan.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {attemptRequestPlan.mode ? (
        <Space size={4} wrap>
          <Tag>{attemptRequestPlanMode}</Tag>
          <Tag color={attemptRequestPlan.request_materialization_state === 'blocked' ? 'red' : 'gold'}>
            request {String(attemptRequestPlan.request_materialization_state || 'blocked')}
          </Tag>
          <Tag>{String(attemptRequestPlan.method || 'redacted method')}</Tag>
          <Tag>{safeProviderEndpointTemplateLabel(attemptRequestPlan.endpoint_path_template_key)}</Tag>
          <Tag>{String(attemptRequestPlan.payload_shape || 'redacted payload')}</Tag>
          <Tag>{attemptRequestPlan.request_url_materialized === true ? 'url materialized' : 'url redacted'}</Tag>
          <Tag>{attemptRequestPlan.request_body_materialized === true ? 'body materialized' : 'body redacted'}</Tag>
        </Space>
      ) : null}
      {attemptBranchPolicyPlan.mode ? (
        <Space size={4} wrap>
          <Tag>{attemptBranchPolicyPlanMode}</Tag>
          <Tag color={attemptBranchPolicyPlan.branch_policy_state === 'blocked' ? 'red' : 'gold'}>
            branch policy {String(attemptBranchPolicyPlan.branch_policy_state || 'blocked')}
          </Tag>
          <Tag>{String(attemptBranchPolicyPlan.branch_policy_ready_reason || 'provider_branch_policy_not_armed')}</Tag>
          <Tag>{attemptBranchPolicyPlan.default_branch_direct_write_allowed === true ? 'default policy open' : 'default direct write blocked'}</Tag>
          <Tag>{attemptBranchPolicyPlan.protected_branch_direct_write_allowed === true ? 'protected policy open' : 'protected direct write blocked'}</Tag>
          <Tag>{attemptBranchPolicyPlan.branch_name_included === true ? 'branch included' : 'branch redacted'}</Tag>
          <Tag>{String(attemptBranchPolicyPlan.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {attemptTransportPlan.mode ? (
        <Space size={4} wrap>
          <Tag>{attemptTransportPlanMode}</Tag>
          <Tag>{String(attemptTransportPlan.auth_scheme || 'redacted auth')}</Tag>
          <Tag>{String(attemptTransportPlan.accept_header || 'redacted accept')}</Tag>
          <Tag>timeout {Number(attemptTransportPlan.timeout_seconds || 0)}s</Tag>
          <Tag>{String(attemptTransportPlan.provider_api_mutation || 'disabled')}</Tag>
          <Tag>auth redacted</Tag>
        </Space>
      ) : null}
      {attemptResponsePlan.mode ? (
        <Space size={4} wrap>
          <Tag>{attemptResponsePlanMode}</Tag>
          <Tag color={attemptResponsePlan.response_recording_state === 'blocked' ? 'red' : 'gold'}>
            response {String(attemptResponsePlan.response_recording_state || 'blocked')}
          </Tag>
          <Tag>{String(attemptResponsePlan.response_status || 'pending')}</Tag>
          <Tag>
            {String(attemptResponsePlan.success_attempt_status || 'completed')} / {String(attemptResponsePlan.retry_attempt_status || 'planned')} / {String(attemptResponsePlan.failure_attempt_status || 'failed')}
          </Tag>
          <Tag>result {String(attemptResultRecordingPlan.result_recording_state ?? 'blocked')}</Tag>
          <Tag>{String(attemptResponsePlan.dependency_unlocks_operation || 'no dependency unlock')}</Tag>
          <Tag>{String(attemptResponsePlan.provider_api_mutation || 'disabled')}</Tag>
          <Tag>body redacted</Tag>
        </Space>
      ) : null}
      {attemptCredentialPlan.mode ? (
        <Space size={4} wrap>
          <Tag>{attemptCredentialPlanMode}</Tag>
          <Tag color={attemptCredentialPlan.credential_binding_state === 'blocked' ? 'red' : 'gold'}>
            credential {String(attemptCredentialPlan.credential_binding_state || 'blocked')}
          </Tag>
          <Tag>{String(attemptCredentialPlan.credential_source_kind || 'credential source kind redacted')}</Tag>
          <Tag>{safeProviderAuthSchemeLabel(attemptCredentialPlan.auth_scheme)}</Tag>
          <Tag>{attemptCredentialPlan.token_env_name_included === true ? 'env name included' : 'env name redacted'}</Tag>
          <Tag>{attemptCredentialPlan.token_value_included === true ? 'token included' : 'token redacted'}</Tag>
          <Tag>{String(attemptCredentialPlan.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {attemptRuntimePlan.mode ? (
        <Space size={4} wrap>
          <Tag>{attemptRuntimePlanMode}</Tag>
          <Tag color={attemptRuntimePlan.runtime_state === 'blocked' ? 'red' : 'gold'}>
            runtime {String(attemptRuntimePlan.runtime_state ?? 'blocked')}
          </Tag>
          <Tag>{String(attemptRuntimePlan.adapter_kind ?? 'redacted adapter')}</Tag>
          <Tag>{String(attemptRuntimeProviderClientPlan.client_kind ?? 'redacted provider client')}</Tag>
          <Tag>{String(attemptRuntimeExecuteMethodPlan.method_name ?? 'redacted execute method')}</Tag>
          <Tag>{String(attemptRuntimeRequestBuilderPlan.builder_name ?? 'redacted builder')}</Tag>
          <Tag>{String(attemptRuntimeResponseHandlerPlan.handler_name ?? 'redacted response handler')}</Tag>
          <Tag>{attemptRuntimePlan.adapter_interface_registered === true ? 'interface registered' : 'interface missing'}</Tag>
          <Tag>{attemptRuntimePlan.live_adapter_implemented === true ? 'live adapter ready' : 'live adapter blocked'}</Tag>
          <Tag>{attemptRuntimePlan.provider_api_call_made === true ? 'api called' : 'no api call'}</Tag>
          <Tag>{String(attemptRuntimePlan.provider_api_mutation ?? 'disabled')}</Tag>
        </Space>
      ) : null}
      {attemptTransactionPlan.mode ? (
        <Space size={4} wrap>
          <Tag>{attemptTransactionPlanMode}</Tag>
          <Tag color={attemptTransactionPlan.transaction_state === 'blocked' ? 'red' : 'gold'}>
            transaction {String(attemptTransactionPlan.transaction_state ?? 'blocked')}
          </Tag>
          <Tag color={attemptTransactionPlan.transaction_metadata_ready === true ? 'green' : 'gold'}>
            metadata {attemptTransactionPlan.transaction_metadata_ready === true ? 'ready' : 'blocked'}
          </Tag>
          <Tag>
            {String(attemptTransactionPlan.claim_status_from ?? 'planned')} -&gt; {String(attemptTransactionPlan.claim_status_to ?? 'running')}
          </Tag>
          <Tag>
            {String(attemptTransactionPlan.success_attempt_status ?? 'completed')} / {String(attemptTransactionPlan.retry_attempt_status ?? 'planned')} / {String(attemptTransactionPlan.failure_attempt_status ?? 'failed')}
          </Tag>
          <Tag>call boundary {String(attemptTransactionProviderCallBoundaryPlan.provider_call_boundary_state ?? 'blocked')}</Tag>
          <Tag>{attemptTransactionPlan.provider_api_call_made === true ? 'api called' : 'no api call'}</Tag>
          <Tag>{String(attemptTransactionPlan.provider_api_mutation ?? 'disabled')}</Tag>
        </Space>
      ) : null}
      {attemptInvocationPlan.mode ? (
        <Space size={4} wrap>
          <Tag>{attemptInvocationPlanMode}</Tag>
          <Tag color={attemptInvocationPlan.invocation_state === 'blocked' ? 'red' : 'gold'}>
            invocation {String(attemptInvocationPlan.invocation_state || 'blocked')}
          </Tag>
          <Tag>{String(attemptInvocationPlan.invocation_ready_reason || 'provider_api_invocation_not_armed')}</Tag>
          <Tag>steps {attemptInvocationSequence.length}</Tag>
          <Tag>lock {String(attemptInvocationExecutionLockPlan.execution_lock_state ?? 'blocked')}</Tag>
          <Tag>activation {String(attemptInvocationActivationPlan.adapter_activation_state ?? 'blocked')}</Tag>
          <Tag>live {String(attemptInvocationLiveAdapterPlan.live_adapter_state ?? 'blocked')}</Tag>
          <Tag>send {String(attemptInvocationProviderSendPlan.provider_send_state ?? 'blocked')}</Tag>
          <Tag>retry {String(attemptInvocationRetryBackoffPlan.retry_backoff_state ?? 'blocked')}</Tag>
          <Tag>{attemptInvocationPlan.provider_api_call_made === true ? 'api called' : 'no api call'}</Tag>
          <Tag>{String(attemptInvocationPlan.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {attemptInvocationLiveAdapterContractPlan.mode ? (
        <Space size={4} wrap>
          <Tag>{String(attemptInvocationLiveAdapterContractPlan.mode).replaceAll('_', ' ')}</Tag>
          <Tag color={attemptInvocationLiveAdapterContractPlan.contract_state === 'blocked' ? 'red' : 'gold'}>
            contract {String(attemptInvocationLiveAdapterContractPlan.contract_state ?? 'blocked')}
          </Tag>
          <Tag>{String(attemptInvocationLiveAdapterContractPlan.contract_ready_reason ?? 'provider_review_live_adapter_contract_not_armed')}</Tag>
          <Tag>{attemptInvocationLiveAdapterContractPlan.contract_registered === true ? 'contract registered' : 'contract missing'}</Tag>
          <Tag>{attemptInvocationLiveAdapterContractPlan.contract_implemented === true ? 'contract spec defined' : 'contract spec pending'}</Tag>
          <Tag>inputs {attemptInvocationLiveAdapterContractInputs.length}</Tag>
          <Tag>outputs {attemptInvocationLiveAdapterContractOutputs.length}</Tag>
          <Tag>errors {attemptInvocationLiveAdapterContractErrors.length}</Tag>
          <Tag>persist {attemptInvocationLiveAdapterContractPersistedFields.length}</Tag>
          <Tag>suppressed {attemptInvocationLiveAdapterContractSuppressedFields.length}</Tag>
          <Tag>{attemptInvocationLiveAdapterContractPlan.live_adapter_contract_boundary_redacted === true ? 'boundary redacted' : 'boundary open'}</Tag>
          <Tag>{attemptInvocationLiveAdapterContractPlan.provider_api_call_made === true ? 'api called' : 'no api call'}</Tag>
          <Tag>{String(attemptInvocationLiveAdapterContractPlan.provider_api_mutation ?? 'disabled')}</Tag>
        </Space>
      ) : null}
      {attemptExecutionCandidateGates.length ? (
        <Space size={4} wrap>
          {attemptExecutionCandidateGates.map((gate: AnyRow, index: number) => (
            <Tag key={String(gate.gate || `attempt-candidate-gate-${index}`)} color={gate.status === 'ready' ? 'green' : gate.status === 'blocked' ? 'red' : 'gold'}>
              {String(gate.category || 'gate').replaceAll('_', ' ')} / {String(gate.gate || 'gate').replaceAll('_', ' ')}: {String(gate.status || 'unknown')}
            </Tag>
          ))}
        </Space>
      ) : null}
      {attemptOperations.length ? (
        <Space size={4} wrap>
          {attemptOperations.map((operation: AnyRow, index: number) => (
            <Space.Compact key={`${String(operation.id || operation.endpoint_key || operation.name || 'attempt')}-${index}`}>
              <Tag>
                {Number(operation.operation_order || index + 1)} {String(operation.endpoint_key || operation.name || 'provider.api')}: {String(operation.status || 'planned')}
                {operation.depends_on_operation ? ` after ${String(operation.depends_on_operation)}` : ''}
              </Tag>
              <Tag color={operation.dependency_status === 'dependency_failed' ? 'red' : operation.dependency_status === 'dependency_satisfied' ? 'green' : operation.dependency_status === 'waiting_for_dependency' ? 'gold' : 'default'}>
                {String(operation.dependency_status || 'independent').replaceAll('_', ' ')}
              </Tag>
              {operation.request_summary?.payload_builder ? (
                <Tag>
                  {String(operation.request_summary.payload_builder)} / {String(operation.request_summary.response_handler || 'handle_provider_response')}
                </Tag>
              ) : null}
              {operation.response_diagnostics?.mode ? (
                <Tag>
                  response {String(operation.response_diagnostics.status || 'pending')}
                  {Array.isArray(operation.response_diagnostics.retryable_status_classes) && operation.response_diagnostics.retryable_status_classes.length
                    ? ` retry ${operation.response_diagnostics.retryable_status_classes.join(',')}`
                    : ''}
                </Tag>
              ) : null}
            </Space.Compact>
          ))}
        </Space>
      ) : null}
    </Space>
  );
}

function canRetryTemplateProvision(row: AnyRow) {
  if (row.result?.repository_provisioned) return false;
  if (!row.project_id) return false;
  return row.status === 'failed' || row.status === 'completed';
}

function templateProvisionRetryTitle(row: AnyRow) {
  const details = row.result?.details || {};
  if (details.repository_exists && details.starter_push_skipped) return 'Retry after enabling allow_existing_repository_push or reconciling the repository';
  if (details.starter_push_skipped) return 'Retry after reconciling template remote protection';
  return 'Retry repository provisioning';
}

function templateRunErrorText(row: AnyRow) {
  if (row.error_message) return shortText(row.error_message, 72);
  if (row.status === 'failed' && row.result?.error) return shortText(row.result.error, 72);
  return '-';
}

function TemplateDetailModal({ template, open, setOpen }: { template?: AnyRow; open: boolean; setOpen: (v: boolean) => void }) {
  const detail = useLoad(() => open && template ? api(`/api/project-templates/${template.id}`) : Promise.resolve({}), [open, template?.id]);
  const row = detail.data || template;
  return (
    <Modal title={row?.name || 'Project template'} open={open} onCancel={() => setOpen(false)} footer={null} width={900} destroyOnHidden>
      {row && <Space direction="vertical" size={16} className="full">
        <Space wrap>
          <Tag>{row.slug}</Tag>
          <Tag>{row.version}</Tag>
          <Tag color={row.status === 'active' ? 'green' : 'default'}>{row.status}</Tag>
        </Space>
        <Typography.Paragraph>{row.description}</Typography.Paragraph>
        <Tabs items={[
          { key: 'defaults', label: 'Defaults', children: <JSONBlock value={row.defaults} /> },
          { key: 'steps', label: 'Steps', children: <JSONBlock value={row.steps} /> },
          { key: 'metadata', label: 'Metadata', children: <JSONBlock value={row.metadata} /> }
        ]} />
      </Space>}
    </Modal>
  );
}

function TemplateUseModal({ template, open, setOpen, onSubmit }: { template?: AnyRow; open: boolean; setOpen: (v: boolean) => void; onSubmit: (values: AnyRow) => Promise<any> }) {
  const [form] = Form.useForm();
  const [preview, setPreview] = useState<AnyRow>();
  const [loading, setLoading] = useState(false);
  const providerAccounts = useLoad(() => open ? api('/api/provider-accounts') : Promise.resolve({ items: [] }), [open]);
  const providerRows = providerAccounts.data?.items || [];
  const giteaAccounts = providerRows.filter((row: AnyRow) => row.provider_type === 'gitea' && row.enabled);
  const githubAccounts = providerRows.filter((row: AnyRow) => row.provider_type === 'github' && row.enabled);
  useEffect(() => {
    if (open) {
      form.resetFields();
      setPreview(undefined);
    }
  }, [open, form]);
  async function runPreview(values: AnyRow) {
    if (!template) return;
    setLoading(true);
    try {
      const parameters = templateParametersWithProviderAccounts(values, providerRows);
      const data = await api(`/api/project-templates/${template.id}/preview`, {
        method: 'POST',
        body: JSON.stringify({ ...values, parameters })
      });
      setPreview(data);
    } catch (error: any) {
      message.error(error.message);
    } finally {
      setLoading(false);
    }
  }
  async function submitTemplate(values: AnyRow) {
    try {
      const parameters = templateParametersWithProviderAccounts(values, providerRows);
      await onSubmit({ ...values, parameters });
      setOpen(false);
      setPreview(undefined);
      form.resetFields();
    } catch (error: any) {
      message.error(error instanceof SyntaxError ? 'Invalid JSON in parameters' : error.message);
    }
  }
  return (
    <Modal title={`Use template${template ? `: ${template.name}` : ''}`} open={open} onCancel={() => setOpen(false)} onOk={() => form.submit()} width={980} destroyOnHidden>
      <Space direction="vertical" size={16} className="full">
        <Form form={form} layout="vertical" onFinish={submitTemplate}>
          <Form.Item name="name" label="name" rules={fieldRules('name')}>
            <Input />
          </Form.Item>
          <Form.Item name="slug" label="slug">
            <Input />
          </Form.Item>
          <Form.Item name="description" label="description">
            <Input />
          </Form.Item>
          <Space size={12} className="full" wrap>
            <Form.Item name="gitea_provider_account_id" label="Gitea account" className="templateAccountField">
              <Select allowClear options={rowOptions(giteaAccounts)} placeholder="Source account" disabled={!giteaAccounts.length} />
            </Form.Item>
            <Form.Item name="github_provider_account_id" label="GitHub account" className="templateAccountField">
              <Select allowClear options={rowOptions(githubAccounts)} placeholder="Mirror account" disabled={!githubAccounts.length} />
            </Form.Item>
          </Space>
          <Form.Item name="parameters_json" label="parameters">
            <Input.TextArea autoSize={{ minRows: 6, maxRows: 12 }} placeholder='{"remotes":[{"remote_key":"gitea","provider_type":"gitea","remote_url":"git@example.com:org/repo.git"}],"repo_sync":{"source_remote_key":"gitea","target_remote_key":"github"}}' />
          </Form.Item>
          <Button onClick={() => runPreview(form.getFieldsValue())} loading={loading}>Preview</Button>
        </Form>
        {preview && <Tabs items={[
          { key: 'summary', label: 'Summary', children: <Space direction="vertical" size={8}>
            <Typography.Text>Project: {preview.project?.name} / {preview.project?.slug}</Typography.Text>
            <Typography.Text>Repository: {preview.repository?.repo_key} ({preview.repository?.default_branch})</Typography.Text>
            <Typography.Text>Remotes: {Array.isArray(preview.remotes) ? preview.remotes.length : 0}</Typography.Text>
            <Typography.Text>RepoSync: <Tag>{preview.repo_sync?.status}</Tag> {preview.repo_sync?.reason}</Typography.Text>
            <Typography.Text>Files: {Array.isArray(preview.files) ? preview.files.length : 0}</Typography.Text>
          </Space> },
          { key: 'files', label: 'Files', children: <JSONBlock value={preview.files} /> },
          { key: 'steps', label: 'Steps', children: <JSONBlock value={preview.steps} /> },
          { key: 'defaults', label: 'Defaults', children: <JSONBlock value={preview.defaults} /> },
          { key: 'parameters', label: 'Parameters', children: <JSONBlock value={preview.parameters} /> }
        ]} />}
      </Space>
    </Modal>
  );
}

function ProviderAccounts() {
  const accounts = useLoad(() => api('/api/provider-accounts'), []);
  const tokenRotationSummary = accounts.data?.token_rotation_summary || {};
  const tokenRotationPlan = accounts.data?.token_rotation_plan || {};
  const tokenRotationPlanByID = providerAutoRotationPlanByID(tokenRotationPlan);
  const [open, setOpen] = useState(false);
  const [checkingID, setCheckingID] = useState('');
  const [rotatingID, setRotatingID] = useState('');
  const [rotateForm] = Form.useForm();
  async function createAccount(values: AnyRow) {
    await api('/api/provider-accounts', {
      method: 'POST',
      body: JSON.stringify({
        ...values,
        enabled: values.enabled !== 'false',
        metadata: parseJSONField(values.metadata_json)
      })
    });
    message.success('Provider account created');
    accounts.reload();
  }
  async function checkAccount(id: string) {
    setCheckingID(id);
    try {
      const res = await api(`/api/provider-accounts/${id}/check`, { method: 'POST' });
      message[res.check?.status === 'ok' ? 'success' : 'warning'](res.check?.message || 'Provider check completed');
      accounts.reload();
    } finally {
      setCheckingID('');
    }
  }
  function openRotateToken(row: AnyRow) {
    setRotatingID(row.id);
    rotateForm.setFieldsValue({ token_env: '', reason: '' });
  }
  async function rotateTokenEnv(values: AnyRow) {
    if (!rotatingID) return;
    await api(`/api/provider-accounts/${rotatingID}/rotate-token-env`, {
      method: 'POST',
      body: JSON.stringify({ token_env: values.token_env, reason: values.reason || '' })
    });
    message.success('Provider token env rotated');
    setRotatingID('');
    rotateForm.resetFields();
    accounts.reload();
  }
  async function executeReadyTokenRotations() {
    try {
      const result = await api('/api/provider-accounts/execute-token-rotation-plan', {
        method: 'POST',
        body: JSON.stringify({ reason: 'operator executed ready provider token rotation plan' })
      });
      message.success(`Rotated ${result.rotated_count || 0} provider account${result.rotated_count === 1 ? '' : 's'}`);
    } catch (err: any) {
      message.error(err?.message || 'Provider token rotation failed');
    } finally {
      accounts.reload();
    }
  }
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="Provider Accounts" onCreate={() => setOpen(true)} />
      <Space wrap>
        <Tag>{tokenRotationSummary.total || 0} accounts</Tag>
        {providerTokenRotationSummaryTags(tokenRotationSummary).map((item) => <Tag key={item.key} color={item.color}>{item.label}</Tag>)}
        <Typography.Text type={tokenRotationSummary.action_required ? 'danger' : 'secondary'}>{tokenRotationSummary.next_action || 'No provider accounts configured.'}</Typography.Text>
      </Space>
      <Space wrap>
        {providerAutoRotationPlanTags(tokenRotationPlan).map((item) => <Tag key={item.key} color={item.color}>{item.label}</Tag>)}
        <Typography.Text type={tokenRotationPlan.blocked ? 'danger' : 'secondary'}>{tokenRotationPlan.next_action || 'No automated rotation plan available.'}</Typography.Text>
        <Button size="small" onClick={executeReadyTokenRotations} disabled={!Number(tokenRotationPlan.ready || 0)}>Execute ready rotations</Button>
      </Space>
      <Table<AnyRow> rowKey="id" dataSource={accounts.data?.items || []} pagination={{ pageSize: 10 }} columns={[
        { title: 'Name', dataIndex: 'name' },
        { title: 'Provider', dataIndex: 'provider_type' },
        { title: 'API base', dataIndex: 'api_base_url' },
        { title: 'Owner', dataIndex: 'default_owner' },
        { title: 'Visibility', dataIndex: 'visibility' },
        { title: 'Token env', dataIndex: 'masked_token_env' },
        {
          title: 'Rotation',
          render: (_, row) => {
            const rotation = providerTokenRotationSummary(row);
            return (
              <Space direction="vertical" size={2}>
                <Tag color={rotation.color}>{rotation.label}</Tag>
                {rotation.detail ? <Typography.Text type="secondary">{rotation.detail}</Typography.Text> : null}
              </Space>
            );
          }
        },
        {
          title: 'Auto rotation',
          render: (_, row) => {
            const rotation = providerAutoRotationStatus(row, tokenRotationPlanByID);
            return (
              <Space direction="vertical" size={2}>
                <Tag color={rotation.color}>{rotation.label}</Tag>
                {rotation.candidate ? <Typography.Text type="secondary">{rotation.candidate}</Typography.Text> : null}
                {rotation.next ? <Typography.Text type="secondary">{shortText(rotation.next, 72)}</Typography.Text> : null}
              </Space>
            );
          }
        },
        {
          title: 'Check',
          render: (_, row) => {
            const check = row.metadata?.provider_check || {};
            const ok = check.status === 'ok';
            return (
              <Space direction="vertical" size={2}>
                <Tag color={ok ? 'green' : check.status ? 'red' : 'default'}>{check.status || 'unchecked'}</Tag>
                {check.actor ? <Typography.Text type="secondary">{check.actor}</Typography.Text> : null}
                {check.http_status ? <Typography.Text type="secondary">HTTP {check.http_status}</Typography.Text> : null}
                {check.message ? <Typography.Text type="secondary">{check.message}</Typography.Text> : null}
              </Space>
            );
          }
	        },
	        { title: 'Status', render: (_, row) => <Tag color={row.enabled ? 'green' : 'default'}>{row.enabled ? 'enabled' : 'disabled'}</Tag> },
	        { title: 'Updated', dataIndex: 'updated_at' },
	        { title: 'Action', render: (_, row) => <Space><Button size="small" onClick={() => checkAccount(row.id)} loading={checkingID === row.id}>Check</Button><Button size="small" onClick={() => openRotateToken(row)}>Rotate</Button></Space> }
	      ]} />
	      <CreateModal
	        title="Create provider account"
        open={open}
        setOpen={setOpen}
	        fields={['name', 'provider_type', 'api_base_url', 'web_base_url', 'token_env', 'default_owner', 'visibility', 'enabled', 'metadata_json']}
	        onSubmit={createAccount}
	      />
      <Modal title="Rotate provider token env" open={Boolean(rotatingID)} onCancel={() => setRotatingID('')} onOk={() => rotateForm.submit()} destroyOnHidden>
        <Form form={rotateForm} layout="vertical" onFinish={rotateTokenEnv}>
          <Form.Item name="token_env" label="token env" rules={[{ required: true, message: 'token env is required' }]}>
            <Input />
          </Form.Item>
          <Form.Item name="reason" label="reason">
            <Input />
          </Form.Item>
        </Form>
      </Modal>
	    </Space>
	  );
	}

function AssetCenter() {
	const projects = useLoad(() => api('/api/projects'), []);
	const projectRows = projects.data?.items || [];
	const projectPick = useSelectedRow(projectRows);
	const project = projectPick.selected;
	const assetViews = useLoad(() => api('/api/asset-graph-views'), []);
	const [assetType, setAssetType] = useState<string>();
	const [assetSearch, setAssetSearch] = useState('');
	const [assetViewID, setAssetViewID] = useState<string>();
	const [assetViewName, setAssetViewName] = useState('');
	const [relationForm] = Form.useForm();
	const debouncedAssetSearch = useDebouncedValue(assetSearch, 300);
	const assetParams = new URLSearchParams();
	if (assetType) assetParams.set('asset_type', assetType);
	if (debouncedAssetSearch.trim()) assetParams.set('q', debouncedAssetSearch.trim());
	const assetQuery = assetParams.toString() ? `?${assetParams.toString()}` : '';
	const assets = useLoad(() => project ? api(`/api/projects/${project.id}/assets${assetQuery}`) : api(`/api/assets${assetQuery}`), [project?.id, assetType, debouncedAssetSearch]);
	const graphParams = new URLSearchParams(assetParams);
	if (project) graphParams.set('project_id', project.id);
	graphParams.set('limit', '80');
	const graphQuery = graphParams.toString() ? `?${graphParams.toString()}` : '';
	const searchGraphResult = useLoad(() => api(`/api/assets/graph${graphQuery}`), [project?.id, assetType, debouncedAssetSearch]);
	const assetRows = assets.data?.items || [];
	const assetPick = useSelectedRow(assetRows);
	const relationQuery = project ? `?project_id=${encodeURIComponent(project.id)}` : '';
	const relations = useLoad(() => assetPick.selectedID ? api(`/api/assets/${encodeURIComponent(assetPick.selectedID)}/relations${relationQuery}`) : Promise.resolve({ items: [] }), [assetPick.selectedID, project?.id]);
	const statusSnapshots = useLoad(() => assetPick.selectedID ? api(`/api/assets/${encodeURIComponent(assetPick.selectedID)}/status-snapshots`) : Promise.resolve({ items: [] }), [assetPick.selectedID]);
	const downstream = useLoad(() => assetPick.selectedID ? api(assetDependencyPath(assetPick.selectedID, 'downstream', project?.id)) : Promise.resolve({ items: [] }), [assetPick.selectedID, project?.id]);
	const upstream = useLoad(() => assetPick.selectedID ? api(assetDependencyPath(assetPick.selectedID, 'upstream', project?.id)) : Promise.resolve({ items: [] }), [assetPick.selectedID, project?.id]);
	const selectedGraph = buildAssetGraph(assetPick.selected, assetRows, relations.data?.items || []);
	const searchGraph = buildAssetSearchGraph(searchGraphResult.data?.nodes || [], searchGraphResult.data?.edges || []);
	const searchGraphSummary = assetGraphRankingSummary(searchGraphResult.data?.nodes || [], searchGraphResult.data?.edges || [], Boolean(searchGraphResult.data?.truncated));
	const dependencyColumns = [
		{ title: 'Depth', dataIndex: 'depth' },
		{ title: 'From', dataIndex: 'from_asset_id' },
		{ title: 'Relation', render: (_: unknown, row: AnyRow) => <Tag color="geekblue">{row.relation_type}</Tag> },
		{ title: 'To', dataIndex: 'to_asset_id' },
		{ title: 'Path', render: (_: unknown, row: AnyRow) => <Typography.Paragraph className="mono-pre">{row.path_text}</Typography.Paragraph> }
	];
	const dependencyRowKey = (row: AnyRow) => `${row.id}:${row.depth}:${row.current_asset_id}:${String(row.path_text || '').length}`;
	const dependencyAlert = (result: { data: AnyRow | null; error: string }) => (
		<>
			{result.error && <Alert showIcon type="error" message={result.error} />}
			{result.data?.truncated && <Alert showIcon type="warning" message="Results are truncated. Select a project or reduce depth to narrow the path search." />}
		</>
	);
	function currentAssetViewFilters() {
		return {
			project_id: project?.id || '',
			asset_type: assetType || '',
			q: assetSearch.trim(),
			selected_asset_id: assetPick.selectedID || ''
		};
	}
	function applyAssetView(id?: string) {
		setAssetViewID(id);
		if (!id) return;
		const view = (assetViews.data?.items || []).find((row: AnyRow) => row.id === id);
		const filters = view?.filters || {};
		setAssetViewName(view?.name || '');
		if (filters.project_id && projectRows.some((row: AnyRow) => row.id === filters.project_id)) {
			projectPick.setSelectedID(String(filters.project_id));
		}
		setAssetType(filters.asset_type ? String(filters.asset_type) : undefined);
		setAssetSearch(String(filters.q || ''));
		if (filters.selected_asset_id) {
			assetPick.setSelectedID(String(filters.selected_asset_id));
		}
	}
	async function saveAssetView() {
		const name = assetViewName.trim();
		if (!name) {
			message.warning('View name is required');
			return;
		}
		try {
			const view = await api('/api/asset-graph-views', { method: 'POST', body: JSON.stringify({ name, filters: currentAssetViewFilters() }) });
			message.success('Asset view saved');
			setAssetViewID(view.id);
			assetViews.reload();
		} catch (err: any) {
			message.error(err.message || 'Could not save asset view');
		}
	}
	async function updateAssetView() {
		if (!assetViewID) return;
		const name = assetViewName.trim();
		try {
			const view = await api(`/api/asset-graph-views/${assetViewID}`, { method: 'PATCH', body: JSON.stringify({ name, filters: currentAssetViewFilters() }) });
			message.success('Asset view updated');
			setAssetViewName(view.name || name);
			assetViews.reload();
		} catch (err: any) {
			message.error(err.message || 'Could not update asset view');
		}
	}
	function deleteAssetView() {
		if (!assetViewID) return;
		Modal.confirm({
			title: 'Delete asset view?',
			okText: 'Delete',
			okButtonProps: { danger: true },
			onOk: async () => {
				await api(`/api/asset-graph-views/${assetViewID}`, { method: 'DELETE' });
				message.success('Asset view deleted');
				setAssetViewID(undefined);
				setAssetViewName('');
				assetViews.reload();
			}
		});
	}
	async function createAssetRelation(values: AnyRow) {
		await api('/api/asset-relations', { method: 'POST', body: JSON.stringify(values) });
		message.success('Asset relation saved');
		relationForm.resetFields();
		relations.reload();
		downstream.reload();
		upstream.reload();
	}
	function deleteAssetRelation(row: AnyRow) {
		Modal.confirm({
			title: 'Delete manual relation?',
			okText: 'Delete',
			okButtonProps: { danger: true },
			onOk: async () => {
				await api(`/api/asset-relations/${row.id}`, { method: 'DELETE' });
				message.success('Asset relation deleted');
				relations.reload();
				downstream.reload();
				upstream.reload();
			}
		});
	}
	const typeOptions = [
		'project',
		'project_template',
		'template_file',
		'repository',
		'git_remote',
		'repo_sync',
		'webhook_connection',
		'pipeline_run',
		'host',
		'argo_connection',
		'deployment_target',
		'deployment_record',
		'rollback_point',
		'argo_app',
		'ai_runtime',
		'node_agent'
	].map((value) => ({ value, label: value.replaceAll('_', ' ') }));
	return (
		<Space direction="vertical" size={16} className="full">
			<Typography.Title level={2}>Assets</Typography.Title>
			<Space wrap>
				<Select allowClear value={assetViewID} placeholder="Saved graph view" style={{ width: 220 }} onChange={(value) => applyAssetView(value)} options={(assetViews.data?.items || []).map((row: AnyRow) => ({ value: row.id, label: row.name }))} />
				<Input placeholder="View name" value={assetViewName} onChange={(event) => setAssetViewName(event.target.value)} style={{ width: 180 }} />
				<Button onClick={saveAssetView}>Save view</Button>
				<Button disabled={!assetViewID} onClick={updateAssetView}>Update view</Button>
				<Button danger disabled={!assetViewID} onClick={deleteAssetView}>Delete view</Button>
			</Space>
			<div className="selectorRow">
				<EntitySelect label="Project" rows={projectRows} value={projectPick.selectedID} onChange={projectPick.setSelectedID} />
				<Space direction="vertical" size={4} className="selector">
					<Typography.Text type="secondary">Asset type</Typography.Text>
					<Select allowClear value={assetType} onChange={setAssetType} options={typeOptions} placeholder="All assets" />
				</Space>
				<Space direction="vertical" size={4} className="selector">
					<Typography.Text type="secondary">Search</Typography.Text>
					<Input allowClear value={assetSearch} onChange={(event) => setAssetSearch(event.target.value)} placeholder="Name, source, external id" />
				</Space>
			</div>
			<Table<AnyRow> rowKey="id" dataSource={assetRows} pagination={{ pageSize: 10 }} onRow={(row) => ({ onClick: () => assetPick.setSelectedID(row.id) })} rowClassName={(row) => row.id === assetPick.selectedID ? 'selectedRow' : ''} columns={[
				{ title: 'Name', dataIndex: 'name' },
				{ title: 'Type', render: (_, row) => <Tag>{String(row.asset_type || '').replaceAll('_', ' ')}</Tag> },
				{ title: 'Status', render: (_, row) => <Tag color={row.status === 'failed' || row.status === 'OutOfSync' ? 'red' : row.status === 'completed' || row.status === 'Synced' || row.status === 'active' ? 'green' : 'blue'}>{row.status || 'unknown'}</Tag> },
				{ title: 'Source', dataIndex: 'source' },
				{ title: 'External ID', dataIndex: 'external_id' },
				{ title: 'Updated', dataIndex: 'updated_at' }
			]} />
			<Typography.Title level={3}>Search graph</Typography.Title>
			{searchGraphResult.error && <Alert showIcon type="error" message={searchGraphResult.error} />}
			{searchGraphResult.data?.truncated && <Alert showIcon type="warning" message="Graph results are truncated. Select a project, asset type, or search term to narrow the view." />}
			<Space wrap>
				<Tag>{searchGraphSummary.nodesLabel}</Tag>
				<Tag>{searchGraphSummary.edges} visible edges</Tag>
				<Tag color="geekblue">{searchGraphSummary.topLabel}</Tag>
			</Space>
			<AssetRelationGraph graph={searchGraph} />
			<Typography.Title level={3}>Relations</Typography.Title>
			<Form form={relationForm} layout="inline" onFinish={createAssetRelation}>
				<Form.Item name="from_asset_id" rules={[{ required: true, message: 'from asset is required' }]}>
					<Select showSearch placeholder="From asset" style={{ width: 260 }} optionFilterProp="label" options={rowOptions(assetRows, 'name')} />
				</Form.Item>
				<Form.Item name="relation_type" rules={[{ required: true, message: 'relation type is required' }]}>
					<Input placeholder="relation type" style={{ width: 180 }} />
				</Form.Item>
				<Form.Item name="to_asset_id" rules={[{ required: true, message: 'to asset is required' }]}>
					<Select showSearch placeholder="To asset" style={{ width: 260 }} optionFilterProp="label" options={rowOptions(assetRows, 'name')} />
				</Form.Item>
				<Button htmlType="submit" type="primary">Save relation</Button>
			</Form>
				<Table<AnyRow> rowKey="id" dataSource={relations.data?.items || []} pagination={{ pageSize: 8 }} columns={[
				{ title: 'From', dataIndex: 'from_asset_id' },
				{ title: 'Relation', render: (_, row) => <Tag color="geekblue">{row.relation_type}</Tag> },
				{ title: 'To', dataIndex: 'to_asset_id' },
				{ title: 'Source', render: (_, row) => row.metadata?.source || row.source || 'system' },
				{ title: 'Created', dataIndex: 'created_at' },
				{ title: 'Action', render: (_, row) => row.metadata?.source === 'manual' ? <Button size="small" danger onClick={() => deleteAssetRelation(row)}>Delete</Button> : null }
			]} />
			<Typography.Title level={3}>Status history</Typography.Title>
			<Table<AnyRow> rowKey="id" loading={statusSnapshots.loading} dataSource={statusSnapshots.data?.items || []} pagination={{ pageSize: 5 }} columns={[
				{ title: 'Status', render: (_, row) => <Tag color={row.status === 'failed' || row.status === 'OutOfSync' ? 'red' : row.status === 'completed' || row.status === 'Synced' || row.status === 'active' ? 'green' : 'blue'}>{row.status || 'unknown'}</Tag> },
				{ title: 'Health', render: (_, row) => <Tag>{row.health || '-'}</Tag> },
				{ title: 'Summary', render: (_, row) => shortText(row.summary, 90) },
				{ title: 'Collected', dataIndex: 'collected_at' }
			]} />
			<Typography.Title level={3}>Selected asset graph</Typography.Title>
			<AssetRelationGraph graph={selectedGraph} />
			<Typography.Title level={3}>Paths</Typography.Title>
			<Tabs items={[
				{
					key: 'downstream',
					label: 'Downstream',
					children: <Space direction="vertical" size={12} className="full">{dependencyAlert(downstream)}<Table<AnyRow> rowKey={dependencyRowKey} loading={downstream.loading} dataSource={downstream.data?.items || []} pagination={{ pageSize: 6 }} columns={dependencyColumns} /></Space>
				},
				{
					key: 'upstream',
					label: 'Upstream',
					children: <Space direction="vertical" size={12} className="full">{dependencyAlert(upstream)}<Table<AnyRow> rowKey={dependencyRowKey} loading={upstream.loading} dataSource={upstream.data?.items || []} pagination={{ pageSize: 6 }} columns={dependencyColumns} /></Space>
				}
			]} />
		</Space>
	);
}

function AssetRelationGraph({ graph }: { graph: AnyRow }) {
  const nodes: AnyRow[] = graph.nodes || [];
  const edges: AnyRow[] = graph.edges || [];
  const byID = new Map(nodes.map((node) => [node.id, node]));
  if (!nodes.length) {
    return <div className="assetGraphEmpty"><Typography.Text type="secondary">Select an asset</Typography.Text></div>;
  }
  return (
    <div className="assetGraph">
      <svg viewBox={`0 0 800 ${graph.height || 260}`} role="img" aria-label="Asset relation graph">
        <defs>
          <marker id="assetArrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M 0 0 L 10 5 L 0 10 z" fill="#94a3b8" />
          </marker>
        </defs>
        {edges.map((edge) => {
          const from = byID.get(edge.from);
          const to = byID.get(edge.to);
          if (!from || !to) return null;
          const x1 = from.x + 84;
          const y1 = from.y;
          const x2 = to.x - 84;
          const y2 = to.y;
          const midX = (x1 + x2) / 2;
          return (
            <g key={edge.id}>
              <path d={`M ${x1} ${y1} C ${midX} ${y1}, ${midX} ${y2}, ${x2} ${y2}`} className="assetGraphEdge" markerEnd="url(#assetArrow)" />
              <text x={midX} y={(y1 + y2) / 2 - 8} className="assetGraphEdgeLabel">{edge.relation_type}</text>
            </g>
          );
        })}
        {nodes.map((node) => (
          <g key={node.id} transform={`translate(${node.x - 84}, ${node.y - 28})`}>
            <rect width="168" height="56" rx="8" className={node.external ? 'assetGraphNodeExternal' : node.column === 'center' ? 'assetGraphNodeSelected' : 'assetGraphNode'} />
            <circle cx="18" cy="18" r="6" fill={graphNodeColor(node.asset_type)} />
            <text x="32" y="21" className="assetGraphNodeTitle">{graphLabel(node.label)}</text>
            <text x="18" y="42" className="assetGraphNodeMeta">{String(node.asset_type).replaceAll('_', ' ')}</text>
          </g>
        ))}
      </svg>
    </div>
  );
}

function ProjectDetail() {
	const projects = useLoad(() => api('/api/projects'), []);
  const projectRows = projects.data?.items || [];
  const projectPick = useSelectedRow(projectRows);
  const project = projectPick.selected;
  const repos = useLoad(() => project ? api(`/api/projects/${project.id}/git-repositories`) : Promise.resolve({ items: [] }), [project?.id]);
  const repoRows = repos.data?.items || [];
  const repoIDs = repoRows.map((row: AnyRow) => row.id).join(',');
  const projectRemotes = useLoad(() => {
    if (!repoRows.length) return Promise.resolve({ items: [] });
    return Promise.all(repoRows.map((repo: AnyRow) => api(`/api/git-repositories/${repo.id}/remotes`).then((result) => (result.items || []).map((remote: AnyRow) => ({ ...remote, repository_id: repo.id, repository_key: repo.repo_key || repo.name }))))).then((groups) => ({ items: groups.flat() }));
  }, [project?.id, repoIDs]);
  const versions = useLoad(() => project ? api(`/api/projects/${project.id}/versions`) : Promise.resolve({ items: [] }), [project?.id]);
  const [repoOpen, setRepoOpen] = useState(false);
  const [versionOpen, setVersionOpen] = useState(false);
  const [versionValidation, setVersionValidation] = useState<AnyRow>();
  const [versionRefreshResult, setVersionRefreshResult] = useState<AnyRow>();
  const [versionValidationRerunResult, setVersionValidationRerunResult] = useState<AnyRow>();
  const [versionValidationAutoReload, setVersionValidationAutoReload] = useState<AnyRow>();
  const versionValidationAutoReloadAttempts = useRef(0);
  const [validatingVersionID, setValidatingVersionID] = useState<string>();
  const [refreshingVersionID, setRefreshingVersionID] = useState<string>();
  const [rerunningValidationID, setRerunningValidationID] = useState<string>();
  const [recordingValidationSnapshotID, setRecordingValidationSnapshotID] = useState<string>();
  const [configPinningVersionID, setConfigPinningVersionID] = useState<string>();
  const [configInitializing, setConfigInitializing] = useState(false);
  const [configWorkflowRequesting, setConfigWorkflowRequesting] = useState(false);
  const [configWorkflowResult, setConfigWorkflowResult] = useState<AnyRow>();
  const [configPinResult, setConfigPinResult] = useState<AnyRow>();
  const [versionSnapshotResult, setVersionSnapshotResult] = useState<AnyRow>();
  const configRepo = repoRows.find((row: AnyRow) => row.repo_role === 'config');
  const configScaffold = useLoad(() => configRepo ? api(`/api/git-repositories/${configRepo.id}/config-scaffold`) : Promise.resolve(undefined), [configRepo?.id]);
  async function initializeConfigRepo() {
    if (!project || configRepo || repos.loading || configInitializing) return;
    setConfigInitializing(true);
    try {
      await api(`/api/projects/${project.id}/git-repositories`, {
        method: 'POST',
        body: JSON.stringify({
          name: 'Config Repository',
          repo_key: 'config',
          display_name: 'Config',
          repo_role: 'config',
          description: 'Environment configuration repository for envs/dev, envs/test, and envs/prod.',
          default_branch: 'main'
        })
      });
      message.success('Config repository initialized');
      repos.reload();
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setConfigInitializing(false);
    }
  }
  async function requestConfigGitWorkflow() {
    if (!configRepo || configWorkflowRequesting) return;
    setConfigWorkflowRequesting(true);
    try {
      const result = await api(`/api/git-repositories/${configRepo.id}/config-scaffold/request-git-workflow`, { method: 'POST', body: '{}' });
      setConfigWorkflowResult(result);
      configScaffold.reload();
      message.success(result.approval ? 'Config workflow approval requested' : 'Config workflow audit queued');
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setConfigWorkflowRequesting(false);
    }
  }
  async function createVersion(values: AnyRow) {
    if (!project) return;
    const metadata = projectVersionMetadata(values, repoRows, projectRemotes.data?.items || []);
    if (!Array.isArray(metadata.repositories) || metadata.repositories.length === 0) {
      throw new Error('Add at least one repository manifest item');
    }
    await api(`/api/projects/${project.id}/versions`, {
      method: 'POST',
      body: JSON.stringify({
        version: values.version,
        source: values.source || 'manual',
        metadata
      })
    });
    versions.reload();
  }
  async function validateVersion(row: AnyRow) {
    setValidatingVersionID(row.id);
    try {
      const result = await api(`/api/project-versions/${row.id}/validation`);
      setVersionValidation(result);
      setVersionRefreshResult(undefined);
      message.success('Version validation preview ready');
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setValidatingVersionID(undefined);
    }
  }
  async function refreshVersion(row: AnyRow) {
    setRefreshingVersionID(row.id);
    try {
      const result = await api(`/api/project-versions/${row.id}/refresh`, { method: 'POST', body: '{}' });
      setVersionRefreshResult(result);
      setVersionValidationAutoReload({
        version_id: row.id,
        status: 'polling',
        attempts: 0,
        active_count: result.operation_count || 0,
        operation_count: result.operation_count || 0,
        validation_rerun_status: 'waiting_for_workers'
      });
      versionValidationAutoReloadAttempts.current = 0;
      message.success('Version refresh operations queued');
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setRefreshingVersionID(undefined);
    }
  }
  async function requestValidationRerun(row: AnyRow) {
    setRerunningValidationID(row.id);
    try {
      const result = await api(`/api/project-versions/${row.id}/validation-rerun`, { method: 'POST', body: '{}' });
      setVersionValidationRerunResult(result);
      setVersionValidationAutoReload({
        version_id: row.id,
        status: 'polling',
        attempts: 0,
        active_count: result.background_worker_enqueued ? 1 : 0,
        operation_count: result.operation_enqueued ? 1 : 0,
        validation_rerun_status: result.background_worker_enqueued ? 'waiting_for_workers' : 'not_requested'
      });
      versionValidationAutoReloadAttempts.current = 0;
      if (versionValidation?.version_id === row.id) {
        const validation = await api(`/api/project-versions/${row.id}/validation`);
        setVersionValidation(validation);
      }
      message.success(result.background_worker_enqueued ? 'Validation rerun queued' : 'Validation rerun reviewed');
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setRerunningValidationID(undefined);
    }
  }
  async function pinConfigCommit(row: AnyRow) {
    if (!configRepo || configPinningVersionID) return;
    setConfigPinningVersionID(row.id);
    try {
      const result = await api(`/api/project-versions/${row.id}/pin-config-commit`, {
        method: 'POST',
        body: JSON.stringify({ repository_id: configRepo.id })
      });
      const safeResult = sanitizedConfigPinResult(result);
      setConfigPinResult(safeResult);
      versions.reload();
      configScaffold.reload();
      if (versionValidation?.version_id === row.id) {
        const validation = await api(`/api/project-versions/${row.id}/validation`);
        setVersionValidation(validation);
      }
      message.success(safeResult.project_version_metadata_written ? 'Config pin written' : 'Config pin already recorded');
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setConfigPinningVersionID(undefined);
    }
  }
  async function recordValidationSnapshot(row: AnyRow) {
    setRecordingValidationSnapshotID(row.id);
    try {
      const result = await api(`/api/project-versions/${row.id}/validation-snapshot`, {
        method: 'POST',
        body: JSON.stringify({ dry_run: false })
      });
      const safeResult = sanitizedValidationSnapshotResult(result);
      setVersionSnapshotResult(safeResult);
      if (versionValidation?.version_id === row.id) {
        const validation = await api(`/api/project-versions/${row.id}/validation`);
        setVersionValidation(validation);
      }
      message.success(safeResult.validation_snapshot_written ? 'Validation snapshot recorded' : 'Validation snapshot already current');
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setRecordingValidationSnapshotID(undefined);
    }
  }
  async function autoRecordValidationSnapshot(versionID: string) {
    setRecordingValidationSnapshotID(versionID);
    try {
      const result = await api(`/api/project-versions/${versionID}/validation-rerun-snapshot`, {
        method: 'POST',
        body: JSON.stringify({ dry_run: false })
      });
      const safeResult = sanitizedValidationSnapshotResult(result);
      setVersionSnapshotResult(safeResult);
      return safeResult;
    } finally {
      setRecordingValidationSnapshotID(undefined);
    }
  }
  useEffect(() => {
    const versionID = versionValidationAutoReload?.version_id;
    if (!versionID || versionValidationAutoReload?.status !== 'polling') return;
    let alive = true;
    versionValidationAutoReloadAttempts.current = Math.max(versionValidationAutoReloadAttempts.current, Number(versionValidationAutoReload?.attempts || 0));
    let timer: number | undefined;
    const poll = async () => {
      versionValidationAutoReloadAttempts.current += 1;
      const attempts = versionValidationAutoReloadAttempts.current;
      try {
        const result = await api(`/api/project-versions/${versionID}/validation`);
        if (!alive) return;
        setVersionValidation(result);
        const summary = result.provider_refresh_result_summary || {};
        const activeCount = Number(summary.active_count || 0);
        const operationCount = Number(summary.operation_count || 0);
        const rerunStatus = summary.validation_rerun_status || 'not_requested';
        if (operationCount > 0 && activeCount === 0) {
          const finalStatus = rerunStatus === 'recorded' ? 'completed' : rerunStatus === 'refresh_failed' ? 'done_with_errors' : rerunStatus === 'refresh_canceled' ? 'canceled' : 'unknown';
          setVersionValidationAutoReload({
            version_id: versionID,
            status: finalStatus,
            attempts,
            active_count: activeCount,
            operation_count: operationCount,
            validation_rerun_status: rerunStatus,
            last_checked_at: new Date().toISOString()
          });
          if (timer !== undefined) window.clearInterval(timer);
          if (rerunStatus === 'recorded') {
            try {
              const snapshot = await autoRecordValidationSnapshot(versionID);
              if (snapshot.recording_ready === false) {
                message.warning(snapshot.message || 'Version validation refreshed, but snapshot recording is not ready yet');
              } else {
                message.success(snapshot.validation_snapshot_written ? 'Version validation refreshed and snapshot recorded' : 'Version validation refreshed; snapshot already current');
              }
            } catch (error: any) {
              message.warning(error.message || 'Version validation refreshed, but snapshot recording failed');
            }
          } else if (rerunStatus === 'refresh_failed') {
            message.error('Version refresh finished with failed operations');
          } else if (rerunStatus === 'refresh_canceled') {
            message.warning('Version refresh was canceled');
          }
          return;
        }
        if (attempts >= 60) {
          setVersionValidationAutoReload({
            version_id: versionID,
            status: 'timeout',
            attempts,
            active_count: activeCount,
            operation_count: operationCount,
            validation_rerun_status: rerunStatus,
            last_checked_at: new Date().toISOString()
          });
          message.warning('Version refresh is still running. Validation was refreshed with the latest observed state.');
          if (timer !== undefined) window.clearInterval(timer);
          return;
        }
        setVersionValidationAutoReload({
          version_id: versionID,
          status: 'polling',
          attempts,
          active_count: activeCount,
          operation_count: operationCount,
          validation_rerun_status: rerunStatus,
          last_checked_at: new Date().toISOString()
        });
      } catch (error: any) {
        if (!alive) return;
        setVersionValidationAutoReload({
          version_id: versionID,
          status: 'error',
          attempts,
          error: error.message || 'Request failed',
          last_checked_at: new Date().toISOString()
        });
        message.error(error.message || 'Validation refresh failed');
        if (timer !== undefined) window.clearInterval(timer);
      }
    };
    poll();
    timer = window.setInterval(poll, 2000);
    return () => {
      alive = false;
      if (timer !== undefined) window.clearInterval(timer);
    };
  }, [versionValidationAutoReload?.version_id, versionValidationAutoReload?.status]);
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>Project Detail</Typography.Title>
      <EntitySelect label="Project" rows={projectRows} value={projectPick.selectedID} onChange={projectPick.setSelectedID} />
      {!project && <Alert type="info" showIcon message="Create a project first." />}
      {project && (
        <>
          <Card title={project.name} extra={<Button onClick={() => api(`/api/projects/${project.id}/context/generate`, { method: 'POST' }).then(() => message.success('Context generated'))}>Generate context</Button>}>
            <Typography.Paragraph>{project.description || 'No description'}</Typography.Paragraph>
          </Card>
          <div className="toolbar">
            <Typography.Title level={2}>Git repositories</Typography.Title>
            <Space>
              <Button onClick={initializeConfigRepo} disabled={Boolean(configRepo) || repos.loading} loading={configInitializing} icon={<SettingOutlined />}>{configRepo ? 'Config ready' : 'Init config'}</Button>
              <Button type="primary" onClick={() => setRepoOpen(true)}>Create</Button>
            </Space>
          </div>
          <Table<AnyRow> rowKey="id" dataSource={repos.data?.items || []} pagination={false} columns={[
            { title: 'Name', dataIndex: 'name' },
            { title: 'Key', dataIndex: 'repo_key' },
            { title: 'Role', render: (_, row) => <Tag color={row.repo_role === 'config' ? 'geekblue' : 'default'}>{row.repo_role || 'code'}</Tag> },
            { title: 'Status', render: (_, row) => <Tag>{row.status || 'active'}</Tag> },
            { title: 'Default branch', dataIndex: 'default_branch' }
          ]} />
          {configScaffold.data && (
            <Card title="Config scaffold preview" loading={configScaffold.loading}>
              <Space direction="vertical" size={8} className="full">
                <Space wrap>
                  <Tag color={configScaffold.data.scaffold_state === 'ready' ? 'green' : 'red'}>{configScaffold.data.scaffold_state || 'blocked'}</Tag>
                  <Tag>{configScaffold.data.file_count || 0} files</Tag>
                  <Tag>{configScaffold.data.remote_count || 0} remotes</Tag>
                  <Tag>{configScaffold.data.git_write_performed ? 'git write' : 'no git write'}</Tag>
                  <Tag>{configScaffold.data.external_call_made ? 'external call' : 'no external call'}</Tag>
                  <Tag>{configScaffold.data.file_content_included ? 'content included' : 'paths only'}</Tag>
                  {configScaffold.data.git_commit_plan ? <Tag color={configScaffold.data.git_commit_plan.plan_state === 'planned' ? 'blue' : 'red'}>commit {configScaffold.data.git_commit_plan.plan_state}</Tag> : null}
                  {configScaffold.data.git_commit_plan ? <Tag color={configScaffold.data.git_commit_plan.operation_request_enabled ? 'blue' : 'default'}>{configScaffold.data.git_commit_plan.operation_request_enabled ? 'request enabled' : 'request blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.approval_request_plan ? <Tag color="gold">approval {configScaffold.data.git_commit_plan.approval_request_plan.request_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.approval_request_plan ? <Tag>{configScaffold.data.git_commit_plan.approval_request_plan.metadata_ready ? 'approval metadata ready' : 'approval metadata missing'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.approval_request_plan ? <Tag>{configScaffold.data.git_commit_plan.approval_request_plan.request_ready ? 'request ready' : 'request disabled'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.workspace_execution_plan ? <Tag color="gold">workspace {configScaffold.data.git_commit_plan.workspace_execution_plan.workspace_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan ? <Tag>{configScaffold.data.git_commit_plan.git_commit_created ? 'commit created' : 'no commit'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.remote_review_plan ? <Tag color={configScaffold.data.git_commit_plan.remote_review_plan.review_state === 'planned' ? 'gold' : 'red'}>review {configScaffold.data.git_commit_plan.remote_review_plan.review_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.remote_review_plan ? <Tag>{configScaffold.data.git_commit_plan.remote_review_plan.provider_review_created ? 'review created' : 'no review'}</Tag> : null}
                  {configScaffold.data.git_commit_plan ? <Tag>{configScaffold.data.git_commit_plan.live_commit_validation_performed ? 'live validation' : 'no live validation'}</Tag> : null}
                  {configScaffold.data.git_commit_plan ? <Tag color={configScaffold.data.git_commit_plan.project_version_pin_observed ? 'green' : 'orange'}>{configScaffold.data.git_commit_plan.project_version_pin_observed ? 'pin observed' : 'no pin evidence'}</Tag> : null}
                  {configScaffold.data.git_commit_plan ? <Tag color={configScaffold.data.git_commit_plan.live_commit_validation_observed ? 'green' : 'orange'}>{configScaffold.data.git_commit_plan.live_commit_validation_observed ? 'validation observed' : 'no validation evidence'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.project_version_pin_plan ? <Tag color="gold">pin {configScaffold.data.git_commit_plan.project_version_pin_plan.pin_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.project_version_pin_plan?.pin_write_preflight_plan ? <Tag color={configScaffold.data.git_commit_plan.project_version_pin_plan.pin_write_preflight_plan.pin_write_ready_for_review ? 'gold' : configScaffold.data.git_commit_plan.project_version_pin_plan.pin_write_preflight_plan.preflight_state === 'observed' ? 'green' : 'default'}>pin preflight {configScaffold.data.git_commit_plan.project_version_pin_plan.pin_write_preflight_plan.preflight_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.project_version_pin_plan?.pin_write_preflight_plan ? <Tag>{configScaffold.data.git_commit_plan.project_version_pin_plan.pin_write_preflight_plan.project_version_update_enabled ? 'pin write enabled' : 'no pin write'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.result_recording_plan ? <Tag color="gold">result {configScaffold.data.git_commit_plan.result_recording_plan.result_recording_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.result_recording_plan ? <Tag>{configScaffold.data.git_commit_plan.result_recording_plan.result_written ? 'result recorded' : 'no result record'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.result_recording_plan ? <Tag>{configScaffold.data.git_commit_plan.result_recording_plan.project_version_pin_written ? 'pin recorded' : 'no pin record'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.promotion_readiness_plan ? <Tag color={configScaffold.data.git_commit_plan.promotion_readiness_plan.promotion_ready ? 'green' : 'orange'}>promotion {configScaffold.data.git_commit_plan.promotion_readiness_plan.promotion_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.promotion_readiness_plan ? <Tag>{configScaffold.data.git_commit_plan.promotion_readiness_plan.live_git_workflow_enabled ? 'live promotion' : 'no live promotion'}</Tag> : null}
                  {configScaffold.data.git_workflow_audit_evidence && (configScaffold.data.git_workflow_audit_evidence.operation_count || 0) > 0 ? <Tag color={configWorkflowAuditEvidenceColor(configScaffold.data.git_workflow_audit_evidence.evidence_state)}>workflow audit {configScaffold.data.git_workflow_audit_evidence.evidence_state || 'unknown'}</Tag> : null}
                  {configScaffold.data.git_workflow_audit_evidence && (configScaffold.data.git_workflow_audit_evidence.operation_count || 0) > 0 ? <Tag>{configScaffold.data.git_workflow_audit_evidence.operation_count || 0} audit ops</Tag> : null}
                  {configScaffold.data.git_workflow_audit_evidence && (configScaffold.data.git_workflow_audit_evidence.operation_count || 0) > 0 ? <Tag>{configScaffold.data.git_workflow_audit_evidence.operation_log_count || 0} audit logs</Tag> : null}
                  {configScaffold.data.git_workflow_audit_evidence && (configScaffold.data.git_workflow_audit_evidence.active_count || 0) > 0 ? <Tag color="blue">{configScaffold.data.git_workflow_audit_evidence.active_count || 0} active audits</Tag> : null}
                  {configScaffold.data.git_workflow_audit_evidence && (configScaffold.data.git_workflow_audit_evidence.failed_count || 0) > 0 ? <Tag color="red">{configScaffold.data.git_workflow_audit_evidence.failed_count || 0} failed audits</Tag> : null}
                  {configScaffold.data.project_version_pin_evidence ? <Tag color={configScaffold.data.project_version_pin_evidence.pin_state === 'recorded' ? 'green' : 'default'}>pin evidence {configScaffold.data.project_version_pin_evidence.pin_state || 'not_recorded'}</Tag> : null}
                  {configScaffold.data.project_version_pin_evidence ? <Tag>{configScaffold.data.project_version_pin_evidence.pinned_version_count || 0} pinned versions</Tag> : null}
                  {configScaffold.data.project_version_pin_evidence ? <Tag>{configScaffold.data.project_version_pin_evidence.validated_version_count || 0} validated versions</Tag> : null}
                  {configScaffold.data.git_commit_plan ? <Tag>{configScaffold.data.git_commit_plan.steps?.length || 0} commit steps</Tag> : null}
                </Space>
                <Space wrap>
                  <Button size="small" type="primary" loading={configWorkflowRequesting} disabled={!configScaffold.data.git_commit_plan?.operation_request_enabled} onClick={requestConfigGitWorkflow}>Request config workflow audit</Button>
                  {configWorkflowResult?.approval ? <Tag color="gold">approval requested</Tag> : null}
                  {configWorkflowResult?.operation ? <Tag color="blue">operation queued</Tag> : null}
                  {configWorkflowResult?.operation_request_result ? <Tag>{configWorkflowResult.operation_request_result.git_write_performed ? 'git write' : 'no git write'}</Tag> : null}
                  {configWorkflowResult?.operation_request_result ? <Tag>{configWorkflowResult.operation_request_result.sanitized_result_expected ? 'sanitized result expected' : 'result blocked'}</Tag> : null}
                  {configPinResult ? <Tag color={configPinResult.project_version_metadata_written ? 'green' : 'default'}>pin {configPinResult.pin_state || 'recorded'}</Tag> : null}
                  {configPinResult ? <Tag>{configPinResult.project_version_metadata_written ? 'metadata written' : 'no metadata write'}</Tag> : null}
                  {configPinResult ? <Tag>{configPinResult.external_call_made ? 'external call' : 'no external call'}</Tag> : null}
                  {configPinResult ? <Tag>{configPinResult.commit_sha_included ? 'sha included' : 'sha hidden'}</Tag> : null}
                </Space>
                {Array.isArray(configScaffold.data.blocked_reasons) && configScaffold.data.blocked_reasons.length > 0 && (
                  <Alert showIcon type="warning" message={configScaffold.data.blocked_reasons.join(', ')} />
                )}
                {Array.isArray(configScaffold.data.git_commit_plan?.blocked_reasons) && configScaffold.data.git_commit_plan.blocked_reasons.length > 0 && (
                  <Alert showIcon type="warning" message={configScaffold.data.git_commit_plan.blocked_reasons.join(', ')} />
                )}
                <Table<AnyRow>
                  size="small"
                  rowKey="path"
                  dataSource={configScaffold.data.files || []}
                  pagination={false}
                  columns={[
                    { title: 'Path', dataIndex: 'path' },
                    { title: 'Env', render: (_, row) => <Tag>{row.environment || 'all'}</Tag> },
                    { title: 'Purpose', dataIndex: 'purpose' }
                  ]}
                />
              </Space>
            </Card>
          )}
          <CreateModal title="Create repository" open={repoOpen} setOpen={setRepoOpen} fields={['name', 'repo_key', 'display_name', 'repo_role', 'description', 'default_branch']} onSubmit={(v) => api(`/api/projects/${project.id}/git-repositories`, { method: 'POST', body: JSON.stringify(v) }).then(repos.reload)} />
          <Toolbar title="Versions" onCreate={() => setVersionOpen(true)} />
          {versionValidation && (
            <Card title="Version validation">
              <Space direction="vertical" size={8} className="full">
                <Space wrap>
                  <Tag color={versionValidation.validation_state === 'ready' ? 'green' : versionValidation.validation_state === 'partial' ? 'gold' : 'red'}>{versionValidation.validation_state || 'blocked'}</Tag>
                  <Tag>{versionValidation.external_call_made ? 'external call' : 'no external call'}</Tag>
                  <Tag>{versionValidation.git_fetch_performed ? 'git fetch' : 'no git fetch'}</Tag>
                  <Tag>{versionValidation.provider_api_called ? 'provider API called' : 'local synced state'}</Tag>
                  {versionValidation.provider_refresh_plan ? <Tag color={versionValidation.provider_refresh_plan.plan_state === 'planned' ? 'blue' : versionValidation.provider_refresh_plan.plan_state === 'partial' ? 'gold' : 'red'}>refresh {versionValidation.provider_refresh_plan.plan_state}</Tag> : null}
                  {versionValidation.provider_refresh_plan ? <Tag>{versionValidation.provider_refresh_plan.step_count || 0} refresh steps</Tag> : null}
                  {versionValidation.provider_refresh_plan?.execution_plan ? <Tag color={versionValidation.provider_refresh_plan.execution_plan.execution_state === 'ready_for_approval' ? 'blue' : versionValidation.provider_refresh_plan.execution_plan.execution_state === 'partial' ? 'gold' : 'red'}>execute {versionValidation.provider_refresh_plan.execution_plan.execution_state}</Tag> : null}
                  {versionValidation.provider_refresh_plan?.execution_plan ? <Tag>{versionValidation.provider_refresh_plan.execution_plan.execution_enabled ? 'refresh executable' : 'refresh blocked'}</Tag> : null}
                  {versionValidation.provider_refresh_plan?.execution_plan ? <Tag>{versionValidation.provider_refresh_plan.execution_plan.operation_enqueued ? 'operation enqueued' : 'no operation'}</Tag> : null}
                  {versionValidation.provider_refresh_plan?.execution_plan ? <Tag>{versionValidation.provider_refresh_plan.execution_plan.synced_state_written ? 'state written' : 'no state write'}</Tag> : null}
                  {versionValidation.provider_refresh_plan?.execution_plan ? <Tag>{versionValidation.provider_refresh_plan.execution_plan.secret_included ? 'secrets included' : 'no secrets'}</Tag> : null}
                  {versionValidation.provider_refresh_plan?.execution_plan ? <Tag>{versionValidation.provider_refresh_plan.execution_plan.disabled_backends?.length || 0} disabled backends</Tag> : null}
                  {versionValidation.provider_refresh_plan?.execution_plan?.git_ref_fetch_plan ? <Tag color={versionValidation.provider_refresh_plan.execution_plan.git_ref_fetch_plan.refresh_state === 'planned' ? 'gold' : versionValidation.provider_refresh_plan.execution_plan.git_ref_fetch_plan.refresh_state === 'not_required' ? 'default' : 'red'}>git refs {versionValidation.provider_refresh_plan.execution_plan.git_ref_fetch_plan.refresh_state || 'blocked'}</Tag> : null}
                  {versionValidation.provider_refresh_plan?.execution_plan?.github_actions_refresh_plan ? <Tag color={versionValidation.provider_refresh_plan.execution_plan.github_actions_refresh_plan.refresh_state === 'planned' ? 'gold' : versionValidation.provider_refresh_plan.execution_plan.github_actions_refresh_plan.refresh_state === 'not_required' ? 'default' : 'red'}>actions {versionValidation.provider_refresh_plan.execution_plan.github_actions_refresh_plan.refresh_state || 'blocked'}</Tag> : null}
                  {versionValidation.provider_refresh_plan?.execution_plan?.argo_revision_refresh_plan ? <Tag color={versionValidation.provider_refresh_plan.execution_plan.argo_revision_refresh_plan.refresh_state === 'planned' ? 'gold' : versionValidation.provider_refresh_plan.execution_plan.argo_revision_refresh_plan.refresh_state === 'not_required' ? 'default' : 'red'}>argo {versionValidation.provider_refresh_plan.execution_plan.argo_revision_refresh_plan.refresh_state || 'blocked'}</Tag> : null}
                  {versionValidation.provider_refresh_plan?.execution_plan?.result_recording_plan ? <Tag color="gold">result {versionValidation.provider_refresh_plan.execution_plan.result_recording_plan.result_recording_state || 'blocked'}</Tag> : null}
                  {versionValidation.provider_refresh_plan?.execution_plan?.result_recording_plan ? <Tag>{versionValidation.provider_refresh_plan.execution_plan.result_recording_plan.result_written ? 'result recorded' : 'no result record'}</Tag> : null}
                  {versionValidation.provider_refresh_plan?.execution_plan?.result_recording_plan ? <Tag>{versionValidation.provider_refresh_plan.execution_plan.result_recording_plan.validation_rerun_recorded ? 'validation rerun recorded' : 'no validation rerun'}</Tag> : null}
                  {versionValidation.provider_refresh_result_summary ? <Tag color={versionValidation.provider_refresh_result_summary.validation_rerun_status === 'recorded' ? 'green' : versionValidation.provider_refresh_result_summary.validation_rerun_status === 'waiting_for_workers' ? 'blue' : versionValidation.provider_refresh_result_summary.validation_rerun_status === 'refresh_failed' ? 'red' : versionValidation.provider_refresh_result_summary.validation_rerun_status === 'refresh_canceled' ? 'orange' : 'default'}>refresh result {versionValidation.provider_refresh_result_summary.validation_rerun_status || 'not_requested'}</Tag> : null}
                  {versionValidation.provider_refresh_result_summary ? <Tag>{versionValidation.provider_refresh_result_summary.operation_count || 0} refresh ops observed</Tag> : null}
                  {versionValidation.provider_refresh_result_summary?.active_count ? <Tag color="blue">{versionValidation.provider_refresh_result_summary.active_count} active refresh ops</Tag> : null}
                  {versionValidation.provider_refresh_result_summary?.failed_count ? <Tag color="red">{versionValidation.provider_refresh_result_summary.failed_count} failed refresh ops</Tag> : null}
                  {versionValidation.provider_refresh_result_summary?.canceled_count ? <Tag color="orange">{versionValidation.provider_refresh_result_summary.canceled_count} canceled refresh ops</Tag> : null}
                  {versionValidation.provider_refresh_plan?.worker_result_binding_evidence ? <Tag color={versionValidation.provider_refresh_plan.worker_result_binding_evidence.binding_state === 'recorded' ? 'green' : versionValidation.provider_refresh_plan.worker_result_binding_evidence.binding_state === 'waiting_for_workers' ? 'blue' : versionValidation.provider_refresh_plan.worker_result_binding_evidence.binding_state === 'failed' ? 'red' : versionValidation.provider_refresh_plan.worker_result_binding_evidence.binding_state === 'partial_recorded' ? 'gold' : 'default'}>worker binding {versionValidation.provider_refresh_plan.worker_result_binding_evidence.binding_state || 'not_recorded'}</Tag> : null}
                  {versionValidation.provider_refresh_plan?.worker_result_binding_evidence ? <Tag>{versionValidation.provider_refresh_plan.worker_result_binding_evidence.all_planned_results_observed ? 'all planned results observed' : `${versionValidation.provider_refresh_plan.worker_result_binding_evidence.missing_planned_result_kinds?.length || 0} results missing`}</Tag> : null}
                  {versionValidation.validation_rerun_evidence ? <Tag color={versionValidation.validation_rerun_evidence.server_side_validation_recheck ? 'green' : 'default'}>{versionValidation.validation_rerun_evidence.server_side_validation_recheck ? 'server recheck observed' : 'no server recheck'}</Tag> : null}
                  {versionValidation.validation_rerun_evidence ? <Tag>{versionValidation.validation_rerun_evidence.automatic_background_rerun ? 'background rerun' : 'background rerun off'}</Tag> : null}
                  {versionValidation.background_validation_rerun_plan ? <Tag color={versionValidation.background_validation_rerun_plan.plan_state === 'ready_for_operator_review' ? 'gold' : versionValidation.background_validation_rerun_plan.plan_state === 'waiting_for_workers' ? 'blue' : 'default'}>background {versionValidation.background_validation_rerun_plan.plan_state || 'blocked'}</Tag> : null}
                  {versionValidation.background_validation_rerun_plan ? <Tag color={versionValidation.background_validation_rerun_plan.control_worker_auto_snapshot_ready ? 'green' : 'default'}>{versionValidation.background_validation_rerun_plan.control_worker_auto_snapshot_supported ? 'control-worker auto snapshot' : 'manual snapshot only'}</Tag> : null}
                  {versionValidation.background_validation_rerun_plan ? <Tag>{versionValidation.background_validation_rerun_plan.background_rerun_ready_for_review ? 'rerun review ready' : 'background rerun disabled'}</Tag> : null}
                  {versionValidation.background_validation_rerun_plan?.validation_snapshot_write_plan ? <Tag color={versionValidation.background_validation_rerun_plan.validation_snapshot_write_plan.snapshot_ready_for_review ? 'gold' : versionValidation.background_validation_rerun_plan.validation_snapshot_write_plan.snapshot_state === 'waiting_for_workers' ? 'blue' : 'default'}>snapshot preflight {versionValidation.background_validation_rerun_plan.validation_snapshot_write_plan.snapshot_state || 'blocked'}</Tag> : null}
                  {versionValidation.background_validation_rerun_plan?.validation_snapshot_write_plan ? <Tag>{versionValidation.background_validation_rerun_plan.validation_snapshot_write_plan.snapshot_write_enabled ? 'snapshot write enabled' : 'no snapshot write'}</Tag> : null}
                  {versionValidationAutoReload?.version_id === versionValidation.version_id ? <Tag color={versionValidationAutoReload.status === 'polling' ? 'blue' : versionValidationAutoReload.status === 'completed' ? 'green' : versionValidationAutoReload.status === 'done_with_errors' || versionValidationAutoReload.status === 'error' ? 'red' : 'gold'}>auto reload {versionValidationAutoReload.status}</Tag> : null}
                  {versionValidationAutoReload?.version_id === versionValidation.version_id ? <Tag>{versionValidationAutoReload.attempts || 0} reload attempts</Tag> : null}
                  {versionValidationAutoReload?.version_id === versionValidation.version_id ? <Tag>{versionValidationAutoReload.validation_rerun_status || 'not_requested'}</Tag> : null}
                </Space>
                {versionValidation.provider_refresh_plan?.execution_plan?.execution_enabled && (
                  <Space wrap>
                    <Button
                      size="small"
                      type="primary"
                      loading={refreshingVersionID === versionValidation.version_id}
                      onClick={() => refreshVersion({ id: versionValidation.version_id })}
                    >
                      Run refresh
                    </Button>
                    <Typography.Text type="secondary">{versionValidation.provider_refresh_plan.execution_plan.required_operator_action}</Typography.Text>
                  </Space>
                )}
                <Space wrap>
                  <Button
                    size="small"
                    loading={recordingValidationSnapshotID === versionValidation.version_id}
                    disabled={!versionValidation.version_id}
                    onClick={() => recordValidationSnapshot({ id: versionValidation.version_id })}
                  >
                    Record validation snapshot
                  </Button>
                  {versionSnapshotResult ? <Tag color={versionSnapshotResult.validation_snapshot_written ? 'green' : versionSnapshotResult.recording_state === 'asset_missing' ? 'red' : 'default'}>snapshot {versionSnapshotResult.recording_state || 'unknown'}</Tag> : null}
                  {versionSnapshotResult ? <Tag>{versionSnapshotResult.recording_trigger || 'operator_request'}</Tag> : null}
                  {versionSnapshotResult?.auto_record_terminal_required ? <Tag color={versionSnapshotResult.auto_record_terminal_satisfied ? 'green' : 'gold'}>{versionSnapshotResult.auto_record_terminal_satisfied ? 'auto terminal satisfied' : 'auto terminal waiting'}</Tag> : null}
                  {versionSnapshotResult ? <Tag>{versionSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'no asset status write'}</Tag> : null}
                  {versionSnapshotResult ? <Tag>{versionSnapshotResult.operation_log_written ? 'operation log' : 'no operation log'}</Tag> : null}
                  {versionSnapshotResult ? <Tag>{versionSnapshotResult.external_call_made ? 'external call' : 'no external call'}</Tag> : null}
                </Space>
                {versionRefreshResult && (
                  <Space wrap>
                    <Tag color="blue">{versionRefreshResult.operation_count || 0} operations queued</Tag>
                    <Tag>{versionRefreshResult.worker_job_created ? 'worker jobs created' : 'no worker jobs'}</Tag>
                    <Tag>{versionRefreshResult.external_call_made ? 'external call' : 'queued only'}</Tag>
                    <Tag>{versionRefreshResult.secret_included ? 'secrets included' : 'no secrets'}</Tag>
                    <Tag>{versionRefreshResult.result_recording_scope || 'sanitized result'}</Tag>
                    <Tag>{versionRefreshResult.validation_auto_reload_supported ? 'auto reload supported' : 'manual reload'}</Tag>
                  </Space>
                )}
                {versionValidationRerunResult && (
                  <Space wrap>
                    <Tag color={versionValidationRerunResult.background_worker_enqueued ? 'blue' : 'default'}>{versionValidationRerunResult.background_worker_enqueued ? 'validation rerun queued' : 'validation rerun not queued'}</Tag>
                    <Tag>{versionValidationRerunResult.operation_run_id || 'no operation'}</Tag>
                    <Tag>{versionValidationRerunResult.validation_snapshot_write_requested ? 'snapshot requested' : 'snapshot not requested'}</Tag>
                    <Tag>{versionValidationRerunResult.external_call_made ? 'external call' : 'local synced state'}</Tag>
                    <Tag>{versionValidationRerunResult.raw_provider_response_recorded ? 'raw response recorded' : 'no raw response'}</Tag>
                  </Space>
                )}
                <JSONBlock value={versionValidation} />
                {versionRefreshResult ? <JSONBlock value={versionRefreshResult} /> : null}
                {versionValidationRerunResult ? <JSONBlock value={versionValidationRerunResult} /> : null}
                {configPinResult ? <JSONBlock value={configPinResult} /> : null}
                {versionSnapshotResult ? <JSONBlock value={versionSnapshotResult} /> : null}
              </Space>
            </Card>
          )}
          <Table<AnyRow>
            rowKey="id"
            dataSource={versions.data?.items || []}
            pagination={false}
            expandable={{ expandedRowRender: (row) => <JSONBlock value={row.metadata} /> }}
            columns={[
              { title: 'Version', dataIndex: 'version' },
              { title: 'Source', render: (_, row) => <Tag>{row.source || 'manual'}</Tag> },
              { title: 'Repositories', render: (_, row) => Array.isArray(row.metadata?.repositories) ? row.metadata.repositories.length : 0 },
              { title: 'Created', render: (_, row) => shortText(row.created_at, 24) },
              {
                title: 'Actions',
                render: (_, row) => (
                  <Space>
                    <Button size="small" loading={validatingVersionID === row.id} onClick={() => validateVersion(row)}>Validate</Button>
                    <Button size="small" loading={rerunningValidationID === row.id} onClick={() => requestValidationRerun(row)}>Background rerun</Button>
                    <Button size="small" disabled={!configRepo} loading={configPinningVersionID === row.id} onClick={() => pinConfigCommit(row)}>Pin config</Button>
                  </Space>
                )
              }
            ]}
          />
          <VersionManifestModal open={versionOpen} setOpen={setVersionOpen} repos={repoRows} remotes={projectRemotes.data?.items || []} onSubmit={createVersion} />
        </>
      )}
    </Space>
  );
}

function VersionManifestModal({ open, setOpen, repos, remotes, onSubmit }: { open: boolean; setOpen: (value: boolean) => void; repos: AnyRow[]; remotes: AnyRow[]; onSubmit: (values: AnyRow) => Promise<any> }) {
  const [form] = Form.useForm();
  const [submitting, setSubmitting] = useState(false);
  async function submit(values: AnyRow) {
    setSubmitting(true);
    try {
      await onSubmit(values);
      form.resetFields();
      setOpen(false);
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setSubmitting(false);
    }
  }
  return (
    <Modal title="Create version manifest" open={open} onCancel={() => setOpen(false)} onOk={() => form.submit()} confirmLoading={submitting} okButtonProps={{ disabled: submitting || !repos.length || !remotes.length }} width={820} destroyOnHidden>
      <Form form={form} layout="vertical" onFinish={submit} initialValues={{ source: 'manual', repositories: [{}] }}>
        <Space className="full" size={12}>
          <Form.Item name="version" label="version" rules={[{ required: true, message: 'version is required' }]} className="selector">
            <Input placeholder="v0.1.0" />
          </Form.Item>
          <Form.Item name="source" label="source" className="selector">
            <Input placeholder="manual" />
          </Form.Item>
        </Space>
        {(!repos.length || !remotes.length) && <Alert type="warning" showIcon message="Add at least one Git repository and Git remote before creating a version manifest." />}
        <Form.List name="repositories">
          {(fields, { add, remove }) => (
            <Space direction="vertical" size={8} className="full">
              {fields.map((field) => (
                <Card key={field.key} size="small" title={`Repository item ${field.name + 1}`} extra={fields.length > 1 ? <Button size="small" danger onClick={() => remove(field.name)}>Remove</Button> : null}>
                  <div className="manifestGrid">
                    <Form.Item {...field} name={[field.name, 'repository_id']} label="repository" rules={[{ required: true, message: 'repository is required' }]}>
                      <Select
                        options={repos.map((repo) => ({ value: repo.id, label: `${repo.repo_key || repo.name} (${repo.repo_role || 'code'})` }))}
                        onChange={() => {
                          form.setFieldValue(['repositories', field.name, 'remote_id'], undefined);
                          form.setFieldValue(['repositories', field.name, 'config_commit_sha'], undefined);
                        }}
                      />
                    </Form.Item>
                    <Form.Item noStyle shouldUpdate>
                      {({ getFieldValue }) => {
                        const repositoryID = getFieldValue(['repositories', field.name, 'repository_id']);
                        const remoteOptions = remotes.filter((remote) => !repositoryID || remote.repository_id === repositoryID);
                        return (
                          <Form.Item {...field} name={[field.name, 'remote_id']} label="remote" rules={[{ required: true, message: 'remote is required' }]}>
                            <Select options={remoteOptions.map((remote) => ({ value: remote.id, label: `${remote.repository_key || 'repo'} / ${remote.remote_key || remote.name}` }))} />
                          </Form.Item>
                        );
                      }}
                    </Form.Item>
                    <Form.Item {...field} name={[field.name, 'tag']} label="tag">
                      <Input placeholder="v0.1.0" />
                    </Form.Item>
                    <Form.Item {...field} name={[field.name, 'commit_sha']} label="commit sha">
                      <Input placeholder="abc123" />
                    </Form.Item>
                    <Form.Item noStyle shouldUpdate>
                      {({ getFieldValue }) => {
                        const repositoryID = getFieldValue(['repositories', field.name, 'repository_id']);
                        const repo = repos.find((item) => item.id === repositoryID);
                        if ((repo?.repo_role || '') !== 'config') return null;
                        return (
                          <Form.Item {...field} name={[field.name, 'config_commit_sha']} label="config commit sha">
                            <Input placeholder="config repository commit" />
                          </Form.Item>
                        );
                      }}
                    </Form.Item>
                    <Form.Item {...field} name={[field.name, 'github_action_run_id']} label="actions run">
                      <Input placeholder="123456" />
                    </Form.Item>
                    <Form.Item {...field} name={[field.name, 'argo_revision']} label="argo revision">
                      <Input placeholder="optional" />
                    </Form.Item>
                  </div>
                </Card>
              ))}
              <Button onClick={() => add({})} disabled={!repos.length || !remotes.length}>Add repository item</Button>
            </Space>
          )}
        </Form.List>
        <Form.Item name="metadata_json" label="extra metadata JSON">
          <Input.TextArea autoSize={{ minRows: 3, maxRows: 8 }} placeholder='{"notes":"release candidate"}' />
        </Form.Item>
      </Form>
    </Modal>
  );
}

function GitRemotes() {
  const projects = useLoad(() => api('/api/projects'), []);
  const projectRows = projects.data?.items || [];
  const projectPick = useSelectedRow(projectRows);
  const project = projectPick.selected;
  const repos = useLoad(() => project ? api(`/api/projects/${project.id}/git-repositories`) : Promise.resolve({ items: [] }), [project?.id]);
  const repoRows = repos.data?.items || [];
  const repoPick = useSelectedRow(repoRows);
  const repo = repoPick.selected;
  const remotes = useLoad(() => repo ? api(`/api/git-repositories/${repo.id}/remotes`) : Promise.resolve({ items: [] }), [repo?.id]);
  const remoteRows = remotes.data?.items || [];
  const sourcePick = useSelectedRow(remoteRows);
  const sourceRemote = sourcePick.selected;
  const targetPick = useSelectedRow(remoteRows.filter((row: AnyRow) => row.id !== sourcePick.selectedID));
  const actions = useLoad(() => sourcePick.selectedID ? api(`/api/git-remotes/${sourcePick.selectedID}/github-actions`) : Promise.resolve({ items: [] }), [sourcePick.selectedID]);
  const actionsSummary = githubActionsSummary(actions.data?.items || []);
  const [includeArchivedSyncAssets, setIncludeArchivedSyncAssets] = useState(false);
  const [runStatusFilter, setRunStatusFilter] = useState('');
  const runs = useLoad(() => {
    if (!repo) return Promise.resolve({ items: [] });
    const params = new URLSearchParams({ repo_id: repo.id });
    if (runStatusFilter) params.set('status', runStatusFilter);
    return api(`/api/repo-sync-runs?${params.toString()}`);
  }, [repo?.id, runStatusFilter]);
  const tagRuns = useLoad(() => repo ? api(`/api/repo-tag-runs?repo_id=${repo.id}`) : Promise.resolve({ items: [] }), [repo?.id]);
  const syncAssets = useLoad(() => {
    if (!repo) return Promise.resolve({ items: [] });
    const suffix = includeArchivedSyncAssets ? '?include_archived=true' : '';
    return api(`/api/git-repositories/${repo.id}/repo-sync-assets${suffix}`);
  }, [repo?.id, includeArchivedSyncAssets]);
  const webhookConnections = useLoad(() => project ? api(`/api/projects/${project.id}/webhook-connections`) : Promise.resolve({ items: [] }), [project?.id]);
  const webhookEvents = useLoad(() => project ? api(`/api/projects/${project.id}/webhook-events`) : Promise.resolve({ items: [] }), [project?.id]);
  const [branchRefs, setBranchRefs] = useState<string[]>([]);
  const [tagRefs, setTagRefs] = useState<string[]>([]);
  const [allTags, setAllTags] = useState(false);
  const [syncAssetID, setSyncAssetID] = useState<string>();
  const [open, setOpen] = useState(false);
  const [tagOpen, setTagOpen] = useState(false);
  const [syncAssetOpen, setSyncAssetOpen] = useState(false);
  const [syncAssetEditOpen, setSyncAssetEditOpen] = useState(false);
  const [webhookOpen, setWebhookOpen] = useState(false);
  const [recordingThresholdAuditID, setRecordingThresholdAuditID] = useState<string>();
  const [applyingThresholdConfigID, setApplyingThresholdConfigID] = useState<string>();
  const [recordingTagSnapshotID, setRecordingTagSnapshotID] = useState<string>();
  const [tagSnapshotResults, setTagSnapshotResults] = useState<Record<string, AnyRow>>({});
  const syncAssetDetail = useLoad(() => syncAssetID ? api(`/api/repo-sync-assets/${syncAssetID}`) : Promise.resolve(null), [syncAssetID]);
  useEffect(() => {
    const defaultBranch = sourceRemote?.default_branch || repo?.default_branch || '';
    setBranchRefs(defaultBranch ? [defaultBranch] : []);
    setTagRefs([]);
    setAllTags(false);
  }, [repo?.id, repo?.default_branch, sourceRemote?.id, sourceRemote?.default_branch]);
  function selectedRefs() {
    const branches = cleanedList(branchRefs);
    const tags = allTags ? ['*'] : cleanedList(tagRefs);
    return { branches, tags };
  }
  async function runSync() {
    if (!repo || !sourcePick.selectedID || !targetPick.selectedID) return;
    const { branches, tags } = selectedRefs();
    if (!branches.length && !tags.length) {
      message.error('Select at least one branch or tag');
      return;
    }
    await api(`/api/git-repositories/${repo.id}/sync`, {
      method: 'POST',
      body: JSON.stringify({ source_remote_id: sourcePick.selectedID, target_remote_ids: [targetPick.selectedID], refs: { branches, tags } })
    });
    message.success('Sync queued');
    runs.reload();
    remotes.reload();
  }
  async function createRepoSyncAsset(values: AnyRow) {
    if (!repo || !sourcePick.selectedID || !targetPick.selectedID) {
      message.error('Select a repository, source remote, and target remote first');
      return;
    }
    const { branches, tags } = selectedRefs();
    if (!branches.length && !tags.length) {
      message.error('Select at least one branch or tag');
      return;
    }
    await api(`/api/git-repositories/${repo.id}/repo-sync-assets`, {
      method: 'POST',
      body: JSON.stringify({
        name: values.name,
        source_remote_id: sourcePick.selectedID,
        target_remote_id: targetPick.selectedID,
        trigger_mode: values.trigger_mode || 'manual_or_webhook',
        sync_mode: values.sync_mode || 'selected_refs',
        transport: values.transport || 'ssh',
        driver: values.driver || 'projectops_worker_git_ssh',
        refs: { branches, tags }
      })
    });
    message.success('Repo sync asset saved');
    syncAssets.reload();
  }
  async function createWebhookConnection(values: AnyRow) {
    if (!project || !sourcePick.selectedID) {
      message.error('Select a project and source remote first');
      return;
    }
    const created = await api(`/api/projects/${project.id}/webhook-connections`, {
      method: 'POST',
      body: JSON.stringify({
        name: values.name,
        provider: values.provider || 'gitea',
        source_remote_id: sourcePick.selectedID,
        secret_token: values.secret_token
      })
    });
    webhookConnections.reload();
    if (created.secret_token_once) {
      Modal.info({ title: 'Webhook secret', content: <Typography.Text copyable>{created.secret_token_once}</Typography.Text> });
    } else {
      message.success('Webhook connection created');
    }
  }
  async function rotateWebhookSecret(id: string) {
    Modal.confirm({
      title: 'Rotate webhook secret?',
      okText: 'Rotate',
      onOk: async () => {
        const updated = await api(`/api/webhook-connections/${id}/rotate-secret`, { method: 'POST', body: '{}' });
        webhookConnections.reload();
        if (updated.secret_token_once) {
          Modal.info({ title: 'Webhook secret rotated', content: <Typography.Text copyable>{updated.secret_token_once}</Typography.Text> });
        } else {
          message.success('Webhook secret rotated');
        }
      }
    });
  }
  async function replayWebhookEvent(id: string) {
    await api(`/api/webhook-events/${id}/replay`, { method: 'POST', body: '{}' });
    message.success('Webhook replay queued');
    runs.reload();
    syncAssets.reload();
    webhookConnections.reload();
    webhookEvents.reload();
    syncAssetDetail.reload();
  }
  async function recordWebhookThresholdDecisionAudit(id: string) {
    setRecordingThresholdAuditID(id);
    try {
      const result = await api(`/api/webhook-connections/${id}/threshold-decision-audit`, { method: 'POST', body: '{}' });
      const audit = result.audit || {};
      message.success(audit.id ? 'Threshold decision audit recorded' : 'Threshold audit reviewed');
      webhookConnections.reload();
    } finally {
      setRecordingThresholdAuditID(undefined);
    }
  }
  async function applyWebhookThresholdConfiguration(id: string) {
    setApplyingThresholdConfigID(id);
    try {
      const result = await api(`/api/webhook-connections/${id}/threshold-configuration`, { method: 'POST', body: '{}' });
      message.success(result.threshold_configuration_written ? 'Threshold configuration applied' : 'Threshold configuration reviewed');
      webhookConnections.reload();
    } finally {
      setApplyingThresholdConfigID(undefined);
    }
  }
  async function runRepoSyncAsset(id: string) {
    await api(`/api/repo-sync-assets/${id}/run`, { method: 'POST', body: '{}' });
    message.success('Repo sync asset queued');
    runs.reload();
    remotes.reload();
    syncAssets.reload();
    syncAssetDetail.reload();
  }
  async function updateRepoSyncAsset(values: AnyRow) {
    if (!syncAssetID) return;
    const body: AnyRow = {};
    for (const field of ['name', 'trigger_mode', 'sync_mode', 'transport', 'driver']) {
      if (values[field]) body[field] = values[field];
    }
    if (values.enabled !== undefined && String(values.enabled).trim() !== '') {
      body.enabled = ['true', '1', 'yes', 'enabled'].includes(String(values.enabled).trim().toLowerCase());
    }
    await api(`/api/repo-sync-assets/${syncAssetID}`, { method: 'PATCH', body: JSON.stringify(body) });
    message.success('Repo sync asset updated');
    syncAssets.reload();
    syncAssetDetail.reload();
  }
  async function toggleRepoSyncAsset(id: string, enabled: boolean) {
    await api(`/api/repo-sync-assets/${id}`, { method: 'PATCH', body: JSON.stringify({ enabled }) });
    message.success(enabled ? 'Repo sync asset enabled' : 'Repo sync asset disabled');
    syncAssets.reload();
    syncAssetDetail.reload();
  }
  async function archiveRepoSyncAsset(id: string) {
    Modal.confirm({
      title: 'Archive sync asset?',
      okText: 'Archive',
      okButtonProps: { danger: true },
      onOk: async () => {
        await api(`/api/repo-sync-assets/${id}/archive`, { method: 'POST', body: '{}' });
        message.success('Repo sync asset archived');
        setSyncAssetID(undefined);
        syncAssets.reload();
      }
    });
  }
  async function restoreRepoSyncAsset(id: string) {
    await api(`/api/repo-sync-assets/${id}/restore`, { method: 'POST', body: '{}' });
    message.success('Repo sync asset restored');
    syncAssets.reload();
    syncAssetDetail.reload();
  }
  async function rerunRepoSyncRun(id: string) {
    await api(`/api/repo-sync-runs/${id}/rerun`, { method: 'POST', body: '{}' });
    message.success('Repo sync rerun queued');
    runs.reload();
    syncAssets.reload();
    syncAssetDetail.reload();
  }
  async function createRemote(values: AnyRow) {
    if (!repo) {
      message.error('Select a repository first');
      return;
    }
    await api(`/api/git-repositories/${repo.id}/remotes`, {
      method: 'POST',
      body: JSON.stringify({
        ...values,
        kind: values.provider_type || values.kind,
        urls: String(values.urls || values.remote_url || '').split(',').map((x) => x.trim()).filter(Boolean)
      })
    });
    remotes.reload();
  }
  async function createTag(values: AnyRow) {
    if (!repo || !targetPick.selectedID) {
      message.error('Select a repository and target remote first');
      return;
    }
    const result = await api(`/api/git-repositories/${repo.id}/tags`, {
      method: 'POST',
      body: JSON.stringify({ ...values, target_remote_ids: [targetPick.selectedID] })
    });
    message.success(result.approval ? 'Approval requested' : 'Tag queued');
    tagRuns.reload();
    remotes.reload();
  }
  async function recordTagResultSnapshot(id: string) {
    setRecordingTagSnapshotID(id);
    try {
      const result = await api(`/api/repo-tag-runs/${id}/result-snapshot`, {
        method: 'POST',
        body: JSON.stringify({ dry_run: false })
      });
      setTagSnapshotResults((current) => ({ ...current, [id]: result }));
      tagRuns.reload();
      message.success(result.tag_result_snapshot_written ? 'Tag result snapshot recorded' : 'Tag result snapshot already current');
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setRecordingTagSnapshotID(undefined);
    }
  }
  async function syncGitHubActions() {
    if (!sourcePick.selectedID) {
      message.error('Select a GitHub remote first');
      return;
    }
    try {
      await api(`/api/git-remotes/${sourcePick.selectedID}/github-actions/sync`, { method: 'POST', body: '{}' });
      message.success('GitHub Actions sync queued. Refresh shortly to see results.');
      actions.reload();
    } catch (error: any) {
      message.error(error.message);
    }
  }
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="Git Remotes" onCreate={() => setOpen(true)} disabled={!repo} />
      <div className="selectorRow">
        <EntitySelect label="Project" rows={projectRows} value={projectPick.selectedID} onChange={projectPick.setSelectedID} />
        <EntitySelect label="Repository" rows={repoRows} value={repoPick.selectedID} onChange={repoPick.setSelectedID} />
        <EntitySelect label="Source remote" rows={remoteRows} value={sourcePick.selectedID} onChange={sourcePick.setSelectedID} />
        <EntitySelect label="Target remote" rows={remoteRows.filter((row: AnyRow) => row.id !== sourcePick.selectedID)} value={targetPick.selectedID} onChange={targetPick.setSelectedID} />
      </div>
      <Space>
        <Button type="primary" onClick={runSync} disabled={!repo || !sourcePick.selectedID || !targetPick.selectedID}>Sync selected remotes</Button>
        <Button onClick={() => setSyncAssetOpen(true)} disabled={!repo || !sourcePick.selectedID || !targetPick.selectedID}>Save sync asset</Button>
        <Button onClick={() => setWebhookOpen(true)} disabled={!project || !sourcePick.selectedID}>Create webhook</Button>
        <Button onClick={() => setTagOpen(true)} disabled={!repo || !targetPick.selectedID}>Create tag</Button>
        <Button onClick={syncGitHubActions} disabled={!sourcePick.selectedID}>Sync GitHub Actions</Button>
      </Space>
      <div className="refsRow">
        <Space direction="vertical" size={4} className="selector">
          <Typography.Text type="secondary">Branches</Typography.Text>
          <Select mode="tags" value={branchRefs} onChange={setBranchRefs} tokenSeparators={[',']} placeholder="main" />
        </Space>
        <Space direction="vertical" size={4} className="selector">
          <Typography.Text type="secondary">Tags</Typography.Text>
          <Select mode="tags" value={tagRefs} onChange={setTagRefs} tokenSeparators={[',']} placeholder="v1.0.0" disabled={allTags} />
        </Space>
        <Checkbox checked={allTags} onChange={(event) => setAllTags(event.target.checked)}>All tags</Checkbox>
      </div>
      <Table<AnyRow> rowKey="id" dataSource={remotes.data?.items || []} pagination={false} columns={[
        { title: 'Name', dataIndex: 'name' },
        { title: 'Key', dataIndex: 'remote_key' },
        { title: 'Provider', render: (_, row) => row.provider_type || row.kind },
        { title: 'Role', dataIndex: 'remote_role' },
        { title: 'Primary', render: (_, row) => row.is_primary ? <Tag color="green">primary</Tag> : null },
        { title: 'Sync', render: (_, row) => <Tag>{row.last_sync_status || 'never'}</Tag> },
        { title: 'URL', render: (_, row) => urlsText(row) }
      ]} />
      {!repo && <Alert type="info" showIcon message="Create a project repository before adding remotes." />}
      <CreateModal title="Create remote" open={open} setOpen={setOpen} fields={['name', 'remote_key', 'provider_type', 'remote_url', 'web_url', 'remote_role', 'urls', 'default_branch']} onSubmit={createRemote} />
      <CreateModal title="Create tag" open={tagOpen} setOpen={setTagOpen} fields={['tag_name', 'target_sha', 'branch', 'tag_message']} onSubmit={createTag} />
      <CreateModal title="Save repo sync asset" open={syncAssetOpen} setOpen={setSyncAssetOpen} fields={['name', 'trigger_mode', 'sync_mode', 'transport', 'driver']} onSubmit={createRepoSyncAsset} />
      <CreateModal title="Edit repo sync asset" open={syncAssetEditOpen} setOpen={setSyncAssetEditOpen} fields={['name', 'trigger_mode', 'sync_mode', 'transport', 'driver', 'enabled']} onSubmit={updateRepoSyncAsset} />
      <CreateModal title="Create webhook" open={webhookOpen} setOpen={setWebhookOpen} fields={['name', 'provider', 'secret_token']} onSubmit={createWebhookConnection} />
      <Tabs items={[
        { key: 'assets', label: 'Sync assets', children: <Space direction="vertical" size={12} className="full">
          <Checkbox checked={includeArchivedSyncAssets} onChange={(event) => setIncludeArchivedSyncAssets(event.target.checked)}>Show archived</Checkbox>
          <Table<AnyRow> rowKey="id" dataSource={syncAssets.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: 'Name', dataIndex: 'name' },
            { title: 'Source', dataIndex: 'source_remote_name' },
            { title: 'Target', dataIndex: 'target_remote_name' },
            { title: 'Trigger', dataIndex: 'trigger_mode' },
            { title: 'Status', render: (_, row) => <Space><Tag color={row.last_sync_status === 'completed' ? 'green' : row.last_sync_status === 'failed' ? 'red' : row.last_sync_status === 'running' ? 'blue' : 'default'}>{row.last_sync_status || 'never'}</Tag>{row.archived_at ? <Tag>archived</Tag> : null}</Space> },
            { title: 'Risk', render: (_, row) => <Space size={4} wrap><Tag color={signalSeverityColor(row.risk_severity)}>{row.risk_severity || 'ok'}</Tag><Typography.Text>{shortText(row.risk_summary, 48)}</Typography.Text></Space> },
            { title: 'Runs', render: (_, row) => <Tag>{row.total_runs || 0}</Tag> },
            { title: 'Success', render: (_, row) => `${row.success_rate ?? 0}%` },
            { title: 'Avg', render: (_, row) => secondsText(row.avg_duration_seconds) },
            { title: 'Last failure', render: (_, row) => shortText(row.last_failure_message || row.last_failure_at, 56) },
            { title: 'Action', render: (_, row) => <Space><Button size="small" onClick={() => setSyncAssetID(row.id)}>View</Button><Button size="small" onClick={() => runRepoSyncAsset(row.id)} disabled={!row.enabled || Boolean(row.archived_at)}>Run</Button>{row.archived_at ? <Button size="small" onClick={() => restoreRepoSyncAsset(row.id)}>Restore</Button> : null}</Space> }
          ]} />
        </Space> },
        { key: 'webhooks', label: 'Webhooks', children: <Space direction="vertical" size={16} className="full">
          <Table<AnyRow> rowKey="id" dataSource={webhookConnections.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: 'Name', dataIndex: 'name' },
            { title: 'Provider', dataIndex: 'provider' },
            { title: 'Source', dataIndex: 'source_remote_name' },
            { title: 'URL', render: (_, row) => <Typography.Text copyable>{row.webhook_url || row.webhook_path}</Typography.Text> },
            { title: 'Delivery', render: (_, row) => <Tag color={row.last_delivery_status === 'queued' ? 'green' : row.last_delivery_status === 'failed' || row.last_delivery_status === 'rejected' ? 'red' : 'default'}>{row.last_delivery_status || 'never'}</Tag> },
            { title: 'Health', render: (_, row) => <Space size={4} wrap><Tag color={signalSeverityColor(row.webhook_health)}>{row.webhook_health || 'unknown'}</Tag><Typography.Text>{shortText(row.webhook_summary, 48)}</Typography.Text></Space> },
            { title: 'Rehearsal', render: (_, row) => {
              const readiness = row.callback_rehearsal || {};
              const providerPlan = readiness.provider_rehearsal_plan || {};
              const publicEndpointPlan = providerPlan.public_endpoint_plan || {};
              const deliveryPlan = providerPlan.provider_delivery_plan || {};
              const thresholdPlan = providerPlan.threshold_tuning_plan || {};
              const thresholdVolume = thresholdPlan.volume_evidence || {};
              const thresholdConfig = thresholdPlan.threshold_configuration_plan || {};
              const thresholdAudit = thresholdConfig.threshold_decision_audit_plan || {};
              const metricsComparison = thresholdPlan.provider_metrics_comparison_plan || thresholdConfig.provider_metrics_comparison_plan || {};
              const resultPlan = providerPlan.result_recording_plan || {};
              const callbackEvidence = readiness.callback_evidence || {};
              const replayProof = callbackEvidence.operator_replay_proof || providerPlan.operator_replay_proof || {};
              const status = readiness.status || 'unknown';
              return <Space size={4} wrap>
                <Tag color={status === 'ready' ? 'green' : status === 'blocked' ? 'red' : 'default'}>{status}</Tag>
                <Tag color={providerPlan.plan_state === 'planned' ? 'gold' : 'red'}>{providerPlan.plan_state || 'blocked'}</Tag>
                <Tag color={publicEndpointPlan.public_origin_ready ? 'green' : 'red'}>{publicEndpointPlan.public_origin_ready ? 'public origin' : 'no public origin'}</Tag>
                <Tag color={deliveryPlan.delivery_state === 'planned' ? 'gold' : 'red'}>{deliveryPlan.provider_test_delivery_sent ? 'test delivered' : 'no test delivery'}</Tag>
                <Tag color={thresholdPlan.threshold_state === 'planned' ? 'gold' : 'red'}>{thresholdPlan.provider_pair_thresholds_tuned ? 'thresholds tuned' : 'thresholds pending'}</Tag>
                <Tag color={thresholdVolumeColor(thresholdPlan, thresholdVolume)}>{thresholdVolume.local_volume_observed ? `volume ${thresholdPlan.threshold_review_state || 'observed'}` : 'volume pending'}</Tag>
                {metricsComparison.mode ? <Tag color={metricsComparison.comparison_ready_for_review ? 'gold' : metricsComparison.comparison_state === 'needs_failure_review' ? 'red' : 'default'}>metrics {metricsComparison.comparison_state || 'blocked'}</Tag> : null}
                {metricsComparison.mode ? <Tag>{metricsComparison.provider_metrics_fetched ? 'provider metrics' : 'no provider metrics'}</Tag> : null}
                {thresholdConfig.mode ? <Tag color={thresholdConfig.configuration_review_ready === true ? 'gold' : 'default'}>{thresholdConfig.configuration_review_ready === true ? 'config review ready' : `config ${thresholdConfig.configuration_state || 'blocked'}`}</Tag> : null}
                {thresholdConfig.threshold_configuration_written ? <Tag color="green">{thresholdConfig.threshold_configuration_count || 0} configs</Tag> : null}
                {thresholdAudit.mode ? <Tag color={thresholdAudit.decision_ready_for_review ? 'gold' : thresholdAudit.decision_state === 'needs_failure_review' ? 'red' : 'default'}>threshold audit {thresholdAudit.decision_state || 'blocked'}</Tag> : null}
                {thresholdAudit.mode ? <Tag>{thresholdAudit.audit_insert_enabled ? 'audit write enabled' : 'no audit write'}</Tag> : null}
                {thresholdAudit.threshold_decision_audit_count ? <Tag color="green">{thresholdAudit.threshold_decision_audit_count} audit rows</Tag> : null}
                <Tag>{providerPlan.external_call_made ? 'provider call' : 'no provider call'}</Tag>
                <Tag>{resultPlan.result_written ? 'result recorded' : 'no result record'}</Tag>
                <Tag color={replayProof.proof_state === 'recorded' ? 'green' : replayProof.proof_state === 'failed' ? 'red' : replayProof.operator_replay_observed ? 'gold' : 'default'}>{replayProof.operator_replay_observed ? `replay proof ${replayProof.proof_state || 'observed'}` : 'replay proof pending'}</Tag>
                {callbackEvidence.delivery_count_7d ? <Tag color={callbackEvidenceColor(callbackEvidence.evidence_state)}>callback {callbackEvidence.evidence_state || 'observed'}</Tag> : null}
                {callbackEvidence.delivery_count_7d ? <Tag>{callbackEvidence.delivery_count_7d} deliveries</Tag> : null}
                {callbackEvidence.repo_sync_enqueue_observed ? <Tag color="green">repo sync observed</Tag> : null}
                {callbackEvidence.failed_count_7d ? <Tag color="red">{callbackEvidence.failed_count_7d} failed callbacks</Tag> : null}
                <Typography.Text>{shortText(readiness.message, 56)}</Typography.Text>
              </Space>;
            } },
            { title: 'Action', render: (_, row) => {
              const thresholdPlan = row.callback_rehearsal?.provider_rehearsal_plan?.threshold_tuning_plan || {};
              const thresholdConfig = thresholdPlan.threshold_configuration_plan || {};
              const thresholdAudit = thresholdConfig.threshold_decision_audit_plan || {};
              return <Space size={4} wrap>
                <Button size="small" onClick={() => rotateWebhookSecret(row.id)}>Rotate secret</Button>
                <Button
                  size="small"
                  onClick={() => recordWebhookThresholdDecisionAudit(row.id)}
                  disabled={!thresholdAudit.decision_ready_for_review || Boolean(thresholdAudit.threshold_decision_audit_count)}
                  loading={recordingThresholdAuditID === row.id}
                >
                  {thresholdAudit.threshold_decision_audit_count ? 'Audit recorded' : 'Record threshold audit'}
                </Button>
                <Button
                  size="small"
                  onClick={() => applyWebhookThresholdConfiguration(row.id)}
                  disabled={!thresholdConfig.configuration_write_enabled || Boolean(thresholdConfig.threshold_configuration_written)}
                  loading={applyingThresholdConfigID === row.id}
                >
                  {thresholdConfig.threshold_configuration_written ? 'Config applied' : 'Apply threshold config'}
                </Button>
              </Space>;
            } }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={webhookEvents.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: 'Status', render: (_, row) => <Tag color={row.status === 'queued' ? 'green' : row.status === 'failed' || row.status === 'rejected' ? 'red' : 'default'}>{row.status}</Tag> },
            { title: 'Event', dataIndex: 'event_type' },
            { title: 'Delivery', dataIndex: 'delivery_id' },
            { title: 'Received', dataIndex: 'received_at' },
            { title: 'Action', render: (_, row) => row.event_type === 'push' ? <Button size="small" onClick={() => replayWebhookEvent(row.id)}>Replay</Button> : null }
          ]} />
        </Space> },
        { key: 'runs', label: 'Sync runs', children: <Space direction="vertical" size={12} className="full">
          <Select allowClear value={runStatusFilter || undefined} placeholder="Status" style={{ width: 180 }} onChange={(value) => setRunStatusFilter(value || '')} options={['queued', 'running', 'completed', 'failed'].map((value) => ({ value, label: value }))} />
          <Table<AnyRow> rowKey="id" dataSource={runs.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: 'Status', render: (_, row) => <Tag color={row.status === 'completed' ? 'green' : row.status === 'failed' ? 'red' : 'blue'}>{row.status}</Tag> },
            { title: 'Ref', dataIndex: 'ref' },
            { title: 'Source', dataIndex: 'source_remote_id' },
            { title: 'Target', dataIndex: 'target_remote_id' },
            { title: 'Created', dataIndex: 'created_at' }
          ]} />
        </Space> },
        { key: 'tags', label: 'Tag runs', children: <Table<AnyRow> rowKey="id" dataSource={tagRuns.data?.items || []} pagination={{ pageSize: 6 }} columns={[
          { title: 'Status', render: (_, row) => <Tag color={row.status === 'completed' ? 'green' : row.status === 'failed' ? 'red' : 'blue'}>{row.status}</Tag> },
          { title: 'Rehearsal', render: (_, row) => {
            const plan = row.remote_rehearsal_plan || {};
            const liveResultPlan = plan.live_result_plan || {};
            const actionsRefreshPlan = plan.actions_refresh_plan || {};
            const resultPlan = plan.result_recording_plan || {};
            const lookupPreflight = plan.live_remote_lookup_preflight || liveResultPlan.live_remote_lookup_preflight || {};
            const tagResultEvidence = plan.tag_result_evidence || resultPlan.tag_result_evidence || {};
            const snapshotResult = tagSnapshotResults[row.id];
            return <Space size={4} wrap>
              <Tag color={plan.rehearsal_state === 'observed' ? 'green' : plan.rehearsal_state === 'blocked' || plan.rehearsal_state === 'failed' ? 'red' : 'gold'}>{plan.rehearsal_state || 'planned'}</Tag>
              <Tag>{plan.live_remote_tag_success_observed ? 'remote success' : 'no remote success'}</Tag>
              {lookupPreflight.mode ? <Tag color={lookupPreflight.lookup_state === 'observed' ? 'green' : lookupPreflight.lookup_state === 'failed' || lookupPreflight.lookup_state === 'blocked' ? 'red' : 'gold'}>lookup {lookupPreflight.lookup_state || 'blocked'}</Tag> : null}
              {lookupPreflight.mode ? <Tag>{lookupPreflight.remote_tag_lookup_performed ? 'remote lookup' : 'no remote lookup'}</Tag> : null}
              <Tag color={liveResultPlan.live_result_state === 'planned' ? 'gold' : 'red'}>{liveResultPlan.repo_tag_run_result_written ? 'tag result saved' : liveResultPlan.live_result_state === 'failed' ? 'tag result failed' : 'tag result pending'}</Tag>
              <Tag color={actionsRefreshPlan.refresh_state === 'planned' ? 'gold' : 'red'}>{actionsRefreshPlan.github_actions_refresh_performed ? 'actions refreshed' : actionsRefreshPlan.refresh_state === 'failed' ? 'actions refresh failed' : 'actions refresh pending'}</Tag>
              <Tag>{resultPlan.result_written ? 'result recorded' : 'no result record'}</Tag>
              {resultPlan.result_recording_state ? <Tag color={tagResultEvidenceColor(resultPlan.result_recording_state)}>recording {resultPlan.result_recording_state}</Tag> : null}
              {snapshotResult ? <Tag color={snapshotResult.tag_result_snapshot_written ? 'green' : snapshotResult.recording_state === 'asset_missing' ? 'red' : 'default'}>snapshot {snapshotResult.recording_state || 'unknown'}</Tag> : null}
              {tagResultEvidence.waiting_for_worker ? <Tag color="blue">tag worker pending</Tag> : null}
              {tagResultEvidence.live_remote_tag_failed_observed ? <Tag color="red">tag failure observed</Tag> : null}
            </Space>;
          } },
          { title: 'Tag', dataIndex: 'tag_name' },
          { title: 'Target SHA', dataIndex: 'target_sha' },
          { title: 'Target', dataIndex: 'target_remote_id' },
          { title: 'Created', dataIndex: 'created_at' },
          { title: 'Action', render: (_, row) => {
            const resultPlan = row.remote_rehearsal_plan?.result_recording_plan || {};
            return <Button
              size="small"
              onClick={() => recordTagResultSnapshot(row.id)}
              disabled={!resultPlan.result_recording_ready}
              loading={recordingTagSnapshotID === row.id}
            >
              Record result snapshot
            </Button>;
          } }
        ]} /> },
        { key: 'actions', label: 'GitHub Actions', children: <Space direction="vertical" size={12} className="full">
          <Alert
            showIcon
            type={actionsSummary.failures > 0 ? 'warning' : actionsSummary.total > 0 ? 'success' : 'info'}
            message={actionsSummary.latestLabel}
            description={githubActionRemoteDescription(sourceRemote, repo, project, actionsSummary)}
          />
          <div className="metricGrid">
            <Card><Typography.Text type="secondary">Runs</Typography.Text><Typography.Title level={4}>{actionsSummary.total}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">Success</Typography.Text><Typography.Title level={4}>{actionsSummary.successes}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">Failures</Typography.Text><Typography.Title level={4}>{actionsSummary.failures}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">Active</Typography.Text><Typography.Title level={4}>{actionsSummary.active}</Typography.Title></Card>
          </div>
          <Table<AnyRow> rowKey="id" dataSource={actions.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: 'Workflow', dataIndex: 'workflow_name' },
            { title: 'Branch', dataIndex: 'branch' },
            { title: 'Status', render: (_, row) => <Tag color={githubActionStatusColor(row)}>{row.conclusion || row.status}</Tag> },
            { title: 'SHA', dataIndex: 'commit_sha' },
            { title: 'Synced', dataIndex: 'synced_at' }
          ]} />
        </Space> }
      ]} />
      <Modal title={syncAssetDetail.data?.asset?.name || 'Sync asset'} open={Boolean(syncAssetID)} onCancel={() => setSyncAssetID(undefined)} footer={null} width={980} destroyOnHidden>
        {syncAssetDetail.data && <Space direction="vertical" size={16} className="full">
          <Space wrap>
            <Tag>{syncAssetDetail.data.asset?.trigger_mode}</Tag>
            <Tag>{syncAssetDetail.data.asset?.sync_mode}</Tag>
            <Tag color={syncAssetDetail.data.asset?.last_sync_status === 'completed' ? 'green' : syncAssetDetail.data.asset?.last_sync_status === 'failed' ? 'red' : 'blue'}>{syncAssetDetail.data.asset?.last_sync_status || 'never'}</Tag>
            <Tag color={syncAssetDetail.data.asset?.enabled ? 'green' : 'default'}>{syncAssetDetail.data.asset?.enabled ? 'enabled' : 'disabled'}</Tag>
            {syncAssetDetail.data.asset?.archived_at ? <Tag>archived</Tag> : null}
          </Space>
          <Space wrap>
            <Button size="small" type="primary" onClick={() => runRepoSyncAsset(syncAssetDetail.data.asset.id)} disabled={!syncAssetDetail.data.asset?.enabled || Boolean(syncAssetDetail.data.asset?.archived_at)}>Run</Button>
            <Button size="small" onClick={() => setSyncAssetEditOpen(true)} disabled={Boolean(syncAssetDetail.data.asset?.archived_at)}>Edit</Button>
            <Button size="small" onClick={() => toggleRepoSyncAsset(syncAssetDetail.data.asset.id, !syncAssetDetail.data.asset?.enabled)} disabled={Boolean(syncAssetDetail.data.asset?.archived_at)}>{syncAssetDetail.data.asset?.enabled ? 'Disable' : 'Enable'}</Button>
            {syncAssetDetail.data.asset?.archived_at ? <Button size="small" onClick={() => restoreRepoSyncAsset(syncAssetDetail.data.asset.id)}>Restore</Button> : <Button size="small" danger onClick={() => archiveRepoSyncAsset(syncAssetDetail.data.asset.id)}>Archive</Button>}
          </Space>
          <div className="metricGrid">
            <Card><Typography.Text type="secondary">Runs</Typography.Text><Typography.Title level={4}>{syncAssetDetail.data.asset?.total_runs || 0}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">Success rate</Typography.Text><Typography.Title level={4}>{syncAssetDetail.data.asset?.success_rate ?? 0}%</Typography.Title></Card>
            <Card><Typography.Text type="secondary">Avg duration</Typography.Text><Typography.Title level={4}>{secondsText(syncAssetDetail.data.asset?.avg_duration_seconds)}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">Last failure</Typography.Text><Typography.Title level={5}>{shortText(syncAssetDetail.data.asset?.last_failure_message || syncAssetDetail.data.asset?.last_failure_at)}</Typography.Title></Card>
          </div>
          <Typography.Title level={5}>Capacity signals</Typography.Title>
          <Table<AnyRow> rowKey="name" size="small" dataSource={syncAssetDetail.data.capacity_signals || []} pagination={false} columns={[
            { title: 'Signal', dataIndex: 'name' },
            { title: 'Severity', render: (_, row) => <Tag color={signalSeverityColor(row.severity)}>{row.severity || 'ok'}</Tag> },
            { title: 'Status', render: (_, row) => String(row.status ?? '-') },
            { title: 'Threshold', render: (_, row) => row.threshold ? shortText(row.threshold, 88) : '-' },
            { title: 'Detail', render: (_, row) => shortText(row.detail, 120) }
          ]} />
          <Typography.Title level={5}>14-day trend</Typography.Title>
          <Table<AnyRow> rowKey="day" size="small" dataSource={syncAssetDetail.data.trend || []} pagination={{ pageSize: 7 }} columns={[
            { title: 'Day', dataIndex: 'day' },
            { title: 'Runs', dataIndex: 'total_runs' },
            { title: 'Completed', dataIndex: 'completed_runs' },
            { title: 'Failed', dataIndex: 'failed_runs' },
            { title: 'Active', dataIndex: 'active_runs' },
            { title: 'Avg', render: (_, row) => secondsText(row.avg_duration_seconds) }
          ]} />
          <Table<AnyRow> rowKey="id" size="small" dataSource={syncAssetDetail.data.runs || []} pagination={{ pageSize: 5 }} columns={[
            { title: 'Run', dataIndex: 'id' },
            { title: 'Status', render: (_, row) => <Tag color={row.status === 'completed' ? 'green' : row.status === 'failed' ? 'red' : 'blue'}>{row.status}</Tag> },
            { title: 'Ref', dataIndex: 'ref' },
            { title: 'Error', dataIndex: 'error_message' },
            { title: 'Created', dataIndex: 'created_at' },
            { title: 'Action', render: (_, row) => row.status === 'failed' ? <Button size="small" onClick={() => rerunRepoSyncRun(row.id)}>Rerun</Button> : null }
          ]} />
          <Table<AnyRow> rowKey="id" size="small" dataSource={syncAssetDetail.data.webhook_events || []} pagination={{ pageSize: 5 }} columns={[
            { title: 'Webhook', dataIndex: 'delivery_id' },
            { title: 'Status', render: (_, row) => <Tag color={row.status === 'queued' ? 'green' : row.status === 'failed' || row.status === 'rejected' ? 'red' : 'default'}>{row.status}</Tag> },
            { title: 'Event', dataIndex: 'event_type' },
            { title: 'Error', dataIndex: 'error_message' },
            { title: 'Received', dataIndex: 'received_at' }
          ]} />
          <Table<AnyRow> rowKey="id" size="small" dataSource={syncAssetDetail.data.operation_logs || []} pagination={{ pageSize: 5 }} columns={[
            { title: 'Level', dataIndex: 'level' },
            { title: 'Message', dataIndex: 'message' },
            { title: 'Created', dataIndex: 'created_at' }
          ]} />
        </Space>}
      </Modal>
    </Space>
  );
}

function approvalRoles(value: any): string[] {
  const clean = (items: string[]) => Array.from(new Set(items.map((item) => item.trim().replace(/^"|"$/g, '').toLowerCase()).filter(Boolean)));
  const fallback = (items: string[]) => items.length > 0 ? items : ['admin', 'owner'];
  if (Array.isArray(value)) return fallback(clean(value.map((item) => String(item))));
  const text = String(value || '').trim();
  if (!text || text === '<nil>') return ['admin', 'owner'];
  if (text.startsWith('{') && text.endsWith('}')) return fallback(clean(parsePostgresTextArray(text)));
  try {
    const parsed = JSON.parse(text);
    if (Array.isArray(parsed)) return fallback(clean(parsed.map((item) => String(item))));
  } catch {
    return fallback(clean([text]));
  }
  return fallback(clean([text]));
}

function approvalDestinationTags(value: any) {
  const destinations = Array.isArray(value) ? value : [];
  if (!destinations.length) return '-';
  return (
    <Space wrap size={4}>
      {destinations.map((destination: AnyRow, index: number) => {
        const status = String(destination.adapter_status || 'unknown');
        const color = status === 'enabled' ? 'green' : status === 'environment_backed' ? 'blue' : status === 'unknown' ? 'red' : 'gold';
        return (
          <Tag key={`${destination.channel || destination.label || index}`} color={color}>
            {destination.label || destination.channel || destination.kind} · {status.replaceAll('_', ' ')}
          </Tag>
        );
      })}
    </Space>
  );
}

function approvalEscalationDestinationTags(row: AnyRow) {
  if (!row.escalation_after_minutes) return '-';
  const destinations = Array.isArray(row.escalation_destinations) ? row.escalation_destinations : [];
  return (
    <Space wrap size={4}>
      <Tag>{row.escalation_after_minutes}m</Tag>
      {destinations.length ? approvalDestinationTags(destinations) : <Tag color="gold">No targets</Tag>}
    </Space>
  );
}

function agentReadinessGateTags(value: any) {
  const gates = Array.isArray(value) ? value : [];
  if (!gates.length) return null;
  return (
    <Space wrap size={4}>
      {gates.map((gate: AnyRow, index: number) => {
        const status = String(gate.status || 'unknown');
        const color = status === 'blocked' ? 'red' : status === 'ready' ? 'green' : status.startsWith('audit_') ? 'blue' : status === 'unknown' ? 'default' : 'gold';
        return <Tag key={`${gate.gate || index}`} color={color}>{String(gate.gate || 'gate').replaceAll('_', ' ')}: {status}</Tag>;
      })}
    </Space>
  );
}

function agentAuditEvidenceColor(state?: string) {
  if (state === 'recorded') return 'green';
  if (state === 'failed' || state === 'mixed_failed') return 'red';
  if (state === 'waiting_for_worker') return 'blue';
  if (state === 'partial_recorded') return 'gold';
  if (state === 'canceled') return 'orange';
  if (state === 'unknown' || state === 'absent') return 'purple';
  return 'default';
}

function parsePostgresTextArray(value: string): string[] {
  const body = value.slice(1, -1);
  const items: string[] = [];
  let current = '';
  let quoted = false;
  let escaping = false;
  for (const char of body) {
    if (escaping) {
      current += char;
      escaping = false;
      continue;
    }
    if (char === '\\') {
      escaping = true;
      continue;
    }
    if (char === '"') {
      quoted = !quoted;
      continue;
    }
    if (char === ',' && !quoted) {
      items.push(current);
      current = '';
      continue;
    }
    current += char;
  }
  items.push(current);
  return items;
}

function approvalStillActive(row: AnyRow) {
  if (row.status !== 'pending') return false;
  if (!row.expires_at) return true;
  const expiresAt = new Date(row.expires_at).getTime();
  return Number.isNaN(expiresAt) || expiresAt > Date.now();
}

function canActOnApproval(row: AnyRow, role: string) {
  return Boolean(row.can_current_user_decide) || approvalRoles(row.required_approver_roles).includes(role);
}

function roleCanApprove(row: AnyRow, role: string) {
  const roles = approvalRoles(row.required_approver_roles);
  const approverRoles = roles.length ? roles : ['admin', 'owner'];
  return approverRoles.includes(role);
}

function canRevokeApprovalDelegation(row: AnyRow, approval: AnyRow | undefined, user: AnyRow | undefined, role: string) {
  if (row.revoked_at || !approval || !user) return false;
  if (['admin', 'owner'].includes(role)) return true;
  if (String(row.from_user_id || '') === String(user.id || '')) return true;
  return roleCanApprove(approval, role);
}

function csvValue(value: any) {
  if (Array.isArray(value)) return value.join(', ');
  return String(value || '');
}

function splitCSV(value: any) {
  return String(value || '').split(',').map((item) => item.trim()).filter(Boolean);
}

function approvalRuleInitialValues(row?: AnyRow | null) {
  return {
    resource_type: row?.resource_type || '',
    action: row?.action || '',
    required_approver_roles: csvValue(row?.required_approver_roles || ['admin', 'owner']),
    required_approval_count: row?.required_approval_count ?? 1,
    expires_after_minutes: row?.expires_after_minutes ?? 1440,
    notification_channels: csvValue(row?.notification_channels || ['ui']),
    escalation_after_minutes: row?.escalation_after_minutes ?? 0,
    escalation_channels: csvValue(row?.escalation_channels || []),
    priority: row?.priority ?? 100,
    enabled: row?.enabled ?? true,
    metadata_json: JSON.stringify(row?.metadata || {}, null, 2)
  };
}

function approvalRulePayload(values: AnyRow) {
  return {
    resource_type: String(values.resource_type || '').trim(),
    action: String(values.action || '').trim(),
    required_approver_roles: splitCSV(values.required_approver_roles),
    required_approval_count: Number(values.required_approval_count || 1),
    expires_after_minutes: Number(values.expires_after_minutes || 1440),
    notification_channels: splitCSV(values.notification_channels),
    escalation_after_minutes: Number(values.escalation_after_minutes || 0),
    escalation_channels: splitCSV(values.escalation_channels),
    priority: Number(values.priority || 100),
    enabled: Boolean(values.enabled),
    metadata: JSON.parse(values.metadata_json || '{}')
  };
}

function Operations({ embedded = false }: { embedded?: boolean }) {
  const ops = useLoad(() => api('/api/operations'), []);
  const approvalSummary = useLoad(() => api('/api/operation-approvals/summary'), []);
  const approvalReminderCandidates = useLoad(() => api('/api/operation-approvals/reminder-candidates'), []);
  const approvalRules = useLoad(() => api('/api/operation-approval-rules'), []);
  const approvalViews = useLoad(() => api('/api/operation-approval-views'), []);
  const [approvalStatusFilter, setApprovalStatusFilter] = useState('');
  const [approvalActionFilter, setApprovalActionFilter] = useState('');
  const [approvalResourceTypeFilter, setApprovalResourceTypeFilter] = useState('');
  const [approvalRequestedByFilter, setApprovalRequestedByFilter] = useState('');
  const [approvalSinceFilter, setApprovalSinceFilter] = useState('');
  const [approvalUntilFilter, setApprovalUntilFilter] = useState('');
  const [approvalSearch, setApprovalSearch] = useState('');
  const debouncedApprovalSearch = useDebouncedValue(approvalSearch, 300);
  const debouncedApprovalRequestedBy = useDebouncedValue(approvalRequestedByFilter, 300);
  const approvals = useLoad(() => {
    const params = new URLSearchParams();
    if (approvalStatusFilter) params.set('status', approvalStatusFilter);
    if (approvalActionFilter) params.set('action', approvalActionFilter);
    if (approvalResourceTypeFilter.trim()) params.set('resource_type', approvalResourceTypeFilter.trim());
    if (debouncedApprovalSearch.trim()) params.set('q', debouncedApprovalSearch.trim());
    if (debouncedApprovalRequestedBy.trim()) params.set('requested_by', debouncedApprovalRequestedBy.trim());
    if (approvalSinceFilter.trim()) params.set('since', approvalSinceFilter.trim());
    if (approvalUntilFilter.trim()) params.set('until', approvalUntilFilter.trim());
    const suffix = params.toString();
    return api(`/api/operation-approvals${suffix ? `?${suffix}` : ''}`);
  }, [approvalStatusFilter, approvalActionFilter, approvalResourceTypeFilter, debouncedApprovalSearch, debouncedApprovalRequestedBy, approvalSinceFilter, approvalUntilFilter]);
  const me = useLoad(() => api('/api/auth/me'), []);
  const [approvalViewID, setApprovalViewID] = useState<string>();
  const [approvalViewName, setApprovalViewName] = useState('');
  const [approvalAuditID, setApprovalAuditID] = useState<string>();
  const [liveOperationID, setLiveOperationID] = useState<string>();
  const [ruleOpen, setRuleOpen] = useState(false);
  const [editingRule, setEditingRule] = useState<AnyRow | null>(null);
  const [ruleAuditID, setRuleAuditID] = useState<string>();
  const [delegateEmail, setDelegateEmail] = useState('');
  const [delegateReason, setDelegateReason] = useState('');
  const [ruleForm] = Form.useForm();
  const approvalAudit = useLoad(() => approvalAuditID ? api(`/api/operation-approvals/${approvalAuditID}`) : Promise.resolve({}), [approvalAuditID]);
  const ruleAudits = useLoad(() => ruleAuditID ? api(`/api/operation-approval-rules/${ruleAuditID}/audits`) : Promise.resolve({ items: [] }), [ruleAuditID]);
  const liveLogs = useOperationLogStream(liveOperationID);
  const liveOperation = (ops.data?.items || []).find((row: AnyRow) => row.id === liveOperationID);
  const liveLogTag = operationLogStreamTag(liveLogs);
  const currentRole = String(me.data?.user?.role || '').toLowerCase();
  const canEditApprovalRules = ['admin', 'owner'].includes(currentRole);
  useEffect(() => {
    if (ruleOpen) {
      ruleForm.setFieldsValue(approvalRuleInitialValues(editingRule));
    }
  }, [ruleOpen, editingRule?.id]);
  const approvalActionOptions = Array.from(new Set([
    'repo.tag',
    'ssh.exec',
    'operation.cancel',
    'agent.execute',
    ...(Array.isArray(approvalSummary.data?.by_action) ? approvalSummary.data.by_action.map((row: AnyRow) => String(row.action || '')).filter(Boolean) : []),
  ])).map((value) => ({ value, label: value }));
  function currentApprovalFilters() {
    return {
      status: approvalStatusFilter,
      action: approvalActionFilter,
      resource_type: approvalResourceTypeFilter.trim(),
      q: approvalSearch.trim(),
      requested_by: approvalRequestedByFilter.trim(),
      since: approvalSinceFilter.trim(),
      until: approvalUntilFilter.trim()
    };
  }
  function applyApprovalView(id?: string) {
    setApprovalViewID(id);
    if (!id) return;
    const view = (approvalViews.data?.items || []).find((row: AnyRow) => row.id === id);
    const filters = view?.filters || {};
    setApprovalViewName(view?.name || '');
    setApprovalStatusFilter(String(filters.status || ''));
    setApprovalActionFilter(String(filters.action || ''));
    setApprovalResourceTypeFilter(String(filters.resource_type || ''));
    setApprovalSearch(String(filters.q || ''));
    setApprovalRequestedByFilter(String(filters.requested_by || ''));
    setApprovalSinceFilter(String(filters.since || ''));
    setApprovalUntilFilter(String(filters.until || ''));
  }
  async function saveApprovalView() {
    const name = approvalViewName.trim();
    if (!name) {
      message.warning('View name is required');
      return;
    }
    try {
      const view = await api('/api/operation-approval-views', { method: 'POST', body: JSON.stringify({ name, filters: currentApprovalFilters() }) });
      message.success('Approval view saved');
      setApprovalViewID(view.id);
      approvalViews.reload();
    } catch (err: any) {
      message.error(err.message || 'Could not save approval view');
    }
  }
  async function updateApprovalView() {
    if (!approvalViewID) return;
    const name = approvalViewName.trim();
    try {
      const view = await api(`/api/operation-approval-views/${approvalViewID}`, { method: 'PATCH', body: JSON.stringify({ name, filters: currentApprovalFilters() }) });
      message.success('Approval view updated');
      setApprovalViewName(view.name || name);
      approvalViews.reload();
    } catch (err: any) {
      message.error(err.message || 'Could not update approval view');
    }
  }
  function deleteApprovalView() {
    if (!approvalViewID) return;
    Modal.confirm({
      title: 'Delete approval view?',
      okText: 'Delete',
      okButtonProps: { danger: true },
      onOk: async () => {
        await api(`/api/operation-approval-views/${approvalViewID}`, { method: 'DELETE' });
        message.success('Approval view deleted');
        setApprovalViewID(undefined);
        setApprovalViewName('');
        approvalViews.reload();
      }
    });
  }
  async function decideApproval(id: string, decision: 'approve' | 'reject') {
    const result = await api(`/api/operation-approvals/${id}/${decision}`, { method: 'POST', body: '{}' });
    message.success(decision === 'approve' && result.status === 'pending' ? 'Approval recorded' : decision === 'approve' ? 'Approval approved' : 'Approval rejected');
    approvals.reload();
    approvalSummary.reload();
    approvalReminderCandidates.reload();
    ops.reload();
  }
  async function sendApprovalReminder(id: string) {
    const result = await api(`/api/operation-approvals/${id}/remind`, { method: 'POST', body: '{}' });
    if (result.notification_status === 'failed') {
      message.warning('Reminder failed');
    } else {
      message.success('Reminder sent');
    }
    approvals.reload();
    approvalSummary.reload();
    approvalReminderCandidates.reload();
  }
  function openApprovalRule(row?: AnyRow) {
    setEditingRule(row || null);
    setRuleOpen(true);
  }
  async function saveApprovalRule(values: AnyRow) {
    let payload: AnyRow;
    try {
      payload = approvalRulePayload(values);
    } catch (err: any) {
      message.error(err.message || 'Metadata must be valid JSON');
      return;
    }
    const path = editingRule ? `/api/operation-approval-rules/${editingRule.id}` : '/api/operation-approval-rules';
    const method = editingRule ? 'PATCH' : 'POST';
    await api(path, { method, body: JSON.stringify(payload) });
    message.success(editingRule ? 'Approval rule updated' : 'Approval rule created');
    setRuleOpen(false);
    setEditingRule(null);
    ruleForm.resetFields();
    approvalRules.reload();
  }
  async function delegateApproval() {
    if (!approvalAuditID) return;
    if (!delegateEmail.trim()) {
      message.warning('Delegate email is required');
      return;
    }
    await api(`/api/operation-approvals/${approvalAuditID}/delegations`, { method: 'POST', body: JSON.stringify({ to_email: delegateEmail.trim(), reason: delegateReason.trim() }) });
    message.success('Approval delegated');
    setDelegateEmail('');
    setDelegateReason('');
    approvalAudit.reload();
    approvals.reload();
    approvalReminderCandidates.reload();
  }
  async function revokeDelegation(id: string) {
    if (!approvalAuditID) return;
    try {
      await api(`/api/operation-approvals/${approvalAuditID}/delegations/${id}/revoke`, { method: 'POST', body: '{}' });
      message.success('Delegation revoked');
      approvalAudit.reload();
      approvals.reload();
      approvalReminderCandidates.reload();
    } catch (error: any) {
      message.error(error.message || 'Failed to revoke delegation');
    }
  }
  return (
    <Space direction="vertical" size={16} className="full">
      {!embedded && <Typography.Title level={2}>Operations</Typography.Title>}
      <div className="metricGrid">
        <Card><Typography.Text type="secondary">Pending approvals</Typography.Text><Typography.Title level={3}>{approvalSummary.data?.pending ?? 0}</Typography.Title></Card>
        <Card><Typography.Text type="secondary">Expiring soon</Typography.Text><Typography.Title level={3}>{approvalSummary.data?.expiring_soon ?? 0}</Typography.Title></Card>
        <Card><Typography.Text type="secondary">Notification failures</Typography.Text><Typography.Title level={3}>{approvalSummary.data?.notification_failed ?? 0}</Typography.Title></Card>
        <Card loading={approvalReminderCandidates.loading}><Typography.Text type="secondary">SLA watch</Typography.Text><Typography.Title level={3}>{approvalReminderCandidates.data?.items?.length ?? 0}</Typography.Title></Card>
      </div>
      {approvalReminderCandidates.error && <Alert showIcon type="error" message={approvalReminderCandidates.error} />}
      {Array.isArray(approvalSummary.data?.by_action) && approvalSummary.data.by_action.length > 0 && (
        <Space wrap>
          {approvalSummary.data.by_action.map((row: AnyRow) => <Tag key={row.action}>{row.action}: {row.count}</Tag>)}
        </Space>
      )}
      <Typography.Title level={5}>Reminder candidates</Typography.Title>
      <Table<AnyRow> rowKey="id" size="small" dataSource={approvalReminderCandidates.data?.items || []} pagination={{ pageSize: 4 }} columns={[
        { title: 'Approval', dataIndex: 'title' },
        { title: 'Action', dataIndex: 'action' },
        { title: 'Project', dataIndex: 'project_name' },
        { title: 'Reason', render: (_, row) => <Tag color={row.escalation_level === 'danger' ? 'red' : row.escalation_level === 'warning' ? 'gold' : 'blue'}>{row.reminder_reason}</Tag> },
        { title: 'Progress', render: (_, row) => `${row.approved_count || 0}/${row.required_approval_count || 1}` },
        { title: 'Age', render: (_, row) => `${row.age_minutes || 0}m` },
        { title: 'Expires In', render: (_, row) => row.minutes_until_expiry === null || row.minutes_until_expiry === undefined ? '-' : `${row.minutes_until_expiry}m` },
        { title: 'Reminders', render: (_, row) => `${row.reminder_count || 0}` },
        { title: 'Last Reminded', render: (_, row) => row.last_reminded_at || '-' },
        { title: 'Escalations', render: (_, row) => `${row.escalation_count || 0}` },
        { title: 'Last Escalated', render: (_, row) => row.last_escalated_at || '-' },
        { title: 'Requester', dataIndex: 'requested_by_email' },
        { title: 'Action', render: (_, row) => <Space>{canActOnApproval(row, currentRole) && <Button size="small" onClick={() => sendApprovalReminder(row.id)}>Remind</Button>}<Button size="small" onClick={() => setApprovalAuditID(row.id)}>Open</Button></Space> }
      ]} />
      <Space wrap>
        <Typography.Title level={5} style={{ margin: 0 }}>Approval rules</Typography.Title>
        {canEditApprovalRules && <Button size="small" onClick={() => openApprovalRule()}>New rule</Button>}
      </Space>
      <Table<AnyRow> rowKey="id" size="small" dataSource={approvalRules.data?.items || []} pagination={{ pageSize: 4 }} columns={[
        { title: 'Action', dataIndex: 'action' },
        { title: 'Resource', render: (_, row) => row.resource_type || '*' },
        { title: 'Approvers', render: (_, row) => approvalRoles(row.required_approver_roles).join(', ') },
        { title: 'Count', dataIndex: 'required_approval_count' },
        { title: 'Expires', render: (_, row) => `${row.expires_after_minutes || 0}m` },
        { title: 'Notify', render: (_, row) => approvalDestinationTags(row.notification_destinations) },
        { title: 'Escalate', render: (_, row) => approvalEscalationDestinationTags(row) },
        { title: 'Enabled', render: (_, row) => <Tag color={row.enabled ? 'green' : 'default'}>{row.enabled ? 'enabled' : 'disabled'}</Tag> },
        { title: 'Action', render: (_, row) => <Space>{canEditApprovalRules && <Button size="small" onClick={() => openApprovalRule(row)}>Edit</Button>}<Button size="small" onClick={() => setRuleAuditID(row.id)}>History</Button></Space> }
      ]} />
      <Modal title={editingRule ? 'Edit approval rule' : 'Create approval rule'} open={ruleOpen} onCancel={() => { setRuleOpen(false); setEditingRule(null); }} onOk={() => ruleForm.submit()} destroyOnHidden>
        <Form form={ruleForm} layout="vertical" onFinish={saveApprovalRule} initialValues={approvalRuleInitialValues(editingRule)}>
          <Form.Item name="resource_type" label="Resource type">
            <Input placeholder="git_remote, ssh_machine, agent_task, operation, or blank" />
          </Form.Item>
          <Form.Item name="action" label="Action" rules={[{ required: true, message: 'action is required' }]}>
            <Input placeholder="repo.tag" />
          </Form.Item>
          <Form.Item name="required_approver_roles" label="Approver roles">
            <Input placeholder="admin, owner" />
          </Form.Item>
          <Form.Item name="required_approval_count" label="Required approval count">
            <Input type="number" min={1} />
          </Form.Item>
          <Form.Item name="expires_after_minutes" label="Expires after minutes">
            <Input type="number" min={1} />
          </Form.Item>
          <Form.Item name="notification_channels" label="Notification channels">
            <Input placeholder="ui, webhook" />
          </Form.Item>
          <Form.Item name="escalation_after_minutes" label="Escalation after minutes">
            <Input type="number" min={0} />
          </Form.Item>
          <Form.Item name="escalation_channels" label="Escalation channels">
            <Input placeholder="ui, webhook" />
          </Form.Item>
          <Form.Item name="priority" label="Priority">
            <Input type="number" />
          </Form.Item>
          <Form.Item name="enabled" valuePropName="checked">
            <Checkbox>Enabled</Checkbox>
          </Form.Item>
          <Form.Item name="metadata_json" label="Metadata JSON">
            <Input.TextArea rows={4} />
          </Form.Item>
        </Form>
      </Modal>
      <Modal title="Approval rule history" open={Boolean(ruleAuditID)} onCancel={() => setRuleAuditID(undefined)} footer={null} width={900} destroyOnHidden>
        <Table<AnyRow> rowKey="id" size="small" loading={ruleAudits.loading} dataSource={ruleAudits.data?.items || []} pagination={{ pageSize: 5 }} columns={[
          { title: 'Action', render: (_, row) => <Tag color={row.action === 'create' ? 'green' : 'blue'}>{row.action}</Tag> },
          { title: 'Actor', render: (_, row) => row.actor_email || row.actor_user_id || '-' },
          { title: 'Before', render: (_, row) => <JSONBlock value={row.before_state || {}} /> },
          { title: 'After', render: (_, row) => <JSONBlock value={row.after_state || {}} /> },
          { title: 'Created', dataIndex: 'created_at' }
        ]} />
      </Modal>
      <Space wrap>
        <Select allowClear value={approvalViewID} placeholder="Saved view" style={{ width: 220 }} onChange={(value) => applyApprovalView(value)} options={(approvalViews.data?.items || []).map((row: AnyRow) => ({ value: row.id, label: row.name }))} />
        <Input placeholder="View name" value={approvalViewName} onChange={(event) => setApprovalViewName(event.target.value)} style={{ width: 180 }} />
        <Button onClick={saveApprovalView}>Save view</Button>
        <Button disabled={!approvalViewID} onClick={updateApprovalView}>Update view</Button>
        <Button danger disabled={!approvalViewID} onClick={deleteApprovalView}>Delete view</Button>
      </Space>
      <Space wrap>
        <Select allowClear value={approvalStatusFilter || undefined} placeholder="Approval status" style={{ width: 180 }} onChange={(value) => setApprovalStatusFilter(value || '')} options={['pending', 'approved', 'rejected', 'expired'].map((value) => ({ value, label: value }))} />
        <Select allowClear value={approvalActionFilter || undefined} placeholder="Action" style={{ width: 200 }} onChange={(value) => setApprovalActionFilter(value || '')} options={approvalActionOptions} />
        <Input allowClear placeholder="Resource type" value={approvalResourceTypeFilter} onChange={(event) => setApprovalResourceTypeFilter(event.target.value)} style={{ width: 180 }} />
        <Input allowClear placeholder="Search approval" value={approvalSearch} onChange={(event) => setApprovalSearch(event.target.value)} style={{ width: 260 }} />
        <Input allowClear placeholder="Requester" value={approvalRequestedByFilter} onChange={(event) => setApprovalRequestedByFilter(event.target.value)} style={{ width: 220 }} />
        <Input allowClear placeholder="Since RFC3339" value={approvalSinceFilter} onChange={(event) => setApprovalSinceFilter(event.target.value)} style={{ width: 220 }} />
        <Input allowClear placeholder="Until RFC3339" value={approvalUntilFilter} onChange={(event) => setApprovalUntilFilter(event.target.value)} style={{ width: 220 }} />
      </Space>
      <Table<AnyRow> rowKey="id" dataSource={approvals.data?.items || []} pagination={{ pageSize: 6 }} columns={[
        { title: 'Approval', dataIndex: 'title' },
        { title: 'Action', dataIndex: 'action' },
        { title: 'Project', dataIndex: 'project_name' },
        { title: 'Status', render: (_, row) => <Tag color={row.status === 'approved' ? 'green' : row.status === 'rejected' || row.status === 'expired' ? 'red' : 'gold'}>{row.status}</Tag> },
        { title: 'Progress', render: (_, row) => `${row.approved_count || 0}/${row.required_approval_count || 1}` },
        { title: 'Notify', render: (_, row) => <Tag color={row.notification_status === 'failed' ? 'red' : row.notification_status === 'delivered' ? 'green' : 'default'}>{row.notification_status || 'pending'}</Tag> },
        { title: 'Requested By', dataIndex: 'requested_by_email' },
        { title: 'Created', dataIndex: 'created_at' },
        { title: 'Expires', render: (_, row) => row.expires_at ? <Tag color={approvalStillActive(row) ? 'gold' : 'red'}>{row.expires_at}</Tag> : '-' },
        {
          title: 'Decision',
          render: (_, row) => approvalStillActive(row) && canActOnApproval(row, currentRole) ? (
            <Space>
              <Button size="small" type="primary" onClick={() => decideApproval(row.id, 'approve')}>Approve</Button>
              <Button size="small" danger onClick={() => decideApproval(row.id, 'reject')}>Reject</Button>
            </Space>
          ) : row.status === 'pending' ? 'Pending' : row.decided_by_email || row.decision_reason || '-'
        },
        {
          title: 'Audit',
          render: (_, row) => <Button size="small" onClick={() => setApprovalAuditID(row.id)}>Open</Button>
        }
      ]} />
      <Table<AnyRow> rowKey="id" dataSource={ops.data?.items || []} pagination={{ pageSize: 8 }} columns={[
        { title: 'Type', dataIndex: 'operation_type' },
        { title: 'Title', dataIndex: 'title' },
        { title: 'Status', render: (_, row) => <Tag color={operationStatusColor(row.status)}>{row.status}</Tag> },
        { title: 'Created', dataIndex: 'created_at' },
        { title: 'Logs', render: (_, row) => <Button size="small" onClick={() => setLiveOperationID(row.id)}>Live</Button> }
      ]} />
      {liveOperationID && (
        <div className="liveLogPanel">
          <Space direction="vertical" size={12} className="full">
            <Space wrap>
              <Typography.Title level={5} style={{ margin: 0 }}>{liveOperation?.title || liveOperationID}</Typography.Title>
              <Tag color={liveLogTag.color}>{liveLogTag.label}</Tag>
              <Tag>{liveLogs.logs.length} log{liveLogs.logs.length === 1 ? '' : 's'}</Tag>
              <Button size="small" onClick={() => setLiveOperationID(undefined)}>Close</Button>
            </Space>
            {liveLogs.error && <Alert type="error" showIcon message={liveLogs.error} />}
            {!liveLogs.error && liveLogs.logs.length === 0 && <Alert type="info" showIcon message={emptyOperationLogMessage(liveLogs)} />}
            <Table<AnyRow> rowKey="id" size="small" dataSource={liveLogs.logs} pagination={{ pageSize: 8 }} columns={[
              { title: 'Level', dataIndex: 'level', width: 110 },
              { title: 'Message', dataIndex: 'message' },
              { title: 'Fields', render: (_, row) => <JSONBlock value={row.fields} /> },
              { title: 'Created', dataIndex: 'created_at', width: 220 }
            ]} />
          </Space>
        </div>
      )}
      <Modal title="Approval audit" open={Boolean(approvalAuditID)} onCancel={() => setApprovalAuditID(undefined)} footer={null} width={980} destroyOnHidden>
        <Space direction="vertical" size={16} className="full">
          <Table<AnyRow> rowKey="id" size="small" pagination={false} dataSource={approvalAudit.data?.approval ? [approvalAudit.data.approval] : []} columns={[
            { title: 'Approval', dataIndex: 'title' },
            { title: 'Status', render: (_, row) => <Tag color={row.status === 'approved' ? 'green' : row.status === 'rejected' || row.status === 'expired' ? 'red' : 'gold'}>{row.status}</Tag> },
            { title: 'Progress', render: (_, row) => `${row.approved_count || 0}/${row.required_approval_count || 1}` },
            { title: 'Notify', render: (_, row) => <Tag color={row.notification_status === 'failed' ? 'red' : row.notification_status === 'delivered' ? 'green' : 'default'}>{row.notification_status || 'pending'}</Tag> },
            { title: 'Requester', dataIndex: 'requested_by_email' },
            { title: 'Decider', dataIndex: 'decided_by_email' },
            { title: 'Created', dataIndex: 'created_at' }
          ]} />
          {approvalAudit.data?.approval?.notification_last_error && <Alert type="error" showIcon message={approvalAudit.data.approval.notification_last_error} />}
          <ProviderReviewApprovalAudit value={approvalAudit.data?.approval_payload_audit} persistedAttemptLedger={approvalAudit.data?.provider_review_attempt_ledger} />
          {approvalAudit.data?.approval && approvalStillActive(approvalAudit.data.approval) && canActOnApproval(approvalAudit.data.approval, currentRole) && (
            <Space wrap>
              <Input placeholder="Delegate to email" value={delegateEmail} onChange={(event) => setDelegateEmail(event.target.value)} style={{ width: 240 }} />
              <Input placeholder="Reason" value={delegateReason} onChange={(event) => setDelegateReason(event.target.value)} style={{ width: 260 }} />
              <Button onClick={delegateApproval}>Delegate</Button>
            </Space>
          )}
          <Typography.Title level={5}>Delegations</Typography.Title>
          <Table<AnyRow> rowKey="id" size="small" dataSource={approvalAudit.data?.delegations || []} pagination={{ pageSize: 5 }} columns={[
            { title: 'From', render: (_, row) => row.from_user_email || row.from_user_id || '-' },
            { title: 'To', render: (_, row) => row.to_user_email || row.to_user_id || '-' },
            { title: 'Reason', dataIndex: 'reason' },
            { title: 'Status', render: (_, row) => <Tag color={row.revoked_at ? 'default' : 'green'}>{row.revoked_at ? 'revoked' : 'active'}</Tag> },
            { title: 'Created', dataIndex: 'created_at' },
            { title: 'Action', render: (_, row) => canRevokeApprovalDelegation(row, approvalAudit.data?.approval, me.data?.user, currentRole) ? <Button size="small" danger onClick={() => revokeDelegation(row.id)}>Revoke</Button> : '-' }
          ]} />
          <Typography.Title level={5}>Decisions</Typography.Title>
          <Table<AnyRow> rowKey="id" size="small" dataSource={approvalAudit.data?.decisions || []} pagination={{ pageSize: 5 }} columns={[
            { title: 'Decision', render: (_, row) => <Tag color={row.decision === 'approved' ? 'green' : 'red'}>{row.decision}</Tag> },
            { title: 'User', render: (_, row) => row.user_email || row.user_id || '-' },
            { title: 'Reason', dataIndex: 'reason' },
            { title: 'Decided', dataIndex: 'decided_at' }
          ]} />
          <Typography.Title level={5}>Operation</Typography.Title>
          {approvalAudit.data?.operation ? <JSONBlock value={approvalAudit.data.operation} /> : <Typography.Text type="secondary">No operation has been created yet.</Typography.Text>}
          <Typography.Title level={5}>Worker Jobs</Typography.Title>
          <Table<AnyRow> rowKey="id" size="small" dataSource={approvalAudit.data?.worker_jobs || []} pagination={{ pageSize: 5 }} columns={[
            { title: 'Tool', dataIndex: 'tool_name' },
            { title: 'Status', dataIndex: 'status' },
            { title: 'Worker', dataIndex: 'assigned_worker_node_id' },
            { title: 'Error', dataIndex: 'error' },
            { title: 'Created', dataIndex: 'created_at' },
            { title: 'Finished', dataIndex: 'finished_at' }
          ]} />
          <Typography.Title level={5}>Operation Logs</Typography.Title>
          <Table<AnyRow> rowKey="id" size="small" dataSource={approvalAudit.data?.operation_logs || []} pagination={{ pageSize: 6 }} columns={[
            { title: 'Level', dataIndex: 'level' },
            { title: 'Message', dataIndex: 'message' },
            { title: 'Fields', render: (_, row) => <JSONBlock value={row.fields} /> },
            { title: 'Created', dataIndex: 'created_at' }
          ]} />
          <Typography.Title level={5}>Run Records</Typography.Title>
          <JSONBlock value={approvalAudit.data?.run_records || {}} />
        </Space>
      </Modal>
    </Space>
  );
}

function WorkerNodes() {
  const summary = useLoad(() => api('/api/worker-queue/summary'), []);
  const data = summary.data || {};
  const backend = data.backend_summary || {};
  const hasQueueRisk = (data.stale_nodes || 0) > 0 || (data.aged_queued_jobs || 0) > 0 || (data.stale_running_jobs || 0) > 0 || (data.failed_24h || 0) > 0;
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>Worker Nodes</Typography.Title>
      <Alert showIcon message="Node workers register through /api/worker-nodes/register. Start one with go run ./backend/cmd/node-worker." />
      {summary.error && <Alert showIcon type="error" message={summary.error} />}
      {hasQueueRisk && <Alert showIcon type="warning" message="Worker queue needs attention" description={`${data.stale_nodes || 0} stale nodes, ${data.aged_queued_jobs || 0} aged queued jobs, ${data.stale_running_jobs || 0} stale running jobs, ${data.failed_24h || 0} failures in 24h.`} />}
      {backend.message && (
        <Alert
          showIcon
          type="info"
          message="Queue backend"
          description={
            <Space direction="vertical" size={8}>
              <Typography.Text>{backend.message}</Typography.Text>
              <Space size={[8, 8]} wrap>
                <Tag color="blue">backend: {backend.backend}</Tag>
                <Tag color="geekblue">claiming: {backend.claiming}</Tag>
                <Tag color="orange">redis locks: {backend.redis_locking}</Tag>
                <Tag color="orange">pub/sub: {backend.pubsub}</Tag>
                <Tag color="cyan">logs: {backend.log_fanout}</Tag>
              </Space>
            </Space>
          }
        />
      )}
      <div className="metricGrid">
        <Card loading={summary.loading}><Typography.Text type="secondary">Online nodes</Typography.Text><Typography.Title level={4}>{data.online_nodes || 0}/{data.total_nodes || 0}</Typography.Title></Card>
        <Card loading={summary.loading}><Typography.Text type="secondary">Queued jobs</Typography.Text><Typography.Title level={4}>{data.queued_jobs || 0}</Typography.Title><Typography.Text type="secondary">{data.aged_queued_jobs || 0} older than 15m</Typography.Text></Card>
        <Card loading={summary.loading}><Typography.Text type="secondary">Running jobs</Typography.Text><Typography.Title level={4}>{data.running_jobs || 0}</Typography.Title><Typography.Text type="secondary">{data.stale_running_jobs || 0} older than 15m</Typography.Text></Card>
        <Card loading={summary.loading}><Typography.Text type="secondary">24h outcome</Typography.Text><Typography.Title level={4}>{data.completed_24h || 0}/{data.failed_24h || 0}</Typography.Title><Typography.Text type="secondary">completed / failed</Typography.Text></Card>
      </div>
      <Button type="primary" onClick={() => api('/api/worker-nodes/test-job', { method: 'POST', body: JSON.stringify({ message: 'hello node-worker' }) }).then(() => { message.success('Echo job queued'); summary.reload(); })}>Queue echo job</Button>
      <Tabs
        items={[
          {
            key: 'queue',
            label: 'Queue',
            children: (
              <Table<AnyRow> rowKey="tool_name" size="small" dataSource={data.queue_by_tool || []} pagination={false} columns={[
                { title: 'Tool', dataIndex: 'tool_name' },
                { title: 'Queued', dataIndex: 'queued' }
              ]} />
            )
          },
          {
            key: 'nodes',
            label: 'Node kinds',
            children: (
              <Table<AnyRow> rowKey="kind" size="small" dataSource={data.nodes_by_kind || []} pagination={false} columns={[
                { title: 'Kind', dataIndex: 'kind' },
                { title: 'Count', dataIndex: 'count' }
              ]} />
            )
          },
          {
            key: 'failures',
            label: 'Recent failures',
            children: (
              <Table<AnyRow> rowKey="id" size="small" dataSource={data.recent_failures || []} pagination={false} columns={[
                { title: 'Tool', dataIndex: 'tool_name' },
                { title: 'Error', render: (_, row) => shortText(row.error || '-') },
                { title: 'Updated', dataIndex: 'updated_at' }
              ]} />
            )
          }
        ]}
      />
      <Operations embedded />
    </Space>
  );
}

function AIRuntime() {
  const runtimes = useLoad(() => api('/api/ai-runtimes'), []);
  const [open, setOpen] = useState(false);
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="AI Runtime" onCreate={() => setOpen(true)} />
      <Table<AnyRow> rowKey="id" dataSource={runtimes.data?.items || []} pagination={false} columns={[
        { title: 'Name', dataIndex: 'name' },
        { title: 'Type', dataIndex: 'runtime_type' },
        { title: 'Binary', dataIndex: 'codex_binary' },
        { title: 'Status', dataIndex: 'status' },
        { title: 'Action', render: (_, row) => <Button size="small" onClick={() => api(`/api/ai-runtimes/${row.id}/verify`, { method: 'POST' }).then(runtimes.reload)}>Verify</Button> }
      ]} />
      <CreateModal title="Create AI runtime" open={open} setOpen={setOpen} fields={['name', 'runtime_type', 'codex_binary', 'model']} onSubmit={(v) => api('/api/ai-runtimes', { method: 'POST', body: JSON.stringify(v) }).then(runtimes.reload)} />
    </Space>
  );
}

function AgentTasks() {
  const projects = useLoad(() => api('/api/projects'), []);
  const projectRows = projects.data?.items || [];
  const projectPick = useSelectedRow(projectRows);
  const project = projectPick.selected || projectRows[0];
  const tasks = useLoad(() => project ? api(`/api/projects/${project.id}/agent/tasks`) : Promise.resolve({ items: [] }), [project?.id]);
  const [open, setOpen] = useState(false);
  const [taskID, setTaskID] = useState<string>();
  const [toolAuditSnapshotLoading, setToolAuditSnapshotLoading] = useState(false);
  const [toolAuditSnapshotResult, setToolAuditSnapshotResult] = useState<AnyRow>();
  const [codeAuditSnapshotLoading, setCodeAuditSnapshotLoading] = useState(false);
  const [codeAuditSnapshotResult, setCodeAuditSnapshotResult] = useState<AnyRow>();
  const taskDetail = useLoad(() => taskID ? api(`/api/agent/tasks/${taskID}`) : Promise.resolve(null), [taskID]);
  useEffect(() => {
    setToolAuditSnapshotResult(undefined);
    setCodeAuditSnapshotResult(undefined);
  }, [taskID]);
  async function createTask(values: AnyRow) {
    if (!project) {
      message.error('Select a project first');
      return;
    }
    await api(`/api/projects/${project.id}/agent/tasks`, { method: 'POST', body: JSON.stringify(values) });
    message.success('Task created');
    tasks.reload();
  }
  async function generateAgentPlan(id: string) {
    await api(`/api/agent/tasks/${id}/generate-plan`, { method: 'POST', body: '{}' });
    message.success('Plan generated');
    tasks.reload();
    taskDetail.reload();
  }
  async function approveAgentPlan(id: string) {
    await api(`/api/agent/tasks/${id}/approve-plan`, { method: 'POST', body: '{}' });
    message.success('Plan approved');
    tasks.reload();
    taskDetail.reload();
  }
  async function executeAgentTask(id: string) {
    const result = await api(`/api/agent/tasks/${id}/execute`, { method: 'POST', body: '{}' });
    message.success(result.approval ? 'Approval requested' : 'Agent execution queued');
    tasks.reload();
    taskDetail.reload();
  }
  async function recordToolAuditSnapshot(id: string) {
    setToolAuditSnapshotLoading(true);
    try {
      const result = await api(`/api/agent/tasks/${id}/tool-audit-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setToolAuditSnapshotResult(result);
      taskDetail.reload();
      if (result.recording_ready === false) {
        message.warning(result.message || 'Agent audit snapshot is not ready yet');
      } else {
        message.success(result.agent_tool_audit_snapshot_written ? 'Agent audit snapshot recorded' : 'Agent audit snapshot already current');
      }
    } catch (error: any) {
      setToolAuditSnapshotResult(undefined);
      message.error(error.message || 'Request failed');
    } finally {
      setToolAuditSnapshotLoading(false);
    }
  }
  async function recordCodeAuditSnapshot(id: string) {
    setCodeAuditSnapshotLoading(true);
    try {
      const result = await api(`/api/agent/tasks/${id}/code-audit-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setCodeAuditSnapshotResult(result);
      taskDetail.reload();
      if (result.recording_ready === false) {
        message.warning(result.message || 'Agent code audit snapshot is not ready yet');
      } else {
        message.success(result.agent_code_audit_snapshot_written ? 'Agent code audit snapshot recorded' : 'Agent code audit snapshot already current');
      }
    } catch (error: any) {
      setCodeAuditSnapshotResult(undefined);
      message.error(error.message || 'Request failed');
    } finally {
      setCodeAuditSnapshotLoading(false);
    }
  }
  function latestPlanApproved(row: AnyRow) {
    return row.latest_plan_status === 'approved' || row.plans?.[0]?.status === 'approved';
  }
  function toolCallSummary(row: AnyRow) {
    const input = row.input || {};
    const output = row.output || {};
    if (row.tool_name === 'runtime.check') {
      const cliReadiness = output.codex_cli_readiness || {};
      const gates = agentReadinessGateTags(cliReadiness.gates);
      return <Space size={4} wrap>
        <Tag color={output.readiness === 'verified' ? 'green' : output.readiness === 'missing' ? 'red' : 'gold'}>{output.readiness || input.status || 'unknown'}</Tag>
        <Typography.Text>{input.runtime_name || input.codex_binary || 'No runtime'}</Typography.Text>
        {cliReadiness.readiness ? <Tag color={cliReadiness.readiness === 'metadata_ready' ? 'blue' : 'red'}>CLI {cliReadiness.readiness}</Tag> : null}
        {gates}
        {cliReadiness.next_step ? <Typography.Text type="secondary">{shortText(cliReadiness.next_step, 96)}</Typography.Text> : null}
      </Space>;
    }
    if (row.tool_name === 'patch.prepare') {
      const guardrail = output.patch_workflow_guardrail || {};
      const codePlan = guardrail.code_modification_plan || {};
      const codeEvidence = codePlan.code_modification_evidence || {};
      const executionArming = codePlan.execution_arming_plan || {};
      const sourceReview = codePlan.source_checkout_branch_review_plan || {};
      const reasons = Array.isArray(guardrail.blocked_reasons) ? guardrail.blocked_reasons : [];
      const readiness = agentReadinessGateTags(guardrail.execution_readiness);
      return <Space size={4} wrap>
        <Tag color="gold">{codePlan.plan_state || guardrail.execution_mode || input.mode || 'simulation_only'}</Tag>
        <Tag color={guardrail.repository_mutation_allowed === true ? 'red' : 'green'}>{guardrail.repository_mutation_allowed === true ? 'Repo mutation allowed' : 'Repo mutation blocked'}</Tag>
        <Tag color={codePlan.source_checkout_performed === true ? 'red' : 'green'}>{codePlan.source_checkout_performed === true ? 'Checkout done' : 'No checkout'}</Tag>
        <Tag color={codePlan.branch_created === true ? 'red' : 'green'}>{codePlan.branch_created === true ? 'Branch created' : 'No branch'}</Tag>
        <Tag color={codePlan.diff_materialized === true ? 'red' : 'green'}>{codePlan.diff_materialized === true ? 'Diff ready' : 'No diff'}</Tag>
        <Tag color={codePlan.commit_push_agent_invoked === true ? 'blue' : 'gold'}>{codePlan.commit_push_agent_invoked === true ? 'Commit agent invoked' : 'No commit agent'}</Tag>
        {codePlan.execution_arming_plan ? <Tag color={executionArming.arming_ready === true ? 'gold' : 'red'}>arming {executionArming.arming_state || 'blocked'}</Tag> : null}
        {codePlan.execution_arming_plan ? <Tag>{executionArming.arming_ready === true ? 'operator review ready' : 'execution blocked'}</Tag> : null}
        {codePlan.source_checkout_branch_review_plan ? <Tag color={sourceReview.review_ready === true ? 'gold' : 'red'}>source review {sourceReview.review_state || 'blocked'}</Tag> : null}
        {codePlan.source_checkout_branch_review_plan ? <Tag color={sourceReview.default_branch_direct_write_blocked === true ? 'green' : 'red'}>{sourceReview.default_branch_direct_write_blocked === true ? 'default branch blocked' : 'default branch writable'}</Tag> : null}
        {codePlan.source_checkout_branch_review_plan ? <Tag>{sourceReview.review_branch_required === true ? 'review branch required' : 'no review branch policy'}</Tag> : null}
        {codeEvidence.has_code_modification_audit ? <Tag color={agentAuditEvidenceColor(codeEvidence.evidence_state)}>code audit {codeEvidence.evidence_state}</Tag> : null}
        {codeEvidence.codex_execution_plan_recorded ? <Tag color="blue">codex plan audit</Tag> : null}
        {codeEvidence.patch_prepare_audit_recorded ? <Tag color="blue">patch audit</Tag> : null}
        {readiness}
        {reasons.length ? <Typography.Text>{reasons.length} blocked reason{reasons.length === 1 ? '' : 's'}</Typography.Text> : <Typography.Text>{output.message || 'Mutation disabled'}</Typography.Text>}
        {codePlan.message ? <Typography.Text type="secondary">{shortText(codePlan.message, 96)}</Typography.Text> : null}
        {guardrail.next_step ? <Typography.Text type="secondary">{shortText(guardrail.next_step, 96)}</Typography.Text> : null}
      </Space>;
    }
    if (row.tool_name === 'codex.execution.plan') {
      const plan = output.codex_execution_plan || {};
      const backends = Array.isArray(plan.disabled_backends) ? plan.disabled_backends : [];
      const controls = Array.isArray(plan.required_controls) ? plan.required_controls : [];
      return <Space size={4} wrap>
        <Tag color="gold">{plan.plan_state || 'blocked'}</Tag>
        <Tag color={plan.prerequisite_state === 'metadata_available' ? 'blue' : 'red'}>{plan.prerequisite_state || 'metadata_blocked'}</Tag>
        <Tag color={plan.process_spawn_enabled === true ? 'red' : 'green'}>{plan.process_spawn_enabled === true ? 'Process enabled' : 'No process'}</Tag>
        <Tag color={plan.repository_mutation_allowed === true ? 'red' : 'green'}>{plan.repository_mutation_allowed === true ? 'Repo mutation allowed' : 'Repo mutation blocked'}</Tag>
        <Typography.Text>{backends.length} disabled backend{backends.length === 1 ? '' : 's'}</Typography.Text>
        <Typography.Text type="secondary">{controls.length} required control{controls.length === 1 ? '' : 's'}</Typography.Text>
        {plan.message ? <Typography.Text type="secondary">{shortText(plan.message, 96)}</Typography.Text> : null}
      </Space>;
    }
    if (row.tool_name === 'worker.dispatch.plan') {
      const plan = output.worker_dispatch_plan || {};
      const backends = Array.isArray(plan.disabled_backends) ? plan.disabled_backends : [];
      const controls = Array.isArray(plan.required_controls) ? plan.required_controls : [];
      const capabilities = Array.isArray(plan.required_worker_capabilities) ? plan.required_worker_capabilities : [];
      const claimPlan = plan.worker_claim_plan || {};
      const toolPlan = plan.tool_invocation_plan || {};
      const armingPlan = plan.tool_execution_arming_plan || {};
      const reviewPlan = plan.tool_invocation_review_plan || {};
      const callbackPlan = plan.result_callback_plan || {};
      const evidence = plan.tool_call_audit_evidence || {};
      return <Space size={4} wrap>
        <Tag color="gold">{plan.dispatch_state || 'blocked'}</Tag>
        <Tag color={plan.prerequisite_state === 'metadata_available' ? 'blue' : 'red'}>{plan.prerequisite_state || 'metadata_blocked'}</Tag>
        <Tag color="gold">claim {claimPlan.claim_state || 'blocked'}</Tag>
        <Tag color={plan.audit_worker_execution_enabled === true ? 'blue' : 'default'}>{plan.audit_worker_execution_enabled === true ? 'Audit worker queued' : 'Audit worker blocked'}</Tag>
        <Tag color={plan.worker_claim_enabled === true ? 'blue' : 'green'}>{plan.worker_claim_enabled === true ? 'Worker claim wired' : 'No worker claim'}</Tag>
        <Tag color="gold">tools {toolPlan.invocation_state || 'blocked'}</Tag>
        <Tag color={plan.tool_invocation_enabled === true ? 'red' : 'green'}>{plan.tool_invocation_enabled === true ? 'Tools enabled' : 'Tools blocked'}</Tag>
        {plan.tool_execution_arming_plan ? <Tag color={armingPlan.arming_ready === true ? 'gold' : 'red'}>arming {armingPlan.arming_state || 'blocked'}</Tag> : null}
        {plan.tool_execution_arming_plan ? <Tag>{armingPlan.arming_ready === true ? 'operator review ready' : 'arming blocked'}</Tag> : null}
        {plan.tool_invocation_review_plan ? <Tag color={reviewPlan.review_ready === true ? 'gold' : 'red'}>tool review {reviewPlan.review_state || 'blocked'}</Tag> : null}
        {plan.tool_invocation_review_plan ? <Tag color={reviewPlan.live_tool_invocation_allowed === true ? 'red' : 'green'}>{reviewPlan.live_tool_invocation_allowed === true ? 'Live tool allowed' : 'No live tool call'}</Tag> : null}
        {plan.tool_invocation_review_plan ? <Tag>{reviewPlan.raw_tool_output_recorded === true ? 'raw output recorded' : 'no raw output'}</Tag> : null}
        <Tag color="gold">callback {callbackPlan.callback_state || 'blocked'}</Tag>
        <Tag color={plan.result_callback_enabled === true ? 'blue' : 'green'}>{plan.result_callback_enabled === true ? 'Callback wired' : 'Callback blocked'}</Tag>
        {evidence.tool_call_count ? <Tag color={agentAuditEvidenceColor(evidence.evidence_state)}>audit {evidence.evidence_state}</Tag> : null}
        {evidence.tool_call_count ? <Tag>{evidence.tool_call_count} audit calls</Tag> : null}
        {callbackPlan.callback_scope ? <Tag>{callbackPlan.callback_scope}</Tag> : null}
        <Typography.Text>{capabilities.length} worker capabilit{capabilities.length === 1 ? 'y' : 'ies'}</Typography.Text>
        <Typography.Text>{backends.length} disabled backend{backends.length === 1 ? '' : 's'}</Typography.Text>
        <Typography.Text type="secondary">{controls.length} required control{controls.length === 1 ? '' : 's'}</Typography.Text>
        {plan.message ? <Typography.Text type="secondary">{shortText(plan.message, 96)}</Typography.Text> : null}
      </Space>;
    }
    return <Typography.Text>{output.message || '-'}</Typography.Text>;
  }
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="Agent Tasks" onCreate={() => setOpen(true)} />
      <Select value={project?.id} style={{ width: 280 }} onChange={projectPick.setSelectedID} options={projectRows.map((row: AnyRow) => ({ value: row.id, label: row.name }))} />
      <Alert showIcon message="Agent plans are read-only context summaries first. Execution goes through approval and records tool-call audit rows before any future mutation path." />
      <Table<AnyRow> rowKey="id" dataSource={tasks.data?.items || []} pagination={{ pageSize: 8 }} columns={[
        { title: 'Title', dataIndex: 'title' },
        { title: 'Status', render: (_, row) => <Tag>{row.status}</Tag> },
        { title: 'Latest plan', render: (_, row) => row.latest_plan_status ? <Tag color={row.latest_plan_status === 'approved' ? 'green' : 'blue'}>{row.latest_plan_status}</Tag> : '-' },
        { title: 'Created', dataIndex: 'created_at' },
        { title: 'Action', render: (_, row) => <Space><Button size="small" onClick={() => setTaskID(row.id)}>View</Button><Button size="small" onClick={() => generateAgentPlan(row.id)}>Generate</Button><Button size="small" onClick={() => executeAgentTask(row.id)} disabled={!latestPlanApproved(row)}>Execute</Button></Space> }
      ]} />
      <CreateModal title="Create agent task" open={open} setOpen={setOpen} fields={['title', 'prompt']} onSubmit={createTask} />
      <Modal title={taskDetail.data?.title || 'Agent task'} open={Boolean(taskID)} onCancel={() => setTaskID(undefined)} footer={null} width={980} destroyOnHidden>
        {taskDetail.data && <Space direction="vertical" size={16} className="full">
          <Typography.Paragraph>{taskDetail.data.prompt}</Typography.Paragraph>
          <Space wrap>
            <Button size="small" type="primary" onClick={() => generateAgentPlan(taskDetail.data.id)}>Generate plan</Button>
            <Button size="small" onClick={() => approveAgentPlan(taskDetail.data.id)} disabled={!taskDetail.data.plans?.length}>Approve latest</Button>
            <Button size="small" onClick={() => executeAgentTask(taskDetail.data.id)} disabled={!latestPlanApproved(taskDetail.data)}>Execute</Button>
            <Button
              size="small"
              onClick={() => recordToolAuditSnapshot(taskDetail.data.id)}
              loading={toolAuditSnapshotLoading}
              disabled={!taskDetail.data.tool_call_audit_evidence?.sanitized_result_recorded}
            >
              Record audit snapshot
            </Button>
            <Button
              size="small"
              onClick={() => recordCodeAuditSnapshot(taskDetail.data.id)}
              loading={codeAuditSnapshotLoading}
              disabled={
                taskDetail.data.code_modification_evidence?.evidence_state !== 'recorded' ||
                !taskDetail.data.code_modification_evidence?.sanitized_result_recorded ||
                !taskDetail.data.code_modification_evidence?.worker_dispatch_audit_recorded ||
                !taskDetail.data.code_modification_evidence?.codex_execution_plan_recorded ||
                !taskDetail.data.code_modification_evidence?.patch_prepare_audit_recorded ||
                Boolean(taskDetail.data.code_modification_evidence?.active_tool_call_count)
              }
            >
              Record code audit snapshot
            </Button>
          </Space>
          <Table<AnyRow> rowKey="id" size="small" dataSource={taskDetail.data.plans || []} pagination={{ pageSize: 4 }} columns={[
            { title: 'Status', render: (_, row) => <Tag color={row.status === 'approved' ? 'green' : 'blue'}>{row.status}</Tag> },
            { title: 'Created', dataIndex: 'created_at' },
            { title: 'Plan', render: (_, row) => <Typography.Paragraph className="mono-pre">{row.content}</Typography.Paragraph> }
          ]} />
          <Typography.Title level={5}>Tool-call audit</Typography.Title>
          {taskDetail.data.tool_call_audit_evidence?.tool_call_count ? (
            <Space wrap>
              <Tag color={agentAuditEvidenceColor(taskDetail.data.tool_call_audit_evidence.evidence_state)}>audit {taskDetail.data.tool_call_audit_evidence.evidence_state || 'not_recorded'}</Tag>
              <Tag>{taskDetail.data.tool_call_audit_evidence.tool_call_count || 0} calls</Tag>
              {taskDetail.data.tool_call_audit_evidence.active_count ? <Tag color="blue">{taskDetail.data.tool_call_audit_evidence.active_count} active</Tag> : null}
              {taskDetail.data.tool_call_audit_evidence.failed_count ? <Tag color="red">{taskDetail.data.tool_call_audit_evidence.failed_count} failed</Tag> : null}
              <Tag>{taskDetail.data.tool_call_audit_evidence.sanitized_result_recorded ? 'sanitized result recorded' : 'sanitized result pending'}</Tag>
            </Space>
          ) : null}
          {toolAuditSnapshotResult ? (
            <Space wrap>
              <Tag color={toolAuditSnapshotResult.recording_state === 'recorded' ? 'green' : toolAuditSnapshotResult.recording_state === 'asset_missing' ? 'red' : 'gold'}>snapshot {toolAuditSnapshotResult.recording_state || 'pending'}</Tag>
              <Tag>{toolAuditSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
              <Tag>{toolAuditSnapshotResult.agent_task_asset_observed ? 'agent task asset observed' : 'agent task asset missing'}</Tag>
              <Tag>{toolAuditSnapshotResult.raw_tool_output_recorded ? 'raw output recorded' : 'no raw output'}</Tag>
              <Tag>{toolAuditSnapshotResult.secret_included ? 'secret included' : 'no secrets'}</Tag>
            </Space>
          ) : null}
          {taskDetail.data.code_modification_evidence ? (
            <Space wrap>
              <Tag color={agentAuditEvidenceColor(taskDetail.data.code_modification_evidence.evidence_state)}>code audit {taskDetail.data.code_modification_evidence.evidence_state || 'not_recorded'}</Tag>
              <Tag>{taskDetail.data.code_modification_evidence.codex_execution_plan_recorded ? 'codex plan recorded' : 'no codex plan'}</Tag>
              <Tag>{taskDetail.data.code_modification_evidence.patch_prepare_audit_recorded ? 'patch audit recorded' : 'no patch audit'}</Tag>
              <Tag>{taskDetail.data.code_modification_evidence.sanitized_result_recorded ? 'code audit recorded' : 'code audit pending'}</Tag>
            </Space>
          ) : null}
          {codeAuditSnapshotResult ? (
            <Space wrap>
              <Tag color={codeAuditSnapshotResult.recording_state === 'recorded' ? 'green' : codeAuditSnapshotResult.recording_state === 'asset_missing' ? 'red' : 'gold'}>code snapshot {codeAuditSnapshotResult.recording_state || 'pending'}</Tag>
              <Tag>{codeAuditSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
              <Tag>{codeAuditSnapshotResult.agent_task_asset_observed ? 'agent task asset observed' : 'agent task asset missing'}</Tag>
              <Tag>{codeAuditSnapshotResult.source_checkout_performed ? 'checkout done' : 'no checkout'}</Tag>
              <Tag>{codeAuditSnapshotResult.branch_created ? 'branch created' : 'no branch'}</Tag>
              <Tag>{codeAuditSnapshotResult.diff_materialized ? 'diff materialized' : 'no diff'}</Tag>
              <Tag>{codeAuditSnapshotResult.file_patch_applied ? 'patch applied' : 'no patch apply'}</Tag>
              <Tag>{codeAuditSnapshotResult.git_commit_created ? 'commit created' : 'no commit'}</Tag>
              <Tag>{codeAuditSnapshotResult.git_push_performed ? 'push performed' : 'no push'}</Tag>
              <Tag>{codeAuditSnapshotResult.contains_token ? 'token present' : 'no tokens'}</Tag>
            </Space>
          ) : null}
          <Table<AnyRow> rowKey="id" size="small" dataSource={taskDetail.data.tool_calls || []} pagination={{ pageSize: 5 }} columns={[
            { title: 'Tool', dataIndex: 'tool_name' },
            { title: 'Status', render: (_, row) => <Tag color={row.status === 'completed' ? 'green' : row.status === 'failed' ? 'red' : 'blue'}>{row.status}</Tag> },
            { title: 'Summary', render: (_, row) => toolCallSummary(row) },
            { title: 'Operation', dataIndex: 'operation_run_id', render: (value) => value || '-' },
            { title: 'Error', dataIndex: 'error_message', render: (value) => value || '-' },
            { title: 'Input', render: (_, row) => <JSONBlock value={row.input} /> },
            { title: 'Output', render: (_, row) => <JSONBlock value={row.output} /> },
            { title: 'Created', dataIndex: 'created_at' }
          ]} />
        </Space>}
      </Modal>
    </Space>
  );
}

function argoStatusColor(status: any) {
  switch (String(status || '').toLowerCase()) {
    case 'synced':
    case 'completed':
      return 'green';
    case 'outofsync':
      return 'orange';
    case 'failed':
    case 'error':
    case 'degraded':
      return 'red';
    default:
      return 'blue';
  }
}

function rollbackReadinessColor(status: any) {
  switch (String(status || '').toLowerCase()) {
    case 'previewable':
      return 'blue';
    case 'incomplete':
      return 'orange';
    case 'blocked':
      return 'red';
    default:
      return 'default';
  }
}

function buildDeploymentPosture(targets: AnyRow[], records: AnyRow[], rollbackPoints: AnyRow[]) {
  const unhealthyTargets = targets.filter((row) => deploymentStatusUnhealthy(row.status)).length;
  const environments = new Set([
    ...targets.map((row) => String(row.environment || '').trim()).filter(Boolean),
    ...records.map((row) => String(row.environment || '').trim()).filter(Boolean)
  ]).size;
  const availableRollbacks = rollbackPoints.filter((row) => String(row.rollback_readiness || '').toLowerCase() === 'previewable').length;
  const latestRecord = records[0];
  const summary = targets.length === 0
    ? 'No deployment targets yet'
    : unhealthyTargets > 0
      ? `${unhealthyTargets} deployment target${unhealthyTargets === 1 ? '' : 's'} need attention`
      : latestRecord
        ? `Latest observed deployment: ${latestRecord.name || latestRecord.deployment_target_name || 'unknown'}`
        : 'Deployment targets look healthy';
  return {
    targets: targets.length,
    unhealthy: unhealthyTargets,
    environments,
    rollbackPoints: availableRollbacks,
    summary
  };
}

function buildRollbackGuardrail(rollbackPoints: AnyRow[]) {
  if (!rollbackPoints.length) return null;
  const previewable = rollbackPoints.filter((row) => String(row.rollback_readiness || '').toLowerCase() === 'previewable').length;
  const executable = rollbackPoints.filter((row) => row.rollback_executable === true).length;
  const mode = String(rollbackPoints[0]?.rollback_execution_mode || '').trim() || 'read_only_preview';
  return {
    type: executable > 0 ? 'warning' as const : 'info' as const,
    message: executable > 0
      ? `${executable} rollback point${executable === 1 ? '' : 's'} marked executable`
      : 'Rollback execution is disabled in this first version',
    description: executable > 0
      ? `Executable rollback points are visible in ${mode}, but operators should confirm approval and environment rules before queueing any rollback action.`
      : previewable > 0
        ? `${previewable} rollback point${previewable === 1 ? '' : 's'} have revision metadata available for review; execution mode is ${mode}, and no rollback action will be queued from ASSOPS yet.`
        : 'Rollback points are tracked for audit and context, but none are currently previewable.'
  };
}

function buildDeploymentExecutionGuardrail(targets: AnyRow[]) {
  if (!targets.length) return null;
  const planned = targets.filter((row) => row.deployment_execution_readiness?.status === 'planned').length;
  const blocked = targets.filter((row) => row.deployment_execution_readiness?.status === 'blocked').length;
  return {
    type: planned > 0 && blocked === 0 ? 'info' as const : 'warning' as const,
    message: 'Deployment execution is dry-run only',
    description: planned > 0
      ? `${planned} target${planned === 1 ? '' : 's'} have dry-run execution prerequisites planned; Helm/k8s execution remains disabled.`
      : `${blocked} target${blocked === 1 ? '' : 's'} need metadata or health review before execution can be planned.`
  };
}

function deploymentExecutionReadinessView(row: AnyRow) {
  const readiness = row.deployment_execution_readiness || {};
  const executionPlan = readiness.execution_plan || {};
  const requiredControls = Array.isArray(executionPlan.required_controls) ? executionPlan.required_controls : [];
  const disabledBackends = Array.isArray(executionPlan.disabled_backends) ? executionPlan.disabled_backends : [];
  const suppressedFields = Array.isArray(executionPlan.suppressed_fields) ? executionPlan.suppressed_fields : [];
  const status = String(readiness.status || 'unknown');
  const reasons = Array.isArray(readiness.blocked_reasons) ? readiness.blocked_reasons : [];
  return (
    <Space direction="vertical" size={2}>
      <Space size={4} wrap>
        <Tag color={status === 'planned' ? 'blue' : status === 'blocked' ? 'red' : 'default'}>{status}</Tag>
        <Tag>{readiness.mode || 'dry_run'}</Tag>
        <Tag>execution: {readiness.execution_enabled === true ? 'enabled' : 'disabled'}</Tag>
      </Space>
      {executionPlan.mode ? (
        <Space size={4} wrap>
          <Tag>{String(executionPlan.mode).replaceAll('_', ' ')}</Tag>
          <Tag color={executionPlan.plan_state === 'blocked' ? 'red' : 'blue'}>plan {String(executionPlan.plan_state || 'blocked')}</Tag>
          <Tag color={executionPlan.prerequisite_state === 'planned' ? 'blue' : 'gold'}>prereq {String(executionPlan.prerequisite_state || 'blocked')}</Tag>
          <Tag>controls {requiredControls.length}</Tag>
          <Tag>disabled backends {disabledBackends.length}</Tag>
          <Tag>suppressed {suppressedFields.length}</Tag>
          <Tag>{executionPlan.kubernetes_api_call_made === true ? 'k8s called' : 'no k8s call'}</Tag>
          <Tag>{executionPlan.helm_command_invoked === true ? 'helm invoked' : 'helm disabled'}</Tag>
          <Tag>{String(executionPlan.deployment_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {reasons.length ? <Typography.Text type="secondary">{shortText(String(reasons[0]), 72)}</Typography.Text> : <Typography.Text type="secondary">{shortText(String(readiness.message || ''), 72)}</Typography.Text>}
    </Space>
  );
}

function rollbackExecutionPlanView(row: AnyRow) {
  const plan = row.rollback_execution_plan || {};
  const requiredControls = Array.isArray(plan.required_controls) ? plan.required_controls : [];
  const disabledBackends = Array.isArray(plan.disabled_backends) ? plan.disabled_backends : [];
  const suppressedFields = Array.isArray(plan.suppressed_fields) ? plan.suppressed_fields : [];
  if (!plan.mode) {
    return <Tag>{row.rollback_execution_mode || 'read_only_preview'}</Tag>;
  }
  return (
    <Space direction="vertical" size={2}>
      <Space size={4} wrap>
        <Tag>{String(plan.mode).replaceAll('_', ' ')}</Tag>
        <Tag color={plan.plan_state === 'blocked' ? 'red' : 'blue'}>plan {String(plan.plan_state || 'blocked')}</Tag>
        <Tag color={plan.prerequisite_state === 'metadata_available' ? 'blue' : 'gold'}>metadata {String(plan.prerequisite_state || 'metadata_blocked')}</Tag>
        <Tag>controls {requiredControls.length}</Tag>
        <Tag>disabled backends {disabledBackends.length}</Tag>
        <Tag>suppressed {suppressedFields.length}</Tag>
      </Space>
      <Space size={4} wrap>
        <Tag>{plan.kubernetes_api_call_made === true ? 'k8s called' : 'no k8s call'}</Tag>
        <Tag>{plan.helm_command_invoked === true ? 'helm invoked' : 'helm disabled'}</Tag>
        <Tag>{plan.rollback_mutation || 'disabled'}</Tag>
      </Space>
    </Space>
  );
}

function deploymentStatusUnhealthy(status: any) {
  const value = String(status || '').toLowerCase();
  return ['failed', 'error', 'degraded', 'outofsync', 'missing', 'unknown'].includes(value);
}

function ConfigPage() {
  const projects = useLoad(() => api('/api/projects'), []);
  const projectRows = projects.data?.items || [];
  const projectPick = useSelectedRow(projectRows);
  const project = projectPick.selected;
  const [argoOpen, setArgoOpen] = useState(false);
  const [argoSyncOpID, setArgoSyncOpID] = useState<string>();
  const [podLogPreview, setPodLogPreview] = useState<AnyRow>();
  const [podLogLoading, setPodLogLoading] = useState(false);
  const [podLogRunLoading, setPodLogRunLoading] = useState(false);
  const [podLogRunResult, setPodLogRunResult] = useState<AnyRow>();
  const [podLogSnapshotLoading, setPodLogSnapshotLoading] = useState(false);
  const [podLogSnapshotResult, setPodLogSnapshotResult] = useState<AnyRow>();
  const [sshOpen, setSSHOpen] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);
  const [sshSnapshotLoading, setSSHSnapshotLoading] = useState(false);
  const [sshSnapshotResult, setSSHSnapshotResult] = useState<AnyRow>();
  const argoConnections = useLoad(() => project ? api(`/api/projects/${project.id}/argo/connections`) : Promise.resolve({ items: [] }), [project?.id]);
  const argoRows = argoConnections.data?.items || [];
  const argoPick = useSelectedRow(argoRows);
  const argoApps = useLoad(() => project ? api(`/api/projects/${project.id}/argo/apps`) : Promise.resolve({ items: [] }), [project?.id]);
  const deploymentTargets = useLoad(() => project ? api(`/api/projects/${project.id}/deployment-targets`) : Promise.resolve({ items: [] }), [project?.id]);
  const deploymentRecords = useLoad(() => project ? api(`/api/projects/${project.id}/deployment-records`) : Promise.resolve({ items: [] }), [project?.id]);
  const rollbackPoints = useLoad(() => project ? api(`/api/projects/${project.id}/rollback-points`) : Promise.resolve({ items: [] }), [project?.id]);
  const ssh = useLoad(() => project ? api(`/api/projects/${project.id}/ssh-machines`) : Promise.resolve({ items: [] }), [project?.id]);
  const sshRows = ssh.data?.items || [];
  const sshPick = useSelectedRow(sshRows);
  const sshRehearsal = useLoad(() => sshPick.selectedID ? api(`/api/ssh-machines/${sshPick.selectedID}/rehearsal`) : Promise.resolve(null), [sshPick.selectedID]);
  const sshRehearsalView = sshRehearsal.data ? {
    mode: sshRehearsal.data.mode,
    rehearsal_state: sshRehearsal.data.rehearsal_state,
    execution_enabled: sshRehearsal.data.execution_enabled,
    external_call_made: sshRehearsal.data.external_call_made,
    ssh_process_started: sshRehearsal.data.ssh_process_started,
    command_executed: sshRehearsal.data.command_executed,
    live_evidence_recorded: sshRehearsal.data.live_evidence_recorded,
    sanitized_result_recorded: sshRehearsal.data.sanitized_result_recorded,
    result_recording_state: sshRehearsal.data.result_recording_state,
    private_key_included: sshRehearsal.data.private_key_included,
    stdout_included: sshRehearsal.data.stdout_included,
    stderr_included: sshRehearsal.data.stderr_included,
    approval_request_plan: sshRehearsal.data.approval_request_plan,
    auth_binding_plan: sshRehearsal.data.auth_binding_plan,
    verify_execution_plan: sshRehearsal.data.verify_execution_plan,
    exec_execution_plan: sshRehearsal.data.exec_execution_plan,
    result_recording_plan: sshRehearsal.data.result_recording_plan,
    live_rehearsal_control_evidence: sshRehearsal.data.live_rehearsal_control_evidence,
    live_rehearsal_controls_ready: sshRehearsal.data.live_rehearsal_controls_ready,
    environment_proof_plan: sshRehearsal.data.environment_proof_plan,
    environment_proof_ready: sshRehearsal.data.environment_proof_ready,
    target_environment_attestation_plan: sshRehearsal.data.target_environment_attestation_plan,
    target_environment_attestation_ready: sshRehearsal.data.target_environment_attestation_ready,
    operator_approved_proof_recorded: sshRehearsal.data.operator_approved_proof_recorded,
    required_live_rehearsal: sshRehearsal.data.required_live_rehearsal,
    required_controls: sshRehearsal.data.required_controls,
    steps: sshRehearsal.data.steps,
    recent_evidence: sshRehearsal.data.recent_evidence
  } : null;
  const sshRuns = useLoad(() => project ? api(`/api/ssh-command-runs?project_id=${project.id}`) : Promise.resolve({ items: [] }), [project?.id]);
  useEffect(() => {
    setSSHSnapshotResult(undefined);
  }, [sshPick.selectedID]);
  const deploymentPosture = buildDeploymentPosture(
    deploymentTargets.data?.items || [],
    deploymentRecords.data?.items || [],
    rollbackPoints.data?.items || []
  );
  const rollbackGuardrail = buildRollbackGuardrail(rollbackPoints.data?.items || []);
  const deploymentExecutionGuardrail = buildDeploymentExecutionGuardrail(deploymentTargets.data?.items || []);
  useEffect(() => {
    if (!argoSyncOpID) return;
    let alive = true;
    let attempts = 0;
    const poll = async () => {
      attempts += 1;
      try {
        const op = await api(`/api/operations/${argoSyncOpID}`);
        if (!alive) return;
        if (op.status === 'completed') {
          if (!alive) return;
          message.success('Argo apps synced');
          setArgoSyncOpID(undefined);
          argoConnections.reload();
          argoApps.reload();
          deploymentTargets.reload();
          deploymentRecords.reload();
          rollbackPoints.reload();
        } else if (op.status === 'failed' || op.status === 'canceled') {
          if (!alive) return;
          message.error(op.error || 'Argo app sync failed');
          setArgoSyncOpID(undefined);
          argoConnections.reload();
        } else if (attempts >= 150) {
          if (!alive) return;
          message.warning('Argo app sync is still running. Refresh later to check progress.');
          setArgoSyncOpID(undefined);
          argoConnections.reload();
        }
      } catch (error: any) {
        if (alive) {
          message.error(error.message);
          setArgoSyncOpID(undefined);
        }
      }
    };
    poll();
    const timer = window.setInterval(poll, 2000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, [argoSyncOpID]);
  async function createArgoConnection(values: AnyRow) {
    if (!project) return;
    await api(`/api/projects/${project.id}/argo/connections`, {
      method: 'POST',
      body: JSON.stringify({
        name: values.name,
        server_url: values.server_url,
        auth_type: values.auth_type || 'token',
        config: {
          token: values.token,
          insecure_skip_verify: values.insecure_skip_verify === 'true'
        }
      })
    });
    argoConnections.reload();
  }
  async function syncArgoApps() {
    if (!argoPick.selectedID) {
      message.error('Select an Argo connection first');
      return;
    }
    try {
      const op = await api(`/api/argo/connections/${argoPick.selectedID}/apps/sync`, { method: 'POST', body: '{}' });
      setArgoSyncOpID(op.id);
      message.success('Argo app sync queued');
      argoConnections.reload();
    } catch (error: any) {
      message.error(error.message);
    }
  }
  async function previewPodLogs(values: AnyRow) {
    if (!project) return;
    setPodLogLoading(true);
    try {
      const result = await api(`/api/projects/${project.id}/argo/pod-log-query-preview`, {
        method: 'POST',
        body: JSON.stringify({
          deployment_target_id: values.deployment_target_id,
          pod_name: values.pod_name,
          container_name: values.container_name,
          tail_lines: Number(values.tail_lines || 200),
          since_seconds: Number(values.since_seconds || 0)
        })
      });
      setPodLogPreview(result);
      setPodLogRunResult(undefined);
      setPodLogSnapshotResult(undefined);
      message.success('Pod log query preview ready');
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setPodLogLoading(false);
    }
  }
  async function requestPodLogAudit() {
    if (!project || !podLogPreview) return;
    const target = podLogPreview.deployment_target || {};
    const query = podLogPreview.query || {};
    setPodLogRunLoading(true);
    try {
      const result = await api(`/api/projects/${project.id}/argo/pod-logs`, {
        method: 'POST',
        body: JSON.stringify({
          deployment_target_id: target.id,
          pod_name: query.pod_name,
          container_name: query.container_name,
          tail_lines: Number(query.tail_lines || 200),
          since_seconds: Number(query.since_seconds || 0)
        })
      });
      setPodLogRunResult(result);
      message.success(result.approval ? 'Pod log approval requested' : 'Pod log audit queued');
    } catch (error: any) {
      setPodLogRunResult(undefined);
      message.error(error.message || 'Request failed');
    } finally {
      setPodLogRunLoading(false);
    }
  }
  async function recordPodLogAuditSnapshot() {
    if (!project || !podLogPreview) return;
    const target = podLogPreview.deployment_target || {};
    const query = podLogPreview.query || {};
    setPodLogSnapshotLoading(true);
    try {
      const result = await api(`/api/projects/${project.id}/argo/pod-log-audit-snapshot`, {
        method: 'POST',
        body: JSON.stringify({
          deployment_target_id: target.id,
          pod_name: query.pod_name,
          container_name: query.container_name,
          tail_lines: Number(query.tail_lines || 200),
          since_seconds: Number(query.since_seconds || 0)
        })
      });
      setPodLogSnapshotResult(result);
      const refreshed = await api(`/api/projects/${project.id}/argo/pod-log-query-preview`, {
        method: 'POST',
        body: JSON.stringify({
          deployment_target_id: target.id,
          pod_name: query.pod_name,
          container_name: query.container_name,
          tail_lines: Number(query.tail_lines || 200),
          since_seconds: Number(query.since_seconds || 0)
        })
      });
      setPodLogPreview(refreshed);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Pod log audit snapshot is not ready yet');
      } else {
        message.success(result.pod_log_audit_snapshot_written ? 'Pod log audit snapshot recorded' : 'Pod log audit snapshot already current');
      }
    } catch (error: any) {
      setPodLogSnapshotResult(undefined);
      message.error(error.message || 'Request failed');
    } finally {
      setPodLogSnapshotLoading(false);
    }
  }
  async function runSSHCommand(values: AnyRow) {
    if (!sshPick.selectedID) {
      message.error('Select an SSH machine first');
      return;
    }
    try {
      const result = await api(`/api/ssh-machines/${sshPick.selectedID}/commands`, {
        method: 'POST',
        body: JSON.stringify({ command: values.command, timeout_seconds: Number(values.timeout_seconds || 60) })
      });
      message.success(result.approval ? 'Approval requested' : 'SSH command queued');
      sshRuns.reload();
      sshRehearsal.reload();
    } catch (error: any) {
      message.error(error.message);
    }
  }
  async function verifySSHMachine() {
    if (!sshPick.selectedID) {
      message.error('Select an SSH machine first');
      return;
    }
    try {
      await api(`/api/ssh-machines/${sshPick.selectedID}/verify`, { method: 'POST', body: '{}' });
      message.success('SSH verify queued');
      sshRuns.reload();
      sshRehearsal.reload();
    } catch (error: any) {
      message.error(error.message);
    }
  }
  async function recordSSHRehearsalSnapshot() {
    if (!sshPick.selectedID) {
      message.error('Select an SSH machine first');
      return;
    }
    setSSHSnapshotLoading(true);
    try {
      const result = await api(`/api/ssh-machines/${sshPick.selectedID}/rehearsal-snapshot`, {
        method: 'POST',
        body: '{}'
      });
      setSSHSnapshotResult(result);
      sshRehearsal.reload();
      if (result.recording_ready === false) {
        message.warning(result.message || 'SSH rehearsal snapshot is not ready yet');
      } else {
        message.success(result.ssh_rehearsal_snapshot_written ? 'SSH rehearsal snapshot recorded' : 'SSH rehearsal snapshot already current');
      }
    } catch (error: any) {
      setSSHSnapshotResult(undefined);
      message.error(error.message || 'Request failed');
    } finally {
      setSSHSnapshotLoading(false);
    }
  }
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>Argo / SSH</Typography.Title>
      <EntitySelect label="Project" rows={projectRows} value={projectPick.selectedID} onChange={projectPick.setSelectedID} />
      <Tabs items={[
        { key: 'ssh', label: 'SSH Machines', children: <Space direction="vertical" size={16} className="full">
          <Toolbar title="SSH Machines" onCreate={() => setSSHOpen(true)} disabled={!project} />
          <EntitySelect label="Machine" rows={sshRows} value={sshPick.selectedID} onChange={sshPick.setSelectedID} />
          <Space>
            <Button onClick={verifySSHMachine} disabled={!sshPick.selectedID}>Verify</Button>
            <Button type="primary" onClick={() => setCommandOpen(true)} disabled={!sshPick.selectedID}>Run command</Button>
            <Button onClick={() => { sshRuns.reload(); sshRehearsal.reload(); }} loading={sshRehearsal.loading} disabled={!project}>Refresh runs</Button>
            <Button onClick={recordSSHRehearsalSnapshot} loading={sshSnapshotLoading} disabled={!sshPick.selectedID || !sshRehearsalView?.target_environment_attestation_ready}>Record snapshot</Button>
          </Space>
          {sshRehearsal.error && <Alert showIcon type="warning" message="SSH rehearsal preview unavailable" description={sshRehearsal.error} />}
          {sshRehearsalView && (
            <Card title="SSH rehearsal">
              <Space direction="vertical" size={8} className="full">
                <Space wrap>
                  <Tag color={sshRehearsalView.rehearsal_state === 'ready' ? 'green' : sshRehearsalView.rehearsal_state === 'blocked' ? 'red' : 'gold'}>{sshRehearsalView.rehearsal_state}</Tag>
                  <Tag>{sshRehearsalView.execution_enabled ? 'execution enabled' : 'execution disabled'}</Tag>
                  <Tag>{sshRehearsalView.ssh_process_started ? 'ssh started' : 'no ssh process'}</Tag>
                  <Tag color={sshRehearsalView.live_evidence_recorded ? 'green' : 'default'}>{sshRehearsalView.live_evidence_recorded ? 'live evidence recorded' : 'no live evidence'}</Tag>
                  <Tag color={sshRehearsalView.sanitized_result_recorded ? 'green' : 'default'}>{sshRehearsalView.sanitized_result_recorded ? 'sanitized result recorded' : 'no sanitized result'}</Tag>
                  {sshRehearsalView.approval_request_plan ? <Tag color="gold">approval {sshRehearsalView.approval_request_plan.request_state || 'blocked'}</Tag> : null}
                  {sshRehearsalView.auth_binding_plan ? <Tag color={sshRehearsalView.auth_binding_plan.binding_state === 'planned' ? 'gold' : sshRehearsalView.auth_binding_plan.binding_state === 'observed' ? 'green' : 'red'}>auth {sshRehearsalView.auth_binding_plan.binding_state || 'blocked'}</Tag> : null}
                  {sshRehearsalView.verify_execution_plan ? <Tag color={sshRehearsalView.verify_execution_plan.verify_state === 'observed' ? 'green' : sshRehearsalView.verify_execution_plan.verify_state === 'planned' ? 'gold' : 'red'}>verify {sshRehearsalView.verify_execution_plan.verify_state || 'blocked'}</Tag> : null}
                  {sshRehearsalView.exec_execution_plan ? <Tag color={sshRehearsalView.exec_execution_plan.exec_state === 'observed' ? 'green' : sshRehearsalView.exec_execution_plan.exec_state === 'planned' ? 'gold' : 'red'}>exec {sshRehearsalView.exec_execution_plan.exec_state || 'blocked'}</Tag> : null}
                  <Tag>{sshRehearsalView.private_key_included ? 'key included' : 'no key material'}</Tag>
                  <Tag>{sshRehearsalView.stdout_included || sshRehearsalView.stderr_included ? 'output included' : 'no command output'}</Tag>
                  {sshRehearsalView.result_recording_plan ? <Tag>{sshRehearsalView.result_recording_plan.recording_state || 'blocked'} recording</Tag> : null}
                  {sshRehearsalView.result_recording_plan ? <Tag>{sshRehearsalView.result_recording_plan.auth_binding_recorded ? 'auth recorded' : 'no auth record'}</Tag> : null}
                  {sshRehearsalView.result_recording_plan ? <Tag>{sshRehearsalView.result_recording_plan.verify_result_recorded ? 'verify recorded' : 'no verify record'}</Tag> : null}
                  {sshRehearsalView.result_recording_plan ? <Tag>{sshRehearsalView.result_recording_plan.exec_result_recorded ? 'exec recorded' : 'no exec record'}</Tag> : null}
                  {sshRehearsalView.live_rehearsal_control_evidence ? <Tag color={sshRehearsalView.live_rehearsal_control_evidence.control_state === 'ready' ? 'green' : sshRehearsalView.live_rehearsal_control_evidence.control_state === 'blocked' ? 'red' : 'gold'}>controls {sshRehearsalView.live_rehearsal_control_evidence.control_state || 'blocked'}</Tag> : null}
                  {sshRehearsalView.live_rehearsal_control_evidence ? <Tag>{sshRehearsalView.live_rehearsal_control_evidence.runbook_reference_recorded ? 'runbook recorded' : 'no runbook'}</Tag> : null}
                  {sshRehearsalView.live_rehearsal_control_evidence ? <Tag>{sshRehearsalView.live_rehearsal_control_evidence.fixture_reference_recorded ? 'fixture recorded' : 'no fixture'}</Tag> : null}
                  {sshRehearsalView.operator_approved_proof_recorded ? <Tag color="green">operator proof recorded</Tag> : <Tag>no operator proof</Tag>}
                  {sshRehearsalView.environment_proof_plan ? <Tag color={sshRehearsalView.environment_proof_plan.environment_proof_state === 'ready' ? 'green' : sshRehearsalView.environment_proof_plan.environment_proof_state === 'blocked' ? 'red' : 'gold'}>env proof {sshRehearsalView.environment_proof_plan.environment_proof_state || 'blocked'}</Tag> : null}
                  {sshRehearsalView.environment_proof_plan ? <Tag>{sshRehearsalView.environment_proof_plan.environment_proof_ready ? 'env proof ready' : 'env proof pending'}</Tag> : null}
                  {sshRehearsalView.target_environment_attestation_plan ? <Tag color={sshRehearsalView.target_environment_attestation_plan.attestation_state === 'ready_for_operator_review' ? 'green' : sshRehearsalView.target_environment_attestation_plan.attestation_state === 'blocked' ? 'red' : 'gold'}>target attestation {sshRehearsalView.target_environment_attestation_plan.attestation_state || 'blocked'}</Tag> : null}
                  {sshRehearsalView.target_environment_attestation_plan ? <Tag>{sshRehearsalView.target_environment_attestation_plan.environment_probe_performed ? 'env probed' : 'no env probe'}</Tag> : null}
                  {sshRehearsalView.target_environment_attestation_plan ? <Tag>{sshRehearsalView.target_environment_attestation_plan.raw_output_recorded ? 'raw output recorded' : 'no raw output'}</Tag> : null}
                  {sshRehearsalView.target_environment_attestation_ready ? <Tag color="green">target proof review ready</Tag> : <Tag>target proof pending</Tag>}
                  {sshRehearsalView.recent_evidence?.evidence_state ? <Tag color={sshRehearsalView.recent_evidence.evidence_state === 'recorded' ? 'green' : sshRehearsalView.recent_evidence.evidence_state === 'failed' ? 'red' : 'gold'}>evidence {sshRehearsalView.recent_evidence.evidence_state}</Tag> : null}
                  <Tag>{sshRehearsalView.recent_evidence?.verify_runs || 0} verify runs</Tag>
                  <Tag>{sshRehearsalView.recent_evidence?.exec_runs || 0} exec runs</Tag>
                  <Tag>{sshRehearsalView.recent_evidence?.unknown_runs || 0} unknown runs</Tag>
                  <Tag>{sshRehearsalView.recent_evidence?.active_runs || 0} active runs</Tag>
                  <Tag>{sshRehearsalView.recent_evidence?.failed_runs || 0} failed runs</Tag>
                  <Tag>{sshRehearsalView.recent_evidence?.canceled_runs || 0} canceled runs</Tag>
                </Space>
                {sshSnapshotResult && (
                  <Space wrap>
                    <Tag color={sshSnapshotResult.recording_state === 'recorded' ? 'green' : sshSnapshotResult.recording_state === 'asset_missing' ? 'red' : 'gold'}>snapshot {sshSnapshotResult.recording_state || 'pending'}</Tag>
                    <Tag>{sshSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
                    <Tag>{sshSnapshotResult.ssh_machine_asset_observed ? 'host asset observed' : 'host asset missing'}</Tag>
                    <Tag>{sshSnapshotResult.stdout_included || sshSnapshotResult.stderr_included ? 'output included' : 'no command output'}</Tag>
                    <Tag>{sshSnapshotResult.private_key_included ? 'key included' : 'no key material'}</Tag>
                  </Space>
                )}
                <JSONBlock value={sshRehearsalView} />
              </Space>
            </Card>
          )}
          <Table rowKey="id" dataSource={sshRows} pagination={false} columns={[
            { title: 'Name', dataIndex: 'name' },
            { title: 'Host', dataIndex: 'host' },
            { title: 'Port', dataIndex: 'port' },
            { title: 'User', dataIndex: 'username' },
            { title: 'Auth', dataIndex: 'auth_type' }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={sshRuns.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: 'Type', render: (_, row) => <Tag color={row.operation_type === 'ssh.verify' ? 'cyan' : 'default'}>{row.operation_type || 'unknown'}</Tag> },
            { title: 'Status', render: (_, row) => <Tag color={row.status === 'completed' ? 'green' : row.status === 'failed' ? 'red' : 'blue'}>{row.status}</Tag> },
            { title: 'Command', dataIndex: 'command' },
            { title: 'Exit', dataIndex: 'exit_code' },
            { title: 'Created', dataIndex: 'created_at' },
            { title: 'Finished', dataIndex: 'finished_at' }
          ]} />
        </Space> },
        { key: 'argo', label: 'Argo Apps', children: <Space direction="vertical" size={16} className="full">
          <Toolbar title="Argo Connections" onCreate={() => setArgoOpen(true)} disabled={!project} />
          <EntitySelect label="Connection" rows={argoRows} value={argoPick.selectedID} onChange={argoPick.setSelectedID} />
          <Space>
            <Button type="primary" loading={Boolean(argoSyncOpID)} onClick={syncArgoApps} disabled={!argoPick.selectedID || Boolean(argoSyncOpID)}>Sync apps</Button>
            <Button onClick={() => { argoConnections.reload(); argoApps.reload(); deploymentTargets.reload(); deploymentRecords.reload(); rollbackPoints.reload(); }} disabled={!project}>Refresh</Button>
          </Space>
          <div className="metricGrid">
            <Card><Typography.Text type="secondary">Targets</Typography.Text><Typography.Title level={3}>{deploymentPosture.targets}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">Unhealthy</Typography.Text><Typography.Title level={3}>{deploymentPosture.unhealthy}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">Environments</Typography.Text><Typography.Title level={3}>{deploymentPosture.environments}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">Rollback points</Typography.Text><Typography.Title level={3}>{deploymentPosture.rollbackPoints}</Typography.Title></Card>
          </div>
          {deploymentPosture.summary !== 'No deployment targets yet' && <Alert showIcon type={deploymentPosture.unhealthy > 0 ? 'warning' : 'success'} message={deploymentPosture.summary} />}
          {deploymentExecutionGuardrail && <Alert showIcon type={deploymentExecutionGuardrail.type} message={deploymentExecutionGuardrail.message} description={deploymentExecutionGuardrail.description} />}
          {rollbackGuardrail && <Alert showIcon type={rollbackGuardrail.type} message={rollbackGuardrail.message} description={rollbackGuardrail.description} />}
          <Card title="Pod log query">
            <Space direction="vertical" size={12} className="full">
              <Form layout="inline" onFinish={previewPodLogs} initialValues={{ tail_lines: 200, since_seconds: 0 }}>
                <Form.Item name="deployment_target_id" rules={[{ required: true, message: 'target is required' }]}>
                  <Select placeholder="Target" style={{ width: 220 }} options={(deploymentTargets.data?.items || []).map((target: AnyRow) => ({ value: target.id, label: `${target.name || target.namespace} (${target.environment || 'env'})` }))} />
                </Form.Item>
                <Form.Item name="pod_name" rules={[{ required: true, message: 'pod is required' }]}>
                  <Input placeholder="pod name" style={{ width: 180 }} />
                </Form.Item>
                <Form.Item name="container_name">
                  <Input placeholder="container" style={{ width: 150 }} />
                </Form.Item>
                <Form.Item name="tail_lines">
                  <Input type="number" min={1} max={1000} placeholder="tail" style={{ width: 90 }} />
                </Form.Item>
                <Form.Item name="since_seconds">
                  <Input type="number" min={0} max={86400} placeholder="since sec" style={{ width: 110 }} />
                </Form.Item>
                <Button htmlType="submit" loading={podLogLoading} disabled={!project || !(deploymentTargets.data?.items || []).length}>Preview</Button>
              </Form>
              {podLogPreview && (
                <Space direction="vertical" size={8} className="full">
                  <Space wrap>
                    <Tag color="gold">{podLogPreview.query_state || 'blocked'}</Tag>
                    <Tag>{podLogPreview.execution_enabled ? 'execution enabled' : 'execution disabled'}</Tag>
                    <Tag>{podLogPreview.operation_request_enabled ? 'operation request ready' : 'operation request blocked'}</Tag>
                    <Tag>{podLogPreview.kubernetes_api_call ? 'k8s called' : 'no k8s call'}</Tag>
                    <Tag>{podLogPreview.log_body_included ? 'log body included' : 'no log body'}</Tag>
                    {podLogPreview.retrieval_plan ? <Tag color={podLogPreview.retrieval_plan.plan_state === 'ready_for_approval' ? 'gold' : 'red'}>retrieval {podLogPreview.retrieval_plan.plan_state || 'blocked'}</Tag> : null}
                    {podLogPreview.retrieval_plan ? <Tag>{podLogPreview.retrieval_plan.step_count || 0} retrieval steps</Tag> : null}
                    {podLogPreview.audit_evidence ? <Tag color={podLogPreview.audit_evidence.evidence_state === 'recorded' ? 'green' : podLogPreview.audit_evidence.evidence_state === 'failed' ? 'red' : podLogPreview.audit_evidence.evidence_state === 'waiting_for_worker' ? 'blue' : 'default'}>audit {podLogPreview.audit_evidence.evidence_state || 'not_requested'}</Tag> : null}
                    {podLogPreview.audit_evidence ? <Tag>{podLogPreview.audit_evidence.operation_count || 0} audit ops</Tag> : null}
                    {podLogPreview.audit_evidence?.operation_log_count ? <Tag>{podLogPreview.audit_evidence.operation_log_count} audit logs</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan ? <Tag color={podLogPreview.retrieval_plan.execution_plan.execution_state === 'ready_for_approval' ? 'gold' : 'red'}>execute {podLogPreview.retrieval_plan.execution_plan.execution_state || 'blocked'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.approval_request_plan ? <Tag color="gold">approval {podLogPreview.retrieval_plan.execution_plan.approval_request_plan.request_state || 'blocked'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.audit_worker_job_enabled ? 'audit worker ready' : 'no audit worker'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.audit_operation_observed ? 'audit observed' : 'no audit observed'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.kubeconfig_binding_plan ? <Tag color={podLogPreview.retrieval_plan.execution_plan.kubeconfig_binding_plan.binding_state === 'planned' ? 'gold' : 'red'}>kubeconfig {podLogPreview.retrieval_plan.execution_plan.kubeconfig_binding_plan.binding_state || 'blocked'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.kubeconfig_readiness_plan ? <Tag color={podLogPreview.retrieval_plan.execution_plan.kubeconfig_readiness_plan.readiness_ready ? 'gold' : 'red'}>kube readiness {podLogPreview.retrieval_plan.execution_plan.kubeconfig_readiness_plan.readiness_state || 'blocked'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.kubeconfig_readiness_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.kubeconfig_readiness_plan.namespace_scoped_kubeconfig_bound ? 'namespace kubeconfig bound' : 'no namespace kubeconfig'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.pod_scope_plan ? <Tag color={podLogPreview.retrieval_plan.execution_plan.pod_scope_plan.scope_state === 'planned' ? 'gold' : 'red'}>scope {podLogPreview.retrieval_plan.execution_plan.pod_scope_plan.scope_state || 'blocked'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.log_capture_plan ? <Tag color={podLogPreview.retrieval_plan.execution_plan.log_capture_plan.capture_state === 'planned' ? 'gold' : 'red'}>capture {podLogPreview.retrieval_plan.execution_plan.log_capture_plan.capture_state || 'blocked'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.live_log_stream_plan ? <Tag color={podLogPreview.retrieval_plan.execution_plan.live_log_stream_plan.stream_ready_for_review ? 'green' : podLogPreview.retrieval_plan.execution_plan.live_log_stream_plan.metadata_ready ? 'gold' : 'red'}>live stream {podLogPreview.retrieval_plan.execution_plan.live_log_stream_plan.stream_state || 'blocked'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.live_log_stream_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.live_log_stream_plan.stream_ready_for_review ? 'stream review ready' : 'stream review blocked'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.live_log_stream_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.live_log_stream_plan.live_log_stream_opened ? 'stream opened' : 'no live stream'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.live_log_stream_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.live_log_stream_plan.log_body_included ? 'log body included' : 'no log body'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.secret_included ? 'secrets included' : 'no secrets'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.result_written ? 'result written' : 'no result write'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.result_recording_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.result_recording_plan.recording_state || 'blocked'} recording</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.result_recording_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.result_recording_plan.sanitized_result_observed ? 'sanitized result observed' : 'no sanitized result'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.result_recording_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.result_recording_plan.kubeconfig_binding_recorded ? 'kubeconfig recorded' : 'no kubeconfig record'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.result_recording_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.result_recording_plan.pod_scope_recorded ? 'scope recorded' : 'no scope record'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.result_recording_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.result_recording_plan.log_capture_recorded ? 'capture recorded' : 'no capture record'}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.disabled_backends?.length || 0} disabled backends</Tag> : null}
                  </Space>
                  <Space>
                    <Button type="primary" onClick={requestPodLogAudit} loading={podLogRunLoading} disabled={!podLogPreview.operation_request_enabled || !podLogPreview.retrieval_plan?.execution_plan?.audit_worker_job_enabled}>Request audit</Button>
                    <Button onClick={recordPodLogAuditSnapshot} loading={podLogSnapshotLoading} disabled={podLogPreview.audit_evidence?.evidence_state !== 'recorded'}>Record audit snapshot</Button>
                    {podLogRunResult ? <Tag color={podLogRunResult.approval ? 'gold' : 'blue'}>{podLogRunResult.approval ? 'approval requested' : 'operation queued'}</Tag> : null}
                    {podLogRunResult?.worker_job_created ? <Tag>worker job created</Tag> : null}
                    {podLogRunResult ? <Tag>{podLogRunResult.log_body_included ? 'log body included' : 'no log body'}</Tag> : null}
                    {podLogSnapshotResult ? <Tag color={podLogSnapshotResult.pod_log_audit_snapshot_written ? 'green' : podLogSnapshotResult.recording_state === 'asset_missing' ? 'red' : 'default'}>snapshot {podLogSnapshotResult.recording_state || 'unknown'}</Tag> : null}
                    {podLogSnapshotResult ? <Tag>{podLogSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'no asset status write'}</Tag> : null}
                    {podLogSnapshotResult ? <Tag>{podLogSnapshotResult.log_body_included ? 'log body included' : 'no log body'}</Tag> : null}
                  </Space>
                  {podLogRunResult ? <JSONBlock value={podLogRunResult} /> : null}
                  {podLogSnapshotResult ? <JSONBlock value={podLogSnapshotResult} /> : null}
                  <JSONBlock value={podLogPreview} />
                </Space>
              )}
            </Space>
          </Card>
          <Table<AnyRow> rowKey="id" dataSource={argoRows} pagination={false} columns={[
            { title: 'Name', dataIndex: 'name' },
            { title: 'Server', dataIndex: 'server_url' },
            { title: 'Auth', dataIndex: 'auth_type' },
            { title: 'Sync', render: (_, row) => <Tag color={row.last_sync_status === 'completed' ? 'green' : row.last_sync_status === 'failed' ? 'red' : row.last_sync_status === 'running' ? 'blue' : 'default'}>{row.last_sync_status || 'never'}</Tag> },
            { title: 'Created', dataIndex: 'created_at' }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={deploymentTargets.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: 'Target', dataIndex: 'name' },
            { title: 'Environment', dataIndex: 'environment' },
            { title: 'Namespace', dataIndex: 'namespace' },
            { title: 'Cluster', dataIndex: 'cluster_name' },
            { title: 'Apps', dataIndex: 'argo_app_count' },
            { title: 'Execution', render: (_, row) => deploymentExecutionReadinessView(row) },
            { title: 'Status', render: (_, row) => <Tag color={argoStatusColor(row.status)}>{row.status}</Tag> }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={deploymentRecords.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: 'Deployment', dataIndex: 'name' },
            { title: 'Target', dataIndex: 'deployment_target_name' },
            { title: 'Environment', dataIndex: 'environment' },
            { title: 'Status', render: (_, row) => <Tag color={argoStatusColor(row.status)}>{row.status}</Tag> },
            { title: 'Revision', dataIndex: 'revision' },
            { title: 'Observed', dataIndex: 'observed_at' }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={rollbackPoints.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: 'Rollback point', dataIndex: 'name' },
            { title: 'Target', dataIndex: 'deployment_target_name' },
            { title: 'Environment', dataIndex: 'environment' },
            { title: 'Revision', dataIndex: 'revision' },
            { title: 'Readiness', render: (_, row) => <Tag color={rollbackReadinessColor(row.rollback_readiness)}>{row.rollback_readiness || 'unknown'}</Tag> },
            { title: 'Reason', dataIndex: 'rollback_readiness_reason', render: (value) => value || '-' },
            { title: 'Execution', render: (_, row) => rollbackExecutionPlanView(row) },
            { title: 'Status', render: (_, row) => <Tag color={argoStatusColor(row.status)}>{row.status}</Tag> },
            { title: 'Captured', dataIndex: 'captured_at' }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={argoApps.data?.items || []} pagination={{ pageSize: 8 }} columns={[
            { title: 'Name', dataIndex: 'name' },
            { title: 'Target', dataIndex: 'deployment_target_name' },
            { title: 'Environment', dataIndex: 'environment' },
            { title: 'Namespace', dataIndex: 'namespace' },
            { title: 'Status', render: (_, row) => <Tag color={argoStatusColor(row.status)}>{row.status}</Tag> },
            { title: 'Synced', dataIndex: 'synced_at' },
            { title: 'Updated', dataIndex: 'updated_at' }
          ]} />
        </Space> }
      ]} />
      <CreateModal title="Create Argo connection" open={argoOpen} setOpen={setArgoOpen} fields={['name', 'server_url', 'auth_type', 'token', 'insecure_skip_verify']} onSubmit={createArgoConnection} />
      <CreateModal title="Create SSH machine" open={sshOpen} setOpen={setSSHOpen} fields={['name', 'host', 'port', 'username', 'auth_type']} onSubmit={(v) => project ? api(`/api/projects/${project.id}/ssh-machines`, { method: 'POST', body: JSON.stringify({ ...v, port: Number(v.port || 22) }) }).then(ssh.reload) : Promise.resolve()} />
      <CreateModal title="Run SSH command" open={commandOpen} setOpen={setCommandOpen} fields={['command', 'timeout_seconds']} onSubmit={runSSHCommand} />
    </Space>
  );
}

function Toolbar({ title, onCreate, disabled = false }: { title: string; onCreate: () => void; disabled?: boolean }) {
  return <div className="toolbar"><Typography.Title level={2}>{title}</Typography.Title><Button type="primary" onClick={onCreate} disabled={disabled}>Create</Button></div>;
}

function fieldRules(field: string) {
  const rules: AnyRow[] = [];
  if (field === 'name' || field === 'title' || field === 'command') rules.push({ required: true });
  if (field === 'server_url') rules.push({ type: 'url', message: 'Enter a valid URL' });
  return rules;
}

function fieldInput(field: string) {
  if (field === 'command' || field.endsWith('_json')) return <Input.TextArea autoSize={{ minRows: 3, maxRows: 8 }} />;
  if (field === 'server_url' || field.endsWith('_url')) return <Input type="url" />;
  if (field === 'token' || field === 'password' || field.endsWith('_password')) return <Input.Password />;
  return <Input />;
}

function CreateModal({ title, open, setOpen, fields, onSubmit }: { title: string; open: boolean; setOpen: (v: boolean) => void; fields: string[]; onSubmit: (values: AnyRow) => Promise<any> }) {
  const [form] = Form.useForm();
  const [submitting, setSubmitting] = useState(false);
  async function submit(values: AnyRow) {
    setSubmitting(true);
    try {
      await onSubmit(values);
      setOpen(false);
      form.resetFields();
    } catch (error: any) {
      message.error(error.message || 'Request failed');
    } finally {
      setSubmitting(false);
    }
  }
  return (
    <Modal title={title} open={open} onCancel={() => setOpen(false)} onOk={() => form.submit()} confirmLoading={submitting} okButtonProps={{ disabled: submitting }} destroyOnHidden>
      <Form form={form} layout="vertical" onFinish={submit}>
        {fields.map((field) => (
          <Form.Item key={field} name={field} label={field.replaceAll('_', ' ')} rules={fieldRules(field)}>
            {fieldInput(field)}
          </Form.Item>
        ))}
      </Form>
    </Modal>
  );
}

function MobileAIHome({ setPage }: { setPage: (p: string) => void }) {
  const items: Array<[string, string, React.ReactNode]> = [
    ['Agent', 'agent', <RobotOutlined />],
    ['Operations', 'operations', <PlayCircleOutlined />],
    ['Runtime', 'ai', <CodeOutlined />],
    ['Projects', 'projects', <AppstoreOutlined />]
  ];
  return <div className="mobileHome"><Typography.Title>ASSOPS AI</Typography.Title><List grid={{ column: 2 }} dataSource={items} renderItem={([label, key, icon]) => <List.Item><Card onClick={() => setPage(key)}><Space direction="vertical" align="center">{icon}<span>{label}</span></Space></Card></List.Item>} /></div>;
}

function App() {
  const [authed, setAuthed] = useState(Boolean(authToken()));
  const [page, setPage] = useState('dashboard');
  const menu = useMemo(() => [
    { key: 'dashboard', icon: <DashboardOutlined />, label: 'Dashboard' },
    { key: 'assets', icon: <AppstoreOutlined />, label: 'Assets' },
    { key: 'projects', icon: <AppstoreOutlined />, label: 'Projects' },
    { key: 'providers', icon: <ApiOutlined />, label: 'Providers' },
    { key: 'detail', icon: <BranchesOutlined />, label: 'Project Detail' },
    { key: 'remotes', icon: <ApiOutlined />, label: 'Git Remotes' },
    { key: 'operations', icon: <PlayCircleOutlined />, label: 'Operations' },
    { key: 'nodes', icon: <CloudServerOutlined />, label: 'Worker Nodes' },
    { key: 'ai', icon: <CodeOutlined />, label: 'AI Runtime' },
    { key: 'agent', icon: <RobotOutlined />, label: 'Agent Task' },
    { key: 'config', icon: <DeploymentUnitOutlined />, label: 'Argo / SSH' }
  ], []);
  if (!authed) return <Login onLogin={() => setAuthed(true)} />;
  const content: Record<string, React.ReactNode> = {
    dashboard: <Dashboard />, assets: <AssetCenter />, projects: <Projects />, providers: <ProviderAccounts />, detail: <ProjectDetail />, remotes: <GitRemotes />, operations: <Operations />, nodes: <WorkerNodes />, ai: <AIRuntime />, agent: <AgentTasks />, config: <ConfigPage />
  };
  return (
    <ConfigProvider theme={{ token: { borderRadius: 6, colorPrimary: '#1677ff' } }}>
      <MobileAIHome setPage={setPage} />
      <Layout className="appShell">
        <Sider width={240} breakpoint="lg" collapsedWidth={0}><div className="brand"><SettingOutlined /> ASSOPS</div><Menu theme="dark" mode="inline" selectedKeys={[page]} items={menu} onClick={(e) => setPage(e.key)} /></Sider>
        <Layout>
          <Header className="topbar"><Typography.Text strong>MVP Control Plane</Typography.Text><Button onClick={() => { localStorage.removeItem('assops_token'); setAuthed(false); }}>Sign out</Button></Header>
          <Content className="content">{content[page]}</Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}

createRoot(document.getElementById('root')!).render(<App />);
