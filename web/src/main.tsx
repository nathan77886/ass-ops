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
  QuestionCircleOutlined,
  RobotOutlined,
  SettingOutlined
} from '@ant-design/icons';
import {
  Alert,
  AutoComplete,
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
  Tooltip,
  Typography,
  message
} from 'antd';
import enUS from 'antd/locale/en_US';
import zhCN from 'antd/locale/zh_CN';
import './styles.css';
import { dictionaries, type Language } from './i18n';

const { Header, Sider, Content } = Layout;
const API = import.meta.env.VITE_API_BASE || (globalThis.location?.hostname === 'ass-ops.4nathan.com' ? 'https://ass-ops-api.4nathan.com' : '');

type AnyRow = Record<string, any>;
const I18nContext = React.createContext({
  lang: 'en' as Language,
  setLang: (_lang: Language) => {},
  t: (key: string) => dictionaries.en[key] || key
});

function useI18n() {
  return React.useContext(I18nContext);
}

function createTranslator(lang: Language) {
  return (key: string) => dictionaries[lang][key] || dictionaries.en[key] || key;
}

function getInitialLanguage(): Language {
  return localStorage.getItem('assops_lang') === 'zh' ? 'zh' : 'en';
}

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
  const { t } = useI18n();
  const [loading, setLoading] = useState(false);
  return (
    <div className="loginPage">
      <div className="loginPanel">
        <div className="loginLang"><LanguageSwitch /></div>
        <Typography.Title level={1}>ASSOPS</Typography.Title>
        <Typography.Paragraph>{t('login.description')}</Typography.Paragraph>
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
          <Form.Item name="email" label={t('login.email')} rules={[{ required: true, message: t('common.required') }]}>
            <Input autoComplete="username" />
          </Form.Item>
          <Form.Item name="password" label={t('login.password')} rules={[{ required: true, message: t('common.required') }]}>
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={loading} block>{t('login.signIn')}</Button>
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

function githubActionRemoteDescription(sourceRemote: AnyRow | undefined, repo: AnyRow | undefined, project: AnyRow | undefined, summary: ReturnType<typeof githubActionsSummary>, t: (key: string) => string) {
  if (!sourceRemote) return t('git.actionsSelectRemote');
  const provider = String(sourceRemote.provider_type || sourceRemote.kind || '').toLowerCase();
  const remoteName = sourceRemote.name || sourceRemote.remote_key || sourceRemote.id;
  if (provider !== 'github') {
    return `${t('git.actionsUnavailablePrefix')} ${remoteName} ${t('git.actionsUnavailableMiddle')} ${provider || t('git.nonGitHub')} ${t('git.actionsUnavailableSuffix')}`;
  }
  const failureNote = summary.failures > 0 ? ` ${summary.failures} ${t(summary.failures === 1 ? 'git.problemRun' : 'git.problemRuns')}` : '';
  return `${t('git.actionsAttachedPrefix')} ${remoteName} ${t('git.actionsAttachedMiddle')} ${repo?.display_name || repo?.name || t('git.selectedRepository')} ${t('git.actionsAttachedIn')} ${project?.name || t('git.selectedProject')}. ${t('git.actionsLatestStatus')}: ${translatedValue(summary.latestStatus, t)}.${failureNote}`;
}

function githubActionsSummary(rows: AnyRow[], t: (key: string) => string) {
  const failures = rows.filter((row) => githubActionFailureStates.includes(githubActionState(row))).length;
  const successes = rows.filter((row) => githubActionSuccessStates.includes(githubActionState(row))).length;
  const active = rows.filter((row) => githubActionActiveStates.includes(String(row.status || '').toLowerCase())).length;
  const latest = rows[0];
  return {
    total: rows.length,
    successes,
    failures,
    active,
    latestLabel: latest ? `${latest.workflow_name || 'GitHub Actions'} ${t('git.onBranch')} ${latest.branch || t('git.unknownBranch')}` : t('git.noActionsRunsSynced'),
    latestStatus: latest ? String(latest.conclusion || latest.status || 'unknown') : 'none'
  };
}

function githubActionArtifactsSummary(rows: AnyRow[]) {
  return rows.reduce((summary, row) => {
    const artifacts = Array.isArray(row.artifacts) ? row.artifacts : [];
    const count = Number(row.artifact_count ?? artifacts.length ?? 0);
    const active = Number(row.active_artifact_count ?? artifacts.filter((artifact: AnyRow) => !artifact.expired).length);
    const expired = Number(row.expired_artifact_count ?? artifacts.filter((artifact: AnyRow) => artifact.expired).length);
    const bytes = Number(row.total_artifact_size_in_bytes ?? artifacts.reduce((sum: number, artifact: AnyRow) => sum + Number(artifact.size_in_bytes || 0), 0));
    return {
      total: summary.total + (Number.isFinite(count) ? count : 0),
      active: summary.active + (Number.isFinite(active) ? active : 0),
      expired: summary.expired + (Number.isFinite(expired) ? expired : 0),
      bytes: summary.bytes + (Number.isFinite(bytes) ? bytes : 0)
    };
  }, { total: 0, active: 0, expired: 0, bytes: 0 });
}

function githubLabelsSummary(rows: AnyRow[]) {
  const defaults = rows.filter((row) => row.is_default).length;
  return {
    total: rows.length,
    defaults,
    custom: Math.max(0, rows.length - defaults),
    latestSyncedAt: rows.reduce((latest: string, row) => {
      const syncedAt = String(row.synced_at || '');
      return syncedAt > latest ? syncedAt : latest;
    }, '')
  };
}

function githubLabelSwatchColor(value: any) {
  const color = String(value || '').trim().replace(/^#/, '');
  return /^[0-9a-fA-F]{6}$/.test(color) ? `#${color.toLowerCase()}` : '#d9d9d9';
}

function bytesText(value: any) {
  const bytes = Number(value || 0);
  if (!Number.isFinite(bytes) || bytes <= 0) return '-';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let next = bytes;
  let unit = 0;
  while (next >= 1024 && unit < units.length - 1) {
    next /= 1024;
    unit += 1;
  }
  return `${next.toFixed(next >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function templateProvisionSummary(row: AnyRow, t: (key: string) => string = createTranslator('en')) {
  const details = row.result?.details || {};
  const reconciliation = details.repository_reconciliation || {};
  if (row.result?.repository_provisioned) return { color: 'green', label: t('template.statusProvisioned'), detail: '' };
  if (row.status === 'provisioning') return { color: 'blue', label: t('template.statusProvisioning'), detail: '' };
  if (reconciliation.kind === 'existing_repository') return { color: 'gold', label: t('template.statusNeedsReconcile'), detail: translatedValue('existing repository', t) };
  if (reconciliation.kind === 'protected_branch') return { color: 'gold', label: t('template.statusGuarded'), detail: translatedValue('protected branch', t) };
  if (reconciliation.kind === 'missing_token') return { color: 'red', label: t('template.statusToken'), detail: translatedValue('not configured', t) };
  if (details.starter_push_skipped && details.repository_exists) return { color: 'gold', label: t('template.statusPushSkipped'), detail: translatedValue('repository exists', t) };
  if (details.starter_push_skipped) return { color: 'gold', label: t('template.statusPushSkipped'), detail: shortText(row.result?.repository_provision_reason || details.reason, 44) };
  if (details.provider_status) return { color: 'red', label: `HTTP ${details.provider_status}`, detail: shortText(details.provider_error, 44) };
  if (details.provider_error) return { color: 'red', label: t('template.statusError'), detail: shortText(details.provider_error, 44) };
  if (row.result?.repository_provision_reason) return { color: 'gold', label: t('template.statusNeedsReconcile'), detail: shortText(row.result.repository_provision_reason, 44) };
  return { color: 'default', label: t('template.statusPending'), detail: '' };
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

function templateProvisionGuidance(row: AnyRow, t: (key: string) => string = createTranslator('en')): TemplateProvisionGuidance {
  const details = row.result?.details || {};
  const reconciliation = details.repository_reconciliation || {};
  if (row.result?.repository_provisioned) {
    return templateGuidance({
      status: 'ready',
      color: 'green',
      title: t('template.guidanceRepositoryProvisionedTitle'),
      detail: t('template.guidanceRepositoryProvisionedDetail'),
      next: t('template.guidanceRepositoryProvisionedNext')
    });
  }
  if (row.status === 'provisioning' || row.status === 'running' || row.status === 'queued') {
    return templateGuidance({
      status: 'waiting',
      color: 'blue',
      title: t('template.guidanceProvisioningTitle'),
      detail: t('template.guidanceProvisioningDetail'),
      next: t('template.guidanceProvisioningNext')
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
      existing_repository: t('template.guidanceExistingRepositoryTitle'),
      protected_branch: branchStrategyReady ? t('template.guidanceProtectedBranchReadyTitle') : t('template.guidanceProtectedBranchGuardTitle'),
      missing_token: t('template.guidanceMissingTokenTitle')
    };
    return templateGuidance({
      status: reconciliation.kind === 'missing_token' ? t('template.statusToken') : branchStrategyReady ? t('template.statusBranchStrategy') : t('template.statusManualReconcile'),
      color: reconciliation.kind === 'missing_token' ? 'red' : 'gold',
      title: titles[String(reconciliation.kind)] || t('template.guidanceNeedsReconciliationTitle'),
      detail: String((branchStrategyReady ? reconciliation.action_required : branchStrategy.message) || reconciliation.action_required || details.reason || row.result?.repository_provision_reason || t('template.guidanceNeedsOperatorReview')),
      next: String(providerReview.message || reconciliation.retry_after || t('template.guidanceRetryAfterProviderFixed')),
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
      status: t('template.statusManualReconcile'),
      color: 'gold',
      title: t('template.guidanceExistingRepositoryTitle'),
      detail: t('template.guidanceExistingRepositoryDetail'),
      next: t('template.guidanceExistingRepositoryNext')
    });
  }
  if (details.starter_push_skipped) {
    return templateGuidance({
      status: t('template.statusManualReconcile'),
      color: 'gold',
      title: t('template.guidanceProtectedBranchGuardTitle'),
      detail: String(row.result?.repository_provision_reason || details.reason || t('template.guidanceStarterGuardDetail')),
      next: t('template.guidanceStarterGuardNext')
    });
  }
  if (details.token_configured === false) {
    return templateGuidance({
      status: t('template.statusToken'),
      color: 'red',
      title: t('template.guidanceMissingTokenTitle'),
      detail: t('template.guidanceTokenDetail'),
      next: t('template.guidanceTokenNext')
    });
  }
  if (details.provider_status || details.provider_error) {
    return templateGuidance({
      status: t('template.statusProvider'),
      color: 'red',
      title: details.provider_status ? `${t('template.guidanceProviderHttpTitle')} ${details.provider_status}` : t('template.guidanceProviderApiErrorTitle'),
      detail: shortText(details.provider_error || row.result?.repository_provision_reason, 96),
      next: t('template.guidanceProviderNext')
    });
  }
  if (row.result?.repository_provision_reason) {
    return templateGuidance({
      status: t('template.statusReview'),
      color: 'gold',
      title: t('template.guidanceRepositoryReviewTitle'),
      detail: shortText(row.result.repository_provision_reason, 96),
      next: t('template.guidanceRepositoryReviewNext')
    });
  }
  return templateGuidance({
    status: t('template.statusPending'),
    color: 'default',
    title: t('template.guidanceNotAttemptedTitle'),
    detail: t('template.guidanceNotAttemptedDetail'),
    next: t('template.guidanceNotAttemptedNext')
  });
}

function providerTokenRotationSummary(row: AnyRow, t: (key: string) => string = createTranslator('en')) {
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
  const dueText = Number.isFinite(daysUntilDue) && status !== 'due' ? `${daysUntilDue}${t('provider.daysLeft')}` : '';
  const lastText = rotation.last_rotated_at ? `${t('provider.since')} ${String(rotation.last_rotated_at).slice(0, 10)}` : '';
  return {
    color: colors[status] || 'default',
    label: t(`provider.${status}`),
    detail: [dueText, lastText].filter(Boolean).join(' · ')
  };
}

function providerTokenRotationSummaryTags(summary: AnyRow = {}, t: (key: string) => string = createTranslator('en')) {
  return [
    { key: 'due', label: `${summary.due || 0} ${t('provider.due')}`, color: (summary.due || 0) > 0 ? 'red' : 'default' },
    { key: 'soon', label: `${summary.soon || 0} ${t('provider.soon')}`, color: (summary.soon || 0) > 0 ? 'gold' : 'default' },
    { key: 'missing', label: `${summary.missing || 0} ${t('provider.missing')}`, color: (summary.missing || 0) > 0 ? 'red' : 'default' },
    { key: 'unknown', label: `${summary.unknown || 0} ${t('provider.unknown')}`, color: (summary.unknown || 0) > 0 ? 'orange' : 'default' },
    { key: 'fresh', label: `${summary.fresh || 0} ${t('provider.fresh')}`, color: 'green' }
  ];
}

function providerAutoRotationPlanTags(plan: AnyRow = {}, t: (key: string) => string = createTranslator('en')) {
  return [
    { key: 'ready', label: `${plan.ready || 0} ${t('provider.autoReady')}`, color: (plan.ready || 0) > 0 ? 'green' : 'default' },
    { key: 'blocked', label: `${plan.blocked || 0} ${t('provider.autoBlocked')}`, color: (plan.blocked || 0) > 0 ? 'red' : 'default' },
    { key: 'not_needed', label: `${plan.not_needed || 0} ${t('provider.notNeeded')}`, color: 'default' },
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

function providerAutoRotationStatus(row: AnyRow, planByID: Record<string, AnyRow>, t: (key: string) => string = createTranslator('en')) {
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
    label: translatedValue(status, t),
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

function operationRowAssetID(row: AnyRow = {}) {
  for (const key of ['id', 'asset_id']) {
    const value = String(row[key] || '').trim();
    if (!value || value === '<nil>') continue;
    return value.startsWith('operation_run:') ? value : `operation_run:${value}`;
  }
  return '';
}

function canonicalAssetGraphID(row: AnyRow = {}, type: string) {
  const sourceID = String(row.source_id ?? '').trim();
  if (sourceID && sourceID !== '<nil>') return `${type}:${sourceID}`;
  for (const key of ['asset_id', 'id']) {
    if (typeof row[key] !== 'string') continue;
    const value = row[key].trim();
    if (!value || value === '<nil>') continue;
    if (value.startsWith(`${type}:`)) return value;
    if (!value.includes(':')) return `${type}:${value}`;
  }
  return '';
}

function assetIDsByType(rows: AnyRow[] = [], type: string) {
  return new Set(rows
    .filter((row) => String(row.asset_type || '') === type)
    .map((row) => canonicalAssetGraphID(row, type))
    .filter(Boolean));
}

function assetIDsByTypeMetadata(rows: AnyRow[] = [], type: string, key: string, value: string) {
  return new Set(rows
    .filter((row) => String(row.asset_type || '') === type && metadataValueEqual(row.metadata?.[key], value))
    .map((row) => canonicalAssetGraphID(row, type))
    .filter(Boolean));
}

function tagRunAssetIDsByOperation(rows: AnyRow[] = []) {
  return rows.reduce<Record<string, Set<string>>>((result, row) => {
    if (String(row.asset_type || '') !== 'repo_tag_run') return result;
    const assetID = canonicalAssetGraphID(row, 'repo_tag_run');
    const operationID = cleanOperationAssetID(row.metadata?.operation_run_id);
    if (!assetID || !operationID) return result;
    result[operationID] ??= new Set<string>();
    result[operationID].add(assetID);
    return result;
  }, {});
}

function cleanOperationAssetID(raw: unknown) {
  const value = String(raw || '').trim();
  if (!value || value === '<nil>') return '';
  return value.startsWith('operation_run:') ? value : `operation_run:${value}`;
}

function metadataValueEqual(raw: unknown, value: string) {
  return String(raw || '').trim().toLowerCase() === value.trim().toLowerCase();
}

function assetIDsByGraphType(rows: AnyRow[] = [], assetType: string, graphType: string) {
  return new Set(rows
    .filter((row) => String(row.asset_type || '') === assetType)
    .map((row) => canonicalAssetGraphID(row, graphType))
    .filter(Boolean));
}

function countOperationRowsWithLogs(rows: AnyRow[] = [], operationAssetIDs = new Set<string>()) {
  return rows.filter((row) => Number(row.log_count || 0) > 0 && operationAssetIDs.has(operationRowAssetID(row))).length;
}

function operationIDsByType(rows: AnyRow[] = [], type: string) {
  return new Set(rows
    .filter((row) => String(row.operation_type || '') === type)
    .map(operationRowAssetID)
    .filter(Boolean));
}

function operationIDsByStatus(rows: AnyRow[] = [], status: string) {
  return new Set(rows
    .filter((row) => String(row.status || '') === status)
    .map(operationRowAssetID)
    .filter(Boolean));
}

function mergeSets<T>(...sets: Set<T>[]) {
  return new Set(sets.flatMap((set) => Array.from(set)));
}

function countContextGenerationEvidence(assets: AnyRow[] = []) {
  return assets.filter((row) =>
    String(row.asset_type || '') === 'agent_tool_call' &&
    String(row.metadata?.tool_name || '') === 'context.generate' &&
    String(row.status || '').trim().toLowerCase() === 'completed'
  ).length;
}

function apiAssetGraphID(row: AnyRow = {}) {
  for (const key of ['asset_id', 'id']) {
    if (typeof row[key] !== 'string') continue;
    const value = row[key].trim();
    if (value && value !== '<nil>') return value;
  }
  const type = String(row.asset_type ?? '').trim();
  const sourceID = String(row.source_id ?? '').trim();
  return type && type !== '<nil>' && sourceID && sourceID !== '<nil>' ? `${type}:${sourceID}` : '';
}

function countContextGraphLinks(assets: AnyRow[] = [], graph: AnyRow = {}) {
  const taskAssetIDs = assetIDsByType(assets, 'agent_task');
  const runtimeAssetIDs = assetIDsByType(assets, 'ai_runtime');
  const contextToolCalls = new Set(
    assets
      .filter((row) =>
        String(row.asset_type ?? '') === 'agent_tool_call' &&
        String(row.metadata?.tool_name ?? '') === 'context.generate' &&
        String(row.status ?? '').trim().toLowerCase() === 'completed'
      )
      .map(apiAssetGraphID)
      .filter((assetID) => assetID.startsWith('agent_tool_call:'))
  );
  const byTask: Record<string, { runtimes: Record<string, boolean>; contextTools: Record<string, boolean> }> = {};
  const taskEntry = (assetID: string) => {
    byTask[assetID] ??= { runtimes: {}, contextTools: {} };
    return byTask[assetID];
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type ?? '');
    const from = String(edge.from_asset_id ?? '');
    const to = String(edge.to_asset_id ?? '');
    if (relation === 'uses_runtime' && from.startsWith('agent_task:') && to.startsWith('ai_runtime:')) {
      nextCounts.taskRuntimes += 1;
      taskEntry(from).runtimes[to] = true;
    }
    if (relation === 'records_tool_call' && from.startsWith('agent_task:') && contextToolCalls.has(to)) {
      nextCounts.taskContextToolCalls += 1;
      taskEntry(from).contextTools[to] = true;
    }
    return nextCounts;
  }, { taskRuntimes: 0, taskContextToolCalls: 0, completeContextTasks: 0, completeContextTaskAssets: 0 });
  counts.completeContextTasks = Object.values(byTask).filter((entry) => Object.keys(entry.runtimes).length > 0 && Object.keys(entry.contextTools).length > 0).length;
  counts.completeContextTaskAssets = Object.entries(byTask).filter(([taskID, entry]) => (
    taskAssetIDs.has(taskID) &&
    Object.keys(entry.runtimes).some((runtimeID) => runtimeAssetIDs.has(runtimeID)) &&
    Object.keys(entry.contextTools).some((toolID) => contextToolCalls.has(toolID))
  )).length;
  return counts;
}

function countRowsByTypeStatus(rows: AnyRow[] = [], type: string, status: string) {
  return rows.filter((row) => String(row.asset_type || '') === type && String(row.status || '') === status).length;
}

function activeAssetIDsByTypeStatus(rows: AnyRow[] = [], type: string, status: string) {
  return new Set(rows
    .filter((row) => String(row.asset_type || '') === type && String(row.status || '') === status)
    .map(apiAssetGraphID)
    .filter(Boolean));
}

function countRowsByTypeMetadata(rows: AnyRow[] = [], type: string, key: string, value: string) {
  return rows.filter((row) => String(row.asset_type || '') === type && metadataValueEqual(row.metadata?.[key], value)).length;
}

function graphItems(graph: AnyRow = {}, key: string) {
  return Array.isArray(graph[key]) ? graph[key] : [];
}

function countGraphNodesByPrefix(graph: AnyRow = {}, prefix: string) {
  return graphItems(graph, 'nodes').filter((node: AnyRow) => String(node.id ?? '').startsWith(prefix)).length;
}

function countGraphNodesByKnownIDs(graph: AnyRow = {}, knownIDs = new Set<string>()) {
  return graphItems(graph, 'nodes').filter((node: AnyRow) => knownIDs.has(String(node.id ?? ''))).length;
}

function countRepositoryGraphLinks(graph: AnyRow = {}, projectAssetIDs = new Set<string>(), repositoryAssetIDs = new Set<string>(), remoteAssetIDs = new Set<string>()) {
  const byRepository: Record<string, { projects: Record<string, boolean>; remotes: Record<string, boolean> }> = {};
  const repositoryEntry = (assetID: string) => {
    byRepository[assetID] ??= { projects: {}, remotes: {} };
    return byRepository[assetID];
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type ?? '');
    const from = String(edge.from_asset_id ?? '');
    const to = String(edge.to_asset_id ?? '');
    if (relation === 'owns' && from.startsWith('project:') && to.startsWith('repository:')) {
      nextCounts.projectRepository += 1;
      repositoryEntry(to).projects[from] = true;
    }
    if (relation === 'has_remote' && from.startsWith('repository:') && to.startsWith('git_remote:')) {
      nextCounts.repositoryRemotes += 1;
      repositoryEntry(from).remotes[to] = true;
    }
    return nextCounts;
  }, { projectRepository: 0, repositoryRemotes: 0, completeRepos: 0, completeRepoAssets: 0 });
  counts.completeRepos = Object.values(byRepository).filter((entry) => Object.keys(entry.projects).length > 0 && Object.keys(entry.remotes).length >= 2).length;
  counts.completeRepoAssets = Object.entries(byRepository).filter(([repositoryID, entry]) => (
    Object.keys(entry.projects).some((projectID) => projectAssetIDs.has(projectID)) &&
    repositoryAssetIDs.has(repositoryID) &&
    Object.keys(entry.remotes).filter((remoteID) => remoteAssetIDs.has(remoteID)).length >= 2
  )).length;
  return counts;
}

function countRepoSyncGraphLinks(graph: AnyRow = {}, repositoryAssetIDs = new Set<string>(), repoSyncAssetIDs = new Set<string>(), remoteAssetIDs = new Set<string>()) {
  const bySync: Record<string, { repositories: Record<string, boolean>; sources: Record<string, boolean>; targets: Record<string, boolean> }> = {};
  const syncEntry = (assetID: string) => {
    bySync[assetID] ||= { repositories: {}, sources: {}, targets: {} };
    return bySync[assetID];
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type || '');
    const from = String(edge.from_asset_id || '');
    const to = String(edge.to_asset_id || '');
    if (relation === 'has_sync' && from.startsWith('repository:') && to.startsWith('repo_sync:')) {
      nextCounts.repositorySync += 1;
      syncEntry(to).repositories[from] = true;
    }
    if (relation === 'synced_from' && from.startsWith('repo_sync:') && to.startsWith('git_remote:')) {
      nextCounts.sourceRemotes += 1;
      syncEntry(from).sources[to] = true;
    }
    if (relation === 'mirrors_to' && from.startsWith('repo_sync:') && to.startsWith('git_remote:')) {
      nextCounts.targetRemotes += 1;
      syncEntry(from).targets[to] = true;
    }
    return nextCounts;
  }, { repositorySync: 0, sourceRemotes: 0, targetRemotes: 0, completeSyncs: 0, completeSyncAssets: 0 });
  counts.completeSyncs = Object.values(bySync).filter((entry) => (
    Object.keys(entry.repositories).length > 0 &&
    Object.keys(entry.sources).some((source) => Object.keys(entry.targets).some((target) => source !== target))
  )).length;
  counts.completeSyncAssets = Object.entries(bySync).filter(([syncID, entry]) => (
    repoSyncAssetIDs.has(syncID) &&
    Object.keys(entry.repositories).some((repositoryID) => repositoryAssetIDs.has(repositoryID)) &&
    Object.keys(entry.sources).some((source) => remoteAssetIDs.has(source) && Object.keys(entry.targets).some((target) => source !== target && remoteAssetIDs.has(target)))
  )).length;
  return counts;
}

function countGitHubActionGraphLinks(graph: AnyRow = {}, projectAssetIDs = new Set<string>(), repositoryAssetIDs = new Set<string>(), remoteAssetIDs = new Set<string>(), actionAssetIDs = new Set<string>(), tagRunAssetIDs = new Set<string>(), tagRunAssetIDsByOperation: Record<string, Set<string>> = {}, tagOperationIDs = new Set<string>()) {
  const repositoryProjects: Record<string, Record<string, boolean>> = {};
  const remoteRepositories: Record<string, Record<string, boolean>> = {};
  const remoteActionRuns: Record<string, Record<string, boolean>> = {};
  const taggedRemoteOps: Record<string, Record<string, boolean>> = {};
  const tagActionRuns: Record<string, Record<string, boolean>> = {};
  const addRepositoryProject = (repositoryID: string, projectID: string) => {
    repositoryProjects[repositoryID] ??= {};
    repositoryProjects[repositoryID][projectID] = true;
  };
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
  const addTagActionRun = (tagRunID: string, actionID: string) => {
    tagActionRuns[tagRunID] ??= {};
    tagActionRuns[tagRunID][actionID] = true;
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type || '');
    const from = String(edge.from_asset_id || '');
    const to = String(edge.to_asset_id || '');
    if (relation === 'owns' && from.startsWith('project:') && to.startsWith('repository:')) {
      nextCounts.projectRepositories += 1;
      addRepositoryProject(to, from);
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
    if (relation === 'matched_action_run' && from.startsWith('repo_tag_run:') && to.startsWith('github_action_run:')) {
      nextCounts.tagActionRunLinks += 1;
      addTagActionRun(from, to);
    }
    return nextCounts;
  }, { projectRepositories: 0, repositoryRemotes: 0, remoteActionRuns: 0, taggedRemotes: 0, tagActionRunLinks: 0, completeActionRuns: 0, completeActionAssets: 0, completeTaggedRemotes: 0, completeTaggedRemoteAssets: 0, linkedTagRuns: 0, linkedTagRunAssets: 0 });
  const projectLinkedActionRuns: Record<string, boolean> = {};
  const canonicalProjectLinkedActionRuns: Record<string, boolean> = {};
  const hasCanonicalProjectRemote = (remoteID: string) => Object.keys(remoteRepositories[remoteID] || {}).some((repositoryID) => (
    repositoryAssetIDs.has(repositoryID) &&
    Object.keys(repositoryProjects[repositoryID] || {}).some((projectID) => projectAssetIDs.has(projectID))
  ));
  counts.completeActionRuns = Object.entries(remoteActionRuns).reduce((total, [remoteID, actionRuns]) => {
    const hasProjectRepository = Object.keys(remoteRepositories[remoteID] || {}).some((repositoryID) => Object.keys(repositoryProjects[repositoryID] || {}).length > 0);
    if (!hasProjectRepository) return total;
    Object.keys(actionRuns).forEach((actionID) => { projectLinkedActionRuns[actionID] = true; });
    return total + Object.keys(actionRuns).length;
  }, 0);
  counts.completeActionAssets = Object.entries(remoteActionRuns).reduce((total, [remoteID, actionRuns]) => {
    if (!hasCanonicalProjectRemote(remoteID) || !remoteAssetIDs.has(remoteID)) return total;
    return total + Object.keys(actionRuns).filter((actionID) => {
      const linkedAsset = actionAssetIDs.has(actionID);
      if (linkedAsset) canonicalProjectLinkedActionRuns[actionID] = true;
      return linkedAsset;
    }).length;
  }, 0);
  counts.completeTaggedRemotes = Object.entries(taggedRemoteOps).reduce((total, [remoteID, operations]) => {
    const hasProjectRepository = Object.keys(remoteRepositories[remoteID] || {}).some((repositoryID) => Object.keys(repositoryProjects[repositoryID] || {}).length > 0);
    return hasProjectRepository ? total + Object.keys(operations).length : total;
  }, 0);
  const canonicalTaggedTagRunAssets = new Set<string>();
  counts.completeTaggedRemoteAssets = Object.entries(taggedRemoteOps).reduce((total, [remoteID, operations]) => {
    if (!hasCanonicalProjectRemote(remoteID) || !remoteAssetIDs.has(remoteID)) return total;
    return total + Object.keys(operations).filter((operationID) => {
      const linkedTagRuns = Array.from(tagRunAssetIDsByOperation[operationID] || []).filter((tagRunID) => tagRunAssetIDs.has(tagRunID));
      if (!tagOperationIDs.has(operationID) || !linkedTagRuns.length) return false;
      linkedTagRuns.forEach((tagRunID) => canonicalTaggedTagRunAssets.add(tagRunID));
      return true;
    }).length;
  }, 0);
  counts.linkedTagRuns = Object.values(tagActionRuns).filter((actionRuns) => (
    Object.keys(actionRuns).some((actionID) => projectLinkedActionRuns[actionID])
  )).length;
  counts.linkedTagRunAssets = Object.entries(tagActionRuns).filter(([tagRunID, actionRuns]) => (
    canonicalTaggedTagRunAssets.has(tagRunID) &&
    Object.keys(actionRuns).some((actionID) => actionAssetIDs.has(actionID) && canonicalProjectLinkedActionRuns[actionID])
  )).length;
  return counts;
}

function countWebhookSyncGraphLinks(graph: AnyRow = {}, connectionAssetIDs = new Set<string>(), eventAssetIDs = new Set<string>(), repoSyncAssetIDs = new Set<string>(), syncOperationIDs = new Set<string>()) {
  const byEvent: Record<string, { connections: Record<string, boolean>; repoSyncs: Record<string, boolean>; operations: Record<string, boolean> }> = {};
  const operationRepoSyncs: Record<string, Record<string, boolean>> = {};
  const eventEntry = (assetID: string) => {
    byEvent[assetID] ??= { connections: {}, repoSyncs: {}, operations: {} };
    return byEvent[assetID];
  };
  const addOperationRepoSync = (operationID: string, repoSyncID: string) => {
    operationRepoSyncs[operationID] ??= {};
    operationRepoSyncs[operationID][repoSyncID] = true;
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type || '');
    const from = String(edge.from_asset_id || '');
    const to = String(edge.to_asset_id || '');
    if (relation === 'received_webhook_event' && from.startsWith('webhook_connection:') && to.startsWith('webhook_event:')) {
      nextCounts.connectionEvents += 1;
      eventEntry(to).connections[from] = true;
    }
    if (relation === 'matched_repo_sync' && from.startsWith('webhook_event:') && to.startsWith('repo_sync:')) {
      nextCounts.eventRepoSyncs += 1;
      eventEntry(from).repoSyncs[to] = true;
    }
    // Ignore legacy webhook_connection -> operation_run compatibility edges.
    if (relation === 'triggered_operation' && from.startsWith('webhook_event:') && to.startsWith('operation_run:')) {
      nextCounts.eventOperations += 1;
      eventEntry(from).operations[to] = true;
    }
    if (relation === 'ran_repo_sync' && from.startsWith('operation_run:') && to.startsWith('repo_sync:')) {
      addOperationRepoSync(from, to);
    }
    return nextCounts;
  }, { connectionEvents: 0, eventRepoSyncs: 0, eventOperations: 0, completeChains: 0, completeChainAssets: 0 });
  counts.completeChains = Object.values(byEvent).filter((entry) => (
    Object.keys(entry.connections).length > 0 &&
    Object.keys(entry.operations).some((operationID) => (
      Object.keys(entry.repoSyncs).some((repoSyncID) => operationRepoSyncs[operationID]?.[repoSyncID])
    ))
  )).length;
  counts.completeChainAssets = Object.entries(byEvent).filter(([eventID, entry]) => (
    eventAssetIDs.has(eventID) &&
    Object.keys(entry.connections).some((connectionID) => connectionAssetIDs.has(connectionID)) &&
    Object.keys(entry.operations).some((operationID) => syncOperationIDs.has(operationID) && (
      Object.keys(entry.repoSyncs).some((repoSyncID) => repoSyncAssetIDs.has(repoSyncID) && operationRepoSyncs[operationID]?.[repoSyncID])
    ))
  )).length;
  return counts;
}

function countSSHGraphLinks(graph: AnyRow = {}, commandAssetIDs = new Set<string>(), machineAssetIDs = new Set<string>(), operationIDs = new Set<string>(), verifyOperationIDs = new Set<string>(), runOperationIDs = new Set<string>()) {
  const byCommand: Record<string, { operations: Record<string, boolean>; machines: Record<string, boolean> }> = {};
  const commandEntry = (assetID: string) => {
    byCommand[assetID] ||= { operations: {}, machines: {} };
    return byCommand[assetID];
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type || '');
    const from = String(edge.from_asset_id || '');
    const to = String(edge.to_asset_id || '');
    if (relation === 'ran_ssh_command' && from.startsWith('operation_run:') && to.startsWith('ssh_command_run:')) {
      nextCounts.operationCommands += 1;
      commandEntry(to).operations[from] = true;
    }
    if (relation === 'executed_on' && from.startsWith('ssh_command_run:') && to.startsWith('ssh_machine:')) {
      nextCounts.commandMachines += 1;
      commandEntry(from).machines[to] = true;
    }
    return nextCounts;
  }, { operationCommands: 0, commandMachines: 0, completeCommands: 0, completeCommandAssets: 0, completeVerifyCommandAssets: 0, completeRunCommandAssets: 0 });
  counts.completeCommands = Object.values(byCommand).filter((entry) => Object.keys(entry.operations).length > 0 && Object.keys(entry.machines).length > 0).length;
  counts.completeCommandAssets = Object.entries(byCommand).filter(([commandID, entry]) => (
    commandAssetIDs.has(commandID) &&
    Object.keys(entry.operations).some((operationID) => operationIDs.has(operationID)) &&
    Object.keys(entry.machines).some((machineID) => machineAssetIDs.has(machineID))
  )).length;
  counts.completeVerifyCommandAssets = Object.entries(byCommand).filter(([commandID, entry]) => (
    commandAssetIDs.has(commandID) &&
    Object.keys(entry.operations).some((operationID) => verifyOperationIDs.has(operationID)) &&
    Object.keys(entry.machines).some((machineID) => machineAssetIDs.has(machineID))
  )).length;
  counts.completeRunCommandAssets = Object.entries(byCommand).filter(([commandID, entry]) => (
    commandAssetIDs.has(commandID) &&
    Object.keys(entry.operations).some((operationID) => runOperationIDs.has(operationID)) &&
    Object.keys(entry.machines).some((machineID) => machineAssetIDs.has(machineID))
  )).length;
  return counts;
}

function countArgoGraphLinks(graph: AnyRow = {}, connectionAssetIDs = new Set<string>(), appAssetIDs = new Set<string>(), targetAssetIDs = new Set<string>(), syncOperationIDs = new Set<string>()) {
  const byApp: Record<string, { connections: Record<string, boolean>; targets: Record<string, boolean> }> = {};
  const syncedConnections: Record<string, Record<string, boolean>> = {};
  const appEntry = (assetID: string) => {
    byApp[assetID] ||= { connections: {}, targets: {} };
    return byApp[assetID];
  };
  const addSyncedConnection = (connectionID: string, operationID: string) => {
    syncedConnections[connectionID] ??= {};
    syncedConnections[connectionID][operationID] = true;
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type || '');
    const from = String(edge.from_asset_id || '');
    const to = String(edge.to_asset_id || '');
    if (relation === 'manages' && from.startsWith('argo_connection:') && to.startsWith('argo_app:')) {
      nextCounts.connectionApps += 1;
      appEntry(to).connections[from] = true;
    }
    if (relation === 'deployed_to' && from.startsWith('argo_app:') && to.startsWith('deployment_target:')) {
      nextCounts.appTargets += 1;
      appEntry(from).targets[to] = true;
    }
    if (relation === 'synced_argo_connection' && from.startsWith('operation_run:') && to.startsWith('argo_connection:')) {
      addSyncedConnection(to, from);
    }
    return nextCounts;
  }, { connectionApps: 0, appTargets: 0, completeApps: 0, completeAppAssets: 0 });
  counts.completeApps = Object.values(byApp).filter((entry) => (
    Object.keys(entry.targets).length > 0 && Object.keys(entry.connections).some((connectionID) => Object.keys(syncedConnections[connectionID] || {}).length > 0)
  )).length;
  counts.completeAppAssets = Object.entries(byApp).filter(([appID, entry]) => (
    appAssetIDs.has(appID) &&
    Object.keys(entry.connections).some((connectionID) => (
      connectionAssetIDs.has(connectionID) &&
      Object.keys(syncedConnections[connectionID] || {}).some((operationID) => syncOperationIDs.has(operationID)) &&
      Object.keys(entry.targets).some((targetID) => targetAssetIDs.has(targetID))
    ))
  )).length;
  return counts;
}

function countApprovalGraphLinks(graph: AnyRow = {}, activeRuleIDs = new Set<string>(), approvalAssetIDs = new Set<string>(), operationAssetIDs = new Set<string>(), pendingOperationIDs = new Set<string>()) {
  const byApproval: Record<string, { rules: Record<string, boolean>; operations: Record<string, boolean> }> = {};
  const approvalEntry = (assetID: string) => {
    byApproval[assetID] ??= { rules: {}, operations: {} };
    return byApproval[assetID];
  };
  const counts = graphItems(graph, 'edges').reduce((nextCounts, edge: AnyRow) => {
    const relation = String(edge.relation_type || '');
    const from = String(edge.from_asset_id || '');
    const to = String(edge.to_asset_id || '');
    if (relation === 'governs' && activeRuleIDs.has(from) && approvalAssetIDs.has(to)) {
      nextCounts.ruleApprovals += 1;
      approvalEntry(to).rules[from] = true;
    }
    if (relation === 'gates_operation' && from.startsWith('operation_approval:') && to.startsWith('operation_run:')) {
      nextCounts.approvalOperations += 1;
      approvalEntry(from).operations[to] = true;
    }
    return nextCounts;
  }, { ruleApprovals: 0, approvalOperations: 0, completeApprovalChains: 0, completeApprovalAssetChains: 0 });
  counts.completeApprovalChains = Object.values(byApproval).filter((entry) => Object.keys(entry.rules).length > 0 && Object.keys(entry.operations).length > 0).length;
  counts.completeApprovalAssetChains = Object.entries(byApproval).filter(([approvalID, entry]) => (
    approvalAssetIDs.has(approvalID) &&
    Object.keys(entry.rules).length > 0 &&
    Object.keys(entry.operations).some((operationID) => operationAssetIDs.has(operationID) && pendingOperationIDs.has(operationID))
  )).length;
  return counts;
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
    project_asset_node: Number(evidenceCounts.project_asset_nodes || 0) > 0,
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
    required_environment_fields: ['project_asset', 'project_graph_node', 'project_asset_node', 'repository_asset', 'two_git_remote_assets', 'project_repository_graph_link', 'repository_to_two_remotes_graph_path'],
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

function firstVersionReadinessRows(
  assets: AnyRow[] = [],
  operations: AnyRow[] = [],
  approvalSummary: AnyRow = {},
  graph: AnyRow = {},
  t: (key: string) => string = createTranslator('en')
) {
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
  const operationLogs = countOperationRowsWithLogs(operations, assetIDsByType(assets, 'operation_run'));
  const contextEvidence = (assetCounts.agent_task || 0) + (assetCounts.ai_runtime || 0);
  const contextGenerations = countContextGenerationEvidence(assets);
  const repositoryGraphLinks = countRepositoryGraphLinks(graph, assetIDsByType(assets, 'project'), assetIDsByType(assets, 'repository'), assetIDsByType(assets, 'git_remote'));
  const repoSyncGraphLinks = countRepoSyncGraphLinks(graph, assetIDsByType(assets, 'repository'), assetIDsByType(assets, 'repo_sync'), assetIDsByType(assets, 'git_remote'));
  const syncOperationIDs = mergeSets(operationIDsByType(operations, 'repo.sync'), operationIDsByType(operations, 'repo.sync_remote'));
  const webhookSyncGraphLinks = countWebhookSyncGraphLinks(
    graph,
    assetIDsByTypeMetadata(assets, 'webhook_connection', 'provider', 'gitea'),
    assetIDsByTypeMetadata(assets, 'webhook_event', 'provider', 'gitea'),
    assetIDsByType(assets, 'repo_sync'),
    syncOperationIDs
  );
  const repoTagRuns = (operationCounts['repo.tag'] || 0) + (operationCounts['repo.create_tag'] || 0);
  const tagOperationIDs = mergeSets(operationIDsByType(operations, 'repo.tag'), operationIDsByType(operations, 'repo.create_tag'));
  const githubActionLinks = countGitHubActionGraphLinks(
    graph,
    assetIDsByType(assets, 'project'),
    assetIDsByType(assets, 'repository'),
    assetIDsByType(assets, 'git_remote'),
    assetIDsByGraphType(assets, 'pipeline_run', 'github_action_run'),
    assetIDsByType(assets, 'repo_tag_run'),
    tagRunAssetIDsByOperation(assets),
    tagOperationIDs
  );
  const sshVerifyOperationIDs = operationIDsByType(operations, 'ssh.verify');
  const sshRunOperationIDs = mergeSets(operationIDsByType(operations, 'ssh.exec'), operationIDsByType(operations, 'ssh.command'));
  const sshOperationIDs = mergeSets(sshVerifyOperationIDs, sshRunOperationIDs);
  const sshMachineAssetIDs = mergeSets(assetIDsByGraphType(assets, 'host', 'ssh_machine'), assetIDsByType(assets, 'ssh_machine'));
  const sshMachineAssets = (assetCounts.host || 0) + (assetCounts.ssh_machine || 0);
  const sshGraphLinks = countSSHGraphLinks(graph, assetIDsByType(assets, 'ssh_command_run'), sshMachineAssetIDs, sshOperationIDs, sshVerifyOperationIDs, sshRunOperationIDs);
  const argoGraphLinks = countArgoGraphLinks(
    graph,
    assetIDsByType(assets, 'argo_connection'),
    assetIDsByType(assets, 'argo_app'),
    assetIDsByType(assets, 'deployment_target'),
    operationIDsByType(operations, 'argo.apps.sync')
  );
  const activeApprovalRuleIDs = activeAssetIDsByTypeStatus(assets, 'operation_approval_rule', 'active');
  const approvalGraphLinks = countApprovalGraphLinks(
    graph,
    activeApprovalRuleIDs,
    assetIDsByType(assets, 'operation_approval'),
    assetIDsByType(assets, 'operation_run'),
    operationIDsByStatus(operations, 'pending_approval')
  );
  const contextGraphLinks = countContextGraphLinks(assets, graph);
  const argoEvidence = (assetCounts.argo_connection || 0) + (assetCounts.argo_app || 0) + (assetCounts.deployment_target || 0) + (operationCounts['argo.apps.sync'] || 0) + argoGraphLinks.connectionApps + argoGraphLinks.appTargets + argoGraphLinks.completeAppAssets;
  const argoEvidenceText = `${assetCounts.deployment_target || 0} targets / ${assetCounts.argo_connection || 0} Argo connections / ${assetCounts.argo_app || 0} apps / ${operationCounts['argo.apps.sync'] || 0} sync ops / ${argoGraphLinks.completeApps} complete app links / ${argoGraphLinks.completeAppAssets} app asset chains${argoGraphLinks.completeApps > 0 && argoGraphLinks.completeAppAssets === 0 ? ' / canonical evidence missing' : ''}`;
  const syncTriggerEvidenceText = `${syncTriggered} sync ops / ${giteaWebhooks} Gitea webhooks / ${giteaWebhookEvents} Gitea events / ${webhookSyncGraphLinks.completeChains} any-provider complete webhook chains / ${webhookSyncGraphLinks.completeChainAssets} webhook asset chains${webhookSyncGraphLinks.completeChains > 0 && webhookSyncGraphLinks.completeChainAssets === 0 ? ' / canonical evidence missing' : ''}`;
  const graphNodes = graphItems(graph, 'nodes').length;
  const graphEdges = graphItems(graph, 'edges').length;
  const graphEvidence = graphNodes + graphEdges;
  const projectGraphNodes = countGraphNodesByPrefix(graph, 'project:');
  const projectAssetGraphNodes = countGraphNodesByKnownIDs(graph, assetIDsByType(assets, 'project'));
  const projectState = readinessState((assetCounts.project || 0) > 0 && projectAssetGraphNodes > 0, `${assetCounts.project || 0} project assets / ${projectGraphNodes} project graph nodes / ${projectAssetGraphNodes} project asset nodes`, (assetCounts.project || 0) > 0 || projectGraphNodes > 0 || projectAssetGraphNodes > 0);
  const repositoryState = readinessState((assetCounts.repository || 0) > 0 && (assetCounts.git_remote || 0) >= 2 && repositoryGraphLinks.completeRepoAssets > 0, `${assetCounts.repository || 0} repos / ${assetCounts.git_remote || 0} remotes / ${repositoryGraphLinks.completeRepos} complete repos / ${repositoryGraphLinks.completeRepoAssets} repo asset paths / ${repositoryGraphLinks.projectRepository} project links / ${repositoryGraphLinks.repositoryRemotes} remote links`, (assetCounts.repository || 0) > 0 || (assetCounts.git_remote || 0) > 0 || repositoryGraphLinks.projectRepository > 0 || repositoryGraphLinks.repositoryRemotes > 0 || repositoryGraphLinks.completeRepoAssets > 0);
  return [
    {
      key: 'project',
      label: t('readiness.projectLabel'),
      next: t('readiness.projectNext'),
      ...projectState,
      demo_data_rehearsal_plan: demoDataRehearsalPlan(projectState.status, { project_assets: assetCounts.project || 0, project_graph_nodes: projectGraphNodes, project_asset_nodes: projectAssetGraphNodes }, ['project_asset', 'project_asset_node'])
    },
    {
      key: 'repositories',
      label: t('readiness.repositoriesLabel'),
      next: t('readiness.repositoriesNext'),
      ...repositoryState,
      demo_data_rehearsal_plan: demoDataRehearsalPlan(repositoryState.status, { repository_assets: assetCounts.repository || 0, git_remote_assets: assetCounts.git_remote || 0, complete_repository_paths: repositoryGraphLinks.completeRepoAssets, project_repository_links: repositoryGraphLinks.projectRepository, repository_remote_links: repositoryGraphLinks.repositoryRemotes }, ['repository_asset', 'two_git_remote_assets', 'project_to_repository_graph_link', 'repository_to_two_remotes_graph_path'])
    },
    {
      key: 'repo_sync',
      label: t('readiness.repoSyncLabel'),
      next: t('readiness.repoSyncNext'),
      ...readinessState((assetCounts.repo_sync || 0) > 0 && repoSyncGraphLinks.completeSyncAssets > 0, `${assetCounts.repo_sync || 0} repo syncs / ${repoSyncGraphLinks.completeSyncs} graph-complete syncs / ${repoSyncGraphLinks.completeSyncAssets} sync asset paths / ${repoSyncGraphLinks.repositorySync} repository links / ${repoSyncGraphLinks.sourceRemotes} source links / ${repoSyncGraphLinks.targetRemotes} target links`, (assetCounts.repo_sync || 0) > 0 || repoSyncGraphLinks.repositorySync > 0 || repoSyncGraphLinks.sourceRemotes > 0 || repoSyncGraphLinks.targetRemotes > 0 || repoSyncGraphLinks.completeSyncAssets > 0)
    },
    {
      key: 'sync_trigger',
      label: t('readiness.syncTriggerLabel'),
      next: t('readiness.syncTriggerNext'),
      ...readinessState(syncTriggered > 0 && giteaWebhooks > 0 && giteaWebhookEvents > 0 && webhookSyncGraphLinks.completeChainAssets > 0, syncTriggerEvidenceText, syncTriggered > 0 || giteaWebhooks > 0 || giteaWebhookEvents > 0 || webhookSyncGraphLinks.connectionEvents > 0 || webhookSyncGraphLinks.eventRepoSyncs > 0 || webhookSyncGraphLinks.eventOperations > 0 || webhookSyncGraphLinks.completeChainAssets > 0)
    },
    {
      key: 'github_actions',
      label: t('readiness.githubActionsLabel'),
      next: t('readiness.githubActionsNext'),
      ...readinessState((assetCounts.pipeline_run || 0) > 0 && githubActionLinks.completeActionAssets > 0 && repoTagRuns > 0 && githubActionLinks.completeTaggedRemoteAssets > 0 && githubActionLinks.linkedTagRunAssets > 0, `${assetCounts.pipeline_run || 0} pipeline runs / ${githubActionLinks.completeActionRuns} complete action chains / ${githubActionLinks.completeActionAssets} action asset chains / ${repoTagRuns} tag ops / ${githubActionLinks.completeTaggedRemotes} complete tag links / ${githubActionLinks.completeTaggedRemoteAssets} tag asset links / ${githubActionLinks.linkedTagRuns} linked tag runs / ${githubActionLinks.linkedTagRunAssets} linked tag assets / ${githubActionLinks.projectRepositories} project links / ${githubActionLinks.repositoryRemotes} remote links / ${githubActionLinks.remoteActionRuns} action links / ${githubActionLinks.taggedRemotes} tag links / ${githubActionLinks.tagActionRunLinks} tag-action links`, (assetCounts.pipeline_run || 0) > 0 || repoTagRuns > 0 || githubActionLinks.projectRepositories > 0 || githubActionLinks.repositoryRemotes > 0 || githubActionLinks.remoteActionRuns > 0 || githubActionLinks.taggedRemotes > 0 || githubActionLinks.tagActionRunLinks > 0 || githubActionLinks.completeActionAssets > 0 || githubActionLinks.completeTaggedRemoteAssets > 0 || githubActionLinks.linkedTagRunAssets > 0)
    },
    {
      key: 'ssh',
      label: t('readiness.sshLabel'),
      next: t('readiness.sshNext'),
      ...readinessState(sshMachineAssets > 0 && sshVerifyRuns > 0 && sshCommandRuns > 0 && sshGraphLinks.completeVerifyCommandAssets > 0 && sshGraphLinks.completeRunCommandAssets > 0, `${sshMachineAssets} machines / ${sshVerifyRuns} verify ops / ${sshCommandRuns} command ops / ${assetCounts.ssh_command_run || 0} command assets / ${sshGraphLinks.completeCommands} complete audit chains / ${sshGraphLinks.completeCommandAssets} command asset chains / ${sshGraphLinks.completeVerifyCommandAssets} verify chains / ${sshGraphLinks.completeRunCommandAssets} run chains`, sshMachineAssets > 0 || sshVerifyRuns > 0 || sshCommandRuns > 0 || (assetCounts.ssh_command_run || 0) > 0 || sshGraphLinks.operationCommands > 0 || sshGraphLinks.commandMachines > 0 || sshGraphLinks.completeCommandAssets > 0)
    },
    {
      key: 'argo',
      label: t('readiness.argoLabel'),
      next: t('readiness.argoNext'),
      ...readinessState((assetCounts.argo_connection || 0) > 0 && (assetCounts.argo_app || 0) > 0 && (assetCounts.deployment_target || 0) > 0 && (operationCounts['argo.apps.sync'] || 0) > 0 && argoGraphLinks.completeAppAssets > 0, argoEvidenceText, argoEvidence > 0)
    },
    {
      key: 'operations',
      label: t('readiness.operationsLabel'),
      next: t('readiness.operationsNext'),
      ...readinessState(operationAssets > 0 && operationLogs > 0, `${operationAssets} operation assets / ${listedOperationRuns} listed runs / ${operationLogs} with logs`, operationAssets > 0 || listedOperationRuns > 0 || operationLogs > 0)
    },
    {
      key: 'approval',
      label: t('readiness.approvalLabel'),
      next: t('readiness.approvalNext'),
      ...readinessState(approvalAssets > 0 && pendingApprovalOps > 0 && activeApprovalRules > 0 && approvalGraphLinks.completeApprovalAssetChains > 0, `${approvalEvidence} approvals / ${approvalAssets} approval assets / ${pendingApprovalOps} pending ops / ${activeApprovalRules} active rules / ${approvalGraphLinks.ruleApprovals} governed approvals / ${approvalGraphLinks.approvalOperations} gated ops / ${approvalGraphLinks.completeApprovalChains} complete approval chains / ${approvalGraphLinks.completeApprovalAssetChains} approval asset chains`, approvalEvidence > 0 || approvalAssets > 0 || pendingApprovalOps > 0 || activeApprovalRules > 0 || approvalGraphLinks.ruleApprovals > 0 || approvalGraphLinks.approvalOperations > 0 || approvalGraphLinks.completeApprovalAssetChains > 0)
    },
    {
      key: 'context',
      label: t('readiness.contextLabel'),
      next: t('readiness.contextNext'),
      ...readinessState(contextEvidence > 0 && contextGenerations > 0 && graphEvidence > 0 && contextGraphLinks.completeContextTaskAssets > 0, `${contextEvidence} context assets / ${contextGenerations} context generations / ${contextGraphLinks.completeContextTasks} complete context tasks / ${contextGraphLinks.completeContextTaskAssets} context asset tasks / ${contextGraphLinks.taskRuntimes} runtime links / ${contextGraphLinks.taskContextToolCalls} context tool links / ${graphNodes} graph nodes / ${graphEdges} graph edges`, contextEvidence > 0 || contextGenerations > 0 || graphEvidence > 0 || contextGraphLinks.taskRuntimes > 0 || contextGraphLinks.taskContextToolCalls > 0 || contextGraphLinks.completeContextTaskAssets > 0)
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

function assetGraphRankingSummary(nodes: AnyRow[] = [], edges: AnyRow[] = [], truncated = false, t: (key: string) => string = createTranslator('en')) {
  const ranked = [...nodes].sort((a, b) =>
    Number(b.graph_rank || 0) - Number(a.graph_rank || 0)
      || Number(b.relation_count || 0) - Number(a.relation_count || 0)
  );
  const top = ranked[0];
  const nodesLabelKey = truncated ? 'asset.rankedNodesTruncated' : 'asset.rankedNodes';
  return {
    nodesLabel: t(nodesLabelKey).replace('{count}', String(nodes.length)),
    edgesLabel: t('asset.visibleEdges').replace('{count}', String(edges.length)),
    topLabel: top ? `${top.display_name || top.name || top.id} (${t('asset.links').replace('{count}', String(top.relation_count || 0))})` : t('asset.noRankedAssets')
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
  const { t } = useI18n();
  const ops = useLoad(() => api('/api/operations'), []);
  const assets = useLoad(() => api('/api/assets'), []);
  const graph = useLoad(() => api('/api/assets/graph'), []);
  const approvalSummary = useLoad(() => api('/api/operation-approvals/summary'), []);
  const readinessRows = firstVersionReadinessRows(assets.data?.items || [], ops.data?.items || [], approvalSummary.data || {}, graph.data || {}, t);
  const readinessCounts = countByField(readinessRows, 'status');
  const graphWarning = graph.error ? t('dashboard.graphUnavailable').replace('{error}', graph.error) : graph.data && !graphPayloadAvailable(graph.data) ? t('dashboard.graphMissing') : '';
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
      message.success(result.project_created || result.repository_created || result.git_remote_created ? t('dashboard.demoDataPrepared') : t('dashboard.demoDataCurrent'));
      assets.reload();
      graph.reload();
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setDemoDataLoading(false);
    }
  }
  async function recordDemoReadinessSnapshot() {
    setDemoSnapshotLoading(true);
    try {
      const result = await api('/api/demo-readiness-snapshot', { method: 'POST', body: '{}' });
      setDemoSnapshotResult(result);
      message.success(result.readiness_snapshot_written ? t('dashboard.demoSnapshotRecorded') : result.rows_affected_unknown ? t('dashboard.demoSnapshotUnverified') : t('dashboard.demoSnapshotCurrent'));
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setDemoSnapshotLoading(false);
    }
  }
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>{t('title.dashboard')}</Typography.Title>
      <div className="metricGrid">
        <Card><Typography.Text type="secondary">{t('dashboard.gateway')}</Typography.Text><Typography.Title level={3}>{t('dashboard.online')}</Typography.Title></Card>
        <Card><Typography.Text type="secondary">{t('dashboard.recentOperations')}</Typography.Text><Typography.Title level={3}>{ops.data?.items?.length || 0}</Typography.Title></Card>
        <Card><Typography.Text type="secondary">{t('dashboard.readyChecks')}</Typography.Text><Typography.Title level={3}>{readinessCounts.ready || 0}/{readinessRows.length}</Typography.Title></Card>
        <Card><Typography.Text type="secondary">{t('dashboard.needsEvidence')}</Typography.Text><Typography.Title level={3}>{(readinessCounts.partial || 0) + (readinessCounts.missing || 0)}</Typography.Title></Card>
      </div>
      {graphWarning ? <Alert showIcon closable type="warning" message={graphWarning} action={<Button size="small" onClick={graph.reload}>{t('dashboard.retry')}</Button>} /> : null}
      <Card
        title={t('dashboard.firstVersionReadiness')}
        extra={<Space size={8} wrap>
          {demoDataResult ? <Tag color={demoDataResult.asset_graph_written ? 'green' : 'default'}>{t('dashboard.demoData')} {translatedValue(demoDataResult.recording_state || 'unknown', t)}</Tag> : null}
          {demoDataResult ? <Tag>{demoDataResult.git_remote_count || 0} {t('dashboard.remotes')}</Tag> : null}
          {demoDataResult ? <Tag>{demoDataResult.git_remote_url_written ? t('dashboard.remoteUrlWritten') : t('dashboard.noRemoteUrl')}</Tag> : null}
          {demoDataResult ? <Tag>{demoDataResult.provider_api_called ? t('dashboard.providerCalled') : t('dashboard.noProviderCall')}</Tag> : null}
          {demoSnapshotResult ? <Tag color={demoSnapshotResult.readiness_snapshot_written ? 'green' : demoSnapshotResult.recording_state === 'blocked' ? 'red' : 'default'}>{t('dashboard.snapshot')} {translatedValue(demoSnapshotResult.recording_state || 'unknown', t)}</Tag> : null}
          {demoSnapshotResult ? <Tag>{demoSnapshotResult.asset_graph_snapshot_written ? t('dashboard.assetStatusWritten') : t('dashboard.noAssetStatusWrite')}</Tag> : null}
          {demoSnapshotResult?.rows_affected_unknown ? <Tag color="gold">{t('dashboard.rowsAffectedUnknown')}</Tag> : null}
          <Button size="small" onClick={ensureDemoReadinessData} loading={demoDataLoading}>{t('dashboard.ensureDemoData')}</Button>
          <Button size="small" onClick={recordDemoReadinessSnapshot} loading={demoSnapshotLoading}>{t('dashboard.recordDemoSnapshot')}</Button>
        </Space>}
      >
        <Table<AnyRow>
          rowKey="key"
          dataSource={readinessRows}
          pagination={false}
          size="small"
          columns={[
            { title: t('dashboard.status'), render: (_, row) => <Tag color={row.color}>{translatedValue(row.status, t)}</Tag> },
            { title: t('dashboard.demoProof'), dataIndex: 'label' },
            { title: t('dashboard.evidence'), render: (_, row) => <Typography.Text>{String(row.evidence)}</Typography.Text> },
            { title: t('dashboard.demoPlan'), render: (_, row) => {
              const plan = row.demo_data_rehearsal_plan;
              if (!plan) return null;
              const environmentProof = plan.environment_demo_proof || {};
              const resultPreflight = plan.result_recording_plan?.result_recording_preflight || {};
              return <Space size={4} wrap>
                <Tag color={demoPlanStateColor(plan.plan_state)}>{translatedValue(plan.plan_state, t)}</Tag>
                {plan.environment_evidence_plan ? <Tag color={demoPlanStateColor(plan.environment_evidence_plan.evidence_state)}>{t('dashboard.env')} {translatedValue(plan.environment_evidence_plan.evidence_state || 'blocked', t)}</Tag> : null}
                {environmentProof.proof_state ? <Tag color={demoPlanStateColor(environmentProof.proof_state)}>{t('dashboard.proof')} {translatedValue(environmentProof.proof_state, t)}</Tag> : null}
                {environmentProof.complete_repository_multi_remote_path_observed ? <Tag color="green">{t('dashboard.multiRemotePath')}</Tag> : null}
                {plan.graph_proof_plan ? <Tag color={demoPlanStateColor(plan.graph_proof_plan.proof_state)}>{t('dashboard.graph')} {translatedValue(plan.graph_proof_plan.proof_state || 'blocked', t)}</Tag> : null}
                <Tag>{plan.demo_seed_written ? t('dashboard.seedWritten') : t('dashboard.noSeedWrite')}</Tag>
                <Tag>{plan.asset_graph_written ? t('dashboard.graphWritten') : t('dashboard.noGraphWrite')}</Tag>
                {plan.result_recording_plan ? <Tag color={demoPlanStateColor(plan.result_recording_plan.result_recording_state)}>{translatedValue(plan.result_recording_plan.result_recording_state || 'blocked', t)} {t('dashboard.result')}</Tag> : null}
                {resultPreflight.mode ? <Tag color={resultPreflight.snapshot_contract_ready ? 'green' : 'gold'}>{resultPreflight.snapshot_contract_ready ? t('dashboard.snapshotReview') : t('dashboard.snapshotBlocked')}</Tag> : null}
                {resultPreflight.mode ? <Tag>{resultPreflight.snapshot_write_enabled ? t('dashboard.snapshotWrite') : t('dashboard.noSnapshotWrite')}</Tag> : null}
              </Space>;
            } },
            { title: t('dashboard.next'), dataIndex: 'next' }
          ]}
        />
      </Card>
      <Operations embedded />
    </Space>
  );
}

function Projects() {
  const { t } = useI18n();
	const projects = useLoad(() => api('/api/projects'), []);
	const [open, setOpen] = useState(false);
  function deleteProject(row: AnyRow) {
    if (!row?.id) return;
    Modal.confirm({
      title: t('project.deleteConfirm'),
      okText: t('common.delete'),
      cancelText: t('common.cancel'),
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await api(`/api/projects/${row.id}`, { method: 'DELETE' });
          message.success(t('project.deleted'));
          projects.reload();
        } catch (error: any) {
          message.error(error.message || t('common.requestFailed'));
        }
      }
    });
  }
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="Projects" onCreate={() => setOpen(true)} />
      <Table<AnyRow> rowKey="id" dataSource={projects.data?.items || []} pagination={false} columns={[
        { title: t('common.name'), dataIndex: 'name' },
        { title: t('field.slug'), dataIndex: 'slug' },
        { title: t('common.description'), dataIndex: 'description' },
        { title: t('common.created'), dataIndex: 'created_at' },
        { title: t('common.action'), render: (_, row) => <Button size="small" danger onClick={() => deleteProject(row)}>{t('common.delete')}</Button> }
      ]} />
      <CreateModal title="Create project" open={open} setOpen={setOpen} fields={['name', 'slug', 'description']} onSubmit={(v) => api('/api/projects', { method: 'POST', body: JSON.stringify(v) }).then(projects.reload)} />
    </Space>
  );
}

function templateProvisionStatus(row: AnyRow, t: (key: string) => string = createTranslator('en')) {
  const summary = templateProvisionSummary(row, t);
  return (
    <Space size={4} wrap>
      <Tag color={summary.color}>{summary.label}</Tag>
      {summary.detail ? <Typography.Text type="secondary">{summary.detail}</Typography.Text> : null}
    </Space>
  );
}

function templateProvisionGuidanceView(row: AnyRow, t: (key: string) => string = createTranslator('en'), compact = false) {
  const guidance = templateProvisionGuidance(row, t);
  if (compact) {
    return (
      <Space direction="vertical" size={2}>
        <Space size={4} wrap>
          <Tag color={guidance.color}>{guidance.status}</Tag>
          {guidance.reviewStatus ? <Tag>{translatedValue(guidance.reviewStatus, t)}</Tag> : null}
          {guidance.reviewPlanMode ? <Tag color="blue">{translatedValue(guidance.reviewPlanMode, t)}</Tag> : null}
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
        {guidance.reviewStatus ? <Space size={4} wrap><Tag>{translatedValue(guidance.reviewStatus, t)}</Tag><Tag>{t('template.providerExecution')}: {translatedValue(guidance.reviewExecution, t)}</Tag></Space> : null}
        {guidance.reviewPlanMode ? <TemplateProviderReviewPlan guidance={guidance} t={t} /> : null}
        <Typography.Text strong>{guidance.next}</Typography.Text>
      </Space>}
    />
  );
}

function TemplateProviderReviewPlan({ guidance, t }: { guidance: TemplateProvisionGuidance; t: (key: string) => string }) {
  const requestColor = guidance.executionRequestStatus === 'approval_ready' ? 'green' : guidance.executionRequestStatus === 'blocked' ? 'gold' : 'default';
  const apiPlanColor = guidance.apiPlanStatus === 'ready' ? 'green' : guidance.apiPlanStatus === 'blocked' ? 'gold' : 'default';
  return (
    <Space direction="vertical" size={4}>
      <Space size={4} wrap>
        <Tag color="blue">{translatedValue(guidance.reviewPlanMode, t)}</Tag>
        {guidance.reviewKind ? <Tag>{translatedValue(guidance.reviewKind, t)}</Tag> : null}
        {guidance.approvalAction ? <Tag>{translatedValue(guidance.approvalAction, t)}</Tag> : null}
        {guidance.guardrailMode ? <Tag color="gold">{t('template.guardrail')} {translatedValue(guidance.guardrailMode, t)}</Tag> : null}
        {guidance.executionRequestStatus ? <Tag color={requestColor}>{t('template.request')} {translatedValue(guidance.executionRequestStatus, t)}</Tag> : null}
        {guidance.apiPlanStatus ? <Tag color={apiPlanColor}>{t('template.apiPlan')} {translatedValue(guidance.apiPlanStatus, t)}</Tag> : null}
      </Space>
      {guidance.sourceBranch || guidance.targetBranch ? (
        <Typography.Text type="secondary">{guidance.sourceBranch || '-'} -&gt; {guidance.targetBranch || '-'}</Typography.Text>
      ) : null}
      {guidance.executionRequestResource ? (
        <Typography.Text type="secondary">{t('template.resource')}: {translatedValue(guidance.executionRequestResource, t)}</Typography.Text>
      ) : null}
      {guidance.guardrailGates.length ? (
        <Space size={4} wrap>
          {guidance.guardrailGates.map((gate, index) => (
            <Tag key={`${gate.gate || 'gate'}-${index}`} color={gate.status === 'ready' ? 'green' : 'gold'}>
              {translatedValue(String(gate.gate || 'gate'), t)}: {translatedValue(String(gate.status || 'unknown'), t)}
            </Tag>
          ))}
        </Space>
      ) : null}
      {guidance.guardrailReasons.length ? (
        <Typography.Text type="secondary">{shortText(`${t('template.blocked')}: ${guidance.guardrailReasons.join(', ')}`, 120)}</Typography.Text>
      ) : null}
      {guidance.apiPlanOperations.length ? (
        <Space size={4} wrap>
          {guidance.apiPlanMode ? <Tag>{translatedValue(guidance.apiPlanMode, t)}</Tag> : null}
          <Tag>{t('common.files')} {guidance.apiPlanFileCount}</Tag>
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
              {translatedValue(String(step.name || 'step'), t)}: {translatedValue(String(step.status || 'planned'), t)}
            </Tag>
          ))}
        </Space>
      ) : null}
    </Space>
  );
}

function ProviderReviewApprovalAudit({ value, persistedAttemptLedger, onClaimAttempt, onRecordAttemptResult, onExecuteAttemptLive, onCleanupAttemptLive, onRecordAttemptSnapshot, onRecordAttemptCredentialSnapshot, onRecordAttemptBranchPolicySnapshot, onRecordAttemptRuntimeSnapshot, onRecordAttemptAdapterRehearsalSnapshot, onRecordAttemptAdapterBlueprintSnapshot, onRecordAttemptLiveAdapterContractSnapshot, onRecordAttemptInvocationSnapshot, onRecordAttemptExecutionLockSnapshot, onRecordAttemptRequestEnvelopeSnapshot, onRecordAttemptIdempotencySnapshot, onRecordAttemptRequestValidationSnapshot, onRecordAttemptRequestMaterializationSnapshot, onRecordAttemptActivationSnapshot, onRecordAttemptTransportSnapshot, onRecordAttemptSendSnapshot, onRecordAttemptRetryBackoffSnapshot, onRecordAttemptResponseSnapshot, onRecordAttemptResultRecordingSnapshot, onRecordAttemptProviderCallBoundarySnapshot, onRecordAttemptTransactionSnapshot, onRecordAttemptLiveExecutionReadinessSnapshot, onRecordAttemptLiveExecutionGuardSnapshot, onCheckAttemptLiveExecutionPreflight, onCheckAttemptLiveExecutionLaunchPlan, onRecordCurrentAttemptLiveReadinessSnapshot, onCheckCurrentAttemptLiveExecutionLaunchPlan, onCheckCurrentLiveExecutionGate, onRecordArmingSnapshot, canRecordCurrentAttemptLiveReadinessSnapshot, canCheckCurrentAttemptLiveExecutionLaunchPlan, canCheckCurrentLiveExecutionGate, canRecordArmingSnapshot, claimLoading, resultLoading, liveExecuteLoading, liveCleanupLoading, snapshotLoading, credentialSnapshotLoading, branchPolicySnapshotLoading, runtimeSnapshotLoading, adapterRehearsalSnapshotLoading, adapterBlueprintSnapshotLoading, liveAdapterContractSnapshotLoading, invocationSnapshotLoading, executionLockSnapshotLoading, requestEnvelopeSnapshotLoading, idempotencySnapshotLoading, requestValidationSnapshotLoading, requestMaterializationSnapshotLoading, activationSnapshotLoading, transportSnapshotLoading, sendSnapshotLoading, retryBackoffSnapshotLoading, responseSnapshotLoading, resultRecordingSnapshotLoading, providerCallBoundarySnapshotLoading, transactionSnapshotLoading, liveExecutionReadinessSnapshotLoading, liveExecutionGuardSnapshotLoading, liveExecutionPreflightLoading, liveExecutionLaunchPlanLoading, currentLiveReadinessSnapshotLoading, currentLiveExecutionLaunchPlanLoading, currentLiveExecutionGateLoading, armingSnapshotLoading, snapshotResult, credentialSnapshotResult, branchPolicySnapshotResult, runtimeSnapshotResult, adapterRehearsalSnapshotResult, adapterBlueprintSnapshotResult, liveAdapterContractSnapshotResult, invocationSnapshotResult, executionLockSnapshotResult, requestEnvelopeSnapshotResult, idempotencySnapshotResult, requestValidationSnapshotResult, requestMaterializationSnapshotResult, activationSnapshotResult, transportSnapshotResult, sendSnapshotResult, retryBackoffSnapshotResult, responseSnapshotResult, resultRecordingSnapshotResult, providerCallBoundarySnapshotResult, transactionSnapshotResult, liveExecutionReadinessSnapshotResult, liveExecutionGuardSnapshotResult, liveExecutionPreflightResult, liveExecutionLaunchPlanResult, liveExecutionResult, liveCleanupResult, currentLiveReadinessSnapshotResult, currentLiveExecutionLaunchPlanResult, currentLiveExecutionGateResult, armingSnapshotResult, optimisticallyClaimedAttemptID, optimisticallyRecordedAttemptID, optimisticallyLiveExecutedAttemptID, optimisticallyLiveCleanedAttemptID }: { value?: AnyRow; persistedAttemptLedger?: AnyRow; onClaimAttempt?: (id: string) => void; onRecordAttemptResult?: (id: string, result: 'success' | 'retryable' | 'failed') => void; onExecuteAttemptLive?: (id: string) => void; onCleanupAttemptLive?: (id: string) => void; onRecordAttemptSnapshot?: (id: string) => void; onRecordAttemptCredentialSnapshot?: (id: string) => void; onRecordAttemptBranchPolicySnapshot?: (id: string) => void; onRecordAttemptRuntimeSnapshot?: (id: string) => void; onRecordAttemptAdapterRehearsalSnapshot?: (id: string) => void; onRecordAttemptAdapterBlueprintSnapshot?: (id: string) => void; onRecordAttemptLiveAdapterContractSnapshot?: (id: string) => void; onRecordAttemptInvocationSnapshot?: (id: string) => void; onRecordAttemptExecutionLockSnapshot?: (id: string) => void; onRecordAttemptRequestEnvelopeSnapshot?: (id: string) => void; onRecordAttemptIdempotencySnapshot?: (id: string) => void; onRecordAttemptRequestValidationSnapshot?: (id: string) => void; onRecordAttemptRequestMaterializationSnapshot?: (id: string) => void; onRecordAttemptActivationSnapshot?: (id: string) => void; onRecordAttemptTransportSnapshot?: (id: string) => void; onRecordAttemptSendSnapshot?: (id: string) => void; onRecordAttemptRetryBackoffSnapshot?: (id: string) => void; onRecordAttemptResponseSnapshot?: (id: string) => void; onRecordAttemptResultRecordingSnapshot?: (id: string) => void; onRecordAttemptProviderCallBoundarySnapshot?: (id: string) => void; onRecordAttemptTransactionSnapshot?: (id: string) => void; onRecordAttemptLiveExecutionReadinessSnapshot?: (id: string) => void; onRecordAttemptLiveExecutionGuardSnapshot?: (id: string) => void; onCheckAttemptLiveExecutionPreflight?: (id: string) => void; onCheckAttemptLiveExecutionLaunchPlan?: (id: string) => void; onRecordCurrentAttemptLiveReadinessSnapshot?: () => void; onCheckCurrentAttemptLiveExecutionLaunchPlan?: () => void; onCheckCurrentLiveExecutionGate?: () => void; onRecordArmingSnapshot?: () => void; canRecordCurrentAttemptLiveReadinessSnapshot?: boolean; canCheckCurrentAttemptLiveExecutionLaunchPlan?: boolean; canCheckCurrentLiveExecutionGate?: boolean; canRecordArmingSnapshot?: boolean; claimLoading?: boolean; resultLoading?: boolean; liveExecuteLoading?: boolean; liveCleanupLoading?: boolean; snapshotLoading?: boolean; credentialSnapshotLoading?: boolean; branchPolicySnapshotLoading?: boolean; runtimeSnapshotLoading?: boolean; adapterRehearsalSnapshotLoading?: boolean; adapterBlueprintSnapshotLoading?: boolean; liveAdapterContractSnapshotLoading?: boolean; invocationSnapshotLoading?: boolean; executionLockSnapshotLoading?: boolean; requestEnvelopeSnapshotLoading?: boolean; idempotencySnapshotLoading?: boolean; requestValidationSnapshotLoading?: boolean; requestMaterializationSnapshotLoading?: boolean; activationSnapshotLoading?: boolean; transportSnapshotLoading?: boolean; sendSnapshotLoading?: boolean; retryBackoffSnapshotLoading?: boolean; responseSnapshotLoading?: boolean; resultRecordingSnapshotLoading?: boolean; providerCallBoundarySnapshotLoading?: boolean; transactionSnapshotLoading?: boolean; liveExecutionReadinessSnapshotLoading?: boolean; liveExecutionGuardSnapshotLoading?: boolean; liveExecutionPreflightLoading?: boolean; liveExecutionLaunchPlanLoading?: boolean; currentLiveReadinessSnapshotLoading?: boolean; currentLiveExecutionLaunchPlanLoading?: boolean; currentLiveExecutionGateLoading?: boolean; armingSnapshotLoading?: boolean; snapshotResult?: AnyRow; credentialSnapshotResult?: AnyRow; branchPolicySnapshotResult?: AnyRow; runtimeSnapshotResult?: AnyRow; adapterRehearsalSnapshotResult?: AnyRow; adapterBlueprintSnapshotResult?: AnyRow; liveAdapterContractSnapshotResult?: AnyRow; invocationSnapshotResult?: AnyRow; executionLockSnapshotResult?: AnyRow; requestEnvelopeSnapshotResult?: AnyRow; idempotencySnapshotResult?: AnyRow; requestValidationSnapshotResult?: AnyRow; requestMaterializationSnapshotResult?: AnyRow; activationSnapshotResult?: AnyRow; transportSnapshotResult?: AnyRow; sendSnapshotResult?: AnyRow; retryBackoffSnapshotResult?: AnyRow; responseSnapshotResult?: AnyRow; resultRecordingSnapshotResult?: AnyRow; providerCallBoundarySnapshotResult?: AnyRow; transactionSnapshotResult?: AnyRow; liveExecutionReadinessSnapshotResult?: AnyRow; liveExecutionGuardSnapshotResult?: AnyRow; liveExecutionPreflightResult?: AnyRow; liveExecutionLaunchPlanResult?: AnyRow; liveExecutionResult?: AnyRow; liveCleanupResult?: AnyRow; currentLiveReadinessSnapshotResult?: AnyRow; currentLiveExecutionLaunchPlanResult?: AnyRow; currentLiveExecutionGateResult?: AnyRow; armingSnapshotResult?: AnyRow; optimisticallyClaimedAttemptID?: string; optimisticallyRecordedAttemptID?: string; optimisticallyLiveExecutedAttemptID?: string; optimisticallyLiveCleanedAttemptID?: string }) {
  const { t } = useI18n();
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
  const claimableAttempt = attemptOperations.find((operation: AnyRow) => String(operation.name || '') === String(attemptExecutionCandidate.next_operation || ''));
  const claimableAttemptID = String(claimableAttempt?.id || '');
  const claimOptimistic = Boolean(claimableAttemptID) && optimisticallyClaimedAttemptID === claimableAttemptID;
  const resultRecordableAttempt = attemptOperations.find((operation: AnyRow) => operation.claim_recorded === true && String(operation.status || '') === 'running');
  const resultRecordableAttemptID = String(resultRecordableAttempt?.id || '');
  const resultRecordablePlan = resultRecordableAttempt?.result_recording_plan || {};
  const resultOptimistic = Boolean(resultRecordableAttemptID) && optimisticallyRecordedAttemptID === resultRecordableAttemptID;
  const liveExecuteOptimistic = Boolean(resultRecordableAttemptID) && optimisticallyLiveExecutedAttemptID === resultRecordableAttemptID;
  const cleanupableAttempt = attemptOperations.find((operation: AnyRow) =>
    String(operation.status || '') === 'failed' &&
    operation.cleanup_required === true &&
    String(operation.manual_cleanup_hint || '') === 'review_branch_delete_required'
  );
  const cleanupableAttemptID = String(cleanupableAttempt?.id || '');
  const liveCleanupOptimistic = Boolean(cleanupableAttemptID) && optimisticallyLiveCleanedAttemptID === cleanupableAttemptID;
  const canClaimAttempt = Boolean(claimableAttemptID) && attemptClaimPlan.claim_metadata_ready === true && attemptClaimPlan.claim_recorded !== true && !claimOptimistic;
  const canRecordAttemptResult = Boolean(resultRecordableAttemptID) && resultRecordablePlan.result_recording_metadata_ready === true && !resultOptimistic;
  const canExecuteAttemptLive = Boolean(resultRecordableAttemptID) && !liveExecuteOptimistic && !liveExecuteLoading;
  const canCleanupAttemptLive = Boolean(cleanupableAttemptID) && !liveCleanupOptimistic && !liveCleanupLoading;
  const claimBlockedReason = claimOptimistic
    ? 'claim already requested'
    : attemptClaimPlan.claim_recorded === true
      ? 'claim already recorded'
      : attemptClaimPlan.claim_metadata_ready !== true
        ? String((Array.isArray(attemptClaimPlan.blocked_reasons) && attemptClaimPlan.blocked_reasons[0]) || 'claim metadata not ready')
        : '';
  const resultBlockedReason = resultOptimistic
    ? 'result already requested'
    : !resultRecordableAttemptID
      ? 'claim a running attempt first'
      : resultRecordablePlan.result_recording_metadata_ready !== true
        ? String((Array.isArray(resultRecordablePlan.blocked_reasons) && resultRecordablePlan.blocked_reasons[0]) || 'result metadata not ready')
      : '';
  const liveExecuteBlockedReason = liveExecuteOptimistic
    ? t('providerReview.liveExecutionAlreadyRequested')
    : !resultRecordableAttemptID
      ? t('providerReview.claimRunningAttemptFirst')
      : '';
  const liveCleanupBlockedReason = liveCleanupOptimistic
    ? t('providerReview.cleanupAlreadyRequested')
    : !cleanupableAttemptID
      ? t('providerReview.cleanupNotRequired')
      : '';
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
          {onRecordCurrentAttemptLiveReadinessSnapshot ? (
            <Button size="small" loading={currentLiveReadinessSnapshotLoading} disabled={!canRecordCurrentAttemptLiveReadinessSnapshot || currentLiveReadinessSnapshotLoading} onClick={onRecordCurrentAttemptLiveReadinessSnapshot}>
              Record current readiness
            </Button>
          ) : null}
          {onCheckCurrentAttemptLiveExecutionLaunchPlan ? (
            <Button size="small" loading={currentLiveExecutionLaunchPlanLoading} disabled={!canCheckCurrentAttemptLiveExecutionLaunchPlan || currentLiveExecutionLaunchPlanLoading} onClick={onCheckCurrentAttemptLiveExecutionLaunchPlan}>
              Check current launch
            </Button>
          ) : null}
          {onCheckCurrentLiveExecutionGate ? (
            <Button size="small" loading={currentLiveExecutionGateLoading} disabled={!canCheckCurrentLiveExecutionGate || currentLiveExecutionGateLoading} onClick={onCheckCurrentLiveExecutionGate}>
              Check live gate
            </Button>
          ) : null}
          {onRecordArmingSnapshot ? (
            <Button size="small" loading={armingSnapshotLoading} disabled={!canRecordArmingSnapshot || armingSnapshotLoading} onClick={onRecordArmingSnapshot}>
              Record arming
            </Button>
          ) : null}
        </Space>
      ) : null}
      {armingSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={armingSnapshotResult.provider_review_mutation_arming_snapshot_written ? 'green' : armingSnapshotResult.recording_state === 'asset_missing' ? 'red' : armingSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            arming snapshot {String(armingSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{armingSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{armingSnapshotResult.operation_approval_asset_observed ? 'approval asset observed' : 'approval asset missing'}</Tag>
          <Tag>{armingSnapshotResult.snapshot?.attempt_live_execution_readiness_complete ? 'attempt readiness complete' : 'attempt readiness missing'}</Tag>
          <Tag>{Number(armingSnapshotResult.snapshot?.attempt_live_execution_readiness_count || 0)}/{Number(armingSnapshotResult.snapshot?.attempt_count || 0)} attempt readiness</Tag>
          <Tag>{armingSnapshotResult.mutation_armed ? 'mutation armed' : 'mutation off'}</Tag>
          <Tag>{armingSnapshotResult.provider_api_call_made ? 'api called' : 'no api call'}</Tag>
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
      {snapshotResult ? (
        <Space size={4} wrap>
          <Tag color={snapshotResult.provider_review_attempt_snapshot_written ? 'green' : snapshotResult.recording_state === 'asset_missing' ? 'red' : snapshotResult.recording_ready === false ? 'gold' : 'default'}>
            attempt snapshot {String(snapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{snapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{snapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{snapshotResult.provider_api_call_made ? 'api called' : 'no api call'}</Tag>
          <Tag>{String(snapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {credentialSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={credentialSnapshotResult.provider_review_attempt_credential_snapshot_written ? 'green' : credentialSnapshotResult.recording_state === 'asset_missing' ? 'red' : credentialSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            credential snapshot {String(credentialSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{credentialSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{credentialSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{credentialSnapshotResult.credential_bound ? 'credential bound' : 'credential unbound'}</Tag>
          <Tag>{credentialSnapshotResult.authorization_header_materialized ? 'auth header materialized' : 'no auth header'}</Tag>
          <Tag>{String(credentialSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {branchPolicySnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={branchPolicySnapshotResult.provider_review_attempt_branch_policy_snapshot_written ? 'green' : branchPolicySnapshotResult.recording_state === 'asset_missing' ? 'red' : branchPolicySnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            branch policy snapshot {String(branchPolicySnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{branchPolicySnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{branchPolicySnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{branchPolicySnapshotResult.branch_policy_verified ? 'policy verified' : 'policy not verified'}</Tag>
          <Tag>{branchPolicySnapshotResult.branch_ref_created ? 'branch created' : 'no branch ref'}</Tag>
          <Tag>{branchPolicySnapshotResult.review_request_created ? 'review request created' : 'no review request'}</Tag>
          <Tag>{String(branchPolicySnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {runtimeSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={runtimeSnapshotResult.provider_review_attempt_runtime_snapshot_written ? 'green' : runtimeSnapshotResult.recording_state === 'asset_missing' ? 'red' : runtimeSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            runtime snapshot {String(runtimeSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{runtimeSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{runtimeSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{runtimeSnapshotResult.live_adapter_implemented ? 'live adapter ready' : 'live adapter blocked'}</Tag>
          <Tag>{runtimeSnapshotResult.provider_client_constructed ? 'client constructed' : 'no provider client'}</Tag>
          <Tag>{runtimeSnapshotResult.runtime_bound ? 'runtime bound' : 'runtime unbound'}</Tag>
          <Tag>{String(runtimeSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {adapterRehearsalSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={adapterRehearsalSnapshotResult.provider_review_attempt_adapter_rehearsal_snapshot_written ? 'green' : adapterRehearsalSnapshotResult.recording_state === 'asset_missing' ? 'red' : adapterRehearsalSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            rehearsal snapshot {String(adapterRehearsalSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{adapterRehearsalSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{adapterRehearsalSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{adapterRehearsalSnapshotResult.snapshot?.adapter_rehearsal_ready ? 'rehearsal ready' : 'rehearsal blocked'}</Tag>
          <Tag>{adapterRehearsalSnapshotResult.snapshot?.mutation_arming_candidate ? 'arming candidate' : 'arming not ready'}</Tag>
          <Tag>{adapterRehearsalSnapshotResult.live_adapter_implemented ? 'live adapter ready' : 'live adapter blocked'}</Tag>
          <Tag>{String(adapterRehearsalSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {adapterBlueprintSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={adapterBlueprintSnapshotResult.provider_review_attempt_adapter_blueprint_snapshot_written ? 'green' : adapterBlueprintSnapshotResult.recording_state === 'asset_missing' ? 'red' : adapterBlueprintSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            blueprint snapshot {String(adapterBlueprintSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{adapterBlueprintSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{adapterBlueprintSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{adapterBlueprintSnapshotResult.snapshot?.invocation_contract_ready ? 'invocation contract' : 'invocation blocked'}</Tag>
          <Tag>{adapterBlueprintSnapshotResult.snapshot?.live_adapter_contract_ready ? 'live adapter contract' : 'live adapter blocked'}</Tag>
          <Tag>{adapterBlueprintSnapshotResult.adapter_implemented ? 'adapter implemented' : 'adapter unimplemented'}</Tag>
          <Tag>{adapterBlueprintSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(adapterBlueprintSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {liveAdapterContractSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={liveAdapterContractSnapshotResult.provider_review_attempt_live_adapter_contract_snapshot_written ? 'green' : liveAdapterContractSnapshotResult.recording_state === 'asset_missing' ? 'red' : liveAdapterContractSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            contract snapshot {String(liveAdapterContractSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{liveAdapterContractSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{liveAdapterContractSnapshotResult.recording_state === 'operation_approval_not_approved' ? 'attempt asset not checked' : liveAdapterContractSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{liveAdapterContractSnapshotResult.snapshot?.live_adapter_contract_metadata_ready ? 'contract metadata' : 'contract blocked'}</Tag>
          <Tag>{liveAdapterContractSnapshotResult.snapshot?.no_call_observed ? 'no-call observed' : 'no-call blocked'}</Tag>
          <Tag>inputs {Array.isArray(liveAdapterContractSnapshotResult.snapshot?.contract_input_fields) ? liveAdapterContractSnapshotResult.snapshot.contract_input_fields.length : 0}</Tag>
          <Tag>outputs {Array.isArray(liveAdapterContractSnapshotResult.snapshot?.contract_output_fields) ? liveAdapterContractSnapshotResult.snapshot.contract_output_fields.length : 0}</Tag>
          <Tag>errors {Array.isArray(liveAdapterContractSnapshotResult.snapshot?.contract_error_classes) ? liveAdapterContractSnapshotResult.snapshot.contract_error_classes.length : 0}</Tag>
          <Tag>{liveAdapterContractSnapshotResult.snapshot?.request_contract_materialized ? 'request contract materialized' : 'request contract blocked'}</Tag>
          <Tag>{liveAdapterContractSnapshotResult.snapshot?.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(liveAdapterContractSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {invocationSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={invocationSnapshotResult.provider_review_attempt_invocation_snapshot_written ? 'green' : invocationSnapshotResult.recording_state === 'asset_missing' ? 'red' : invocationSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            invocation snapshot {String(invocationSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{invocationSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{invocationSnapshotResult.recording_state === 'operation_approval_not_approved' ? 'attempt asset not checked' : invocationSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{invocationSnapshotResult.recording_state === 'asset_missing' ? 'invocation not recorded' : invocationSnapshotResult.snapshot?.invocation_contract_ready ? 'invocation contract' : 'invocation blocked'}</Tag>
          <Tag>{invocationSnapshotResult.snapshot?.invocation_ready ? 'invocation ready' : 'invocation not armed'}</Tag>
          <Tag>{invocationSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(invocationSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {executionLockSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={executionLockSnapshotResult.provider_review_attempt_execution_lock_snapshot_written ? 'green' : executionLockSnapshotResult.recording_state === 'asset_missing' ? 'red' : executionLockSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            execution lock snapshot {String(executionLockSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{executionLockSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{executionLockSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{executionLockSnapshotResult.execution_lock_acquired ? 'lock acquired' : 'lock not acquired'}</Tag>
          <Tag>{executionLockSnapshotResult.idempotency_claim_recorded ? 'idempotency claimed' : 'idempotency unclaimed'}</Tag>
          <Tag>{executionLockSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(executionLockSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {activationSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={activationSnapshotResult.provider_review_attempt_activation_snapshot_written ? 'green' : activationSnapshotResult.recording_state === 'asset_missing' ? 'red' : activationSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            activation snapshot {String(activationSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{activationSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{activationSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{activationSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(activationSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {requestEnvelopeSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={requestEnvelopeSnapshotResult.provider_review_attempt_request_envelope_snapshot_written ? 'green' : requestEnvelopeSnapshotResult.recording_state === 'asset_missing' ? 'red' : requestEnvelopeSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            request envelope snapshot {String(requestEnvelopeSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{requestEnvelopeSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{requestEnvelopeSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{requestEnvelopeSnapshotResult.request_envelope_materialized ? 'envelope materialized' : 'envelope blocked'}</Tag>
          <Tag>{requestEnvelopeSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(requestEnvelopeSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {idempotencySnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={idempotencySnapshotResult.provider_review_attempt_idempotency_snapshot_written ? 'green' : idempotencySnapshotResult.recording_state === 'asset_missing' ? 'red' : idempotencySnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            idempotency snapshot {String(idempotencySnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{idempotencySnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{idempotencySnapshotResult.recording_state === 'operation_approval_not_approved' ? 'attempt asset not checked' : idempotencySnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{idempotencySnapshotResult.snapshot?.idempotency_metadata_ready ? 'idempotency metadata' : 'idempotency blocked'}</Tag>
          <Tag>{idempotencySnapshotResult.snapshot?.no_call_observed ? 'no-call observed' : 'no-call blocked'}</Tag>
          <Tag>{String(idempotencySnapshotResult.snapshot?.idempotency_key_kind || 'key redacted')}</Tag>
          <Tag>{String(idempotencySnapshotResult.snapshot?.replay_check || 'replay pending')}</Tag>
          <Tag>{String(idempotencySnapshotResult.snapshot?.conflict_policy || 'conflict pending')}</Tag>
          <Tag>{idempotencySnapshotResult.idempotency_claim_recorded ? 'idempotency claimed' : 'idempotency unclaimed'}</Tag>
          <Tag>{idempotencySnapshotResult.idempotency_key_included ? 'key included' : 'key redacted'}</Tag>
          <Tag>{idempotencySnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(idempotencySnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {requestValidationSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={requestValidationSnapshotResult.provider_review_attempt_request_validation_snapshot_written ? 'green' : requestValidationSnapshotResult.recording_state === 'asset_missing' ? 'red' : requestValidationSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            request validation snapshot {String(requestValidationSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{requestValidationSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{requestValidationSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{requestValidationSnapshotResult.snapshot?.dispatch_metadata_ready ? 'dispatch metadata' : 'dispatch blocked'}</Tag>
          <Tag>{requestValidationSnapshotResult.snapshot?.preflight_ready ? 'preflight ready' : 'preflight blocked'}</Tag>
          <Tag>{requestValidationSnapshotResult.request_validated ? 'request validated' : 'request not validated'}</Tag>
          <Tag>{requestValidationSnapshotResult.request_materialized ? 'request materialized' : 'request not materialized'}</Tag>
          <Tag>{requestValidationSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(requestValidationSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {requestMaterializationSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={requestMaterializationSnapshotResult.provider_review_attempt_request_materialization_snapshot_written ? 'green' : requestMaterializationSnapshotResult.recording_state === 'asset_missing' ? 'red' : requestMaterializationSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            request materialization snapshot {String(requestMaterializationSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{requestMaterializationSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{requestMaterializationSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{requestMaterializationSnapshotResult.snapshot?.request_materialization_contract_ready ? 'materialization contract' : 'materialization blocked'}</Tag>
          <Tag>{requestMaterializationSnapshotResult.request_materialized ? 'request materialized' : 'request not materialized'}</Tag>
          <Tag>{requestMaterializationSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(requestMaterializationSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {transportSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={transportSnapshotResult.provider_review_attempt_transport_snapshot_written ? 'green' : transportSnapshotResult.recording_state === 'asset_missing' ? 'red' : transportSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            transport snapshot {String(transportSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{transportSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{transportSnapshotResult.recording_state === 'operation_approval_not_approved' ? 'attempt asset not checked' : transportSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{transportSnapshotResult.snapshot?.transport_metadata_ready ? 'transport metadata' : 'transport blocked'}</Tag>
          <Tag>{transportSnapshotResult.snapshot?.no_call_observed ? 'no-call observed' : 'no-call blocked'}</Tag>
          <Tag>{String(transportSnapshotResult.snapshot?.method || 'method')}</Tag>
          <Tag>{String(transportSnapshotResult.snapshot?.auth_scheme || 'auth redacted')}</Tag>
          <Tag>timeout {Number(transportSnapshotResult.snapshot?.timeout_seconds || 0)}s</Tag>
          <Tag>classes {Array.isArray(transportSnapshotResult.snapshot?.expected_success_classes) ? transportSnapshotResult.snapshot.expected_success_classes.length : 0}</Tag>
          <Tag>{transportSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(transportSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {sendSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={sendSnapshotResult.provider_review_attempt_send_snapshot_written ? 'green' : sendSnapshotResult.recording_state === 'asset_missing' ? 'red' : sendSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            send snapshot {String(sendSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{sendSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{sendSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{sendSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{sendSnapshotResult.send_attempted ? 'send attempted' : 'no send'}</Tag>
          <Tag>{String(sendSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {retryBackoffSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={retryBackoffSnapshotResult.provider_review_attempt_retry_backoff_snapshot_written ? 'green' : retryBackoffSnapshotResult.recording_state === 'asset_missing' ? 'red' : retryBackoffSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            retry snapshot {String(retryBackoffSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{retryBackoffSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{retryBackoffSnapshotResult.recording_state === 'operation_approval_not_approved' ? 'attempt asset not checked' : retryBackoffSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{retryBackoffSnapshotResult.snapshot?.retry_backoff_metadata_ready ? 'retry metadata' : 'retry blocked'}</Tag>
          <Tag>{retryBackoffSnapshotResult.snapshot?.no_call_observed ? 'no-call observed' : 'no-call blocked'}</Tag>
          <Tag>classes {Array.isArray(retryBackoffSnapshotResult.snapshot?.retryable_status_classes) ? retryBackoffSnapshotResult.snapshot.retryable_status_classes.length : 0}</Tag>
          <Tag>attempts {Number(retryBackoffSnapshotResult.snapshot?.max_attempts || 0)}</Tag>
          <Tag>{retryBackoffSnapshotResult.snapshot?.retry_scheduled ? 'retry scheduled' : 'no retry scheduled'}</Tag>
          <Tag>{retryBackoffSnapshotResult.snapshot?.provider_error_code_included ? 'provider error included' : 'provider error suppressed'}</Tag>
          <Tag>{String(retryBackoffSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {responseSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={responseSnapshotResult.provider_review_attempt_response_snapshot_written ? 'green' : responseSnapshotResult.recording_state === 'asset_missing' ? 'red' : responseSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            response snapshot {String(responseSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{responseSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{responseSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{responseSnapshotResult.provider_response_received ? 'provider response received' : 'no provider response'}</Tag>
          <Tag>{responseSnapshotResult.response_recorded ? 'response recorded' : 'no response record'}</Tag>
          <Tag>{responseSnapshotResult.transaction_recorded ? 'transaction recorded' : 'no transaction'}</Tag>
          <Tag>{String(responseSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {resultRecordingSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={resultRecordingSnapshotResult.provider_review_attempt_result_recording_snapshot_written ? 'green' : resultRecordingSnapshotResult.recording_state === 'asset_missing' ? 'red' : resultRecordingSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            result snapshot {String(resultRecordingSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{resultRecordingSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{resultRecordingSnapshotResult.recording_state === 'operation_approval_not_approved' ? 'attempt asset not checked' : resultRecordingSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{resultRecordingSnapshotResult.snapshot?.result_recording_metadata_ready ? 'result metadata' : 'result blocked'}</Tag>
          <Tag>{resultRecordingSnapshotResult.snapshot?.no_call_observed ? 'no-call observed' : 'no-call blocked'}</Tag>
          <Tag>{resultRecordingSnapshotResult.result_recorded ? 'result recorded' : 'no result record'}</Tag>
          <Tag>{resultRecordingSnapshotResult.attempt_result_persisted ? 'attempt result persisted' : 'attempt result pending'}</Tag>
          <Tag>{resultRecordingSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(resultRecordingSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {providerCallBoundarySnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={providerCallBoundarySnapshotResult.provider_review_attempt_provider_call_boundary_snapshot_written ? 'green' : providerCallBoundarySnapshotResult.recording_state === 'asset_missing' ? 'red' : providerCallBoundarySnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            call boundary snapshot {String(providerCallBoundarySnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{providerCallBoundarySnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{providerCallBoundarySnapshotResult.recording_state === 'operation_approval_not_approved' ? 'attempt asset not checked' : providerCallBoundarySnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{providerCallBoundarySnapshotResult.snapshot?.provider_call_boundary_metadata_ready ? 'call boundary metadata' : 'call boundary blocked'}</Tag>
          <Tag>{providerCallBoundarySnapshotResult.snapshot?.no_call_observed ? 'no-call observed' : 'no-call blocked'}</Tag>
          <Tag>{providerCallBoundarySnapshotResult.provider_call_boundary_recorded ? 'call boundary recorded' : 'no call boundary'}</Tag>
          <Tag>{providerCallBoundarySnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(providerCallBoundarySnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {transactionSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={transactionSnapshotResult.provider_review_attempt_transaction_snapshot_written ? 'green' : transactionSnapshotResult.recording_state === 'asset_missing' ? 'red' : transactionSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            transaction snapshot {String(transactionSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{transactionSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{transactionSnapshotResult.recording_state === 'operation_approval_not_approved' ? 'attempt asset not checked' : transactionSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{transactionSnapshotResult.snapshot?.transaction_metadata_ready ? 'transaction metadata' : 'transaction blocked'}</Tag>
          <Tag>{transactionSnapshotResult.snapshot?.provider_call_boundary_metadata_ready ? 'call boundary metadata' : 'call boundary blocked'}</Tag>
          <Tag>{transactionSnapshotResult.transaction_recorded ? 'transaction recorded' : 'no transaction'}</Tag>
          <Tag>{transactionSnapshotResult.provider_call_boundary_recorded ? 'call boundary recorded' : 'no call boundary'}</Tag>
          <Tag>{transactionSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(transactionSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {liveExecutionReadinessSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={liveExecutionReadinessSnapshotResult.provider_review_attempt_live_execution_readiness_snapshot_written ? 'green' : liveExecutionReadinessSnapshotResult.recording_state === 'asset_missing' ? 'red' : liveExecutionReadinessSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            live readiness snapshot {String(liveExecutionReadinessSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{liveExecutionReadinessSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{liveExecutionReadinessSnapshotResult.recording_state === 'operation_approval_not_approved' ? 'attempt asset not checked' : liveExecutionReadinessSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{liveExecutionReadinessSnapshotResult.snapshot?.all_required_snapshot_evidence_observed ? 'snapshot evidence complete' : 'snapshot evidence missing'}</Tag>
          <Tag>{Number(liveExecutionReadinessSnapshotResult.snapshot?.observed_snapshot_count || 0)}/{Number(liveExecutionReadinessSnapshotResult.snapshot?.required_snapshot_count || 0)} evidence</Tag>
          <Tag>{liveExecutionReadinessSnapshotResult.future_live_execution_still_blocked ? 'future live blocked' : 'live boundary open'}</Tag>
          <Tag>{liveExecutionReadinessSnapshotResult.live_adapter_implemented ? 'live adapter implemented' : 'adapter unimplemented'}</Tag>
          <Tag>{liveExecutionReadinessSnapshotResult.mutation_armed ? 'mutation armed' : 'mutation off'}</Tag>
          <Tag>{liveExecutionReadinessSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(liveExecutionReadinessSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {currentLiveReadinessSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={currentLiveReadinessSnapshotResult.provider_review_attempt_live_execution_readiness_snapshot_written ? 'green' : currentLiveReadinessSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            current readiness {String(currentLiveReadinessSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{currentLiveReadinessSnapshotResult.provider_review_attempt_id ? `attempt ${String(currentLiveReadinessSnapshotResult.next_attempt_operation || currentLiveReadinessSnapshotResult.provider_review_attempt_id)}` : 'no current attempt'}</Tag>
          <Tag>{currentLiveReadinessSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{currentLiveReadinessSnapshotResult.future_live_execution_still_blocked ? 'future live blocked' : 'live boundary open'}</Tag>
          <Tag>{currentLiveReadinessSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
        </Space>
      ) : null}
      {currentLiveExecutionLaunchPlanResult ? (
        <Space size={4} wrap>
          <Tag color={currentLiveExecutionLaunchPlanResult.launch_plan_ready ? 'green' : 'gold'}>
            current launch {String(currentLiveExecutionLaunchPlanResult.launch_plan_state || 'unknown')}
          </Tag>
          <Tag>{currentLiveExecutionLaunchPlanResult.provider_review_attempt_id ? `attempt ${String(currentLiveExecutionLaunchPlanResult.next_attempt_operation || currentLiveExecutionLaunchPlanResult.provider_review_attempt_id)}` : 'no current attempt'}</Tag>
          <Tag>{currentLiveExecutionLaunchPlanResult.launch_plan?.launch_plan_metadata_ready ? 'launch metadata' : 'launch blocked'}</Tag>
          <Tag>{currentLiveExecutionLaunchPlanResult.live_execution_preflight_ready ? 'preflight ready' : 'preflight blocked'}</Tag>
          <Tag>{currentLiveExecutionLaunchPlanResult.provider_request_materialized ? 'request materialized' : 'request not materialized'}</Tag>
          <Tag>{currentLiveExecutionLaunchPlanResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{currentLiveExecutionLaunchPlanResult.transaction_recorded ? 'transaction recorded' : 'no transaction'}</Tag>
          <Tag>{String(currentLiveExecutionLaunchPlanResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {currentLiveExecutionGateResult ? (
        <Space size={4} wrap>
          <Tag color={currentLiveExecutionGateResult.execution_gate_ready ? 'green' : 'gold'}>
            live gate {String(currentLiveExecutionGateResult.execution_gate_state || 'unknown')}
          </Tag>
          <Tag>{currentLiveExecutionGateResult.provider_review_attempt_id ? `attempt ${String(currentLiveExecutionGateResult.next_attempt_operation || currentLiveExecutionGateResult.provider_review_attempt_id)}` : 'no current attempt'}</Tag>
          <Tag>{currentLiveExecutionGateResult.current_launch_plan_ready ? 'launch ready' : 'launch blocked'}</Tag>
          <Tag>{currentLiveExecutionGateResult.live_execution_preflight_ready ? 'preflight ready' : 'preflight blocked'}</Tag>
          <Tag>{currentLiveExecutionGateResult.execution_gate?.live_execution_gate_blocks_provider_send ? 'send blocked' : 'send gate open'}</Tag>
          <Tag>{currentLiveExecutionGateResult.execution_gate?.live_execution_gate_blocks_provider_write ? 'write blocked' : 'write gate open'}</Tag>
          <Tag>{currentLiveExecutionGateResult.provider_request_materialized ? 'request materialized' : 'request not materialized'}</Tag>
          <Tag>{currentLiveExecutionGateResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{currentLiveExecutionGateResult.transaction_recorded ? 'transaction recorded' : 'no transaction'}</Tag>
          <Tag>{String(currentLiveExecutionGateResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {liveExecutionGuardSnapshotResult ? (
        <Space size={4} wrap>
          <Tag color={liveExecutionGuardSnapshotResult.provider_review_attempt_live_execution_guard_written ? 'green' : liveExecutionGuardSnapshotResult.recording_state === 'asset_missing' ? 'red' : liveExecutionGuardSnapshotResult.recording_ready === false ? 'gold' : 'default'}>
            live guard snapshot {String(liveExecutionGuardSnapshotResult.recording_state || 'unknown')}
          </Tag>
          <Tag>{liveExecutionGuardSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
          <Tag>{liveExecutionGuardSnapshotResult.recording_state === 'operation_approval_not_approved' ? 'attempt asset not checked' : liveExecutionGuardSnapshotResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{liveExecutionGuardSnapshotResult.snapshot?.claim_recorded ? 'claim recorded' : 'claim missing'}</Tag>
          <Tag>{liveExecutionGuardSnapshotResult.snapshot?.attempt_live_execution_readiness_observed ? 'readiness observed' : 'readiness missing'}</Tag>
          <Tag>{liveExecutionGuardSnapshotResult.snapshot?.mutation_arming_review_observed ? 'arming observed' : 'arming missing'}</Tag>
          <Tag>{liveExecutionGuardSnapshotResult.future_live_execution_still_blocked ? 'future live blocked' : 'live boundary open'}</Tag>
          <Tag>{liveExecutionGuardSnapshotResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{String(liveExecutionGuardSnapshotResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {liveExecutionPreflightResult ? (
        <Space size={4} wrap>
          <Tag color={liveExecutionPreflightResult.preflight_ready ? 'green' : 'gold'}>
            live preflight {String(liveExecutionPreflightResult.preflight_state || 'unknown')}
          </Tag>
          <Tag>{liveExecutionPreflightResult.provider_review_attempt_asset_observed ? 'attempt asset observed' : 'attempt asset missing'}</Tag>
          <Tag>{liveExecutionPreflightResult.preflight?.claim_recorded ? 'claim recorded' : 'claim missing'}</Tag>
          <Tag>{liveExecutionPreflightResult.preflight?.live_execution_guard_observed ? 'guard observed' : 'guard missing'}</Tag>
          <Tag>{liveExecutionPreflightResult.live_adapter_implemented ? 'live adapter implemented' : 'adapter unimplemented'}</Tag>
          <Tag>{liveExecutionPreflightResult.mutation_armed ? 'mutation armed' : 'mutation off'}</Tag>
          <Tag>{liveExecutionPreflightResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{liveExecutionPreflightResult.future_live_execution_still_blocked ? 'future live blocked' : 'live boundary open'}</Tag>
          <Tag>{String(liveExecutionPreflightResult.provider_api_mutation || 'disabled')}</Tag>
        </Space>
      ) : null}
      {liveExecutionLaunchPlanResult ? (
        <Space size={4} wrap>
          <Tag color={liveExecutionLaunchPlanResult.launch_plan_ready ? 'green' : 'gold'}>
            live launch {String(liveExecutionLaunchPlanResult.launch_plan_state || 'unknown')}
          </Tag>
          <Tag>{liveExecutionLaunchPlanResult.launch_plan?.launch_plan_metadata_ready ? 'launch metadata' : 'launch blocked'}</Tag>
          <Tag>{liveExecutionLaunchPlanResult.live_execution_preflight_ready ? 'preflight ready' : 'preflight blocked'}</Tag>
          <Tag>{liveExecutionLaunchPlanResult.launch_plan?.live_adapter_plan_observed ? 'adapter plan observed' : 'adapter plan missing'}</Tag>
          <Tag>{liveExecutionLaunchPlanResult.launch_plan?.live_adapter_contract_plan_observed ? 'contract observed' : 'contract missing'}</Tag>
          <Tag>{liveExecutionLaunchPlanResult.launch_plan?.live_adapter_implemented ? 'live adapter implemented' : 'adapter unimplemented'}</Tag>
          <Tag>{liveExecutionLaunchPlanResult.provider_request_materialized ? 'request materialized' : 'request not materialized'}</Tag>
          <Tag>{liveExecutionLaunchPlanResult.provider_request_sent ? 'provider request sent' : 'no provider request'}</Tag>
          <Tag>{liveExecutionLaunchPlanResult.transaction_recorded ? 'transaction recorded' : 'no transaction'}</Tag>
          <Tag>{String(liveExecutionLaunchPlanResult.provider_api_mutation || 'disabled')}</Tag>
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
          <Tag>{attemptClaimPlan.claim_recorded === true ? 'claim recorded' : 'claim not recorded'}</Tag>
          {onClaimAttempt ? (
            <Tooltip title={!canClaimAttempt ? claimBlockedReason : ''}>
              <Button size="small" loading={claimLoading && claimOptimistic} disabled={!canClaimAttempt} onClick={() => onClaimAttempt(claimableAttemptID)}>
                Claim attempt
              </Button>
            </Tooltip>
          ) : null}
          {onRecordAttemptResult ? (
            <Tooltip title={!canRecordAttemptResult ? resultBlockedReason : ''}>
              <Button size="small" loading={resultLoading && resultOptimistic} disabled={!canRecordAttemptResult || resultLoading} onClick={() => onRecordAttemptResult(resultRecordableAttemptID, 'success')}>
                Record local success
              </Button>
            </Tooltip>
          ) : null}
          {onRecordAttemptResult ? (
            <Tooltip title={!canRecordAttemptResult ? resultBlockedReason : ''}>
              <Button size="small" disabled={!canRecordAttemptResult || resultLoading} onClick={() => onRecordAttemptResult(resultRecordableAttemptID, 'retryable')}>
                Record retryable
              </Button>
            </Tooltip>
          ) : null}
          {onRecordAttemptResult ? (
            <Tooltip title={!canRecordAttemptResult ? resultBlockedReason : ''}>
              <Button size="small" danger disabled={!canRecordAttemptResult || resultLoading} onClick={() => onRecordAttemptResult(resultRecordableAttemptID, 'failed')}>
                Record failure
              </Button>
            </Tooltip>
          ) : null}
          {onExecuteAttemptLive ? (
            <Tooltip title={!canExecuteAttemptLive ? liveExecuteBlockedReason : ''}>
              <Button size="small" type="primary" loading={liveExecuteLoading && liveExecuteOptimistic} disabled={!canExecuteAttemptLive} onClick={() => onExecuteAttemptLive(resultRecordableAttemptID)}>
                {t('providerReview.executeLive')}
              </Button>
            </Tooltip>
          ) : null}
          {liveExecutionResult?.execution_state ? (
            <Tag color={liveExecutionResult.execution_state === 'executed' ? 'green' : 'gold'}>
              {t('common.live')} {translatedValue(liveExecutionResult.execution_state, t)}
            </Tag>
          ) : null}
          {onCleanupAttemptLive ? (
            <Tooltip title={!canCleanupAttemptLive ? liveCleanupBlockedReason : ''}>
              <Button size="small" loading={liveCleanupLoading && liveCleanupOptimistic} disabled={!canCleanupAttemptLive} onClick={() => onCleanupAttemptLive(cleanupableAttemptID)}>
                {t('providerReview.cleanupLive')}
              </Button>
            </Tooltip>
          ) : null}
          {liveCleanupResult?.live_cleanup_state ? (
            <Tag color={liveCleanupResult.live_cleanup_success === true ? 'green' : 'gold'}>
              {translatedValue(liveCleanupResult.live_cleanup_state, t)}
            </Tag>
          ) : null}
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
              {onRecordAttemptSnapshot ? (
                <Button size="small" loading={snapshotLoading} disabled={!operation.id || snapshotLoading} onClick={() => onRecordAttemptSnapshot(String(operation.id || ''))}>
                  Record snapshot
                </Button>
              ) : null}
              {onRecordAttemptCredentialSnapshot ? (
                <Button size="small" loading={credentialSnapshotLoading} disabled={!operation.id || credentialSnapshotLoading} onClick={() => onRecordAttemptCredentialSnapshot(String(operation.id || ''))}>
                  Record credential
                </Button>
              ) : null}
              {onRecordAttemptRequestEnvelopeSnapshot ? (
                <Button size="small" loading={requestEnvelopeSnapshotLoading} disabled={!operation.id || requestEnvelopeSnapshotLoading} onClick={() => onRecordAttemptRequestEnvelopeSnapshot(String(operation.id || ''))}>
                  Record envelope
                </Button>
              ) : null}
              {onRecordAttemptIdempotencySnapshot ? (
                <Button size="small" loading={idempotencySnapshotLoading} disabled={!operation.id || idempotencySnapshotLoading} onClick={() => onRecordAttemptIdempotencySnapshot(String(operation.id || ''))}>
                  Record idempotency snapshot
                </Button>
              ) : null}
              {onRecordAttemptRequestValidationSnapshot ? (
                <Button size="small" loading={requestValidationSnapshotLoading} disabled={!operation.id || requestValidationSnapshotLoading} onClick={() => onRecordAttemptRequestValidationSnapshot(String(operation.id || ''))}>
                  Record validation snapshot
                </Button>
              ) : null}
              {onRecordAttemptRequestMaterializationSnapshot ? (
                <Button size="small" loading={requestMaterializationSnapshotLoading} disabled={!operation.id || requestMaterializationSnapshotLoading} onClick={() => onRecordAttemptRequestMaterializationSnapshot(String(operation.id || ''))}>
                  Record materialization snapshot
                </Button>
              ) : null}
              {onRecordAttemptBranchPolicySnapshot ? (
                <Button size="small" loading={branchPolicySnapshotLoading} disabled={!operation.id || branchPolicySnapshotLoading} onClick={() => onRecordAttemptBranchPolicySnapshot(String(operation.id || ''))}>
                  Record branch policy
                </Button>
              ) : null}
              {onRecordAttemptRuntimeSnapshot ? (
                <Button size="small" loading={runtimeSnapshotLoading} disabled={!operation.id || runtimeSnapshotLoading} onClick={() => onRecordAttemptRuntimeSnapshot(String(operation.id || ''))}>
                  Record runtime
                </Button>
              ) : null}
              {onRecordAttemptAdapterRehearsalSnapshot ? (
                <Button size="small" loading={adapterRehearsalSnapshotLoading} disabled={!operation.id || adapterRehearsalSnapshotLoading} onClick={() => onRecordAttemptAdapterRehearsalSnapshot(String(operation.id || ''))}>
                  Record rehearsal
                </Button>
              ) : null}
              {onRecordAttemptAdapterBlueprintSnapshot ? (
                <Button size="small" loading={adapterBlueprintSnapshotLoading} disabled={!operation.id || adapterBlueprintSnapshotLoading} onClick={() => onRecordAttemptAdapterBlueprintSnapshot(String(operation.id || ''))}>
                  Record blueprint
                </Button>
              ) : null}
              {onRecordAttemptLiveAdapterContractSnapshot ? (
                <Button size="small" loading={liveAdapterContractSnapshotLoading} disabled={!operation.id || liveAdapterContractSnapshotLoading} onClick={() => onRecordAttemptLiveAdapterContractSnapshot(String(operation.id || ''))}>
                  Record contract
                </Button>
              ) : null}
              {onRecordAttemptInvocationSnapshot ? (
                <Button size="small" loading={invocationSnapshotLoading} disabled={!operation.id || invocationSnapshotLoading} onClick={() => onRecordAttemptInvocationSnapshot(String(operation.id || ''))}>
                  Record invocation
                </Button>
              ) : null}
              {onRecordAttemptExecutionLockSnapshot ? (
                <Button size="small" loading={executionLockSnapshotLoading} disabled={!operation.id || executionLockSnapshotLoading} onClick={() => onRecordAttemptExecutionLockSnapshot(String(operation.id || ''))}>
                  Record lock
                </Button>
              ) : null}
              {onRecordAttemptActivationSnapshot ? (
                <Button size="small" loading={activationSnapshotLoading} disabled={!operation.id || activationSnapshotLoading} onClick={() => onRecordAttemptActivationSnapshot(String(operation.id || ''))}>
                  Record activation
                </Button>
              ) : null}
              {onRecordAttemptTransportSnapshot ? (
                <Button size="small" loading={transportSnapshotLoading} disabled={!operation.id || transportSnapshotLoading} onClick={() => onRecordAttemptTransportSnapshot(String(operation.id || ''))}>
                  Record transport
                </Button>
              ) : null}
              {onRecordAttemptSendSnapshot ? (
                <Button size="small" loading={sendSnapshotLoading} disabled={!operation.id || sendSnapshotLoading} onClick={() => onRecordAttemptSendSnapshot(String(operation.id || ''))}>
                  Record send
                </Button>
              ) : null}
              {onRecordAttemptRetryBackoffSnapshot ? (
                <Button size="small" loading={retryBackoffSnapshotLoading} disabled={!operation.id || retryBackoffSnapshotLoading} onClick={() => onRecordAttemptRetryBackoffSnapshot(String(operation.id || ''))}>
                  Record retry
                </Button>
              ) : null}
              {onRecordAttemptResponseSnapshot ? (
                <Button size="small" loading={responseSnapshotLoading} disabled={!operation.id || responseSnapshotLoading} onClick={() => onRecordAttemptResponseSnapshot(String(operation.id || ''))}>
                  Record response
                </Button>
              ) : null}
              {onRecordAttemptResultRecordingSnapshot ? (
                <Button size="small" loading={resultRecordingSnapshotLoading} disabled={!operation.id || resultRecordingSnapshotLoading} onClick={() => onRecordAttemptResultRecordingSnapshot(String(operation.id || ''))}>
                  Record result
                </Button>
              ) : null}
              {onRecordAttemptProviderCallBoundarySnapshot ? (
                <Button size="small" loading={providerCallBoundarySnapshotLoading} disabled={!operation.id || providerCallBoundarySnapshotLoading} onClick={() => onRecordAttemptProviderCallBoundarySnapshot(String(operation.id || ''))}>
                  Record boundary
                </Button>
              ) : null}
              {onRecordAttemptTransactionSnapshot ? (
                <Button size="small" loading={transactionSnapshotLoading} disabled={!operation.id || transactionSnapshotLoading} onClick={() => onRecordAttemptTransactionSnapshot(String(operation.id || ''))}>
                  Record transaction
                </Button>
              ) : null}
              {onRecordAttemptLiveExecutionReadinessSnapshot ? (
                <Button size="small" loading={liveExecutionReadinessSnapshotLoading} disabled={!operation.id || liveExecutionReadinessSnapshotLoading} onClick={() => onRecordAttemptLiveExecutionReadinessSnapshot(String(operation.id || ''))}>
                  Record live readiness
                </Button>
              ) : null}
              {onRecordAttemptLiveExecutionGuardSnapshot ? (
                <Button size="small" loading={liveExecutionGuardSnapshotLoading} disabled={!operation.id || liveExecutionGuardSnapshotLoading} onClick={() => onRecordAttemptLiveExecutionGuardSnapshot(String(operation.id || ''))}>
                  Record live guard
                </Button>
              ) : null}
              {onCheckAttemptLiveExecutionPreflight ? (
                <Button size="small" loading={liveExecutionPreflightLoading} disabled={!operation.id || liveExecutionPreflightLoading} onClick={() => onCheckAttemptLiveExecutionPreflight(String(operation.id || ''))}>
                  Check live preflight
                </Button>
              ) : null}
              {onCheckAttemptLiveExecutionLaunchPlan ? (
                <Button size="small" loading={liveExecutionLaunchPlanLoading} disabled={!operation.id || liveExecutionLaunchPlanLoading} onClick={() => onCheckAttemptLiveExecutionLaunchPlan(String(operation.id || ''))}>
                  Check live launch
                </Button>
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

function ProviderAccounts() {
  const { t } = useI18n();
  const accounts = useLoad(() => api('/api/provider-accounts'), []);
  const credentials = useLoad(() => api('/api/connection-credentials'), []);
  const credentialRows = credentials.data?.items || [];
  const providerCredentialOptions = credentialRows.filter((row: AnyRow) => row.kind === 'provider_token').map((row: AnyRow) => ({ value: row.id, label: `${row.name || row.id} · ${row.secret_configured ? t('common.configured') : t('common.missing')}` }));
  const tokenRotationSummary = accounts.data?.token_rotation_summary || {};
  const tokenRotationPlan = accounts.data?.token_rotation_plan || {};
  const tokenRotationPlanByID = providerAutoRotationPlanByID(tokenRotationPlan);
  const [open, setOpen] = useState(false);
  const [credentialOpen, setCredentialOpen] = useState(false);
  const [checkingID, setCheckingID] = useState('');
  const [rotatingID, setRotatingID] = useState('');
  const [rotateForm] = Form.useForm();
  async function createAccount(values: AnyRow) {
    await api('/api/provider-accounts', {
      method: 'POST',
      body: JSON.stringify({
        ...values,
        enabled: values.enabled !== false && values.enabled !== 'false',
        metadata: parseJSONField(values.metadata_json)
      })
    });
    message.success(t('provider.accountCreated'));
    accounts.reload();
  }
  async function createConnectionCredential(values: AnyRow) {
    await api('/api/connection-credentials', {
      method: 'POST',
      body: JSON.stringify({
        name: values.name,
        kind: 'provider_token',
        secret_value: values.secret_value,
        public_value: values.public_value,
        metadata: {}
      })
    });
    message.success(t('form.createConnectionCredential'));
    credentials.reload();
  }
  async function checkAccount(id: string) {
    setCheckingID(id);
    try {
      const res = await api(`/api/provider-accounts/${id}/check`, { method: 'POST' });
      message[res.check?.status === 'ok' ? 'success' : 'warning'](res.check?.message || t('provider.checkCompleted'));
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
    message.success(t('provider.tokenEnvRotated'));
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
      const count = Number(result.rotated_count || 0);
      message.success(t('provider.rotatedCount').replace('{count}', String(count)).replace('{plural}', count === 1 ? '' : 's'));
    } catch (err: any) {
      message.error(err?.message || t('provider.tokenRotationFailed'));
    } finally {
      accounts.reload();
    }
  }
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="Provider Accounts" onCreate={() => setOpen(true)} />
      <Space>
        <Button onClick={() => setCredentialOpen(true)}>{t('form.createConnectionCredential')}</Button>
      </Space>
      <Space wrap>
        <Tag>{tokenRotationSummary.total || 0} {t('provider.accounts')}</Tag>
        {providerTokenRotationSummaryTags(tokenRotationSummary, t).map((item) => <Tag key={item.key} color={item.color}>{item.label}</Tag>)}
        <Typography.Text type={tokenRotationSummary.action_required ? 'danger' : 'secondary'}>{tokenRotationSummary.next_action || t('provider.noAccounts')}</Typography.Text>
      </Space>
      <Space wrap>
        {providerAutoRotationPlanTags(tokenRotationPlan, t).map((item) => <Tag key={item.key} color={item.color}>{item.label}</Tag>)}
        <Typography.Text type={tokenRotationPlan.blocked ? 'danger' : 'secondary'}>{tokenRotationPlan.next_action || t('provider.noAutoPlan')}</Typography.Text>
        <Button size="small" onClick={executeReadyTokenRotations} disabled={!Number(tokenRotationPlan.ready || 0)}>{t('provider.executeReadyRotations')}</Button>
      </Space>
      <Table<AnyRow> rowKey="id" dataSource={accounts.data?.items || []} pagination={{ pageSize: 10 }} columns={[
        { title: t('common.name'), dataIndex: 'name' },
        { title: t('common.provider'), render: (_, row) => translatedValue(row.provider_type, t) },
        { title: t('provider.apiBase'), dataIndex: 'api_base_url' },
        { title: t('provider.owner'), dataIndex: 'default_owner' },
        { title: t('provider.visibility'), render: (_, row) => translatedValue(row.visibility, t) },
        { title: t('provider.tokenEnv'), dataIndex: 'masked_token_env' },
        { title: t('common.credential'), render: (_, row) => row.credential_name ? <Tag color={row.credential_configured ? 'green' : 'gold'}>{row.credential_name}</Tag> : <Tag>{t('common.unbound')}</Tag> },
        {
          title: t('provider.rotation'),
          render: (_, row) => {
            const rotation = providerTokenRotationSummary(row, t);
            return (
              <Space direction="vertical" size={2}>
                <Tag color={rotation.color}>{rotation.label}</Tag>
                {rotation.detail ? <Typography.Text type="secondary">{rotation.detail}</Typography.Text> : null}
              </Space>
            );
          }
        },
        {
          title: t('provider.autoRotation'),
          render: (_, row) => {
            const rotation = providerAutoRotationStatus(row, tokenRotationPlanByID, t);
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
          title: t('provider.check'),
          render: (_, row) => {
            const check = row.metadata?.provider_check || {};
            const ok = check.status === 'ok';
            return (
              <Space direction="vertical" size={2}>
                <Tag color={ok ? 'green' : check.status ? 'red' : 'default'}>{check.status ? translatedValue(check.status, t) : t('provider.unchecked')}</Tag>
                {check.actor ? <Typography.Text type="secondary">{check.actor}</Typography.Text> : null}
                {check.http_status ? <Typography.Text type="secondary">{t('provider.httpStatus')} {check.http_status}</Typography.Text> : null}
                {check.message ? <Typography.Text type="secondary">{check.message}</Typography.Text> : null}
              </Space>
            );
          }
	        },
	        { title: t('common.status'), render: (_, row) => <Tag color={row.enabled ? 'green' : 'default'}>{translatedValue(row.enabled ? 'enabled' : 'disabled', t)}</Tag> },
	        { title: t('common.updated'), dataIndex: 'updated_at' },
	        { title: t('common.action'), render: (_, row) => <Space><Button size="small" onClick={() => checkAccount(row.id)} loading={checkingID === row.id}>{t('provider.check')}</Button><Button size="small" onClick={() => openRotateToken(row)}>{t('provider.rotate')}</Button></Space> }
	      ]} />
	      <CreateModal
	        title="Create connection credential"
        open={credentialOpen}
        setOpen={setCredentialOpen}
	        fields={[{ name: 'name', helpKey: 'help.name' }, 'secret_value', 'public_value']}
	        initialValues={{ kind: 'provider_token' }}
	        onSubmit={createConnectionCredential}
	      />
	      <CreateModal
	        title="Create provider account"
        open={open}
        setOpen={setOpen}
	        fields={['name', 'provider_type', 'api_base_url', 'web_base_url', { name: 'token_env', required: false }, { name: 'credential_id', input: 'select', optionItems: providerCredentialOptions, helpKey: 'help.credential_id', required: false }, 'default_owner', 'visibility', 'enabled', 'metadata_json']}
        initialValues={{ provider_type: 'github', visibility: 'private', enabled: true }}
	        onSubmit={createAccount}
	      />
      <Modal title={t('form.rotateProviderTokenEnv')} open={Boolean(rotatingID)} onCancel={() => setRotatingID('')} onOk={() => rotateForm.submit()} destroyOnHidden okText={t('common.ok')} cancelText={t('common.cancel')}>
        <Form form={rotateForm} layout="vertical" onFinish={rotateTokenEnv}>
          <Form.Item name="token_env" label={fieldLabel('token_env', t)} rules={[{ required: true, message: t('provider.tokenEnvRequired') }]}>
            <Input />
          </Form.Item>
          <Form.Item name="reason" label={fieldLabel('reason', t)}>
            <Input />
          </Form.Item>
        </Form>
      </Modal>
	    </Space>
	  );
	}

function AssetCenter() {
	const { t } = useI18n();
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
	const searchGraphSummary = assetGraphRankingSummary(searchGraphResult.data?.nodes || [], searchGraphResult.data?.edges || [], Boolean(searchGraphResult.data?.truncated), t);
	const dependencyColumns = [
		{ title: t('common.depth'), dataIndex: 'depth' },
		{ title: t('common.from'), dataIndex: 'from_asset_id' },
		{ title: t('asset.relationType'), render: (_: unknown, row: AnyRow) => <Tag color="geekblue">{row.relation_type}</Tag> },
		{ title: t('common.to'), dataIndex: 'to_asset_id' },
		{ title: t('common.path'), render: (_: unknown, row: AnyRow) => <Typography.Paragraph className="mono-pre">{row.path_text}</Typography.Paragraph> }
	];
	const dependencyRowKey = (row: AnyRow) => `${row.id}:${row.depth}:${row.current_asset_id}:${String(row.path_text || '').length}`;
	const dependencyAlert = (result: { data: AnyRow | null; error: string }) => (
		<>
			{result.error && <Alert showIcon type="error" message={result.error} />}
			{result.data?.truncated && <Alert showIcon type="warning" message={t('asset.pathTruncated')} />}
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
			message.warning(t('asset.viewNameRequired'));
			return;
		}
		try {
			const view = await api('/api/asset-graph-views', { method: 'POST', body: JSON.stringify({ name, filters: currentAssetViewFilters() }) });
			message.success(t('asset.viewSaved'));
			setAssetViewID(view.id);
			assetViews.reload();
		} catch (err: any) {
			message.error(err.message || t('asset.viewSaveFailed'));
		}
	}
	async function updateAssetView() {
		if (!assetViewID) return;
		const name = assetViewName.trim();
		try {
			const view = await api(`/api/asset-graph-views/${assetViewID}`, { method: 'PATCH', body: JSON.stringify({ name, filters: currentAssetViewFilters() }) });
			message.success(t('asset.viewUpdated'));
			setAssetViewName(view.name || name);
			assetViews.reload();
		} catch (err: any) {
			message.error(err.message || t('asset.viewUpdateFailed'));
		}
	}
	function deleteAssetView() {
		if (!assetViewID) return;
		Modal.confirm({
			title: t('asset.deleteViewConfirm'),
			okText: t('common.remove'),
			cancelText: t('common.cancel'),
			okButtonProps: { danger: true },
			onOk: async () => {
				await api(`/api/asset-graph-views/${assetViewID}`, { method: 'DELETE' });
				message.success(t('asset.viewDeleted'));
				setAssetViewID(undefined);
				setAssetViewName('');
				assetViews.reload();
			}
		});
	}
	async function createAssetRelation(values: AnyRow) {
		await api('/api/asset-relations', { method: 'POST', body: JSON.stringify(values) });
		message.success(t('asset.relationSaved'));
		relationForm.resetFields();
		relations.reload();
		downstream.reload();
		upstream.reload();
	}
	function deleteAssetRelation(row: AnyRow) {
		Modal.confirm({
			title: t('asset.deleteRelationConfirm'),
			okText: t('common.remove'),
			cancelText: t('common.cancel'),
			okButtonProps: { danger: true },
			onOk: async () => {
				await api(`/api/asset-relations/${row.id}`, { method: 'DELETE' });
				message.success(t('asset.relationDeleted'));
				relations.reload();
				downstream.reload();
				upstream.reload();
			}
		});
	}
	const typeOptions = [
		'project',
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
	].map((value) => ({ value, label: translatedValue(value, t) }));
	return (
		<Space direction="vertical" size={16} className="full">
			<Typography.Title level={2}>{t('title.assets')}</Typography.Title>
			<Space wrap>
				<Select allowClear value={assetViewID} placeholder={t('asset.savedGraphView')} style={{ width: 220 }} onChange={(value) => applyAssetView(value)} options={(assetViews.data?.items || []).map((row: AnyRow) => ({ value: row.id, label: row.name }))} />
				<Input placeholder={t('asset.viewName')} value={assetViewName} onChange={(event) => setAssetViewName(event.target.value)} style={{ width: 180 }} />
				<Button onClick={saveAssetView}>{t('asset.saveView')}</Button>
				<Button disabled={!assetViewID} onClick={updateAssetView}>{t('asset.updateView')}</Button>
				<Button danger disabled={!assetViewID} onClick={deleteAssetView}>{t('asset.deleteView')}</Button>
			</Space>
			<div className="selectorRow">
				<EntitySelect label={t('common.project')} rows={projectRows} value={projectPick.selectedID} onChange={projectPick.setSelectedID} />
				<Space direction="vertical" size={4} className="selector">
					<Typography.Text type="secondary">{t('asset.assetType')}</Typography.Text>
					<Select allowClear value={assetType} onChange={setAssetType} options={typeOptions} placeholder={t('asset.allAssets')} />
				</Space>
				<Space direction="vertical" size={4} className="selector">
					<Typography.Text type="secondary">{t('asset.search')}</Typography.Text>
					<Input allowClear value={assetSearch} onChange={(event) => setAssetSearch(event.target.value)} placeholder={t('asset.searchPlaceholder')} />
				</Space>
			</div>
			<Table<AnyRow> rowKey="id" dataSource={assetRows} pagination={{ pageSize: 10 }} onRow={(row) => ({ onClick: () => assetPick.setSelectedID(row.id) })} rowClassName={(row) => row.id === assetPick.selectedID ? 'selectedRow' : ''} columns={[
				{ title: t('common.name'), dataIndex: 'name' },
				{ title: t('common.type'), render: (_, row) => <Tag>{translatedValue(row.asset_type, t)}</Tag> },
				{ title: t('common.status'), render: (_, row) => <Tag color={row.status === 'failed' || row.status === 'OutOfSync' ? 'red' : row.status === 'completed' || row.status === 'Synced' || row.status === 'active' ? 'green' : 'blue'}>{translatedValue(row.status || 'unknown', t)}</Tag> },
				{ title: t('common.source'), dataIndex: 'source' },
				{ title: t('common.externalId'), dataIndex: 'external_id' },
				{ title: t('common.updated'), dataIndex: 'updated_at' }
			]} />
			<Typography.Title level={3}>{t('asset.searchGraph')}</Typography.Title>
			{searchGraphResult.error && <Alert showIcon type="error" message={searchGraphResult.error} />}
			{searchGraphResult.data?.truncated && <Alert showIcon type="warning" message={t('asset.graphTruncated')} />}
			<Space wrap>
				<Tag>{searchGraphSummary.nodesLabel}</Tag>
				<Tag>{searchGraphSummary.edgesLabel}</Tag>
				<Tag color="geekblue">{searchGraphSummary.topLabel}</Tag>
			</Space>
			<AssetRelationGraph graph={searchGraph} />
			<Typography.Title level={3}>{t('asset.relations')}</Typography.Title>
			<Form form={relationForm} layout="inline" onFinish={createAssetRelation}>
				<Form.Item name="from_asset_id" rules={[{ required: true, message: t('common.required') }]}>
					<Select showSearch placeholder={t('asset.fromAsset')} style={{ width: 260 }} optionFilterProp="label" options={rowOptions(assetRows, 'name')} />
				</Form.Item>
				<Form.Item name="relation_type" rules={[{ required: true, message: t('common.required') }]}>
					<Input placeholder={t('asset.relationType')} style={{ width: 180 }} />
				</Form.Item>
				<Form.Item name="to_asset_id" rules={[{ required: true, message: t('common.required') }]}>
					<Select showSearch placeholder={t('asset.toAsset')} style={{ width: 260 }} optionFilterProp="label" options={rowOptions(assetRows, 'name')} />
				</Form.Item>
				<Button htmlType="submit" type="primary">{t('asset.saveRelation')}</Button>
			</Form>
				<Table<AnyRow> rowKey="id" dataSource={relations.data?.items || []} pagination={{ pageSize: 8 }} columns={[
				{ title: t('common.from'), dataIndex: 'from_asset_id' },
				{ title: t('asset.relationType'), render: (_, row) => <Tag color="geekblue">{row.relation_type}</Tag> },
				{ title: t('common.to'), dataIndex: 'to_asset_id' },
				{ title: t('common.source'), render: (_, row) => translatedValue(row.metadata?.source || row.source || 'system', t) },
				{ title: t('common.created'), dataIndex: 'created_at' },
				{ title: t('common.action'), render: (_, row) => row.metadata?.source === 'manual' ? <Button size="small" danger onClick={() => deleteAssetRelation(row)}>{t('common.remove')}</Button> : null }
			]} />
			<Typography.Title level={3}>{t('asset.statusHistory')}</Typography.Title>
			<Table<AnyRow> rowKey="id" loading={statusSnapshots.loading} dataSource={statusSnapshots.data?.items || []} pagination={{ pageSize: 5 }} columns={[
				{ title: t('common.status'), render: (_, row) => <Tag color={row.status === 'failed' || row.status === 'OutOfSync' ? 'red' : row.status === 'completed' || row.status === 'Synced' || row.status === 'active' ? 'green' : 'blue'}>{translatedValue(row.status || 'unknown', t)}</Tag> },
				{ title: t('common.health'), render: (_, row) => <Tag>{translatedValue(row.health, t)}</Tag> },
				{ title: t('common.summary'), render: (_, row) => shortText(row.summary, 90) },
				{ title: t('common.collected'), dataIndex: 'collected_at' }
			]} />
			<Typography.Title level={3}>{t('asset.selectedGraph')}</Typography.Title>
			<AssetRelationGraph graph={selectedGraph} />
			<Typography.Title level={3}>{t('asset.paths')}</Typography.Title>
			<Tabs items={[
				{
					key: 'downstream',
					label: t('asset.downstream'),
					children: <Space direction="vertical" size={12} className="full">{dependencyAlert(downstream)}<Table<AnyRow> rowKey={dependencyRowKey} loading={downstream.loading} dataSource={downstream.data?.items || []} pagination={{ pageSize: 6 }} columns={dependencyColumns} /></Space>
				},
				{
					key: 'upstream',
					label: t('asset.upstream'),
					children: <Space direction="vertical" size={12} className="full">{dependencyAlert(upstream)}<Table<AnyRow> rowKey={dependencyRowKey} loading={upstream.loading} dataSource={upstream.data?.items || []} pagination={{ pageSize: 6 }} columns={dependencyColumns} /></Space>
				}
			]} />
		</Space>
	);
}

function AssetRelationGraph({ graph }: { graph: AnyRow }) {
  const { t } = useI18n();
  const nodes: AnyRow[] = graph.nodes || [];
  const edges: AnyRow[] = graph.edges || [];
  const byID = new Map(nodes.map((node) => [node.id, node]));
  if (!nodes.length) {
    return <div className="assetGraphEmpty"><Typography.Text type="secondary">{t('asset.selectAsset')}</Typography.Text></div>;
  }
  return (
    <div className="assetGraph">
      <svg viewBox={`0 0 800 ${graph.height || 260}`} role="img" aria-label={t('asset.graphAria')}>
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
	const { t } = useI18n();
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
  const [configRefsRefreshing, setConfigRefsRefreshing] = useState(false);
  const [configWorkflowRequesting, setConfigWorkflowRequesting] = useState(false);
  const [configPromotionSnapshotRecording, setConfigPromotionSnapshotRecording] = useState(false);
  const [configRefSnapshotRecording, setConfigRefSnapshotRecording] = useState(false);
  const [configWorkflowResult, setConfigWorkflowResult] = useState<AnyRow>();
  const [configPromotionSnapshotResult, setConfigPromotionSnapshotResult] = useState<AnyRow>();
  const [configRefSnapshotResult, setConfigRefSnapshotResult] = useState<AnyRow>();
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
      message.success(t('config.repoInitialized'));
      repos.reload();
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
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
      message.success(result.approval ? t('config.workflowApprovalRequested') : t('config.workflowAuditQueued'));
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setConfigWorkflowRequesting(false);
    }
  }
  async function refreshConfigRefs() {
    if (!configRepo || configRefsRefreshing) return;
    setConfigRefsRefreshing(true);
    try {
      const result = await api(`/api/git-repositories/${configRepo.id}/config-scaffold/refresh-refs`, { method: 'POST', body: '{}' });
      configScaffold.reload();
      projectRemotes.reload();
      message.success(result.idempotent ? t('config.refsRefreshAlreadyQueued') : t('config.refsRefreshQueued'));
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setConfigRefsRefreshing(false);
    }
  }
  async function recordConfigPromotionSnapshot() {
    if (!configRepo || configPromotionSnapshotRecording) return;
    setConfigPromotionSnapshotRecording(true);
    try {
      const result = await api(`/api/git-repositories/${configRepo.id}/config-scaffold/promotion-snapshot`, { method: 'POST', body: '{}' });
      setConfigPromotionSnapshotResult(result);
      configScaffold.reload();
      if (result.recording_ready === false) {
        message.warning(result.message || t('config.promotionSnapshotNotReady'));
      } else {
        message.success(result.promotion_snapshot_written ? t('config.promotionSnapshotRecorded') : t('config.promotionSnapshotCurrent'));
      }
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setConfigPromotionSnapshotRecording(false);
    }
  }
  async function recordConfigRefSnapshot() {
    if (!configRepo || configRefSnapshotRecording) return;
    setConfigRefSnapshotRecording(true);
    try {
      const result = await api(`/api/git-repositories/${configRepo.id}/config-scaffold/ref-refresh-snapshot`, { method: 'POST', body: '{}' });
      setConfigRefSnapshotResult(result);
      configScaffold.reload();
      if (result.recording_ready === false) {
        message.warning(result.message || t('config.refsSnapshotNotReady'));
      } else {
        message.success(result.ref_refresh_snapshot_written ? t('config.refsSnapshotRecorded') : t('config.refsSnapshotCurrent'));
      }
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setConfigRefSnapshotRecording(false);
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
      message.success(t('version.validationPreviewReady'));
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
      message.success(t('version.refreshQueued'));
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
      message.success(safeResult.project_version_metadata_written ? t('config.pinWritten') : t('config.pinAlreadyRecorded'));
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
      message.success(safeResult.validation_snapshot_written ? t('version.snapshotRecorded') : t('version.snapshotCurrent'));
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
                message.warning(snapshot.message || t('version.refreshSnapshotNotReady'));
              } else {
                message.success(snapshot.validation_snapshot_written ? t('version.refreshSnapshotRecorded') : t('version.refreshSnapshotCurrent'));
              }
            } catch (error: any) {
              message.warning(error.message || t('version.refreshSnapshotFailed'));
            }
          } else if (rerunStatus === 'refresh_failed') {
            message.error(t('version.refreshFailed'));
          } else if (rerunStatus === 'refresh_canceled') {
            message.warning(t('version.refreshCanceled'));
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
          message.warning(t('version.refreshStillRunning'));
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
          error: error.message || t('common.requestFailed'),
          last_checked_at: new Date().toISOString()
        });
        message.error(error.message || t('version.validationRefreshFailed'));
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
      <Typography.Title level={2}>{t('title.projectDetail')}</Typography.Title>
      <EntitySelect label={t('common.project')} rows={projectRows} value={projectPick.selectedID} onChange={projectPick.setSelectedID} />
      {!project && <Alert type="info" showIcon message={t('project.createFirst')} />}
      {project && (
        <>
          <Card title={project.name} extra={<Button onClick={() => api(`/api/projects/${project.id}/context/generate`, { method: 'POST' }).then(() => message.success(t('project.contextGenerated')))}>{t('project.generateContext')}</Button>}>
            <Typography.Paragraph>{project.description || t('project.noDescription')}</Typography.Paragraph>
          </Card>
          <div className="toolbar">
            <Typography.Title level={2}>{t('title.gitRepositories')}</Typography.Title>
            <Space>
              <Button onClick={initializeConfigRepo} disabled={Boolean(configRepo) || repos.loading} loading={configInitializing} icon={<SettingOutlined />}>{configRepo ? t('config.configReady') : t('config.initConfig')}</Button>
              <Button type="primary" onClick={() => setRepoOpen(true)}>{t('common.create')}</Button>
            </Space>
          </div>
          <Table<AnyRow> rowKey="id" dataSource={repos.data?.items || []} pagination={false} columns={[
            { title: t('common.name'), dataIndex: 'name' },
            { title: t('common.key'), dataIndex: 'repo_key' },
            { title: t('common.role'), render: (_, row) => <Tag color={row.repo_role === 'config' ? 'geekblue' : 'default'}>{translatedValue(row.repo_role || 'code', t)}</Tag> },
            { title: t('common.status'), render: (_, row) => <Tag>{translatedValue(row.status || 'active', t)}</Tag> },
            { title: t('field.default_branch'), dataIndex: 'default_branch' }
          ]} />
          {configScaffold.data && (
            <Card title={t('config.scaffoldTitle')} loading={configScaffold.loading}>
              <Space direction="vertical" size={8} className="full">
                <Space wrap>
                  <Tag color={configScaffold.data.scaffold_state === 'ready' ? 'green' : 'red'}>{configScaffold.data.scaffold_state || 'blocked'}</Tag>
                  <Tag>{configScaffold.data.file_count || 0} {t('config.files')}</Tag>
                  <Tag>{configScaffold.data.remote_count || 0} {t('config.remotes')}</Tag>
                  <Tag>{configScaffold.data.git_write_performed ? t('config.gitWrite') : t('config.noGitWrite')}</Tag>
                  <Tag>{configScaffold.data.external_call_made ? t('config.externalCall') : t('config.noExternalCall')}</Tag>
                  <Tag>{configScaffold.data.file_content_included ? t('config.contentIncluded') : t('config.pathsOnly')}</Tag>
                  {configScaffold.data.git_commit_plan ? <Tag color={configScaffold.data.git_commit_plan.plan_state === 'planned' ? 'blue' : 'red'}>{t('config.commit')} {configScaffold.data.git_commit_plan.plan_state}</Tag> : null}
                  {configScaffold.data.git_commit_plan ? <Tag color={configScaffold.data.git_commit_plan.operation_request_enabled ? 'blue' : 'default'}>{configScaffold.data.git_commit_plan.operation_request_enabled ? t('config.requestEnabled') : t('config.requestBlocked')}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.approval_request_plan ? <Tag color="gold">{t('config.approval')} {configScaffold.data.git_commit_plan.approval_request_plan.request_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.approval_request_plan ? <Tag>{configScaffold.data.git_commit_plan.approval_request_plan.metadata_ready ? t('config.approvalMetadataReady') : t('config.approvalMetadataMissing')}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.approval_request_plan ? <Tag>{configScaffold.data.git_commit_plan.approval_request_plan.request_ready ? t('config.requestReady') : t('config.requestDisabled')}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.workspace_execution_plan ? <Tag color="gold">{t('config.workspace')} {configScaffold.data.git_commit_plan.workspace_execution_plan.workspace_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan ? <Tag>{configScaffold.data.git_commit_plan.git_commit_created ? t('config.commitCreated') : t('config.noCommit')}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.remote_review_plan ? <Tag color={configScaffold.data.git_commit_plan.remote_review_plan.review_state === 'planned' ? 'gold' : 'red'}>{t('config.review')} {configScaffold.data.git_commit_plan.remote_review_plan.review_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.remote_review_plan ? <Tag>{configScaffold.data.git_commit_plan.remote_review_plan.provider_review_created ? t('config.reviewCreated') : t('config.noReview')}</Tag> : null}
                  {configScaffold.data.git_commit_plan ? <Tag>{configScaffold.data.git_commit_plan.live_commit_validation_performed ? t('config.liveValidation') : t('config.noLiveValidation')}</Tag> : null}
                  {configScaffold.data.config_ref_refresh_evidence ? <Tag color={configScaffold.data.config_ref_refresh_evidence.refresh_state === 'recorded' ? 'green' : configScaffold.data.config_ref_refresh_evidence.refresh_state === 'waiting_for_worker' ? 'blue' : 'default'}>{t('config.refs')} {configScaffold.data.config_ref_refresh_evidence.refresh_state || 'not_requested'}</Tag> : null}
                  {configScaffold.data.config_ref_refresh_evidence ? <Tag>{configScaffold.data.config_ref_refresh_evidence.git_fetch_performed ? t('config.fetchObserved') : t('config.noFetchEvidence')}</Tag> : null}
                  {configScaffold.data.git_commit_plan ? <Tag color={configScaffold.data.git_commit_plan.project_version_pin_observed ? 'green' : 'orange'}>{configScaffold.data.git_commit_plan.project_version_pin_observed ? t('config.pinObserved') : t('config.noPinEvidence')}</Tag> : null}
                  {configScaffold.data.git_commit_plan ? <Tag color={configScaffold.data.git_commit_plan.live_commit_validation_observed ? 'green' : 'orange'}>{configScaffold.data.git_commit_plan.live_commit_validation_observed ? t('config.validationObserved') : t('config.noValidationEvidence')}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.project_version_pin_plan ? <Tag color="gold">{t('config.pin')} {configScaffold.data.git_commit_plan.project_version_pin_plan.pin_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.project_version_pin_plan?.pin_write_preflight_plan ? <Tag color={configScaffold.data.git_commit_plan.project_version_pin_plan.pin_write_preflight_plan.pin_write_ready_for_review ? 'gold' : configScaffold.data.git_commit_plan.project_version_pin_plan.pin_write_preflight_plan.preflight_state === 'observed' ? 'green' : 'default'}>{t('config.pinPreflight')} {configScaffold.data.git_commit_plan.project_version_pin_plan.pin_write_preflight_plan.preflight_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.project_version_pin_plan?.pin_write_preflight_plan ? <Tag>{configScaffold.data.git_commit_plan.project_version_pin_plan.pin_write_preflight_plan.project_version_update_enabled ? t('config.pinWriteEnabled') : t('config.noPinWrite')}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.result_recording_plan ? <Tag color="gold">{t('config.result')} {configScaffold.data.git_commit_plan.result_recording_plan.result_recording_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.result_recording_plan ? <Tag>{configScaffold.data.git_commit_plan.result_recording_plan.result_written ? t('config.resultRecorded') : t('config.noResultRecord')}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.result_recording_plan ? <Tag>{configScaffold.data.git_commit_plan.result_recording_plan.project_version_pin_written ? t('config.pinRecorded') : t('config.noPinRecord')}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.promotion_readiness_plan ? <Tag color={configScaffold.data.git_commit_plan.promotion_readiness_plan.promotion_ready ? 'green' : 'orange'}>{t('config.promotion')} {configScaffold.data.git_commit_plan.promotion_readiness_plan.promotion_state || 'blocked'}</Tag> : null}
                  {configScaffold.data.git_commit_plan?.promotion_readiness_plan ? <Tag>{configScaffold.data.git_commit_plan.promotion_readiness_plan.live_git_workflow_enabled ? t('config.livePromotion') : t('config.noLivePromotion')}</Tag> : null}
                  {configPromotionSnapshotResult ? <Tag color={configPromotionSnapshotResult.promotion_snapshot_written ? 'green' : configPromotionSnapshotResult.recording_state === 'asset_missing' ? 'red' : configPromotionSnapshotResult.recording_ready ? 'gold' : 'default'}>{t('config.promotionSnapshot')} {configPromotionSnapshotResult.recording_state || 'pending'}</Tag> : null}
                  {configPromotionSnapshotResult ? <Tag>{configPromotionSnapshotResult.asset_status_snapshot_written ? t('config.assetStatusWritten') : t('config.assetStatusUnchanged')}</Tag> : null}
                  {configRefSnapshotResult ? <Tag color={configRefSnapshotResult.ref_refresh_snapshot_written ? 'green' : configRefSnapshotResult.recording_state === 'asset_missing' ? 'red' : configRefSnapshotResult.recording_ready ? 'gold' : 'default'}>{t('config.refsSnapshot')} {configRefSnapshotResult.recording_state || 'pending'}</Tag> : null}
                  {configRefSnapshotResult ? <Tag>{configRefSnapshotResult.asset_status_snapshot_written ? t('config.assetStatusWritten') : t('config.assetStatusUnchanged')}</Tag> : null}
                  {configScaffold.data.git_workflow_audit_evidence && (configScaffold.data.git_workflow_audit_evidence.operation_count || 0) > 0 ? <Tag color={configWorkflowAuditEvidenceColor(configScaffold.data.git_workflow_audit_evidence.evidence_state)}>{t('config.workflowAudit')} {configScaffold.data.git_workflow_audit_evidence.evidence_state || 'unknown'}</Tag> : null}
                  {configScaffold.data.git_workflow_audit_evidence && (configScaffold.data.git_workflow_audit_evidence.operation_count || 0) > 0 ? <Tag>{configScaffold.data.git_workflow_audit_evidence.operation_count || 0} {t('config.auditOps')}</Tag> : null}
                  {configScaffold.data.git_workflow_audit_evidence && (configScaffold.data.git_workflow_audit_evidence.operation_count || 0) > 0 ? <Tag>{configScaffold.data.git_workflow_audit_evidence.operation_log_count || 0} {t('config.auditLogs')}</Tag> : null}
                  {configScaffold.data.git_workflow_audit_evidence && (configScaffold.data.git_workflow_audit_evidence.active_count || 0) > 0 ? <Tag color="blue">{configScaffold.data.git_workflow_audit_evidence.active_count || 0} {t('config.activeAudits')}</Tag> : null}
                  {configScaffold.data.git_workflow_audit_evidence && (configScaffold.data.git_workflow_audit_evidence.failed_count || 0) > 0 ? <Tag color="red">{configScaffold.data.git_workflow_audit_evidence.failed_count || 0} {t('config.failedAudits')}</Tag> : null}
                  {configScaffold.data.config_ref_refresh_evidence && (configScaffold.data.config_ref_refresh_evidence.operation_count || 0) > 0 ? <Tag>{configScaffold.data.config_ref_refresh_evidence.operation_count || 0} {t('config.refOps')}</Tag> : null}
                  {configScaffold.data.config_ref_refresh_evidence && (configScaffold.data.config_ref_refresh_evidence.active_count || 0) > 0 ? <Tag color="blue">{configScaffold.data.config_ref_refresh_evidence.active_count || 0} {t('config.activeRefs')}</Tag> : null}
                  {configScaffold.data.config_ref_refresh_evidence && (configScaffold.data.config_ref_refresh_evidence.failed_count || 0) > 0 ? <Tag color="red">{configScaffold.data.config_ref_refresh_evidence.failed_count || 0} {t('config.failedRefs')}</Tag> : null}
                  {configScaffold.data.project_version_pin_evidence ? <Tag color={configScaffold.data.project_version_pin_evidence.pin_state === 'recorded' ? 'green' : 'default'}>{t('config.pinEvidence')} {configScaffold.data.project_version_pin_evidence.pin_state || 'not_recorded'}</Tag> : null}
                  {configScaffold.data.project_version_pin_evidence ? <Tag>{configScaffold.data.project_version_pin_evidence.pinned_version_count || 0} {t('config.pinnedVersions')}</Tag> : null}
                  {configScaffold.data.project_version_pin_evidence ? <Tag>{configScaffold.data.project_version_pin_evidence.validated_version_count || 0} {t('config.validatedVersions')}</Tag> : null}
                  {configScaffold.data.git_commit_plan ? <Tag>{configScaffold.data.git_commit_plan.steps?.length || 0} {t('config.commitSteps')}</Tag> : null}
                </Space>
                <Space wrap>
                  <Tooltip title={t('config.refreshRefsHelp')}><Button size="small" loading={configRefsRefreshing} disabled={!configScaffold.data.remote_count} onClick={refreshConfigRefs}>{t('config.refreshRefs')}</Button></Tooltip>
                  <Tooltip title={t('config.recordRefsSnapshotHelp')}><Button size="small" loading={configRefSnapshotRecording} disabled={configScaffold.data.config_ref_refresh_evidence?.refresh_state !== 'recorded'} onClick={recordConfigRefSnapshot}>{t('config.recordRefsSnapshot')}</Button></Tooltip>
                  <Tooltip title={t('config.requestWorkflowAuditHelp')}><Button size="small" type="primary" loading={configWorkflowRequesting} disabled={!configScaffold.data.git_commit_plan?.operation_request_enabled} onClick={requestConfigGitWorkflow}>{t('config.requestWorkflowAudit')}</Button></Tooltip>
                  <Tooltip title={t('config.recordPromotionSnapshotHelp')}><Button size="small" loading={configPromotionSnapshotRecording} disabled={!configScaffold.data.git_commit_plan?.promotion_readiness_plan?.promotion_ready} onClick={recordConfigPromotionSnapshot}>{t('config.recordPromotionSnapshot')}</Button></Tooltip>
                  {configWorkflowResult?.approval ? <Tag color="gold">{t('config.approvalRequested')}</Tag> : null}
                  {configWorkflowResult?.operation ? <Tag color="blue">{t('config.operationQueued')}</Tag> : null}
                  {configWorkflowResult?.operation_request_result ? <Tag>{configWorkflowResult.operation_request_result.git_write_performed ? t('config.gitWrite') : t('config.noGitWrite')}</Tag> : null}
                  {configWorkflowResult?.operation_request_result ? <Tag>{configWorkflowResult.operation_request_result.sanitized_result_expected ? t('config.sanitizedResultExpected') : t('config.resultBlocked')}</Tag> : null}
                  {configPinResult ? <Tag color={configPinResult.project_version_metadata_written ? 'green' : 'default'}>{t('config.pin')} {configPinResult.pin_state || 'recorded'}</Tag> : null}
                  {configPinResult ? <Tag>{configPinResult.project_version_metadata_written ? t('config.metadataWritten') : t('config.noMetadataWrite')}</Tag> : null}
                  {configPinResult ? <Tag>{configPinResult.external_call_made ? t('config.externalCall') : t('config.noExternalCall')}</Tag> : null}
                  {configPinResult ? <Tag>{configPinResult.commit_sha_included ? t('config.shaIncluded') : t('config.shaHidden')}</Tag> : null}
                </Space>
                {Array.isArray(configScaffold.data.blocked_reasons) && configScaffold.data.blocked_reasons.length > 0 && (
                  <Alert showIcon type="warning" message={`${t('config.blockedReasons')}: ${configScaffold.data.blocked_reasons.join(', ')}`} />
                )}
                {Array.isArray(configScaffold.data.git_commit_plan?.blocked_reasons) && configScaffold.data.git_commit_plan.blocked_reasons.length > 0 && (
                  <Alert showIcon type="warning" message={`${t('config.blockedReasons')}: ${configScaffold.data.git_commit_plan.blocked_reasons.join(', ')}`} />
                )}
                <Table<AnyRow>
                  size="small"
                  rowKey="path"
                  dataSource={configScaffold.data.files || []}
                  pagination={false}
                  columns={[
                    { title: t('field.path'), dataIndex: 'path' },
                    { title: t('field.environment'), render: (_, row) => <Tag>{row.environment || 'all'}</Tag> },
                    { title: t('field.purpose'), dataIndex: 'purpose' }
                  ]}
                />
              </Space>
            </Card>
          )}
          <CreateModal title="Create repository" open={repoOpen} setOpen={setRepoOpen} fields={['name', 'repo_key', 'display_name', 'repo_role', 'description', 'default_branch']} onSubmit={(v) => api(`/api/projects/${project.id}/git-repositories`, { method: 'POST', body: JSON.stringify(v) }).then(repos.reload)} />
          <Toolbar title="Versions" onCreate={() => setVersionOpen(true)} />
          {versionValidation && (
            <Card title={t('title.versionValidation')}>
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
                      {t('common.refresh')}
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
                    {t('config.recordValidationSnapshot')}
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
              { title: t('field.version'), dataIndex: 'version' },
              { title: t('common.source'), render: (_, row) => <Tag>{translatedValue(row.source || 'manual', t)}</Tag> },
              { title: t('title.gitRepositories'), render: (_, row) => Array.isArray(row.metadata?.repositories) ? row.metadata.repositories.length : 0 },
              { title: t('common.created'), render: (_, row) => shortText(row.created_at, 24) },
              {
                title: t('common.actions'),
                render: (_, row) => (
                  <Space>
                    <Button size="small" loading={validatingVersionID === row.id} onClick={() => validateVersion(row)}>{t('version.validate')}</Button>
                    <Button size="small" loading={rerunningValidationID === row.id} onClick={() => requestValidationRerun(row)}>{t('version.backgroundRerun')}</Button>
                    <Button size="small" disabled={!configRepo} loading={configPinningVersionID === row.id} onClick={() => pinConfigCommit(row)}>{t('version.pinConfig')}</Button>
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
  const { t } = useI18n();
  const [form] = Form.useForm();
  const [submitting, setSubmitting] = useState(false);
  async function submit(values: AnyRow) {
    setSubmitting(true);
    try {
      await onSubmit(values);
      form.resetFields();
      setOpen(false);
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setSubmitting(false);
    }
  }
  return (
    <Modal title={t('form.createVersionManifest')} open={open} onCancel={() => setOpen(false)} onOk={() => form.submit()} confirmLoading={submitting} okButtonProps={{ disabled: submitting || !repos.length || !remotes.length }} width={820} destroyOnHidden okText={t('common.ok')} cancelText={t('common.cancel')}>
      <Form form={form} layout="vertical" onFinish={submit} initialValues={{ source: 'manual', repositories: [{}] }}>
        <Space className="full" size={12}>
          <Form.Item name="version" label={t('field.version')} rules={[{ required: true, message: t('common.required') }]} className="selector">
            <Input placeholder="v0.1.0" />
          </Form.Item>
          <Form.Item name="source" label={t('field.source')} className="selector">
            <Input placeholder="manual" />
          </Form.Item>
        </Space>
        {(!repos.length || !remotes.length) && <Alert type="warning" showIcon message={t('version.addRepoRemoteFirst')} />}
        <Form.List name="repositories">
          {(fields, { add, remove }) => (
            <Space direction="vertical" size={8} className="full">
              {fields.map((field) => (
                <Card key={field.key} size="small" title={`${t('version.repositoryItem')} ${field.name + 1}`} extra={fields.length > 1 ? <Button size="small" danger onClick={() => remove(field.name)}>{t('common.remove')}</Button> : null}>
                  <div className="manifestGrid">
                    <Form.Item {...field} name={[field.name, 'repository_id']} label={t('field.repository')} rules={[{ required: true, message: t('common.required') }]}>
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
                          <Form.Item {...field} name={[field.name, 'remote_id']} label={t('field.remote')} rules={[{ required: true, message: t('common.required') }]}>
                            <Select options={remoteOptions.map((remote) => ({ value: remote.id, label: `${remote.repository_key || 'repo'} / ${remote.remote_key || remote.name}` }))} />
                          </Form.Item>
                        );
                      }}
                    </Form.Item>
                    <Form.Item {...field} name={[field.name, 'tag']} label={t('field.tag')}>
                      <Input placeholder="v0.1.0" />
                    </Form.Item>
                    <Form.Item {...field} name={[field.name, 'commit_sha']} label={t('field.commit_sha')}>
                      <Input placeholder="abc123" />
                    </Form.Item>
                    <Form.Item noStyle shouldUpdate>
                      {({ getFieldValue }) => {
                        const repositoryID = getFieldValue(['repositories', field.name, 'repository_id']);
                        const repo = repos.find((item) => item.id === repositoryID);
                        if ((repo?.repo_role || '') !== 'config') return null;
                        return (
                          <Form.Item {...field} name={[field.name, 'config_commit_sha']} label={t('field.config_commit_sha')}>
                            <Input placeholder="config repository commit" />
                          </Form.Item>
                        );
                      }}
                    </Form.Item>
                    <Form.Item {...field} name={[field.name, 'github_action_run_id']} label={t('field.github_action_run_id')}>
                      <Input placeholder="123456" />
                    </Form.Item>
                    <Form.Item {...field} name={[field.name, 'argo_revision']} label={t('field.argo_revision')}>
                      <Input placeholder="optional" />
                    </Form.Item>
                  </div>
                </Card>
              ))}
              <Button onClick={() => add({})} disabled={!repos.length || !remotes.length}>{t('version.addRepositoryItem')}</Button>
            </Space>
          )}
        </Form.List>
        <Form.Item name="metadata_json" label={t('field.metadata_json')}>
          <Input.TextArea autoSize={{ minRows: 3, maxRows: 8 }} placeholder='{"notes":"release candidate"}' />
        </Form.Item>
      </Form>
    </Modal>
  );
}

function GitRemotes() {
  const { t } = useI18n();
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
  const credentials = useLoad(() => project ? api(`/api/projects/${project.id}/connection-credentials`) : Promise.resolve({ items: [] }), [project?.id]);
  const gitCredentialKinds = ['ssh_key', 'git_https_password', 'git_https_token'];
  const gitCredentialOptions = (credentials.data?.items || []).filter((row: AnyRow) => gitCredentialKinds.includes(row.kind)).map((row: AnyRow) => ({ value: row.id, label: `${row.name || row.id} · ${t(`option.${row.kind}`)} · ${row.secret_configured ? t('common.configured') : t('common.missing')}` }));
  const sourcePick = useSelectedRow(remoteRows);
  const sourceRemote = sourcePick.selected;
  const targetPick = useSelectedRow(remoteRows.filter((row: AnyRow) => row.id !== sourcePick.selectedID));
  const actions = useLoad(() => sourcePick.selectedID ? api(`/api/git-remotes/${sourcePick.selectedID}/github-actions`) : Promise.resolve({ items: [] }), [sourcePick.selectedID]);
  const labels = useLoad(() => sourcePick.selectedID ? api(`/api/git-remotes/${sourcePick.selectedID}/github-labels`) : Promise.resolve({ items: [] }), [sourcePick.selectedID]);
  const actionsSummary = githubActionsSummary(actions.data?.items || [], t);
  const artifactsSummary = githubActionArtifactsSummary(actions.data?.items || []);
  const labelsSummary = githubLabelsSummary(labels.data?.items || []);
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
  const [recordingCallbackSnapshotID, setRecordingCallbackSnapshotID] = useState<string>();
  const [callbackSnapshotResults, setCallbackSnapshotResults] = useState<Record<string, AnyRow>>({});
  const [recordingTagSnapshotID, setRecordingTagSnapshotID] = useState<string>();
  const [recordingTagActionsSnapshotID, setRecordingTagActionsSnapshotID] = useState<string>();
  const [runningTagLookupID, setRunningTagLookupID] = useState<string>();
  const [refreshingTagActionsID, setRefreshingTagActionsID] = useState<string>();
  const [tagSnapshotResults, setTagSnapshotResults] = useState<Record<string, AnyRow>>({});
  const [tagActionsSnapshotResults, setTagActionsSnapshotResults] = useState<Record<string, AnyRow>>({});
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
      message.error(t('git.selectBranchOrTag'));
      return;
    }
    await api(`/api/git-repositories/${repo.id}/sync`, {
      method: 'POST',
      body: JSON.stringify({ source_remote_id: sourcePick.selectedID, target_remote_ids: [targetPick.selectedID], refs: { branches, tags } })
    });
    message.success(t('git.syncQueued'));
    runs.reload();
    remotes.reload();
  }
  async function createRepoSyncAsset(values: AnyRow) {
    if (!repo || !sourcePick.selectedID || !targetPick.selectedID) {
      message.error(t('git.selectRepoSourceTarget'));
      return;
    }
    const { branches, tags } = selectedRefs();
    if (!branches.length && !tags.length) {
      message.error(t('git.selectBranchOrTag'));
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
    message.success(t('git.syncAssetSaved'));
    syncAssets.reload();
  }
  async function createWebhookConnection(values: AnyRow) {
    if (!project || !sourcePick.selectedID) {
      message.error(t('git.selectProjectSource'));
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
      Modal.info({ title: t('git.webhookSecret'), content: <Typography.Text copyable>{created.secret_token_once}</Typography.Text> });
    } else {
      message.success(t('git.webhookConnectionCreated'));
    }
  }
  async function rotateWebhookSecret(id: string) {
    Modal.confirm({
      title: t('git.rotateWebhookSecretConfirm'),
      okText: t('git.rotateSecret'),
      onOk: async () => {
        const updated = await api(`/api/webhook-connections/${id}/rotate-secret`, { method: 'POST', body: '{}' });
        webhookConnections.reload();
        if (updated.secret_token_once) {
          Modal.info({ title: t('git.webhookSecretRotatedTitle'), content: <Typography.Text copyable>{updated.secret_token_once}</Typography.Text> });
        } else {
          message.success(t('git.webhookSecretRotated'));
        }
      }
    });
  }
  async function replayWebhookEvent(id: string) {
    await api(`/api/webhook-events/${id}/replay`, { method: 'POST', body: '{}' });
    message.success(t('git.webhookReplayQueued'));
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
      message.success(audit.id ? t('git.thresholdDecisionAuditRecorded') : t('git.thresholdAuditReviewed'));
      webhookConnections.reload();
    } finally {
      setRecordingThresholdAuditID(undefined);
    }
  }
  async function applyWebhookThresholdConfiguration(id: string) {
    setApplyingThresholdConfigID(id);
    try {
      const result = await api(`/api/webhook-connections/${id}/threshold-configuration`, { method: 'POST', body: '{}' });
      message.success(result.capacity_signals_recomputed ? t('git.thresholdConfigAppliedAndSignals') : result.threshold_configuration_written ? t('git.thresholdConfigApplied') : t('git.thresholdConfigReviewed'));
      webhookConnections.reload();
      syncAssetDetail.reload();
    } finally {
      setApplyingThresholdConfigID(undefined);
    }
  }
  async function recordWebhookProviderCallbackRehearsalSnapshot(id: string) {
    setRecordingCallbackSnapshotID(id);
    try {
      const result = await api(`/api/webhook-connections/${id}/provider-callback-rehearsal-snapshot`, {
        method: 'POST',
        body: JSON.stringify({ dry_run: false })
      });
      setCallbackSnapshotResults((current) => ({ ...current, [id]: result }));
      if (result.recording_state === 'asset_missing') {
        message.warning(t('git.callbackAssetMissing'));
      } else {
        message.success(result.provider_callback_rehearsal_snapshot_written ? t('git.callbackSnapshotRecorded') : result.recording_ready ? t('git.callbackSnapshotCurrent') : t('git.callbackSnapshotNotReady'));
      }
      webhookConnections.reload();
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setRecordingCallbackSnapshotID(undefined);
    }
  }
  async function runRepoSyncAsset(id: string) {
    await api(`/api/repo-sync-assets/${id}/run`, { method: 'POST', body: '{}' });
    message.success(t('git.syncAssetQueued'));
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
    message.success(t('git.syncAssetUpdated'));
    syncAssets.reload();
    syncAssetDetail.reload();
  }
  async function toggleRepoSyncAsset(id: string, enabled: boolean) {
    await api(`/api/repo-sync-assets/${id}`, { method: 'PATCH', body: JSON.stringify({ enabled }) });
    message.success(enabled ? t('git.syncAssetEnabled') : t('git.syncAssetDisabled'));
    syncAssets.reload();
    syncAssetDetail.reload();
  }
  async function archiveRepoSyncAsset(id: string) {
    Modal.confirm({
      title: t('git.archiveSyncAssetConfirm'),
      okText: t('common.archive'),
      okButtonProps: { danger: true },
      onOk: async () => {
        await api(`/api/repo-sync-assets/${id}/archive`, { method: 'POST', body: '{}' });
        message.success(t('git.syncAssetArchived'));
        setSyncAssetID(undefined);
        syncAssets.reload();
      }
    });
  }
  async function restoreRepoSyncAsset(id: string) {
    await api(`/api/repo-sync-assets/${id}/restore`, { method: 'POST', body: '{}' });
    message.success(t('git.syncAssetRestored'));
    syncAssets.reload();
    syncAssetDetail.reload();
  }
  async function rerunRepoSyncRun(id: string) {
    await api(`/api/repo-sync-runs/${id}/rerun`, { method: 'POST', body: '{}' });
    message.success(t('git.syncRerunQueued'));
    runs.reload();
    syncAssets.reload();
    syncAssetDetail.reload();
  }
  async function createRemote(values: AnyRow) {
    if (!repo) {
      message.error(t('git.selectRepositoryFirst'));
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
      message.error(t('git.selectRepositoryAndTarget'));
      return;
    }
    const result = await api(`/api/git-repositories/${repo.id}/tags`, {
      method: 'POST',
      body: JSON.stringify({ ...values, target_remote_ids: [targetPick.selectedID] })
    });
    message.success(result.approval ? t('config.approvalRequested') : t('git.tagQueued'));
    tagRuns.reload();
    remotes.reload();
  }
  async function runTagLookup(id: string) {
    setRunningTagLookupID(id);
    try {
      const result = await api(`/api/repo-tag-runs/${id}/live-lookup`, { method: 'POST', body: '{}' });
      message.success(result.idempotent ? t('git.lookupAlreadyQueued') : t('git.lookupQueued'));
      tagRuns.reload();
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setRunningTagLookupID(undefined);
    }
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
      message.success(result.tag_result_snapshot_written ? t('git.tagSnapshotRecorded') : t('git.tagSnapshotCurrent'));
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setRecordingTagSnapshotID(undefined);
    }
  }
  async function recordTagActionsRefreshSnapshot(id: string) {
    setRecordingTagActionsSnapshotID(id);
    try {
      const result = await api(`/api/repo-tag-runs/${id}/actions-refresh-snapshot`, {
        method: 'POST',
        body: JSON.stringify({ dry_run: false })
      });
      setTagActionsSnapshotResults((current) => ({ ...current, [id]: result }));
      tagRuns.reload();
      message.success(result.actions_refresh_snapshot_written ? t('git.actionsSnapshotRecorded') : result.recording_ready ? t('git.actionsSnapshotCurrent') : t('git.actionsSnapshotNotReady'));
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setRecordingTagActionsSnapshotID(undefined);
    }
  }
  async function refreshTagActions(id: string) {
    setRefreshingTagActionsID(id);
    try {
      const result = await api(`/api/repo-tag-runs/${id}/actions-refresh`, { method: 'POST', body: '{}' });
      message.success(result.idempotent ? t('git.actionsRefreshAlreadyQueued') : t('git.actionsRefreshQueued'));
      tagRuns.reload();
      actions.reload();
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setRefreshingTagActionsID(undefined);
    }
  }
  async function syncGitHubActions() {
    if (!sourcePick.selectedID) {
      message.error(t('git.selectGitHubRemote'));
      return;
    }
    try {
      await api(`/api/git-remotes/${sourcePick.selectedID}/github-actions/sync`, { method: 'POST', body: '{}' });
      message.success(t('git.actionsSyncQueued'));
      actions.reload();
    } catch (error: any) {
      message.error(error.message);
    }
  }
  async function syncGitHubLabels() {
    if (!sourcePick.selectedID) {
      message.error(t('git.selectGitHubRemote'));
      return;
    }
    try {
      await api(`/api/git-remotes/${sourcePick.selectedID}/github-labels/sync`, { method: 'POST', body: '{}' });
      message.success(t('git.labelsSyncQueued'));
      labels.reload();
    } catch (error: any) {
      message.error(error.message);
    }
  }
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="Git Remotes" onCreate={() => setOpen(true)} disabled={!repo} />
      <div className="selectorRow">
        <EntitySelect label={t('common.project')} rows={projectRows} value={projectPick.selectedID} onChange={projectPick.setSelectedID} />
        <EntitySelect label={t('common.repository')} rows={repoRows} value={repoPick.selectedID} onChange={repoPick.setSelectedID} />
        <EntitySelect label={t('git.sourceRemote')} rows={remoteRows} value={sourcePick.selectedID} onChange={sourcePick.setSelectedID} />
        <EntitySelect label={t('git.targetRemote')} rows={remoteRows.filter((row: AnyRow) => row.id !== sourcePick.selectedID)} value={targetPick.selectedID} onChange={targetPick.setSelectedID} />
      </div>
      <Space>
        <Button type="primary" onClick={runSync} disabled={!repo || !sourcePick.selectedID || !targetPick.selectedID}>{t('git.syncSelectedRemotes')}</Button>
        <Button onClick={() => setSyncAssetOpen(true)} disabled={!repo || !sourcePick.selectedID || !targetPick.selectedID}>{t('git.saveSyncAsset')}</Button>
        <Button onClick={() => setWebhookOpen(true)} disabled={!project || !sourcePick.selectedID}>{t('git.createWebhook')}</Button>
        <Button onClick={() => setTagOpen(true)} disabled={!repo || !targetPick.selectedID}>{t('git.createTag')}</Button>
        <Button onClick={syncGitHubActions} disabled={!sourcePick.selectedID}>{t('git.syncGitHubActions')}</Button>
        <Button onClick={syncGitHubLabels} disabled={!sourcePick.selectedID}>{t('git.syncGitHubLabels')}</Button>
      </Space>
      <div className="refsRow">
        <Space direction="vertical" size={4} className="selector">
          <Typography.Text type="secondary">{t('field.branches')}</Typography.Text>
          <Select mode="tags" value={branchRefs} onChange={setBranchRefs} tokenSeparators={[',']} placeholder="main" />
        </Space>
        <Space direction="vertical" size={4} className="selector">
          <Typography.Text type="secondary">{t('field.tags')}</Typography.Text>
          <Select mode="tags" value={tagRefs} onChange={setTagRefs} tokenSeparators={[',']} placeholder="v1.0.0" disabled={allTags} />
        </Space>
        <Checkbox checked={allTags} onChange={(event) => setAllTags(event.target.checked)}>{t('git.allTags')}</Checkbox>
      </div>
      <Table<AnyRow> rowKey="id" dataSource={remotes.data?.items || []} pagination={false} columns={[
        { title: t('common.name'), dataIndex: 'name' },
        { title: t('common.key'), dataIndex: 'remote_key' },
        { title: t('common.provider'), render: (_, row) => translatedValue(row.provider_type || row.kind, t) },
        { title: t('common.role'), render: (_, row) => translatedValue(row.remote_role, t) },
        { title: t('common.primary'), render: (_, row) => row.is_primary ? <Tag color="green">{t('value.primary')}</Tag> : null },
        { title: t('common.sync'), render: (_, row) => <Tag>{row.last_sync_status ? translatedValue(row.last_sync_status, t) : t('common.never')}</Tag> },
        { title: t('common.credential'), render: (_, row) => row.credential_name ? <Tag color={row.credential_configured ? 'green' : 'gold'}>{row.credential_name}</Tag> : <Tag>{t('common.unbound')}</Tag> },
        { title: t('common.url'), render: (_, row) => urlsText(row) }
      ]} />
      {!repo && <Alert type="info" showIcon message={t('git.createRepositoryFirst')} />}
      <CreateModal title="Create remote" open={open} setOpen={setOpen} descriptionKey="git.remoteModalDescription" fields={[{ name: 'name', helpKey: 'help.git_remote_name' }, 'remote_key', 'provider_type', 'remote_url', 'web_url', 'remote_role', { name: 'credential_id', input: 'select', optionItems: gitCredentialOptions, helpKey: 'help.credential_id', required: false }, 'urls', 'default_branch']} initialValues={{ provider_type: 'github', remote_role: 'mirror', default_branch: 'main' }} onSubmit={createRemote} />
      <CreateModal title="Create tag" open={tagOpen} setOpen={setTagOpen} descriptionKey="git.tagModalDescription" fields={['tag_name', 'target_sha', 'branch', 'tag_message']} initialValues={{ branch: repo?.default_branch || sourceRemote?.default_branch || 'main' }} onSubmit={createTag} />
      <CreateModal title="Save repo sync asset" open={syncAssetOpen} setOpen={setSyncAssetOpen} descriptionKey="git.syncAssetModalDescription" fields={[{ name: 'name', helpKey: 'help.repo_sync_name' }, 'trigger_mode', 'sync_mode', 'transport', 'driver']} initialValues={{ trigger_mode: 'manual_or_webhook', sync_mode: 'selected_refs', transport: 'ssh', driver: 'projectops_worker_git_ssh' }} onSubmit={createRepoSyncAsset} />
      <CreateModal title="Edit repo sync asset" open={syncAssetEditOpen} setOpen={setSyncAssetEditOpen} descriptionKey="git.syncAssetModalDescription" fields={[{ name: 'name', helpKey: 'help.repo_sync_name' }, 'trigger_mode', 'sync_mode', 'transport', 'driver', 'enabled']} onSubmit={updateRepoSyncAsset} />
      <CreateModal title="Create webhook" open={webhookOpen} setOpen={setWebhookOpen} descriptionKey="git.webhookModalDescription" fields={[{ name: 'name', helpKey: 'help.webhook_name' }, 'provider', 'secret_token']} initialValues={{ provider: 'gitea' }} onSubmit={createWebhookConnection} />
      <Tabs items={[
        { key: 'assets', label: t('title.syncAssets'), children: <Space direction="vertical" size={12} className="full">
          <Checkbox checked={includeArchivedSyncAssets} onChange={(event) => setIncludeArchivedSyncAssets(event.target.checked)}>{t('git.showArchived')}</Checkbox>
          <Table<AnyRow> rowKey="id" dataSource={syncAssets.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: t('common.name'), dataIndex: 'name' },
            { title: t('common.source'), dataIndex: 'source_remote_name' },
            { title: t('common.target'), dataIndex: 'target_remote_name' },
            { title: t('field.trigger_mode'), render: (_, row) => translatedValue(row.trigger_mode, t) },
            { title: t('common.status'), render: (_, row) => <Space><Tag color={row.last_sync_status === 'completed' ? 'green' : row.last_sync_status === 'failed' ? 'red' : row.last_sync_status === 'running' ? 'blue' : 'default'}>{row.last_sync_status ? translatedValue(row.last_sync_status, t) : t('common.never')}</Tag>{row.archived_at ? <Tag>{t('value.archived')}</Tag> : null}</Space> },
            { title: t('git.risk'), render: (_, row) => <Space size={4} wrap><Tag color={signalSeverityColor(row.risk_severity)}>{translatedValue(row.risk_severity || 'ok', t)}</Tag><Typography.Text>{shortText(row.risk_summary, 48)}</Typography.Text></Space> },
            { title: t('git.runs'), render: (_, row) => <Tag>{row.total_runs || 0}</Tag> },
            { title: t('git.success'), render: (_, row) => `${row.success_rate ?? 0}%` },
            { title: t('git.avgDuration'), render: (_, row) => secondsText(row.avg_duration_seconds) },
            { title: t('git.lastFailure'), render: (_, row) => shortText(row.last_failure_message || row.last_failure_at, 56) },
            { title: t('common.action'), render: (_, row) => <Space><Button size="small" onClick={() => setSyncAssetID(row.id)}>{t('common.view')}</Button><Button size="small" onClick={() => runRepoSyncAsset(row.id)} disabled={!row.enabled || Boolean(row.archived_at)}>{t('common.run')}</Button>{row.archived_at ? <Button size="small" onClick={() => restoreRepoSyncAsset(row.id)}>{t('common.restore')}</Button> : null}</Space> }
          ]} />
        </Space> },
        { key: 'webhooks', label: t('title.webhooks'), children: <Space direction="vertical" size={16} className="full">
          <Table<AnyRow> rowKey="id" dataSource={webhookConnections.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: t('common.name'), dataIndex: 'name' },
            { title: t('common.provider'), render: (_, row) => translatedValue(row.provider, t) },
            { title: t('common.source'), dataIndex: 'source_remote_name' },
            { title: t('common.url'), render: (_, row) => <Typography.Text copyable>{row.webhook_url || row.webhook_path}</Typography.Text> },
            { title: t('common.delivery'), render: (_, row) => <Tag color={row.last_delivery_status === 'queued' ? 'green' : row.last_delivery_status === 'failed' || row.last_delivery_status === 'rejected' ? 'red' : 'default'}>{row.last_delivery_status ? translatedValue(row.last_delivery_status, t) : t('common.never')}</Tag> },
            { title: t('common.health'), render: (_, row) => <Space size={4} wrap><Tag color={signalSeverityColor(row.webhook_health)}>{translatedValue(row.webhook_health || 'unknown', t)}</Tag><Typography.Text>{shortText(row.webhook_summary, 48)}</Typography.Text></Space> },
            { title: t('git.rehearsal'), render: (_, row) => {
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
              const snapshotResult = callbackSnapshotResults[row.id];
              const callbackEvidence = readiness.callback_evidence || {};
              const replayProof = callbackEvidence.operator_replay_proof || providerPlan.operator_replay_proof || {};
              const status = readiness.status || 'unknown';
              return <Space size={4} wrap>
                <Tag color={status === 'ready' ? 'green' : status === 'blocked' ? 'red' : 'default'}>{translatedValue(status, t)}</Tag>
                <Tag color={providerPlan.plan_state === 'planned' ? 'gold' : 'red'}>{translatedValue(providerPlan.plan_state || 'blocked', t)}</Tag>
                <Tag color={publicEndpointPlan.public_origin_ready ? 'green' : 'red'}>{t(publicEndpointPlan.public_origin_ready ? 'value.public_origin' : 'value.no_public_origin')}</Tag>
                <Tag color={deliveryPlan.delivery_state === 'planned' ? 'gold' : 'red'}>{t(deliveryPlan.provider_test_delivery_sent ? 'value.test_delivered' : 'value.no_test_delivery')}</Tag>
                <Tag color={thresholdPlan.threshold_state === 'planned' ? 'gold' : 'red'}>{t(thresholdPlan.provider_pair_thresholds_tuned ? 'value.thresholds_tuned' : 'value.thresholds_pending')}</Tag>
                <Tag color={thresholdVolumeColor(thresholdPlan, thresholdVolume)}>{thresholdVolume.local_volume_observed ? `${t('value.volume')} ${translatedValue(thresholdPlan.threshold_review_state || 'observed', t)}` : t('value.volume_pending')}</Tag>
                {metricsComparison.mode ? <Tag color={metricsComparison.comparison_ready_for_review ? 'gold' : metricsComparison.comparison_state === 'needs_failure_review' ? 'red' : 'default'}>{t('value.metrics')} {translatedValue(metricsComparison.comparison_state || 'blocked', t)}</Tag> : null}
                {metricsComparison.mode ? <Tag>{t(metricsComparison.provider_metrics_fetched ? 'value.provider_metrics' : 'value.no_provider_metrics')}</Tag> : null}
                {thresholdConfig.mode ? <Tag color={thresholdConfig.configuration_review_ready === true ? 'gold' : 'default'}>{thresholdConfig.configuration_review_ready === true ? t('value.config_review_ready') : `${t('value.config')} ${translatedValue(thresholdConfig.configuration_state || 'blocked', t)}`}</Tag> : null}
                {thresholdConfig.threshold_configuration_written ? <Tag color="green">{thresholdConfig.threshold_configuration_count || 0} {t('value.configs')}</Tag> : null}
                {thresholdConfig.capacity_signals_recomputed ? <Tag color="green">{t('value.capacity_recomputed')}</Tag> : null}
                {thresholdAudit.mode ? <Tag color={thresholdAudit.decision_ready_for_review ? 'gold' : thresholdAudit.decision_state === 'needs_failure_review' ? 'red' : 'default'}>{t('value.threshold_audit')} {translatedValue(thresholdAudit.decision_state || 'blocked', t)}</Tag> : null}
                {thresholdAudit.mode ? <Tag>{t(thresholdAudit.audit_insert_enabled ? 'value.audit_write_enabled' : 'value.no_audit_write')}</Tag> : null}
                {thresholdAudit.threshold_decision_audit_count ? <Tag color="green">{thresholdAudit.threshold_decision_audit_count} {t('value.audit_rows')}</Tag> : null}
                <Tag>{t(providerPlan.external_call_made ? 'value.provider_call' : 'value.no_provider_call')}</Tag>
                <Tag>{t(resultPlan.result_written ? 'value.result_recorded' : 'value.no_result_record')}</Tag>
                {snapshotResult ? <Tag color={snapshotResult.provider_callback_rehearsal_snapshot_written ? 'green' : snapshotResult.recording_state === 'asset_missing' ? 'red' : snapshotResult.recording_ready ? 'gold' : 'default'}>{t('value.snapshot')} {translatedValue(snapshotResult.recording_state || 'unknown', t)}</Tag> : null}
                <Tag color={replayProof.proof_state === 'recorded' ? 'green' : replayProof.proof_state === 'failed' ? 'red' : replayProof.operator_replay_observed ? 'gold' : 'default'}>{replayProof.operator_replay_observed ? `${t('value.replay_proof')} ${translatedValue(replayProof.proof_state || 'observed', t)}` : t('value.replay_proof_pending')}</Tag>
                {callbackEvidence.delivery_count_7d ? <Tag color={callbackEvidenceColor(callbackEvidence.evidence_state)}>{t('value.callback')} {translatedValue(callbackEvidence.evidence_state || 'observed', t)}</Tag> : null}
                {callbackEvidence.delivery_count_7d ? <Tag>{callbackEvidence.delivery_count_7d} {t('value.deliveries')}</Tag> : null}
                {callbackEvidence.repo_sync_enqueue_observed ? <Tag color="green">{t('value.repo_sync_observed')}</Tag> : null}
                {callbackEvidence.failed_count_7d ? <Tag color="red">{callbackEvidence.failed_count_7d} {t('value.failed_callbacks')}</Tag> : null}
                <Typography.Text>{shortText(readiness.message, 56)}</Typography.Text>
              </Space>;
            } },
            { title: t('common.action'), render: (_, row) => {
              const thresholdPlan = row.callback_rehearsal?.provider_rehearsal_plan?.threshold_tuning_plan || {};
              const thresholdConfig = thresholdPlan.threshold_configuration_plan || {};
              const thresholdAudit = thresholdConfig.threshold_decision_audit_plan || {};
              const resultPlan = row.callback_rehearsal?.provider_rehearsal_plan?.result_recording_plan || {};
              return <Space size={4} wrap>
                <Button size="small" onClick={() => rotateWebhookSecret(row.id)}>{t('git.rotateSecret')}</Button>
                <Button
                  size="small"
                  onClick={() => recordWebhookProviderCallbackRehearsalSnapshot(row.id)}
                  disabled={!resultPlan.result_recording_ready}
                  loading={recordingCallbackSnapshotID === row.id}
                >
                  {t('git.recordCallbackSnapshot')}
                </Button>
                <Button
                  size="small"
                  onClick={() => recordWebhookThresholdDecisionAudit(row.id)}
                  disabled={!thresholdAudit.decision_ready_for_review || Boolean(thresholdAudit.threshold_decision_audit_count)}
                  loading={recordingThresholdAuditID === row.id}
                >
                  {thresholdAudit.threshold_decision_audit_count ? t('git.auditRecorded') : t('git.recordThresholdAudit')}
                </Button>
                <Button
                  size="small"
                  onClick={() => applyWebhookThresholdConfiguration(row.id)}
                  disabled={!thresholdConfig.configuration_write_enabled || Boolean(thresholdConfig.threshold_configuration_written)}
                  loading={applyingThresholdConfigID === row.id}
                >
                  {thresholdConfig.threshold_configuration_written ? t('git.configApplied') : t('git.applyThresholdConfig')}
                </Button>
              </Space>;
            } }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={webhookEvents.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: t('common.status'), render: (_, row) => <Tag color={row.status === 'queued' ? 'green' : row.status === 'failed' || row.status === 'rejected' ? 'red' : 'default'}>{translatedValue(row.status, t)}</Tag> },
            { title: t('common.event'), dataIndex: 'event_type' },
            { title: t('common.delivery'), dataIndex: 'delivery_id' },
            { title: t('common.received'), dataIndex: 'received_at' },
            { title: t('common.action'), render: (_, row) => row.event_type === 'push' ? <Button size="small" onClick={() => replayWebhookEvent(row.id)}>{t('git.replay')}</Button> : null }
          ]} />
        </Space> },
        { key: 'runs', label: t('title.syncRuns'), children: <Space direction="vertical" size={12} className="full">
          <Select allowClear value={runStatusFilter || undefined} placeholder={t('common.status')} style={{ width: 180 }} onChange={(value) => setRunStatusFilter(value || '')} options={['queued', 'running', 'completed', 'failed'].map((value) => ({ value, label: translatedValue(value, t) }))} />
          <Table<AnyRow> rowKey="id" dataSource={runs.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: t('common.status'), render: (_, row) => <Tag color={row.status === 'completed' ? 'green' : row.status === 'failed' ? 'red' : 'blue'}>{translatedValue(row.status, t)}</Tag> },
            { title: t('common.ref'), dataIndex: 'ref' },
            { title: t('common.source'), dataIndex: 'source_remote_id' },
            { title: t('common.target'), dataIndex: 'target_remote_id' },
            { title: t('common.created'), dataIndex: 'created_at' }
          ]} />
        </Space> },
        { key: 'tags', label: t('title.tagRuns'), children: <Table<AnyRow> rowKey="id" dataSource={tagRuns.data?.items || []} pagination={{ pageSize: 6 }} columns={[
          { title: t('common.status'), render: (_, row) => <Tag color={row.status === 'completed' ? 'green' : row.status === 'failed' ? 'red' : 'blue'}>{translatedValue(row.status, t)}</Tag> },
          { title: t('git.rehearsal'), render: (_, row) => {
            const plan = row.remote_rehearsal_plan || {};
            const liveResultPlan = plan.live_result_plan || {};
            const actionsRefreshPlan = plan.actions_refresh_plan || {};
            const resultPlan = plan.result_recording_plan || {};
            const lookupPreflight = plan.live_remote_lookup_preflight || liveResultPlan.live_remote_lookup_preflight || {};
            const tagResultEvidence = plan.tag_result_evidence || resultPlan.tag_result_evidence || {};
            const snapshotResult = tagSnapshotResults[row.id];
            const actionsSnapshotResult = tagActionsSnapshotResults[row.id];
            return <Space size={4} wrap>
              <Tag color={plan.rehearsal_state === 'observed' ? 'green' : plan.rehearsal_state === 'blocked' || plan.rehearsal_state === 'failed' ? 'red' : 'gold'}>{translatedValue(plan.rehearsal_state || 'planned', t)}</Tag>
              <Tag>{t(plan.live_remote_tag_success_observed ? 'value.remote_success' : 'value.no_remote_success')}</Tag>
              {lookupPreflight.mode ? <Tag color={lookupPreflight.lookup_state === 'observed' ? 'green' : lookupPreflight.lookup_state === 'failed' || lookupPreflight.lookup_state === 'blocked' ? 'red' : 'gold'}>{t('value.lookup')} {translatedValue(lookupPreflight.lookup_state || 'blocked', t)}</Tag> : null}
              {lookupPreflight.mode ? <Tag>{t(lookupPreflight.remote_tag_lookup_performed ? 'value.remote_lookup' : 'value.no_remote_lookup')}</Tag> : null}
              {row.lookup_operation_status ? <Tag color={row.lookup_operation_status === 'completed' ? 'green' : row.lookup_operation_status === 'failed' ? 'red' : 'blue'}>{t('value.operation')} {translatedValue(row.lookup_operation_status, t)}</Tag> : null}
              {lookupPreflight.remote_tag_found !== undefined ? <Tag color={lookupPreflight.remote_tag_found ? 'green' : 'default'}>{lookupPreflight.remote_tag_found ? `${t('value.found')} ${lookupPreflight.matched_count || 0}` : t('value.not_found')}</Tag> : null}
              <Tag color={liveResultPlan.live_result_state === 'planned' ? 'gold' : 'red'}>{liveResultPlan.repo_tag_run_result_written ? t('value.tag_result_saved') : liveResultPlan.live_result_state === 'failed' ? t('value.tag_result_failed') : t('value.tag_result_pending')}</Tag>
              <Tag color={actionsRefreshPlan.refresh_state === 'planned' ? 'gold' : 'red'}>{actionsRefreshPlan.github_actions_refresh_performed ? t('value.actions_refreshed') : actionsRefreshPlan.refresh_state === 'failed' ? t('value.actions_refresh_failed') : t('value.actions_refresh_pending')}</Tag>
              {actionsRefreshPlan.refresh_operation_status ? <Tag color={actionsRefreshPlan.refresh_operation_status === 'completed' ? 'green' : actionsRefreshPlan.refresh_operation_status === 'failed' ? 'red' : 'blue'}>{t('value.actions_operation')} {translatedValue(actionsRefreshPlan.refresh_operation_status, t)}</Tag> : null}
              {actionsRefreshPlan.github_action_runs_synced ? <Tag color="green">{actionsRefreshPlan.github_action_runs_synced_count || 0} {t('value.action_runs')}</Tag> : null}
              <Tag>{t(resultPlan.result_written ? 'value.result_recorded' : 'value.no_result_record')}</Tag>
              {resultPlan.result_recording_state ? <Tag color={tagResultEvidenceColor(resultPlan.result_recording_state)}>{t('value.recording')} {translatedValue(resultPlan.result_recording_state, t)}</Tag> : null}
              {snapshotResult ? <Tag color={snapshotResult.tag_result_snapshot_written ? 'green' : snapshotResult.recording_state === 'asset_missing' ? 'red' : 'default'}>{t('value.snapshot')} {translatedValue(snapshotResult.recording_state || 'unknown', t)}</Tag> : null}
              {actionsSnapshotResult ? <Tag color={actionsSnapshotResult.actions_refresh_snapshot_written ? 'green' : actionsSnapshotResult.recording_state === 'asset_missing' ? 'red' : actionsSnapshotResult.recording_ready ? 'gold' : 'default'}>{t('value.actions_snapshot')} {translatedValue(actionsSnapshotResult.recording_state || 'unknown', t)}</Tag> : null}
              {tagResultEvidence.waiting_for_worker ? <Tag color="blue">{t('value.tag_worker_pending')}</Tag> : null}
              {tagResultEvidence.live_remote_tag_failed_observed ? <Tag color="red">{t('value.tag_failure_observed')}</Tag> : null}
            </Space>;
          } },
          { title: t('field.tag_name'), dataIndex: 'tag_name' },
          { title: t('git.targetSha'), dataIndex: 'target_sha' },
          { title: t('common.target'), dataIndex: 'target_remote_id' },
          { title: t('common.created'), dataIndex: 'created_at' },
          { title: t('common.action'), render: (_, row) => {
            const resultPlan = row.remote_rehearsal_plan?.result_recording_plan || {};
            const lookupPreflight = row.remote_rehearsal_plan?.live_remote_lookup_preflight || {};
            return <Space size={6} wrap>
              <Button
                size="small"
                onClick={() => runTagLookup(row.id)}
                disabled={lookupPreflight.lookup_state === 'running' || !lookupPreflight.lookup_ready_for_review}
                loading={runningTagLookupID === row.id}
              >
                {t('git.runLookup')}
              </Button>
              <Button
                size="small"
                onClick={() => recordTagResultSnapshot(row.id)}
                disabled={!resultPlan.result_recording_ready}
                loading={recordingTagSnapshotID === row.id}
              >
                {t('git.recordResultSnapshot')}
              </Button>
            </Space>;
          } },
          { title: t('common.actions'), render: (_, row) => {
            const plan = row.remote_rehearsal_plan || {};
            const actionsPlan = plan.actions_refresh_plan || {};
            return <Space size={6} wrap>
              <Button
                size="small"
                onClick={() => refreshTagActions(row.id)}
                disabled={!actionsPlan.github_actions_sync_enabled || actionsPlan.refresh_state === 'running'}
                loading={refreshingTagActionsID === row.id}
              >
                {t('git.refreshActions')}
              </Button>
              <Button
                size="small"
                onClick={() => recordTagActionsRefreshSnapshot(row.id)}
                disabled={plan.rehearsal_state !== 'observed' || actionsPlan.refresh_state === 'failed'}
                loading={recordingTagActionsSnapshotID === row.id}
              >
                {t('git.recordActionsSnapshot')}
              </Button>
            </Space>;
          } }
        ]} /> },
        { key: 'actions', label: t('title.githubActions'), children: <Space direction="vertical" size={12} className="full">
          <Alert
            showIcon
            type={actionsSummary.failures > 0 ? 'warning' : actionsSummary.total > 0 ? 'success' : 'info'}
            message={actionsSummary.latestLabel}
            description={githubActionRemoteDescription(sourceRemote, repo, project, actionsSummary, t)}
          />
          <div className="metricGrid">
            <Card><Typography.Text type="secondary">{t('git.runs')}</Typography.Text><Typography.Title level={4}>{actionsSummary.total}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('git.success')}</Typography.Text><Typography.Title level={4}>{actionsSummary.successes}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('git.failures')}</Typography.Text><Typography.Title level={4}>{actionsSummary.failures}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('git.active')}</Typography.Text><Typography.Title level={4}>{actionsSummary.active}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('git.artifacts')}</Typography.Text><Typography.Title level={4}>{artifactsSummary.total}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('git.artifactSize')}</Typography.Text><Typography.Title level={4}>{bytesText(artifactsSummary.bytes)}</Typography.Title></Card>
          </div>
          {artifactsSummary.total > 0 ? (
            <Alert
              showIcon
              type={artifactsSummary.expired > 0 ? 'warning' : 'info'}
              message={`${artifactsSummary.active} ${t('git.activeArtifacts')} / ${artifactsSummary.expired} ${t('git.expiredArtifacts')}`}
              description={t('git.artifactDescription')}
            />
          ) : null}
          <Table<AnyRow> rowKey="id" dataSource={actions.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: t('git.workflow'), dataIndex: 'workflow_name' },
            { title: t('field.branch'), dataIndex: 'branch' },
            { title: t('common.status'), render: (_, row) => <Tag color={githubActionStatusColor(row)}>{translatedValue(row.conclusion || row.status, t)}</Tag> },
            { title: t('git.sha'), dataIndex: 'commit_sha' },
            { title: t('git.artifacts'), render: (_, row) => {
              const artifacts = Array.isArray(row.artifacts) ? row.artifacts : [];
              if (!artifacts.length) return <Typography.Text type="secondary">-</Typography.Text>;
              return <Space size={4} wrap>{artifacts.slice(0, 3).map((artifact: AnyRow) => (
                <Tooltip key={artifact.id || artifact.external_artifact_id} title={`${artifact.expired ? t('git.expiredArtifacts') : t('git.active')} · ${bytesText(artifact.size_in_bytes)} · ${t('git.expires')} ${artifact.expires_at || '-'}`}>
                  <Tag color={artifact.expired ? 'default' : 'blue'}>
                    {shortText(`${artifact.name || artifact.external_artifact_id} ${bytesText(artifact.size_in_bytes)}`, 32)}
                  </Tag>
                </Tooltip>
              ))}{artifacts.length > 3 ? <Tag>+{artifacts.length - 3}</Tag> : null}</Space>;
            } },
            { title: t('common.synced'), dataIndex: 'synced_at' }
          ]} />
        </Space> },
        { key: 'labels', label: t('git.labels'), children: <Space direction="vertical" size={12} className="full">
          <Alert
            showIcon
            type={labelsSummary.total > 0 ? 'success' : 'info'}
            message={`${labelsSummary.total} ${t('git.labels')}`}
            description={t('git.labelDescription')}
          />
          <div className="metricGrid">
            <Card><Typography.Text type="secondary">{t('git.labels')}</Typography.Text><Typography.Title level={4}>{labelsSummary.total}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('git.defaultLabels')}</Typography.Text><Typography.Title level={4}>{labelsSummary.defaults}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('git.customLabels')}</Typography.Text><Typography.Title level={4}>{labelsSummary.custom}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('common.synced')}</Typography.Text><Typography.Title level={4}>{labelsSummary.latestSyncedAt || '-'}</Typography.Title></Card>
          </div>
          <Table<AnyRow> rowKey="id" dataSource={labels.data?.items || []} pagination={{ pageSize: 10 }} columns={[
            { title: t('git.labels'), render: (_, row) => <Space size={6}>
              <span style={{ width: 12, height: 12, borderRadius: 2, background: githubLabelSwatchColor(row.color), display: 'inline-block', border: '1px solid rgba(0,0,0,0.12)' }} />
              <Typography.Text strong>{row.name}</Typography.Text>
              {row.is_default ? <Tag>{t('git.defaultLabels')}</Tag> : null}
            </Space> },
            { title: t('common.description'), dataIndex: 'description' },
            { title: t('common.synced'), dataIndex: 'synced_at' }
          ]} />
        </Space> }
      ]} />
      <Modal title={syncAssetDetail.data?.asset?.name || t('git.syncAsset')} open={Boolean(syncAssetID)} onCancel={() => setSyncAssetID(undefined)} footer={null} width={980} destroyOnHidden>
        {syncAssetDetail.data && <Space direction="vertical" size={16} className="full">
          <Space wrap>
            <Tag>{translatedValue(syncAssetDetail.data.asset?.trigger_mode, t)}</Tag>
            <Tag>{translatedValue(syncAssetDetail.data.asset?.sync_mode, t)}</Tag>
            <Tag color={syncAssetDetail.data.asset?.last_sync_status === 'completed' ? 'green' : syncAssetDetail.data.asset?.last_sync_status === 'failed' ? 'red' : 'blue'}>{syncAssetDetail.data.asset?.last_sync_status ? translatedValue(syncAssetDetail.data.asset?.last_sync_status, t) : t('common.never')}</Tag>
            <Tag color={syncAssetDetail.data.asset?.enabled ? 'green' : 'default'}>{syncAssetDetail.data.asset?.enabled ? t('common.enable') : t('common.disable')}</Tag>
            {syncAssetDetail.data.asset?.archived_at ? <Tag>{t('value.archived')}</Tag> : null}
          </Space>
          <Space wrap>
            <Button size="small" type="primary" onClick={() => runRepoSyncAsset(syncAssetDetail.data.asset.id)} disabled={!syncAssetDetail.data.asset?.enabled || Boolean(syncAssetDetail.data.asset?.archived_at)}>{t('git.run')}</Button>
            <Button size="small" onClick={() => setSyncAssetEditOpen(true)} disabled={Boolean(syncAssetDetail.data.asset?.archived_at)}>{t('common.edit')}</Button>
            <Button size="small" onClick={() => toggleRepoSyncAsset(syncAssetDetail.data.asset.id, !syncAssetDetail.data.asset?.enabled)} disabled={Boolean(syncAssetDetail.data.asset?.archived_at)}>{syncAssetDetail.data.asset?.enabled ? t('common.disable') : t('common.enable')}</Button>
            {syncAssetDetail.data.asset?.archived_at ? <Button size="small" onClick={() => restoreRepoSyncAsset(syncAssetDetail.data.asset.id)}>{t('common.restore')}</Button> : <Button size="small" danger onClick={() => archiveRepoSyncAsset(syncAssetDetail.data.asset.id)}>{t('common.archive')}</Button>}
          </Space>
          <div className="metricGrid">
            <Card><Typography.Text type="secondary">{t('git.runs')}</Typography.Text><Typography.Title level={4}>{syncAssetDetail.data.asset?.total_runs || 0}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('git.successRate')}</Typography.Text><Typography.Title level={4}>{syncAssetDetail.data.asset?.success_rate ?? 0}%</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('git.avgDuration')}</Typography.Text><Typography.Title level={4}>{secondsText(syncAssetDetail.data.asset?.avg_duration_seconds)}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('git.lastFailure')}</Typography.Text><Typography.Title level={5}>{shortText(syncAssetDetail.data.asset?.last_failure_message || syncAssetDetail.data.asset?.last_failure_at)}</Typography.Title></Card>
          </div>
          <Typography.Title level={5}>{t('git.capacitySignals')}</Typography.Title>
          <Table<AnyRow> rowKey="name" size="small" dataSource={syncAssetDetail.data.capacity_signals || []} pagination={false} columns={[
            { title: t('git.signal'), dataIndex: 'name' },
            { title: t('git.severity'), render: (_, row) => <Tag color={signalSeverityColor(row.severity)}>{translatedValue(row.severity || 'ok', t)}</Tag> },
            { title: t('common.status'), render: (_, row) => row.status ? translatedValue(row.status, t) : '-' },
            { title: t('common.source'), render: (_, row) => row.threshold_configuration_applied ? <Tag color="green">{t('common.configured')}</Tag> : <Tag>{t('value.default')}</Tag> },
            { title: t('git.threshold'), render: (_, row) => row.threshold ? shortText(row.threshold, 88) : '-' },
            { title: t('git.detail'), render: (_, row) => shortText(row.detail, 120) }
          ]} />
          <Typography.Title level={5}>{t('git.trend14d')}</Typography.Title>
          <Table<AnyRow> rowKey="day" size="small" dataSource={syncAssetDetail.data.trend || []} pagination={{ pageSize: 7 }} columns={[
            { title: t('common.day'), dataIndex: 'day' },
            { title: t('git.runs'), dataIndex: 'total_runs' },
            { title: t('common.completed'), dataIndex: 'completed_runs' },
            { title: t('common.failed'), dataIndex: 'failed_runs' },
            { title: t('git.active'), dataIndex: 'active_runs' },
            { title: t('common.avg'), render: (_, row) => secondsText(row.avg_duration_seconds) }
          ]} />
          <Table<AnyRow> rowKey="id" size="small" dataSource={syncAssetDetail.data.runs || []} pagination={{ pageSize: 5 }} columns={[
            { title: t('git.run'), dataIndex: 'id' },
            { title: t('common.status'), render: (_, row) => <Tag color={row.status === 'completed' ? 'green' : row.status === 'failed' ? 'red' : 'blue'}>{translatedValue(row.status, t)}</Tag> },
            { title: t('common.ref'), dataIndex: 'ref' },
            { title: t('common.error'), dataIndex: 'error_message' },
            { title: t('common.created'), dataIndex: 'created_at' },
            { title: t('common.action'), render: (_, row) => row.status === 'failed' ? <Button size="small" onClick={() => rerunRepoSyncRun(row.id)}>{t('git.rerun')}</Button> : null }
          ]} />
          <Table<AnyRow> rowKey="id" size="small" dataSource={syncAssetDetail.data.webhook_events || []} pagination={{ pageSize: 5 }} columns={[
            { title: t('title.webhooks'), dataIndex: 'delivery_id' },
            { title: t('common.status'), render: (_, row) => <Tag color={row.status === 'queued' ? 'green' : row.status === 'failed' || row.status === 'rejected' ? 'red' : 'default'}>{translatedValue(row.status, t)}</Tag> },
            { title: t('common.event'), dataIndex: 'event_type' },
            { title: t('common.error'), dataIndex: 'error_message' },
            { title: t('common.received'), dataIndex: 'received_at' }
          ]} />
          <Table<AnyRow> rowKey="id" size="small" dataSource={syncAssetDetail.data.operation_logs || []} pagination={{ pageSize: 5 }} columns={[
            { title: t('common.level'), dataIndex: 'level' },
            { title: t('common.message'), dataIndex: 'message' },
            { title: t('common.created'), dataIndex: 'created_at' }
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

function approvalEscalationDestinationTags(row: AnyRow, t: (key: string) => string = createTranslator('en')) {
  if (!row.escalation_after_minutes) return '-';
  const destinations = Array.isArray(row.escalation_destinations) ? row.escalation_destinations : [];
  return (
    <Space wrap size={4}>
      <Tag>{row.escalation_after_minutes}m</Tag>
      {destinations.length ? approvalDestinationTags(destinations) : <Tag color="gold">{t('ops.noTargets')}</Tag>}
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
  const { t } = useI18n();
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
  const [providerReviewClaimLoading, setProviderReviewClaimLoading] = useState(false);
  const [providerReviewResultLoading, setProviderReviewResultLoading] = useState(false);
  const [providerReviewLiveExecuteLoading, setProviderReviewLiveExecuteLoading] = useState(false);
  const [providerReviewLiveCleanupLoading, setProviderReviewLiveCleanupLoading] = useState(false);
  const [providerReviewSnapshotLoading, setProviderReviewSnapshotLoading] = useState(false);
  const [providerReviewCredentialSnapshotLoading, setProviderReviewCredentialSnapshotLoading] = useState(false);
  const [providerReviewBranchPolicySnapshotLoading, setProviderReviewBranchPolicySnapshotLoading] = useState(false);
  const [providerReviewRuntimeSnapshotLoading, setProviderReviewRuntimeSnapshotLoading] = useState(false);
  const [providerReviewAdapterRehearsalSnapshotLoading, setProviderReviewAdapterRehearsalSnapshotLoading] = useState(false);
  const [providerReviewAdapterBlueprintSnapshotLoading, setProviderReviewAdapterBlueprintSnapshotLoading] = useState(false);
  const [providerReviewLiveAdapterContractSnapshotLoading, setProviderReviewLiveAdapterContractSnapshotLoading] = useState(false);
  const [providerReviewInvocationSnapshotLoading, setProviderReviewInvocationSnapshotLoading] = useState(false);
  const [providerReviewExecutionLockSnapshotLoading, setProviderReviewExecutionLockSnapshotLoading] = useState(false);
  const [providerReviewRequestEnvelopeSnapshotLoading, setProviderReviewRequestEnvelopeSnapshotLoading] = useState(false);
  const [providerReviewIdempotencySnapshotLoading, setProviderReviewIdempotencySnapshotLoading] = useState(false);
  const [providerReviewRequestValidationSnapshotLoading, setProviderReviewRequestValidationSnapshotLoading] = useState(false);
  const [providerReviewRequestMaterializationSnapshotLoading, setProviderReviewRequestMaterializationSnapshotLoading] = useState(false);
  const [providerReviewActivationSnapshotLoading, setProviderReviewActivationSnapshotLoading] = useState(false);
  const [providerReviewTransportSnapshotLoading, setProviderReviewTransportSnapshotLoading] = useState(false);
  const [providerReviewSendSnapshotLoading, setProviderReviewSendSnapshotLoading] = useState(false);
  const [providerReviewRetryBackoffSnapshotLoading, setProviderReviewRetryBackoffSnapshotLoading] = useState(false);
  const [providerReviewResponseSnapshotLoading, setProviderReviewResponseSnapshotLoading] = useState(false);
  const [providerReviewResultRecordingSnapshotLoading, setProviderReviewResultRecordingSnapshotLoading] = useState(false);
  const [providerReviewProviderCallBoundarySnapshotLoading, setProviderReviewProviderCallBoundarySnapshotLoading] = useState(false);
  const [providerReviewTransactionSnapshotLoading, setProviderReviewTransactionSnapshotLoading] = useState(false);
  const [providerReviewLiveExecutionReadinessSnapshotLoading, setProviderReviewLiveExecutionReadinessSnapshotLoading] = useState(false);
  const [providerReviewLiveExecutionGuardSnapshotLoading, setProviderReviewLiveExecutionGuardSnapshotLoading] = useState(false);
  const [providerReviewLiveExecutionPreflightLoading, setProviderReviewLiveExecutionPreflightLoading] = useState(false);
  const [providerReviewLiveExecutionLaunchPlanLoading, setProviderReviewLiveExecutionLaunchPlanLoading] = useState(false);
  const [providerReviewCurrentLiveReadinessSnapshotLoading, setProviderReviewCurrentLiveReadinessSnapshotLoading] = useState(false);
  const [providerReviewCurrentLiveExecutionLaunchPlanLoading, setProviderReviewCurrentLiveExecutionLaunchPlanLoading] = useState(false);
  const [providerReviewCurrentLiveExecutionGateLoading, setProviderReviewCurrentLiveExecutionGateLoading] = useState(false);
  const [providerReviewArmingSnapshotLoading, setProviderReviewArmingSnapshotLoading] = useState(false);
  const [providerReviewSnapshotResult, setProviderReviewSnapshotResult] = useState<AnyRow>();
  const [providerReviewCredentialSnapshotResult, setProviderReviewCredentialSnapshotResult] = useState<AnyRow>();
  const [providerReviewBranchPolicySnapshotResult, setProviderReviewBranchPolicySnapshotResult] = useState<AnyRow>();
  const [providerReviewRuntimeSnapshotResult, setProviderReviewRuntimeSnapshotResult] = useState<AnyRow>();
  const [providerReviewAdapterRehearsalSnapshotResult, setProviderReviewAdapterRehearsalSnapshotResult] = useState<AnyRow>();
  const [providerReviewAdapterBlueprintSnapshotResult, setProviderReviewAdapterBlueprintSnapshotResult] = useState<AnyRow>();
  const [providerReviewLiveAdapterContractSnapshotResult, setProviderReviewLiveAdapterContractSnapshotResult] = useState<AnyRow>();
  const [providerReviewInvocationSnapshotResult, setProviderReviewInvocationSnapshotResult] = useState<AnyRow>();
  const [providerReviewExecutionLockSnapshotResult, setProviderReviewExecutionLockSnapshotResult] = useState<AnyRow>();
  const [providerReviewRequestEnvelopeSnapshotResult, setProviderReviewRequestEnvelopeSnapshotResult] = useState<AnyRow>();
  const [providerReviewIdempotencySnapshotResult, setProviderReviewIdempotencySnapshotResult] = useState<AnyRow>();
  const [providerReviewRequestValidationSnapshotResult, setProviderReviewRequestValidationSnapshotResult] = useState<AnyRow>();
  const [providerReviewRequestMaterializationSnapshotResult, setProviderReviewRequestMaterializationSnapshotResult] = useState<AnyRow>();
  const [providerReviewActivationSnapshotResult, setProviderReviewActivationSnapshotResult] = useState<AnyRow>();
  const [providerReviewTransportSnapshotResult, setProviderReviewTransportSnapshotResult] = useState<AnyRow>();
  const [providerReviewSendSnapshotResult, setProviderReviewSendSnapshotResult] = useState<AnyRow>();
  const [providerReviewRetryBackoffSnapshotResult, setProviderReviewRetryBackoffSnapshotResult] = useState<AnyRow>();
  const [providerReviewResponseSnapshotResult, setProviderReviewResponseSnapshotResult] = useState<AnyRow>();
  const [providerReviewResultRecordingSnapshotResult, setProviderReviewResultRecordingSnapshotResult] = useState<AnyRow>();
  const [providerReviewProviderCallBoundarySnapshotResult, setProviderReviewProviderCallBoundarySnapshotResult] = useState<AnyRow>();
  const [providerReviewTransactionSnapshotResult, setProviderReviewTransactionSnapshotResult] = useState<AnyRow>();
  const [providerReviewLiveExecutionReadinessSnapshotResult, setProviderReviewLiveExecutionReadinessSnapshotResult] = useState<AnyRow>();
  const [providerReviewLiveExecutionGuardSnapshotResult, setProviderReviewLiveExecutionGuardSnapshotResult] = useState<AnyRow>();
  const [providerReviewLiveExecutionPreflightResult, setProviderReviewLiveExecutionPreflightResult] = useState<AnyRow>();
  const [providerReviewLiveExecutionLaunchPlanResult, setProviderReviewLiveExecutionLaunchPlanResult] = useState<AnyRow>();
  const [providerReviewLiveExecutionResult, setProviderReviewLiveExecutionResult] = useState<AnyRow>();
  const [providerReviewLiveCleanupResult, setProviderReviewLiveCleanupResult] = useState<AnyRow>();
  const [providerReviewCurrentLiveReadinessSnapshotResult, setProviderReviewCurrentLiveReadinessSnapshotResult] = useState<AnyRow>();
  const [providerReviewCurrentLiveExecutionLaunchPlanResult, setProviderReviewCurrentLiveExecutionLaunchPlanResult] = useState<AnyRow>();
  const [providerReviewCurrentLiveExecutionGateResult, setProviderReviewCurrentLiveExecutionGateResult] = useState<AnyRow>();
  const [providerReviewArmingSnapshotResult, setProviderReviewArmingSnapshotResult] = useState<AnyRow>();
  const [optimisticallyClaimedProviderReviewAttemptID, setOptimisticallyClaimedProviderReviewAttemptID] = useState<string>();
  const [optimisticallyRecordedProviderReviewAttemptID, setOptimisticallyRecordedProviderReviewAttemptID] = useState<string>();
  const [optimisticallyLiveExecutedProviderReviewAttemptID, setOptimisticallyLiveExecutedProviderReviewAttemptID] = useState<string>();
  const [optimisticallyLiveCleanedProviderReviewAttemptID, setOptimisticallyLiveCleanedProviderReviewAttemptID] = useState<string>();
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
  useEffect(() => {
    setOptimisticallyClaimedProviderReviewAttemptID(undefined);
    setOptimisticallyRecordedProviderReviewAttemptID(undefined);
    setOptimisticallyLiveExecutedProviderReviewAttemptID(undefined);
    setOptimisticallyLiveCleanedProviderReviewAttemptID(undefined);
    setProviderReviewSnapshotResult(undefined);
    setProviderReviewCredentialSnapshotResult(undefined);
    setProviderReviewBranchPolicySnapshotResult(undefined);
    setProviderReviewRuntimeSnapshotResult(undefined);
    setProviderReviewAdapterRehearsalSnapshotResult(undefined);
    setProviderReviewAdapterBlueprintSnapshotResult(undefined);
    setProviderReviewInvocationSnapshotResult(undefined);
    setProviderReviewExecutionLockSnapshotResult(undefined);
    setProviderReviewRequestEnvelopeSnapshotResult(undefined);
    setProviderReviewIdempotencySnapshotResult(undefined);
    setProviderReviewRequestValidationSnapshotResult(undefined);
    setProviderReviewRequestMaterializationSnapshotResult(undefined);
    setProviderReviewActivationSnapshotResult(undefined);
    setProviderReviewTransportSnapshotResult(undefined);
    setProviderReviewSendSnapshotResult(undefined);
    setProviderReviewResponseSnapshotResult(undefined);
    setProviderReviewTransactionSnapshotResult(undefined);
    setProviderReviewLiveExecutionReadinessSnapshotResult(undefined);
    setProviderReviewLiveExecutionGuardSnapshotResult(undefined);
    setProviderReviewLiveExecutionPreflightResult(undefined);
    setProviderReviewLiveExecutionLaunchPlanResult(undefined);
    setProviderReviewLiveExecutionResult(undefined);
    setProviderReviewLiveCleanupResult(undefined);
    setProviderReviewCurrentLiveReadinessSnapshotResult(undefined);
    setProviderReviewCurrentLiveExecutionLaunchPlanResult(undefined);
    setProviderReviewCurrentLiveExecutionGateResult(undefined);
    setProviderReviewArmingSnapshotResult(undefined);
  }, [approvalAuditID]);
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
      message.warning(t('ops.viewNameRequired'));
      return;
    }
    try {
      const view = await api('/api/operation-approval-views', { method: 'POST', body: JSON.stringify({ name, filters: currentApprovalFilters() }) });
      message.success(t('ops.approvalViewSaved'));
      setApprovalViewID(view.id);
      approvalViews.reload();
    } catch (err: any) {
      message.error(err.message || t('common.requestFailed'));
    }
  }
  async function updateApprovalView() {
    if (!approvalViewID) return;
    const name = approvalViewName.trim();
    try {
      const view = await api(`/api/operation-approval-views/${approvalViewID}`, { method: 'PATCH', body: JSON.stringify({ name, filters: currentApprovalFilters() }) });
      message.success(t('ops.approvalViewUpdated'));
      setApprovalViewName(view.name || name);
      approvalViews.reload();
    } catch (err: any) {
      message.error(err.message || t('common.requestFailed'));
    }
  }
  function deleteApprovalView() {
    if (!approvalViewID) return;
    Modal.confirm({
      title: t('ops.deleteApprovalViewConfirm'),
      okText: t('ops.deleteView'),
      okButtonProps: { danger: true },
      onOk: async () => {
        await api(`/api/operation-approval-views/${approvalViewID}`, { method: 'DELETE' });
        message.success(t('ops.approvalViewDeleted'));
        setApprovalViewID(undefined);
        setApprovalViewName('');
        approvalViews.reload();
      }
    });
  }
  async function decideApproval(id: string, decision: 'approve' | 'reject') {
    const result = await api(`/api/operation-approvals/${id}/${decision}`, { method: 'POST', body: '{}' });
    message.success(decision === 'approve' && result.status === 'pending' ? t('ops.approvalRecorded') : decision === 'approve' ? t('ops.approvalApproved') : t('ops.approvalRejected'));
    approvals.reload();
    approvalSummary.reload();
    approvalReminderCandidates.reload();
    ops.reload();
  }
  async function sendApprovalReminder(id: string) {
    const result = await api(`/api/operation-approvals/${id}/remind`, { method: 'POST', body: '{}' });
    if (result.notification_status === 'failed') {
      message.warning(t('ops.reminderFailed'));
    } else {
      message.success(t('ops.reminderSent'));
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
      message.error(err.message || t('ops.metadataJsonInvalid'));
      return;
    }
    const path = editingRule ? `/api/operation-approval-rules/${editingRule.id}` : '/api/operation-approval-rules';
    const method = editingRule ? 'PATCH' : 'POST';
    await api(path, { method, body: JSON.stringify(payload) });
    message.success(editingRule ? t('ops.approvalRuleUpdated') : t('ops.approvalRuleCreated'));
    setRuleOpen(false);
    setEditingRule(null);
    ruleForm.resetFields();
    approvalRules.reload();
  }
  async function delegateApproval() {
    if (!approvalAuditID) return;
    if (!delegateEmail.trim()) {
      message.warning(t('ops.delegateEmailRequired'));
      return;
    }
    await api(`/api/operation-approvals/${approvalAuditID}/delegations`, { method: 'POST', body: JSON.stringify({ to_email: delegateEmail.trim(), reason: delegateReason.trim() }) });
    message.success(t('ops.approvalDelegated'));
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
      message.success(t('ops.delegationRevoked'));
      approvalAudit.reload();
      approvals.reload();
      approvalReminderCandidates.reload();
    } catch (error: any) {
      message.error(error.message || t('ops.delegationRevokeFailed'));
    }
  }
  async function claimProviderReviewAttempt(id: string) {
    if (!id) return;
    setOptimisticallyClaimedProviderReviewAttemptID(id);
    setProviderReviewClaimLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/claim`, { method: 'POST', body: '{}' });
      if (result.claimed === true) {
        message.success(t('providerReview.attemptClaimed'));
      } else {
        message.warning(String(result.claim_state || t('providerReview.attemptNotClaimable')));
      }
      approvalAudit.reload();
    } catch (error: any) {
      message.error(error.message || t('providerReview.claimFailed'));
    } finally {
      setOptimisticallyClaimedProviderReviewAttemptID(undefined);
      setProviderReviewClaimLoading(false);
    }
  }
  async function recordProviderReviewAttemptResult(id: string, result: 'success' | 'retryable' | 'failed') {
    if (!id || providerReviewResultLoading) return;
    setOptimisticallyRecordedProviderReviewAttemptID(id);
    setProviderReviewResultLoading(true);
    try {
      const response = await api(`/api/provider-review-attempts/${id}/local-result`, { method: 'POST', body: JSON.stringify({ result }) });
      if (response.result_recorded === true) {
        message.success(t('providerReview.localResultRecorded'));
      } else {
        message.warning(String(response.result_state || t('providerReview.resultNotRecordable')));
      }
      approvalAudit.reload();
    } catch (error: any) {
      message.error(error.message || t('providerReview.localResultFailed'));
    } finally {
      setOptimisticallyRecordedProviderReviewAttemptID(undefined);
      setProviderReviewResultLoading(false);
    }
  }
  async function executeProviderReviewAttemptLive(id: string) {
    if (!id || providerReviewLiveExecuteLoading) return;
    setOptimisticallyLiveExecutedProviderReviewAttemptID(id);
    setProviderReviewLiveExecutionResult(undefined);
    setProviderReviewLiveExecuteLoading(true);
    try {
      const response = await api(`/api/provider-review-attempts/${id}/execute-live`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewLiveExecutionResult(response);
      if (response.executed === true) {
        message.success(t('providerReview.executeLiveSuccess'));
      } else {
        message.warning(response.message || t('providerReview.executeLiveBlocked'));
      }
      approvalAudit.reload();
    } catch (error: any) {
      setProviderReviewLiveExecutionResult(undefined);
      message.error(error.message || t('providerReview.executeLiveFailed'));
    } finally {
      setOptimisticallyLiveExecutedProviderReviewAttemptID(undefined);
      setProviderReviewLiveExecuteLoading(false);
    }
  }
  async function cleanupProviderReviewAttemptLive(id: string) {
    if (!id || providerReviewLiveCleanupLoading) return;
    setOptimisticallyLiveCleanedProviderReviewAttemptID(id);
    setProviderReviewLiveCleanupResult(undefined);
    setProviderReviewLiveCleanupLoading(true);
    try {
      const response = await api(`/api/provider-review-attempts/${id}/cleanup-live`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewLiveCleanupResult(response);
      if (response.live_cleanup_success === true) {
        message.success(t('providerReview.cleanupLiveSuccess'));
      } else {
        message.warning(response.message || t('providerReview.cleanupLiveBlocked'));
      }
      approvalAudit.reload();
    } catch (error: any) {
      setProviderReviewLiveCleanupResult(undefined);
      message.error(error.message || t('providerReview.cleanupLiveFailed'));
    } finally {
      setOptimisticallyLiveCleanedProviderReviewAttemptID(undefined);
      setProviderReviewLiveCleanupLoading(false);
    }
  }
  async function recordProviderReviewAttemptSnapshot(id: string) {
    if (!id || providerReviewSnapshotLoading) return;
    setProviderReviewSnapshotResult(undefined);
    setProviderReviewSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review attempt snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_snapshot_written ? 'Provider review attempt snapshot recorded' : 'Provider review attempt snapshot already current');
      }
      approvalAudit.reload();
    } catch (error: any) {
      setProviderReviewSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review attempt snapshot');
    } finally {
      setProviderReviewSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptCredentialSnapshot(id: string) {
    if (!id || providerReviewCredentialSnapshotLoading) return;
    setProviderReviewCredentialSnapshotResult(undefined);
    setProviderReviewCredentialSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/credential-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewCredentialSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review credential snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_credential_snapshot_written ? 'Provider review credential snapshot recorded' : 'Provider review credential snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewCredentialSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review credential snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewCredentialSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptBranchPolicySnapshot(id: string) {
    if (!id || providerReviewBranchPolicySnapshotLoading) return;
    setProviderReviewBranchPolicySnapshotResult(undefined);
    setProviderReviewBranchPolicySnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/branch-policy-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewBranchPolicySnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review branch policy snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_branch_policy_snapshot_written ? 'Provider review branch policy snapshot recorded' : 'Provider review branch policy snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewBranchPolicySnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review branch policy snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewBranchPolicySnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptRuntimeSnapshot(id: string) {
    if (!id || providerReviewRuntimeSnapshotLoading) return;
    setProviderReviewRuntimeSnapshotResult(undefined);
    setProviderReviewRuntimeSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/runtime-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewRuntimeSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review runtime snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_runtime_snapshot_written ? 'Provider review runtime snapshot recorded' : 'Provider review runtime snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewRuntimeSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review runtime snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewRuntimeSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptAdapterRehearsalSnapshot(id: string) {
    if (!id || providerReviewAdapterRehearsalSnapshotLoading) return;
    setProviderReviewAdapterRehearsalSnapshotResult(undefined);
    setProviderReviewAdapterRehearsalSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/adapter-rehearsal-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewAdapterRehearsalSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review adapter rehearsal snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_adapter_rehearsal_snapshot_written ? 'Provider review adapter rehearsal snapshot recorded' : 'Provider review adapter rehearsal snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewAdapterRehearsalSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review adapter rehearsal snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewAdapterRehearsalSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptAdapterBlueprintSnapshot(id: string) {
    if (!id || providerReviewAdapterBlueprintSnapshotLoading) return;
    setProviderReviewAdapterBlueprintSnapshotResult(undefined);
    setProviderReviewAdapterBlueprintSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/adapter-blueprint-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewAdapterBlueprintSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review adapter blueprint snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_adapter_blueprint_snapshot_written ? 'Provider review adapter blueprint snapshot recorded' : 'Provider review adapter blueprint snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewAdapterBlueprintSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review adapter blueprint snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewAdapterBlueprintSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptLiveAdapterContractSnapshot(id: string) {
    if (!id || providerReviewLiveAdapterContractSnapshotLoading) return;
    setProviderReviewLiveAdapterContractSnapshotResult(undefined);
    setProviderReviewLiveAdapterContractSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/live-adapter-contract-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewLiveAdapterContractSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review live-adapter contract snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_live_adapter_contract_snapshot_written ? 'Provider review live-adapter contract snapshot recorded' : 'Provider review live-adapter contract snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewLiveAdapterContractSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review live-adapter contract snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewLiveAdapterContractSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptInvocationSnapshot(id: string) {
    if (!id || providerReviewInvocationSnapshotLoading) return;
    setProviderReviewInvocationSnapshotResult(undefined);
    setProviderReviewInvocationSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/invocation-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewInvocationSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review invocation snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_invocation_snapshot_written ? 'Provider review invocation snapshot recorded' : 'Provider review invocation snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewInvocationSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review invocation snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewInvocationSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptExecutionLockSnapshot(id: string) {
    if (!id || providerReviewExecutionLockSnapshotLoading) return;
    setProviderReviewExecutionLockSnapshotResult(undefined);
    setProviderReviewExecutionLockSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/execution-lock-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewExecutionLockSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review execution lock snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_execution_lock_snapshot_written ? 'Provider review execution lock snapshot recorded' : 'Provider review execution lock snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewExecutionLockSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review execution lock snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewExecutionLockSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptRequestEnvelopeSnapshot(id: string) {
    if (!id || providerReviewRequestEnvelopeSnapshotLoading) return;
    setProviderReviewRequestEnvelopeSnapshotResult(undefined);
    setProviderReviewRequestEnvelopeSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/request-envelope-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewRequestEnvelopeSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review request envelope snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_request_envelope_snapshot_written ? 'Provider review request envelope snapshot recorded' : 'Provider review request envelope snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewRequestEnvelopeSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review request envelope snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewRequestEnvelopeSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptIdempotencySnapshot(id: string) {
    if (!id || providerReviewIdempotencySnapshotLoading) return;
    setProviderReviewIdempotencySnapshotResult(undefined);
    setProviderReviewIdempotencySnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/idempotency-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewIdempotencySnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review idempotency snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_idempotency_snapshot_written ? 'Provider review idempotency snapshot recorded' : 'Provider review idempotency snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewIdempotencySnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review idempotency snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewIdempotencySnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptRequestValidationSnapshot(id: string) {
    if (!id || providerReviewRequestValidationSnapshotLoading) return;
    setProviderReviewRequestValidationSnapshotResult(undefined);
    setProviderReviewRequestValidationSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/request-validation-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewRequestValidationSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review request validation snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_request_validation_snapshot_written ? 'Provider review request validation snapshot recorded' : 'Provider review request validation snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewRequestValidationSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review request validation snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewRequestValidationSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptRequestMaterializationSnapshot(id: string) {
    if (!id || providerReviewRequestMaterializationSnapshotLoading) return;
    setProviderReviewRequestMaterializationSnapshotResult(undefined);
    setProviderReviewRequestMaterializationSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/request-materialization-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewRequestMaterializationSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review request materialization snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_request_materialization_snapshot_written ? 'Provider review request materialization snapshot recorded' : 'Provider review request materialization snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewRequestMaterializationSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review request materialization snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewRequestMaterializationSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptActivationSnapshot(id: string) {
    if (!id || providerReviewActivationSnapshotLoading) return;
    setProviderReviewActivationSnapshotResult(undefined);
    setProviderReviewActivationSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/activation-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewActivationSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review activation snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_activation_snapshot_written ? 'Provider review activation snapshot recorded' : 'Provider review activation snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewActivationSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review activation snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewActivationSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptSendSnapshot(id: string) {
    if (!id || providerReviewSendSnapshotLoading) return;
    setProviderReviewSendSnapshotResult(undefined);
    setProviderReviewSendSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/send-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewSendSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review send snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_send_snapshot_written ? 'Provider review send snapshot recorded' : 'Provider review send snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewSendSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review send snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewSendSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptTransportSnapshot(id: string) {
    if (!id || providerReviewTransportSnapshotLoading) return;
    setProviderReviewTransportSnapshotResult(undefined);
    setProviderReviewTransportSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/transport-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewTransportSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review transport snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_transport_snapshot_written ? 'Provider review transport snapshot recorded' : 'Provider review transport snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewTransportSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review transport snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewTransportSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptRetryBackoffSnapshot(id: string) {
    if (!id || providerReviewRetryBackoffSnapshotLoading) return;
    setProviderReviewRetryBackoffSnapshotResult(undefined);
    setProviderReviewRetryBackoffSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/retry-backoff-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewRetryBackoffSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review retry/backoff snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_retry_backoff_snapshot_written ? 'Provider review retry/backoff snapshot recorded' : 'Provider review retry/backoff snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewRetryBackoffSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review retry/backoff snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewRetryBackoffSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptResponseSnapshot(id: string) {
    if (!id || providerReviewResponseSnapshotLoading) return;
    setProviderReviewResponseSnapshotResult(undefined);
    setProviderReviewResponseSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/response-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewResponseSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review response snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_response_snapshot_written ? 'Provider review response snapshot recorded' : 'Provider review response snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewResponseSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review response snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewResponseSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptResultRecordingSnapshot(id: string) {
    if (!id || providerReviewResultRecordingSnapshotLoading) return;
    setProviderReviewResultRecordingSnapshotResult(undefined);
    setProviderReviewResultRecordingSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/result-recording-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewResultRecordingSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review result-recording snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_result_recording_snapshot_written ? 'Provider review result-recording snapshot recorded' : 'Provider review result-recording snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewResultRecordingSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review result-recording snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewResultRecordingSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptProviderCallBoundarySnapshot(id: string) {
    if (!id || providerReviewProviderCallBoundarySnapshotLoading) return;
    setProviderReviewProviderCallBoundarySnapshotResult(undefined);
    setProviderReviewProviderCallBoundarySnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/provider-call-boundary-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewProviderCallBoundarySnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review provider-call-boundary snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_provider_call_boundary_snapshot_written ? 'Provider review provider-call-boundary snapshot recorded' : 'Provider review provider-call-boundary snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewProviderCallBoundarySnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review provider-call-boundary snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewProviderCallBoundarySnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptTransactionSnapshot(id: string) {
    if (!id || providerReviewTransactionSnapshotLoading) return;
    setProviderReviewTransactionSnapshotResult(undefined);
    setProviderReviewTransactionSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/transaction-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewTransactionSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review transaction snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_transaction_snapshot_written ? 'Provider review transaction snapshot recorded' : 'Provider review transaction snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewTransactionSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review transaction snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewTransactionSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptLiveExecutionReadinessSnapshot(id: string) {
    if (!id || providerReviewLiveExecutionReadinessSnapshotLoading) return;
    setProviderReviewLiveExecutionReadinessSnapshotResult(undefined);
    setProviderReviewLiveExecutionReadinessSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/live-execution-readiness-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewLiveExecutionReadinessSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review live execution readiness snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_live_execution_readiness_snapshot_written ? 'Provider review live execution readiness snapshot recorded' : 'Provider review live execution readiness snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewLiveExecutionReadinessSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review live execution readiness snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewLiveExecutionReadinessSnapshotLoading(false);
    }
  }
  async function recordProviderReviewAttemptLiveExecutionGuardSnapshot(id: string) {
    if (!id || providerReviewLiveExecutionGuardSnapshotLoading) return;
    setProviderReviewLiveExecutionGuardSnapshotResult(undefined);
    setProviderReviewLiveExecutionGuardSnapshotLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/live-execution-guard-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewLiveExecutionGuardSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review live execution guard snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_live_execution_guard_written ? 'Provider review live execution guard snapshot recorded' : 'Provider review live execution guard snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewLiveExecutionGuardSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review live execution guard snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewLiveExecutionGuardSnapshotLoading(false);
    }
  }
  async function checkProviderReviewAttemptLiveExecutionPreflight(id: string) {
    if (!id || providerReviewLiveExecutionPreflightLoading) return;
    setProviderReviewLiveExecutionPreflightResult(undefined);
    setProviderReviewLiveExecutionPreflightLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/live-execution-preflight`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewLiveExecutionPreflightResult(result);
      if (result.preflight_ready === false) {
        message.warning(result.message || t('providerReview.preflightBlocked'));
      } else {
        message.success(t('providerReview.preflightReady'));
      }
    } catch (error: any) {
      setProviderReviewLiveExecutionPreflightResult(undefined);
      message.error(error.message || t('providerReview.preflightFailed'));
    } finally {
      approvalAudit.reload();
      setProviderReviewLiveExecutionPreflightLoading(false);
    }
  }
  async function checkProviderReviewAttemptLiveExecutionLaunchPlan(id: string) {
    if (!id || providerReviewLiveExecutionLaunchPlanLoading) return;
    setProviderReviewLiveExecutionLaunchPlanResult(undefined);
    setProviderReviewLiveExecutionLaunchPlanLoading(true);
    try {
      const result = await api(`/api/provider-review-attempts/${id}/live-execution-launch-plan`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewLiveExecutionLaunchPlanResult(result);
      if (result.launch_plan_ready === false) {
        message.warning(result.message || t('providerReview.launchPlanBlocked'));
      } else {
        message.success(t('providerReview.launchPlanReady'));
      }
    } catch (error: any) {
      setProviderReviewLiveExecutionLaunchPlanResult(undefined);
      message.error(error.message || t('providerReview.launchPlanFailed'));
    } finally {
      approvalAudit.reload();
      setProviderReviewLiveExecutionLaunchPlanLoading(false);
    }
  }
  async function recordProviderReviewCurrentAttemptLiveReadinessSnapshot() {
    if (!approvalAuditID || providerReviewCurrentLiveReadinessSnapshotLoading) return;
    setProviderReviewCurrentLiveReadinessSnapshotResult(undefined);
    setProviderReviewCurrentLiveReadinessSnapshotLoading(true);
    try {
      const result = await api(`/api/operation-approvals/${approvalAuditID}/provider-review-current-live-readiness-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewCurrentLiveReadinessSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Current provider review attempt live-readiness snapshot is not ready yet');
      } else {
        message.success(result.provider_review_attempt_live_execution_readiness_snapshot_written ? 'Current provider review attempt live-readiness snapshot recorded' : 'Current provider review attempt live-readiness snapshot already current');
      }
    } catch (error: any) {
      setProviderReviewCurrentLiveReadinessSnapshotResult(undefined);
      message.error(error.message || 'Could not record current provider review attempt live-readiness snapshot');
    } finally {
      approvalAudit.reload();
      setProviderReviewCurrentLiveReadinessSnapshotLoading(false);
    }
  }
  async function checkProviderReviewCurrentAttemptLiveExecutionLaunchPlan() {
    if (!approvalAuditID || providerReviewCurrentLiveExecutionLaunchPlanLoading) return;
    setProviderReviewCurrentLiveExecutionLaunchPlanResult(undefined);
    setProviderReviewCurrentLiveExecutionLaunchPlanLoading(true);
    try {
      const result = await api(`/api/operation-approvals/${approvalAuditID}/provider-review-current-live-launch-plan`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewCurrentLiveExecutionLaunchPlanResult(result);
      if (result.launch_plan_ready === false) {
        message.warning(result.message || t('providerReview.currentLaunchPlanBlocked'));
      } else {
        message.success(t('providerReview.currentLaunchPlanReady'));
      }
    } catch (error: any) {
      setProviderReviewCurrentLiveExecutionLaunchPlanResult(undefined);
      message.error(error.message || t('providerReview.currentLaunchPlanFailed'));
    } finally {
      approvalAudit.reload();
      setProviderReviewCurrentLiveExecutionLaunchPlanLoading(false);
    }
  }
  async function checkProviderReviewCurrentLiveExecutionGate() {
    if (!approvalAuditID || providerReviewCurrentLiveExecutionGateLoading) return;
    setProviderReviewCurrentLiveExecutionGateResult(undefined);
    setProviderReviewCurrentLiveExecutionGateLoading(true);
    try {
      const result = await api(`/api/operation-approvals/${approvalAuditID}/provider-review-current-live-execution-gate`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewCurrentLiveExecutionGateResult(result);
      if (result.execution_gate_ready === false) {
        message.warning(result.message || t('providerReview.currentGateBlocked'));
      } else {
        message.success(t('providerReview.currentGateReady'));
      }
    } catch (error: any) {
      setProviderReviewCurrentLiveExecutionGateResult(undefined);
      message.error(error.message || t('providerReview.currentGateFailed'));
    } finally {
      approvalAudit.reload();
      setProviderReviewCurrentLiveExecutionGateLoading(false);
    }
  }
  async function recordProviderReviewArmingSnapshot() {
    if (!approvalAuditID || providerReviewArmingSnapshotLoading) return;
    setProviderReviewArmingSnapshotResult(undefined);
    setProviderReviewArmingSnapshotLoading(true);
    try {
      const result = await api(`/api/operation-approvals/${approvalAuditID}/provider-review-arming-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setProviderReviewArmingSnapshotResult(result);
      if (result.recording_ready === false) {
        message.warning(result.message || 'Provider review arming snapshot is not ready yet');
      } else {
        message.success(result.provider_review_mutation_arming_snapshot_written ? 'Provider review arming snapshot recorded' : 'Provider review arming snapshot already current');
      }
      approvalAudit.reload();
    } catch (error: any) {
      setProviderReviewArmingSnapshotResult(undefined);
      message.error(error.message || 'Could not record provider review arming snapshot');
    } finally {
      setProviderReviewArmingSnapshotLoading(false);
    }
  }
  return (
    <Space direction="vertical" size={16} className="full">
      {!embedded && <Typography.Title level={2}>{t('title.operations')}</Typography.Title>}
      <div className="metricGrid">
        <Card><Typography.Text type="secondary">{t('ops.pendingApprovals')}</Typography.Text><Typography.Title level={3}>{approvalSummary.data?.pending ?? 0}</Typography.Title></Card>
        <Card><Typography.Text type="secondary">{t('ops.expiringSoon')}</Typography.Text><Typography.Title level={3}>{approvalSummary.data?.expiring_soon ?? 0}</Typography.Title></Card>
        <Card><Typography.Text type="secondary">{t('ops.notificationFailures')}</Typography.Text><Typography.Title level={3}>{approvalSummary.data?.notification_failed ?? 0}</Typography.Title></Card>
        <Card loading={approvalReminderCandidates.loading}><Typography.Text type="secondary">{t('ops.slaWatch')}</Typography.Text><Typography.Title level={3}>{approvalReminderCandidates.data?.items?.length ?? 0}</Typography.Title></Card>
      </div>
      {approvalReminderCandidates.error && <Alert showIcon type="error" message={approvalReminderCandidates.error} />}
      {Array.isArray(approvalSummary.data?.by_action) && approvalSummary.data.by_action.length > 0 && (
        <Space wrap>
          {approvalSummary.data.by_action.map((row: AnyRow) => <Tag key={row.action}>{row.action}: {row.count}</Tag>)}
        </Space>
      )}
      <Typography.Title level={5}>{t('ops.reminderCandidates')}</Typography.Title>
      <Table<AnyRow> rowKey="id" size="small" dataSource={approvalReminderCandidates.data?.items || []} pagination={{ pageSize: 4 }} columns={[
        { title: t('ops.approval'), dataIndex: 'title' },
        { title: t('common.action'), dataIndex: 'action' },
        { title: t('ops.project'), dataIndex: 'project_name' },
        { title: t('common.reason'), render: (_, row) => <Tag color={row.escalation_level === 'danger' ? 'red' : row.escalation_level === 'warning' ? 'gold' : 'blue'}>{translatedValue(row.reminder_reason, t)}</Tag> },
        { title: t('ops.progress'), render: (_, row) => `${row.approved_count || 0}/${row.required_approval_count || 1}` },
        { title: t('ops.age'), render: (_, row) => `${row.age_minutes || 0}m` },
        { title: t('ops.expiresIn'), render: (_, row) => row.minutes_until_expiry === null || row.minutes_until_expiry === undefined ? '-' : `${row.minutes_until_expiry}m` },
        { title: t('ops.reminders'), render: (_, row) => `${row.reminder_count || 0}` },
        { title: t('ops.lastReminded'), render: (_, row) => row.last_reminded_at || '-' },
        { title: t('ops.escalations'), render: (_, row) => `${row.escalation_count || 0}` },
        { title: t('ops.lastEscalated'), render: (_, row) => row.last_escalated_at || '-' },
        { title: t('ops.requester'), dataIndex: 'requested_by_email' },
        { title: t('common.action'), render: (_, row) => <Space>{canActOnApproval(row, currentRole) && <Button size="small" onClick={() => sendApprovalReminder(row.id)}>{t('ops.remind')}</Button>}<Button size="small" onClick={() => setApprovalAuditID(row.id)}>{t('common.open')}</Button></Space> }
      ]} />
      <Space wrap>
        <Typography.Title level={5} style={{ margin: 0 }}>{t('ops.approvalRules')}</Typography.Title>
        {canEditApprovalRules && <Button size="small" onClick={() => openApprovalRule()}>{t('ops.newRule')}</Button>}
      </Space>
      <Table<AnyRow> rowKey="id" size="small" dataSource={approvalRules.data?.items || []} pagination={{ pageSize: 4 }} columns={[
        { title: t('common.action'), dataIndex: 'action' },
        { title: t('ops.resource'), render: (_, row) => row.resource_type || '*' },
        { title: t('ops.approvers'), render: (_, row) => approvalRoles(row.required_approver_roles).join(', ') },
        { title: t('common.count'), dataIndex: 'required_approval_count' },
        { title: t('ops.expires'), render: (_, row) => `${row.expires_after_minutes || 0}m` },
        { title: t('ops.notify'), render: (_, row) => approvalDestinationTags(row.notification_destinations) },
        { title: t('ops.escalate'), render: (_, row) => approvalEscalationDestinationTags(row, t) },
        { title: t('ops.enabled'), render: (_, row) => <Tag color={row.enabled ? 'green' : 'default'}>{row.enabled ? t('common.enable') : t('common.disable')}</Tag> },
        { title: t('common.action'), render: (_, row) => <Space>{canEditApprovalRules && <Button size="small" onClick={() => openApprovalRule(row)}>{t('common.edit')}</Button>}<Button size="small" onClick={() => setRuleAuditID(row.id)}>{t('common.history')}</Button></Space> }
      ]} />
      <Modal title={editingRule ? t('ops.editApprovalRule') : t('ops.createApprovalRule')} open={ruleOpen} onCancel={() => { setRuleOpen(false); setEditingRule(null); }} onOk={() => ruleForm.submit()} destroyOnHidden okText={t('common.ok')} cancelText={t('common.cancel')}>
        <Form form={ruleForm} layout="vertical" onFinish={saveApprovalRule} initialValues={approvalRuleInitialValues(editingRule)}>
          <Form.Item name="resource_type" label={t('ops.resourceType')}>
            <Input placeholder="git_remote, ssh_machine, agent_task, operation, or blank" />
          </Form.Item>
          <Form.Item name="action" label={t('common.action')} rules={[{ required: true, message: t('common.required') }]}>
            <Input placeholder="repo.tag" />
          </Form.Item>
          <Form.Item name="required_approver_roles" label={t('ops.approverRoles')}>
            <Input placeholder="admin, owner" />
          </Form.Item>
          <Form.Item name="required_approval_count" label={t('ops.requiredApprovalCount')}>
            <Input type="number" min={1} />
          </Form.Item>
          <Form.Item name="expires_after_minutes" label={t('ops.expiresAfterMinutes')}>
            <Input type="number" min={1} />
          </Form.Item>
          <Form.Item name="notification_channels" label={t('ops.notificationChannels')}>
            <Input placeholder="ui, webhook" />
          </Form.Item>
          <Form.Item name="escalation_after_minutes" label={t('ops.escalationAfterMinutes')}>
            <Input type="number" min={0} />
          </Form.Item>
          <Form.Item name="escalation_channels" label={t('ops.escalationChannels')}>
            <Input placeholder="ui, webhook" />
          </Form.Item>
          <Form.Item name="priority" label={t('ops.priority')}>
            <Input type="number" />
          </Form.Item>
          <Form.Item name="enabled" valuePropName="checked">
            <Checkbox>{t('ops.enabled')}</Checkbox>
          </Form.Item>
          <Form.Item name="metadata_json" label={t('ops.metadataJson')}>
            <Input.TextArea rows={4} />
          </Form.Item>
        </Form>
      </Modal>
      <Modal title={t('ops.approvalRuleHistory')} open={Boolean(ruleAuditID)} onCancel={() => setRuleAuditID(undefined)} footer={null} width={900} destroyOnHidden>
        <Table<AnyRow> rowKey="id" size="small" loading={ruleAudits.loading} dataSource={ruleAudits.data?.items || []} pagination={{ pageSize: 5 }} columns={[
          { title: t('common.action'), render: (_, row) => <Tag color={row.action === 'create' ? 'green' : 'blue'}>{translatedValue(row.action, t)}</Tag> },
          { title: t('common.actor'), render: (_, row) => row.actor_email || row.actor_user_id || '-' },
          { title: t('common.before'), render: (_, row) => <JSONBlock value={row.before_state || {}} /> },
          { title: t('common.after'), render: (_, row) => <JSONBlock value={row.after_state || {}} /> },
          { title: t('common.created'), dataIndex: 'created_at' }
        ]} />
      </Modal>
      <Space wrap>
        <Select allowClear value={approvalViewID} placeholder={t('ops.savedView')} style={{ width: 220 }} onChange={(value) => applyApprovalView(value)} options={(approvalViews.data?.items || []).map((row: AnyRow) => ({ value: row.id, label: row.name }))} />
        <Input placeholder={t('ops.viewName')} value={approvalViewName} onChange={(event) => setApprovalViewName(event.target.value)} style={{ width: 180 }} />
        <Button onClick={saveApprovalView}>{t('ops.saveView')}</Button>
        <Button disabled={!approvalViewID} onClick={updateApprovalView}>{t('ops.updateView')}</Button>
        <Button danger disabled={!approvalViewID} onClick={deleteApprovalView}>{t('ops.deleteView')}</Button>
      </Space>
      <Space wrap>
        <Select allowClear value={approvalStatusFilter || undefined} placeholder={t('ops.approvalStatus')} style={{ width: 180 }} onChange={(value) => setApprovalStatusFilter(value || '')} options={['pending', 'approved', 'rejected', 'expired'].map((value) => ({ value, label: translatedValue(value, t) }))} />
        <Select allowClear value={approvalActionFilter || undefined} placeholder={t('common.action')} style={{ width: 200 }} onChange={(value) => setApprovalActionFilter(value || '')} options={approvalActionOptions} />
        <Input allowClear placeholder={t('ops.resourceType')} value={approvalResourceTypeFilter} onChange={(event) => setApprovalResourceTypeFilter(event.target.value)} style={{ width: 180 }} />
        <Input allowClear placeholder={t('ops.searchApproval')} value={approvalSearch} onChange={(event) => setApprovalSearch(event.target.value)} style={{ width: 260 }} />
        <Input allowClear placeholder={t('ops.requester')} value={approvalRequestedByFilter} onChange={(event) => setApprovalRequestedByFilter(event.target.value)} style={{ width: 220 }} />
        <Input allowClear placeholder={t('ops.sinceRfc3339')} value={approvalSinceFilter} onChange={(event) => setApprovalSinceFilter(event.target.value)} style={{ width: 220 }} />
        <Input allowClear placeholder={t('ops.untilRfc3339')} value={approvalUntilFilter} onChange={(event) => setApprovalUntilFilter(event.target.value)} style={{ width: 220 }} />
      </Space>
      <Table<AnyRow> rowKey="id" dataSource={approvals.data?.items || []} pagination={{ pageSize: 6 }} columns={[
        { title: t('ops.approval'), dataIndex: 'title' },
        { title: t('common.action'), dataIndex: 'action' },
        { title: t('ops.project'), dataIndex: 'project_name' },
        { title: t('common.status'), render: (_, row) => <Tag color={row.status === 'approved' ? 'green' : row.status === 'rejected' || row.status === 'expired' ? 'red' : 'gold'}>{translatedValue(row.status, t)}</Tag> },
        { title: t('ops.progress'), render: (_, row) => `${row.approved_count || 0}/${row.required_approval_count || 1}` },
        { title: t('ops.notify'), render: (_, row) => <Tag color={row.notification_status === 'failed' ? 'red' : row.notification_status === 'delivered' ? 'green' : 'default'}>{translatedValue(row.notification_status || 'pending', t)}</Tag> },
        { title: t('ops.requestedBy'), dataIndex: 'requested_by_email' },
        { title: t('common.created'), dataIndex: 'created_at' },
        { title: t('ops.expires'), render: (_, row) => row.expires_at ? <Tag color={approvalStillActive(row) ? 'gold' : 'red'}>{row.expires_at}</Tag> : '-' },
        {
          title: t('common.decision'),
          render: (_, row) => approvalStillActive(row) && canActOnApproval(row, currentRole) ? (
            <Space>
              <Button size="small" type="primary" onClick={() => decideApproval(row.id, 'approve')}>{t('common.approve')}</Button>
              <Button size="small" danger onClick={() => decideApproval(row.id, 'reject')}>{t('common.reject')}</Button>
            </Space>
          ) : row.status === 'pending' ? t('common.pending') : row.decided_by_email || row.decision_reason || '-'
        },
        {
          title: t('common.audit'),
          render: (_, row) => <Button size="small" onClick={() => setApprovalAuditID(row.id)}>{t('common.open')}</Button>
        }
      ]} />
      <Table<AnyRow> rowKey="id" dataSource={ops.data?.items || []} pagination={{ pageSize: 8 }} columns={[
        { title: t('common.type'), dataIndex: 'operation_type' },
        { title: t('field.title'), dataIndex: 'title' },
        { title: t('common.status'), render: (_, row) => <Tag color={operationStatusColor(row.status)}>{translatedValue(row.status, t)}</Tag> },
        { title: t('common.created'), dataIndex: 'created_at' },
        { title: t('common.logs'), render: (_, row) => <Button size="small" onClick={() => setLiveOperationID(row.id)}>{t('common.live')}</Button> }
      ]} />
      {liveOperationID && (
        <div className="liveLogPanel">
          <Space direction="vertical" size={12} className="full">
            <Space wrap>
              <Typography.Title level={5} style={{ margin: 0 }}>{liveOperation?.title || liveOperationID}</Typography.Title>
              <Tag color={liveLogTag.color}>{liveLogTag.label}</Tag>
              <Tag>{liveLogs.logs.length === 1 ? t('ops.oneLog') : t('ops.logCount').replace('{count}', String(liveLogs.logs.length))}</Tag>
              <Button size="small" onClick={() => setLiveOperationID(undefined)}>{t('common.close')}</Button>
            </Space>
            {liveLogs.error && <Alert type="error" showIcon message={liveLogs.error} />}
            {!liveLogs.error && liveLogs.logs.length === 0 && <Alert type="info" showIcon message={emptyOperationLogMessage(liveLogs)} />}
            <Table<AnyRow> rowKey="id" size="small" dataSource={liveLogs.logs} pagination={{ pageSize: 8 }} columns={[
              { title: t('common.level'), dataIndex: 'level', width: 110 },
              { title: t('common.message'), dataIndex: 'message' },
              { title: t('common.fields'), render: (_, row) => <JSONBlock value={row.fields} /> },
              { title: t('common.created'), dataIndex: 'created_at', width: 220 }
            ]} />
          </Space>
        </div>
      )}
      <Modal title={t('ops.approval') + ' ' + t('common.audit')} open={Boolean(approvalAuditID)} onCancel={() => setApprovalAuditID(undefined)} footer={null} width={980} destroyOnHidden>
        <Space direction="vertical" size={16} className="full">
          <Table<AnyRow> rowKey="id" size="small" pagination={false} dataSource={approvalAudit.data?.approval ? [approvalAudit.data.approval] : []} columns={[
            { title: t('ops.approval'), dataIndex: 'title' },
            { title: t('common.status'), render: (_, row) => <Tag color={row.status === 'approved' ? 'green' : row.status === 'rejected' || row.status === 'expired' ? 'red' : 'gold'}>{translatedValue(row.status, t)}</Tag> },
            { title: t('ops.progress'), render: (_, row) => `${row.approved_count || 0}/${row.required_approval_count || 1}` },
            { title: t('ops.notify'), render: (_, row) => <Tag color={row.notification_status === 'failed' ? 'red' : row.notification_status === 'delivered' ? 'green' : 'default'}>{translatedValue(row.notification_status || 'pending', t)}</Tag> },
            { title: t('ops.requester'), dataIndex: 'requested_by_email' },
            { title: t('ops.decider'), dataIndex: 'decided_by_email' },
            { title: t('common.created'), dataIndex: 'created_at' }
          ]} />
          {approvalAudit.data?.approval?.notification_last_error && <Alert type="error" showIcon message={approvalAudit.data.approval.notification_last_error} />}
          <ProviderReviewApprovalAudit
            value={approvalAudit.data?.approval_payload_audit}
            persistedAttemptLedger={approvalAudit.data?.provider_review_attempt_ledger}
            onClaimAttempt={claimProviderReviewAttempt}
            onRecordAttemptResult={recordProviderReviewAttemptResult}
            onExecuteAttemptLive={executeProviderReviewAttemptLive}
            onCleanupAttemptLive={cleanupProviderReviewAttemptLive}
            onRecordAttemptSnapshot={recordProviderReviewAttemptSnapshot}
            onRecordAttemptCredentialSnapshot={recordProviderReviewAttemptCredentialSnapshot}
            onRecordAttemptBranchPolicySnapshot={recordProviderReviewAttemptBranchPolicySnapshot}
            onRecordAttemptRuntimeSnapshot={recordProviderReviewAttemptRuntimeSnapshot}
            onRecordAttemptAdapterRehearsalSnapshot={recordProviderReviewAttemptAdapterRehearsalSnapshot}
            onRecordAttemptAdapterBlueprintSnapshot={recordProviderReviewAttemptAdapterBlueprintSnapshot}
            onRecordAttemptLiveAdapterContractSnapshot={recordProviderReviewAttemptLiveAdapterContractSnapshot}
            onRecordAttemptInvocationSnapshot={recordProviderReviewAttemptInvocationSnapshot}
            onRecordAttemptExecutionLockSnapshot={recordProviderReviewAttemptExecutionLockSnapshot}
            onRecordAttemptRequestEnvelopeSnapshot={recordProviderReviewAttemptRequestEnvelopeSnapshot}
            onRecordAttemptIdempotencySnapshot={recordProviderReviewAttemptIdempotencySnapshot}
            onRecordAttemptRequestValidationSnapshot={recordProviderReviewAttemptRequestValidationSnapshot}
            onRecordAttemptRequestMaterializationSnapshot={recordProviderReviewAttemptRequestMaterializationSnapshot}
            onRecordAttemptActivationSnapshot={recordProviderReviewAttemptActivationSnapshot}
            onRecordAttemptTransportSnapshot={recordProviderReviewAttemptTransportSnapshot}
            onRecordAttemptSendSnapshot={recordProviderReviewAttemptSendSnapshot}
            onRecordAttemptRetryBackoffSnapshot={recordProviderReviewAttemptRetryBackoffSnapshot}
            onRecordAttemptResponseSnapshot={recordProviderReviewAttemptResponseSnapshot}
            onRecordAttemptResultRecordingSnapshot={recordProviderReviewAttemptResultRecordingSnapshot}
            onRecordAttemptProviderCallBoundarySnapshot={recordProviderReviewAttemptProviderCallBoundarySnapshot}
            onRecordAttemptTransactionSnapshot={recordProviderReviewAttemptTransactionSnapshot}
            onRecordAttemptLiveExecutionReadinessSnapshot={recordProviderReviewAttemptLiveExecutionReadinessSnapshot}
            onRecordAttemptLiveExecutionGuardSnapshot={recordProviderReviewAttemptLiveExecutionGuardSnapshot}
            onCheckAttemptLiveExecutionPreflight={checkProviderReviewAttemptLiveExecutionPreflight}
            onCheckAttemptLiveExecutionLaunchPlan={checkProviderReviewAttemptLiveExecutionLaunchPlan}
            onRecordCurrentAttemptLiveReadinessSnapshot={recordProviderReviewCurrentAttemptLiveReadinessSnapshot}
            onCheckCurrentAttemptLiveExecutionLaunchPlan={checkProviderReviewCurrentAttemptLiveExecutionLaunchPlan}
            onCheckCurrentLiveExecutionGate={checkProviderReviewCurrentLiveExecutionGate}
            onRecordArmingSnapshot={recordProviderReviewArmingSnapshot}
            canRecordCurrentAttemptLiveReadinessSnapshot={Boolean(approvalAuditID)}
            canCheckCurrentAttemptLiveExecutionLaunchPlan={Boolean(approvalAuditID)}
            canCheckCurrentLiveExecutionGate={Boolean(approvalAuditID)}
            canRecordArmingSnapshot={Boolean(approvalAuditID)}
            claimLoading={providerReviewClaimLoading}
            resultLoading={providerReviewResultLoading}
            liveExecuteLoading={providerReviewLiveExecuteLoading}
            liveCleanupLoading={providerReviewLiveCleanupLoading}
            snapshotLoading={providerReviewSnapshotLoading}
            credentialSnapshotLoading={providerReviewCredentialSnapshotLoading}
            branchPolicySnapshotLoading={providerReviewBranchPolicySnapshotLoading}
            runtimeSnapshotLoading={providerReviewRuntimeSnapshotLoading}
            adapterRehearsalSnapshotLoading={providerReviewAdapterRehearsalSnapshotLoading}
            adapterBlueprintSnapshotLoading={providerReviewAdapterBlueprintSnapshotLoading}
            liveAdapterContractSnapshotLoading={providerReviewLiveAdapterContractSnapshotLoading}
            invocationSnapshotLoading={providerReviewInvocationSnapshotLoading}
            executionLockSnapshotLoading={providerReviewExecutionLockSnapshotLoading}
            requestEnvelopeSnapshotLoading={providerReviewRequestEnvelopeSnapshotLoading}
            idempotencySnapshotLoading={providerReviewIdempotencySnapshotLoading}
            requestValidationSnapshotLoading={providerReviewRequestValidationSnapshotLoading}
            requestMaterializationSnapshotLoading={providerReviewRequestMaterializationSnapshotLoading}
            activationSnapshotLoading={providerReviewActivationSnapshotLoading}
            transportSnapshotLoading={providerReviewTransportSnapshotLoading}
            sendSnapshotLoading={providerReviewSendSnapshotLoading}
            retryBackoffSnapshotLoading={providerReviewRetryBackoffSnapshotLoading}
            responseSnapshotLoading={providerReviewResponseSnapshotLoading}
            resultRecordingSnapshotLoading={providerReviewResultRecordingSnapshotLoading}
            providerCallBoundarySnapshotLoading={providerReviewProviderCallBoundarySnapshotLoading}
            transactionSnapshotLoading={providerReviewTransactionSnapshotLoading}
            liveExecutionReadinessSnapshotLoading={providerReviewLiveExecutionReadinessSnapshotLoading}
            liveExecutionGuardSnapshotLoading={providerReviewLiveExecutionGuardSnapshotLoading}
            liveExecutionPreflightLoading={providerReviewLiveExecutionPreflightLoading}
            liveExecutionLaunchPlanLoading={providerReviewLiveExecutionLaunchPlanLoading}
            currentLiveReadinessSnapshotLoading={providerReviewCurrentLiveReadinessSnapshotLoading}
            currentLiveExecutionLaunchPlanLoading={providerReviewCurrentLiveExecutionLaunchPlanLoading}
            currentLiveExecutionGateLoading={providerReviewCurrentLiveExecutionGateLoading}
            armingSnapshotLoading={providerReviewArmingSnapshotLoading}
            snapshotResult={providerReviewSnapshotResult}
            credentialSnapshotResult={providerReviewCredentialSnapshotResult}
            branchPolicySnapshotResult={providerReviewBranchPolicySnapshotResult}
            runtimeSnapshotResult={providerReviewRuntimeSnapshotResult}
            adapterRehearsalSnapshotResult={providerReviewAdapterRehearsalSnapshotResult}
            adapterBlueprintSnapshotResult={providerReviewAdapterBlueprintSnapshotResult}
            liveAdapterContractSnapshotResult={providerReviewLiveAdapterContractSnapshotResult}
            invocationSnapshotResult={providerReviewInvocationSnapshotResult}
            executionLockSnapshotResult={providerReviewExecutionLockSnapshotResult}
            requestEnvelopeSnapshotResult={providerReviewRequestEnvelopeSnapshotResult}
            idempotencySnapshotResult={providerReviewIdempotencySnapshotResult}
            requestValidationSnapshotResult={providerReviewRequestValidationSnapshotResult}
            requestMaterializationSnapshotResult={providerReviewRequestMaterializationSnapshotResult}
            activationSnapshotResult={providerReviewActivationSnapshotResult}
            transportSnapshotResult={providerReviewTransportSnapshotResult}
            sendSnapshotResult={providerReviewSendSnapshotResult}
            retryBackoffSnapshotResult={providerReviewRetryBackoffSnapshotResult}
            responseSnapshotResult={providerReviewResponseSnapshotResult}
            resultRecordingSnapshotResult={providerReviewResultRecordingSnapshotResult}
            providerCallBoundarySnapshotResult={providerReviewProviderCallBoundarySnapshotResult}
            transactionSnapshotResult={providerReviewTransactionSnapshotResult}
            liveExecutionReadinessSnapshotResult={providerReviewLiveExecutionReadinessSnapshotResult}
            liveExecutionGuardSnapshotResult={providerReviewLiveExecutionGuardSnapshotResult}
            liveExecutionPreflightResult={providerReviewLiveExecutionPreflightResult}
            liveExecutionLaunchPlanResult={providerReviewLiveExecutionLaunchPlanResult}
            liveExecutionResult={providerReviewLiveExecutionResult}
            liveCleanupResult={providerReviewLiveCleanupResult}
            currentLiveReadinessSnapshotResult={providerReviewCurrentLiveReadinessSnapshotResult}
            currentLiveExecutionLaunchPlanResult={providerReviewCurrentLiveExecutionLaunchPlanResult}
            currentLiveExecutionGateResult={providerReviewCurrentLiveExecutionGateResult}
            armingSnapshotResult={providerReviewArmingSnapshotResult}
            optimisticallyClaimedAttemptID={optimisticallyClaimedProviderReviewAttemptID}
            optimisticallyRecordedAttemptID={optimisticallyRecordedProviderReviewAttemptID}
            optimisticallyLiveExecutedAttemptID={optimisticallyLiveExecutedProviderReviewAttemptID}
            optimisticallyLiveCleanedAttemptID={optimisticallyLiveCleanedProviderReviewAttemptID}
          />
          {approvalAudit.data?.approval && approvalStillActive(approvalAudit.data.approval) && canActOnApproval(approvalAudit.data.approval, currentRole) && (
            <Space wrap>
              <Input placeholder={t('ops.delegateToEmail')} value={delegateEmail} onChange={(event) => setDelegateEmail(event.target.value)} style={{ width: 240 }} />
              <Input placeholder={t('common.reason')} value={delegateReason} onChange={(event) => setDelegateReason(event.target.value)} style={{ width: 260 }} />
              <Button onClick={delegateApproval}>{t('ops.delegate')}</Button>
            </Space>
          )}
          <Typography.Title level={5}>{t('ops.delegations')}</Typography.Title>
          <Table<AnyRow> rowKey="id" size="small" dataSource={approvalAudit.data?.delegations || []} pagination={{ pageSize: 5 }} columns={[
            { title: t('common.from'), render: (_, row) => row.from_user_email || row.from_user_id || '-' },
            { title: t('common.to'), render: (_, row) => row.to_user_email || row.to_user_id || '-' },
            { title: t('common.reason'), dataIndex: 'reason' },
            { title: t('common.status'), render: (_, row) => <Tag color={row.revoked_at ? 'default' : 'green'}>{translatedValue(row.revoked_at ? 'revoked' : 'active', t)}</Tag> },
            { title: t('common.created'), dataIndex: 'created_at' },
            { title: t('common.action'), render: (_, row) => canRevokeApprovalDelegation(row, approvalAudit.data?.approval, me.data?.user, currentRole) ? <Button size="small" danger onClick={() => revokeDelegation(row.id)}>{t('common.revoke')}</Button> : '-' }
          ]} />
          <Typography.Title level={5}>{t('ops.decisions')}</Typography.Title>
          <Table<AnyRow> rowKey="id" size="small" dataSource={approvalAudit.data?.decisions || []} pagination={{ pageSize: 5 }} columns={[
            { title: t('common.decision'), render: (_, row) => <Tag color={row.decision === 'approved' ? 'green' : 'red'}>{translatedValue(row.decision, t)}</Tag> },
            { title: t('common.user'), render: (_, row) => row.user_email || row.user_id || '-' },
            { title: t('common.reason'), dataIndex: 'reason' },
            { title: t('ops.decided'), dataIndex: 'decided_at' }
          ]} />
          <Typography.Title level={5}>{t('ops.operation')}</Typography.Title>
          {approvalAudit.data?.operation ? <JSONBlock value={approvalAudit.data.operation} /> : <Typography.Text type="secondary">{t('ops.noOperationYet')}</Typography.Text>}
          <Typography.Title level={5}>{t('ops.workerJobs')}</Typography.Title>
          <Table<AnyRow> rowKey="id" size="small" dataSource={approvalAudit.data?.worker_jobs || []} pagination={{ pageSize: 5 }} columns={[
            { title: t('common.tool'), dataIndex: 'tool_name' },
            { title: t('common.status'), render: (_, row) => translatedValue(row.status, t) },
            { title: t('common.worker'), dataIndex: 'assigned_worker_node_id' },
            { title: t('common.error'), dataIndex: 'error' },
            { title: t('common.created'), dataIndex: 'created_at' },
            { title: t('common.finished'), dataIndex: 'finished_at' }
          ]} />
          <Typography.Title level={5}>{t('ops.operationLogs')}</Typography.Title>
          <Table<AnyRow> rowKey="id" size="small" dataSource={approvalAudit.data?.operation_logs || []} pagination={{ pageSize: 6 }} columns={[
            { title: t('common.level'), dataIndex: 'level' },
            { title: t('common.message'), dataIndex: 'message' },
            { title: t('common.fields'), render: (_, row) => <JSONBlock value={row.fields} /> },
            { title: t('common.created'), dataIndex: 'created_at' }
          ]} />
          <Typography.Title level={5}>{t('ops.runRecords')}</Typography.Title>
          <JSONBlock value={approvalAudit.data?.run_records || {}} />
        </Space>
      </Modal>
    </Space>
  );
}

function WorkerNodes() {
  const { t } = useI18n();
  const summary = useLoad(() => api('/api/worker-queue/summary'), []);
  const data = summary.data || {};
  const backend = data.backend_summary || {};
  const hasQueueRisk = (data.stale_nodes || 0) > 0 || (data.aged_queued_jobs || 0) > 0 || (data.stale_running_jobs || 0) > 0 || (data.failed_24h || 0) > 0;
  const queueRiskDescription = t('worker.queueRiskDescription')
    .replace('{staleNodes}', String(data.stale_nodes || 0))
    .replace('{agedQueuedJobs}', String(data.aged_queued_jobs || 0))
    .replace('{staleRunningJobs}', String(data.stale_running_jobs || 0))
    .replace('{failed24h}', String(data.failed_24h || 0));
  const olderThan15m = (count: any) => t('worker.olderThan15m').replace('{count}', String(count || 0));
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>{t('title.workerNodes')}</Typography.Title>
      <Alert showIcon message={t('worker.registrationHint')} />
      {summary.error && <Alert showIcon type="error" message={summary.error} />}
      {hasQueueRisk && <Alert showIcon type="warning" message={t('worker.queueNeedsAttention')} description={queueRiskDescription} />}
      {backend.message && (
        <Alert
          showIcon
          type="info"
          message={t('worker.queueBackend')}
          description={
            <Space direction="vertical" size={8}>
              <Typography.Text>{backend.message}</Typography.Text>
              <Space size={[8, 8]} wrap>
                <Tag color="blue">backend: {backend.backend}</Tag>
                <Tag color="geekblue">claiming: {backend.claiming}</Tag>
                <Tag color="orange">pub/sub: {backend.pubsub}</Tag>
                <Tag color="cyan">logs: {backend.log_fanout}</Tag>
              </Space>
            </Space>
          }
        />
      )}
      <div className="metricGrid">
        <Card loading={summary.loading}><Typography.Text type="secondary">{t('worker.onlineNodes')}</Typography.Text><Typography.Title level={4}>{data.online_nodes || 0}/{data.total_nodes || 0}</Typography.Title></Card>
        <Card loading={summary.loading}><Typography.Text type="secondary">{t('worker.queuedJobs')}</Typography.Text><Typography.Title level={4}>{data.queued_jobs || 0}</Typography.Title><Typography.Text type="secondary">{olderThan15m(data.aged_queued_jobs)}</Typography.Text></Card>
        <Card loading={summary.loading}><Typography.Text type="secondary">{t('worker.runningJobs')}</Typography.Text><Typography.Title level={4}>{data.running_jobs || 0}</Typography.Title><Typography.Text type="secondary">{olderThan15m(data.stale_running_jobs)}</Typography.Text></Card>
        <Card loading={summary.loading}><Typography.Text type="secondary">{t('worker.outcome24h')}</Typography.Text><Typography.Title level={4}>{data.completed_24h || 0}/{data.failed_24h || 0}</Typography.Title><Typography.Text type="secondary">{t('worker.completedFailed')}</Typography.Text></Card>
      </div>
      <Button type="primary" onClick={() => api('/api/worker-nodes/test-job', { method: 'POST', body: JSON.stringify({ message: 'hello node-worker' }) }).then(() => { message.success(t('worker.echoJobQueued')); summary.reload(); })}>{t('worker.queueEchoJob')}</Button>
      <Tabs
        items={[
          {
            key: 'queue',
            label: t('worker.queue'),
            children: (
              <Table<AnyRow> rowKey="tool_name" size="small" dataSource={data.queue_by_tool || []} pagination={false} columns={[
                { title: t('worker.tool'), dataIndex: 'tool_name' },
                { title: t('worker.queued'), dataIndex: 'queued' }
              ]} />
            )
          },
          {
            key: 'nodes',
            label: t('worker.nodeKinds'),
            children: (
              <Table<AnyRow> rowKey="kind" size="small" dataSource={data.nodes_by_kind || []} pagination={false} columns={[
                { title: t('worker.kind'), dataIndex: 'kind' },
                { title: t('common.count'), dataIndex: 'count' }
              ]} />
            )
          },
          {
            key: 'failures',
            label: t('worker.recentFailures'),
            children: (
              <Table<AnyRow> rowKey="id" size="small" dataSource={data.recent_failures || []} pagination={false} columns={[
                { title: t('worker.tool'), dataIndex: 'tool_name' },
                { title: t('common.error'), render: (_, row) => shortText(row.error || '-') },
                { title: t('common.updated'), dataIndex: 'updated_at' }
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
  const { t } = useI18n();
  const runtimes = useLoad(() => api('/api/ai-runtimes'), []);
  const credentials = useLoad(() => api('/api/connection-credentials'), []);
  const aiCredentialOptions = (credentials.data?.items || []).filter((row: AnyRow) => row.kind === 'ai_provider_api_key').map((row: AnyRow) => ({ value: row.id, label: `${row.name || row.id} · ${row.secret_configured ? t('common.configured') : t('common.missing')}` }));
  const [open, setOpen] = useState(false);
  const [credentialOpen, setCredentialOpen] = useState(false);
  async function createConnectionCredential(values: AnyRow) {
    await api('/api/connection-credentials', {
      method: 'POST',
      body: JSON.stringify({
        name: values.name,
        kind: 'ai_provider_api_key',
        secret_value: values.secret_value,
        public_value: values.public_value,
        metadata: {}
      })
    });
    message.success(t('form.createConnectionCredential'));
    credentials.reload();
  }
  async function createRuntime(values: AnyRow) {
    await api('/api/ai-runtimes', {
      method: 'POST',
      body: JSON.stringify({
        name: values.name,
        runtime_type: values.runtime_type,
        codex_binary: values.codex_binary,
        provider_type: values.provider_type,
        api_base_url: values.api_base_url,
        credential_id: values.credential_id,
        model: values.model,
        config: {}
      })
    });
    runtimes.reload();
  }
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="AI Runtime" onCreate={() => setOpen(true)} />
      <Space>
        <Button onClick={() => setCredentialOpen(true)}>{t('form.createConnectionCredential')}</Button>
      </Space>
      <Table<AnyRow> rowKey="id" dataSource={runtimes.data?.items || []} pagination={false} columns={[
        { title: t('common.name'), dataIndex: 'name' },
        { title: t('common.type'), render: (_, row) => translatedValue(row.runtime_type, t) },
        { title: t('field.ai_provider_type'), render: (_, row) => translatedValue(row.provider_type, t) },
        { title: t('field.api_base_url'), dataIndex: 'api_base_url' },
        { title: t('field.model'), dataIndex: 'model' },
        { title: t('common.credential'), render: (_, row) => row.credential_name ? <Tag color={row.credential_configured ? 'green' : 'gold'}>{row.credential_name}</Tag> : <Tag>{t('common.unbound')}</Tag> },
        { title: t('common.binary'), dataIndex: 'codex_binary' },
        { title: t('common.status'), render: (_, row) => <Tag>{translatedValue(row.status, t)}</Tag> },
        { title: t('common.action'), render: (_, row) => <Button size="small" onClick={() => api(`/api/ai-runtimes/${row.id}/verify`, { method: 'POST' }).then(runtimes.reload)}>{t('ai.verify')}</Button> }
      ]} />
      <CreateModal title="Create connection credential" open={credentialOpen} setOpen={setCredentialOpen} fields={[{ name: 'name', helpKey: 'help.name' }, 'secret_value', 'public_value']} initialValues={{ kind: 'ai_provider_api_key' }} onSubmit={createConnectionCredential} />
      <CreateModal title="Create AI runtime" open={open} setOpen={setOpen} descriptionKey="ai.runtimeDescription" fields={['name', 'runtime_type', 'codex_binary', { name: 'provider_type', input: 'select', options: ['openai', 'anthropic', 'openrouter', 'gemini', 'groq', 'azure_openai', 'custom', 'local'], labelKey: 'field.ai_provider_type', helpKey: 'help.ai_provider_type', required: false }, 'api_base_url', { name: 'credential_id', input: 'select', optionItems: aiCredentialOptions, helpKey: 'help.credential_id', required: false }, 'model']} initialValues={{ runtime_type: 'codex-cli', codex_binary: 'codex', provider_type: 'openai' }} onSubmit={createRuntime} />
    </Space>
  );
}

function AgentTasks() {
  const { t } = useI18n();
  const projects = useLoad(() => api('/api/projects'), []);
  const projectRows = projects.data?.items || [];
  const projectPick = useSelectedRow(projectRows);
  const project = projectPick.selected || projectRows[0];
  const tasks = useLoad(() => project ? api(`/api/projects/${project.id}/agent/tasks`) : Promise.resolve({ items: [] }), [project?.id]);
  const [open, setOpen] = useState(false);
  const [taskID, setTaskID] = useState<string>();
  const [toolAuditSnapshotLoading, setToolAuditSnapshotLoading] = useState(false);
  const [toolAuditSnapshotResult, setToolAuditSnapshotResult] = useState<AnyRow>();
  const [toolArmingSnapshotLoading, setToolArmingSnapshotLoading] = useState(false);
  const [toolArmingSnapshotResult, setToolArmingSnapshotResult] = useState<AnyRow>();
  const [codeAuditSnapshotLoading, setCodeAuditSnapshotLoading] = useState(false);
  const [codeAuditSnapshotResult, setCodeAuditSnapshotResult] = useState<AnyRow>();
  const taskDetail = useLoad(() => taskID ? api(`/api/agent/tasks/${taskID}`) : Promise.resolve(null), [taskID]);
  useEffect(() => {
    setToolAuditSnapshotResult(undefined);
    setToolArmingSnapshotResult(undefined);
    setCodeAuditSnapshotResult(undefined);
  }, [taskID]);
  async function createTask(values: AnyRow) {
    if (!project) {
      message.error(t('agent.selectProjectFirst'));
      return;
    }
    await api(`/api/projects/${project.id}/agent/tasks`, { method: 'POST', body: JSON.stringify(values) });
    message.success(t('agent.taskCreated'));
    tasks.reload();
  }
  async function generateAgentPlan(id: string) {
    await api(`/api/agent/tasks/${id}/generate-plan`, { method: 'POST', body: '{}' });
    message.success(t('agent.planGenerated'));
    tasks.reload();
    taskDetail.reload();
  }
  async function approveAgentPlan(id: string) {
    await api(`/api/agent/tasks/${id}/approve-plan`, { method: 'POST', body: '{}' });
    message.success(t('agent.planApproved'));
    tasks.reload();
    taskDetail.reload();
  }
  async function executeAgentTask(id: string) {
    const result = await api(`/api/agent/tasks/${id}/execute`, { method: 'POST', body: '{}' });
    message.success(result.approval ? t('agent.approvalRequested') : t('agent.executionQueued'));
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
        message.warning(result.message || t('agent.auditSnapshotNotReady'));
      } else {
        message.success(result.agent_tool_audit_snapshot_written ? t('agent.auditSnapshotRecorded') : t('agent.auditSnapshotCurrent'));
      }
    } catch (error: any) {
      setToolAuditSnapshotResult(undefined);
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setToolAuditSnapshotLoading(false);
    }
  }
  async function recordToolArmingSnapshot(id: string) {
    setToolArmingSnapshotLoading(true);
    try {
      const result = await api(`/api/agent/tasks/${id}/tool-arming-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setToolArmingSnapshotResult(result);
      taskDetail.reload();
      if (result.recording_ready === false) {
        message.warning(result.message || t('agent.toolArmingSnapshotNotReady'));
      } else {
        message.success(result.agent_tool_arming_snapshot_written ? t('agent.toolArmingSnapshotRecorded') : t('agent.toolArmingSnapshotCurrent'));
      }
    } catch (error: any) {
      setToolArmingSnapshotResult(undefined);
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setToolArmingSnapshotLoading(false);
    }
  }
  async function recordCodeAuditSnapshot(id: string) {
    setCodeAuditSnapshotLoading(true);
    try {
      const result = await api(`/api/agent/tasks/${id}/code-audit-snapshot`, { method: 'POST', body: JSON.stringify({}) });
      setCodeAuditSnapshotResult(result);
      taskDetail.reload();
      if (result.recording_ready === false) {
        message.warning(result.message || t('agent.codeAuditSnapshotNotReady'));
      } else {
        message.success(result.agent_code_audit_snapshot_written ? t('agent.codeAuditSnapshotRecorded') : t('agent.codeAuditSnapshotCurrent'));
      }
    } catch (error: any) {
      setCodeAuditSnapshotResult(undefined);
      message.error(error.message || t('common.requestFailed'));
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
      <Alert showIcon message={t('agent.guardrailHint')} />
      <Table<AnyRow> rowKey="id" dataSource={tasks.data?.items || []} pagination={{ pageSize: 8 }} columns={[
        { title: fieldLabel('title', t), dataIndex: 'title' },
        { title: t('common.status'), render: (_, row) => <Tag>{translatedValue(row.status, t)}</Tag> },
        { title: t('agent.latestPlan'), render: (_, row) => row.latest_plan_status ? <Tag color={row.latest_plan_status === 'approved' ? 'green' : 'blue'}>{translatedValue(row.latest_plan_status, t)}</Tag> : '-' },
        { title: t('common.created'), dataIndex: 'created_at' },
        { title: t('common.action'), render: (_, row) => <Space><Button size="small" onClick={() => setTaskID(row.id)}>{t('common.view')}</Button><Button size="small" onClick={() => generateAgentPlan(row.id)}>{t('common.generate')}</Button><Button size="small" onClick={() => executeAgentTask(row.id)} disabled={!latestPlanApproved(row)}>{t('value.execute')}</Button></Space> }
      ]} />
      <CreateModal title="Create agent task" open={open} setOpen={setOpen} fields={['title', 'prompt']} onSubmit={createTask} />
      <Modal title={taskDetail.data?.title || t('agent.agentTask')} open={Boolean(taskID)} onCancel={() => setTaskID(undefined)} footer={null} width={980} destroyOnHidden>
        {taskDetail.data && <Space direction="vertical" size={16} className="full">
          <Typography.Paragraph>{taskDetail.data.prompt}</Typography.Paragraph>
          <Space wrap>
            <Button size="small" type="primary" onClick={() => generateAgentPlan(taskDetail.data.id)}>{t('agent.generatePlan')}</Button>
            <Button size="small" onClick={() => approveAgentPlan(taskDetail.data.id)} disabled={!taskDetail.data.plans?.length}>{t('agent.approveLatest')}</Button>
            <Button size="small" onClick={() => executeAgentTask(taskDetail.data.id)} disabled={!latestPlanApproved(taskDetail.data)}>{t('value.execute')}</Button>
            <Button
              size="small"
              onClick={() => recordToolAuditSnapshot(taskDetail.data.id)}
              loading={toolAuditSnapshotLoading}
              disabled={!taskDetail.data.tool_call_audit_evidence?.sanitized_result_recorded}
            >
              {t('agent.recordAuditSnapshot')}
            </Button>
            <Button
              size="small"
              onClick={() => recordToolArmingSnapshot(taskDetail.data.id)}
              loading={toolArmingSnapshotLoading}
              disabled={!taskDetail.data.tool_call_audit_evidence?.sanitized_result_recorded}
            >
              {t('agent.recordArmingSnapshot')}
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
              {t('agent.recordCodeAuditSnapshot')}
            </Button>
          </Space>
          <Table<AnyRow> rowKey="id" size="small" dataSource={taskDetail.data.plans || []} pagination={{ pageSize: 4 }} columns={[
            { title: t('common.status'), render: (_, row) => <Tag color={row.status === 'approved' ? 'green' : 'blue'}>{translatedValue(row.status, t)}</Tag> },
            { title: t('common.created'), dataIndex: 'created_at' },
            { title: t('value.plan'), render: (_, row) => <Typography.Paragraph className="mono-pre">{row.content}</Typography.Paragraph> }
          ]} />
          <Typography.Title level={5}>{t('agent.toolCallAudit')}</Typography.Title>
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
          {toolArmingSnapshotResult ? (
            <Space wrap>
              <Tag color={toolArmingSnapshotResult.recording_state === 'ready_for_operator_review' ? 'green' : toolArmingSnapshotResult.recording_state === 'asset_missing' ? 'red' : 'gold'}>arming snapshot {toolArmingSnapshotResult.recording_state || 'pending'}</Tag>
              <Tag>{toolArmingSnapshotResult.asset_status_snapshot_written ? 'asset status written' : 'asset status unchanged'}</Tag>
              <Tag>{toolArmingSnapshotResult.arming_ready_for_operator_review ? 'operator review ready' : 'arming blocked'}</Tag>
              <Tag>{toolArmingSnapshotResult.tool_review_ready_for_operator ? 'tool review ready' : 'tool review blocked'}</Tag>
              <Tag>{toolArmingSnapshotResult.tool_invocation_enabled ? 'tools enabled' : 'tools blocked'}</Tag>
              <Tag>{toolArmingSnapshotResult.raw_tool_output_recorded ? 'raw output recorded' : 'no raw output'}</Tag>
              <Tag>{toolArmingSnapshotResult.secret_included ? 'secret included' : 'no secrets'}</Tag>
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
            { title: t('worker.tool'), dataIndex: 'tool_name' },
            { title: t('common.status'), render: (_, row) => <Tag color={row.status === 'completed' ? 'green' : row.status === 'failed' ? 'red' : 'blue'}>{translatedValue(row.status, t)}</Tag> },
            { title: t('common.summary'), render: (_, row) => toolCallSummary(row) },
            { title: t('value.operation'), dataIndex: 'operation_run_id', render: (value) => value || '-' },
            { title: t('common.error'), dataIndex: 'error_message', render: (value) => value || '-' },
            { title: t('common.input'), render: (_, row) => <JSONBlock value={row.input} /> },
            { title: t('common.output'), render: (_, row) => <JSONBlock value={row.output} /> },
            { title: t('common.created'), dataIndex: 'created_at' }
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

function buildDeploymentPosture(targets: AnyRow[], records: AnyRow[], rollbackPoints: AnyRow[], t: (key: string) => string) {
  const unhealthyTargets = targets.filter((row) => deploymentStatusUnhealthy(row.status)).length;
  const environments = new Set([
    ...targets.map((row) => String(row.environment || '').trim()).filter(Boolean),
    ...records.map((row) => String(row.environment || '').trim()).filter(Boolean)
  ]).size;
  const availableRollbacks = rollbackPoints.filter((row) => String(row.rollback_readiness || '').toLowerCase() === 'previewable').length;
  const latestRecord = records[0];
  const summary = targets.length === 0
    ? t('deploy.noTargets')
    : unhealthyTargets > 0
      ? `${unhealthyTargets} ${t('deploy.targetsNeedAttention')}`
      : latestRecord
        ? `${t('deploy.latestObserved')}: ${latestRecord.name || latestRecord.deployment_target_name || t('common.unknown')}`
        : t('deploy.targetsHealthy');
  return {
    targets: targets.length,
    unhealthy: unhealthyTargets,
    environments,
    rollbackPoints: availableRollbacks,
    summary
  };
}

function buildRollbackGuardrail(rollbackPoints: AnyRow[], t: (key: string) => string) {
  if (!rollbackPoints.length) return null;
  const previewable = rollbackPoints.filter((row) => String(row.rollback_readiness || '').toLowerCase() === 'previewable').length;
  const executable = rollbackPoints.filter((row) => row.rollback_executable === true).length;
  const mode = String(rollbackPoints[0]?.rollback_execution_mode || '').trim() || 'read_only_preview';
  return {
    type: executable > 0 ? 'warning' as const : 'info' as const,
    message: executable > 0
      ? `${executable} ${t('deploy.rollbackExecutable')}`
      : t('deploy.rollbackDisabled'),
    description: executable > 0
      ? `${t('deploy.rollbackExecutableDescription')} ${t('value.execution')}: ${translatedValue(mode, t)}.`
      : previewable > 0
        ? `${previewable} ${t('deploy.rollbackPreviewablePrefix')} ${translatedValue(mode, t)}; ${t('deploy.rollbackPreviewableSuffix')}`
        : t('deploy.rollbackNonePreviewable')
  };
}

function buildDeploymentExecutionGuardrail(targets: AnyRow[], t: (key: string) => string) {
  if (!targets.length) return null;
  const planned = targets.filter((row) => row.deployment_execution_readiness?.status === 'planned').length;
  const blocked = targets.filter((row) => row.deployment_execution_readiness?.status === 'blocked').length;
  return {
    type: planned > 0 && blocked === 0 ? 'info' as const : 'warning' as const,
    message: t('deploy.executionDryRunOnly'),
    description: planned > 0
      ? `${planned} ${t('deploy.prereqPlanned')}`
      : `${blocked} ${t('deploy.metadataReviewNeeded')}`
  };
}

function deploymentExecutionReadinessView(row: AnyRow, t: (key: string) => string) {
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
        <Tag color={status === 'planned' ? 'blue' : status === 'blocked' ? 'red' : 'default'}>{translatedValue(status, t)}</Tag>
        <Tag>{translatedValue(readiness.mode || 'dry_run', t)}</Tag>
        <Tag>{readiness.execution_enabled === true ? t('value.execution_enabled') : t('value.execution_disabled')}</Tag>
      </Space>
      {executionPlan.mode ? (
        <Space size={4} wrap>
          <Tag>{translatedValue(executionPlan.mode, t)}</Tag>
          <Tag color={executionPlan.plan_state === 'blocked' ? 'red' : 'blue'}>{t('value.plan')} {translatedValue(executionPlan.plan_state || 'blocked', t)}</Tag>
          <Tag color={executionPlan.prerequisite_state === 'planned' ? 'blue' : 'gold'}>{t('value.prereq')} {translatedValue(executionPlan.prerequisite_state || 'blocked', t)}</Tag>
          <Tag>{t('value.controls')} {requiredControls.length}</Tag>
          <Tag>{t('value.disabled_backends')} {disabledBackends.length}</Tag>
          <Tag>{t('value.suppressed')} {suppressedFields.length}</Tag>
          <Tag>{executionPlan.kubernetes_api_call_made === true ? t('value.k8s_called') : t('value.no_k8s_call')}</Tag>
          <Tag>{executionPlan.helm_command_invoked === true ? t('value.helm_invoked') : t('value.helm_disabled')}</Tag>
          <Tag>{translatedValue(executionPlan.deployment_mutation || 'disabled', t)}</Tag>
        </Space>
      ) : null}
      {reasons.length ? <Typography.Text type="secondary">{shortText(String(reasons[0]), 72)}</Typography.Text> : <Typography.Text type="secondary">{shortText(String(readiness.message || ''), 72)}</Typography.Text>}
    </Space>
  );
}

function deploymentExecutionGateView(gate: AnyRow, t: (key: string) => string) {
  const plan = gate.execution_plan || {};
  const disabledBackends = Array.isArray(gate.disabled_backends) ? gate.disabled_backends : [];
  const suppressedFields = Array.isArray(gate.suppressed_fields) ? gate.suppressed_fields : [];
  return (
    <Space direction="vertical" size={2}>
      <Space size={4} wrap>
        <Tag color={gate.execution_gate_ready === true ? 'green' : 'red'}>{translatedValue(gate.execution_gate_state || 'gate_blocked', t)}</Tag>
        <Tag color={gate.readiness_state === 'planned' ? 'blue' : 'gold'}>{t('value.readiness')} {translatedValue(gate.readiness_state || 'blocked', t)}</Tag>
        <Tag>{gate.target_metadata_ready ? t('value.metadata_ready') : t('value.metadata_blocked')}</Tag>
        <Tag>{gate.kubernetes_api_call_made ? t('value.k8s_called') : t('value.no_k8s_call')}</Tag>
        <Tag>{gate.helm_command_invoked ? t('value.helm_invoked') : t('value.helm_disabled')}</Tag>
        <Tag>{gate.rollout_started ? t('value.rollout_started') : t('value.no_rollout')}</Tag>
        <Tag>{translatedValue(gate.deployment_mutation || 'disabled', t)}</Tag>
        <Tag>{disabledBackends.length || plan.disabled_backends?.length || 0} {t('value.disabled_backends')}</Tag>
        <Tag>{suppressedFields.length || plan.suppressed_fields?.length || 0} {t('value.suppressed')}</Tag>
        <Tag>{gate.secret_included || gate.kubeconfig_included ? t('value.sensitive_material_present') : t('value.no_secrets_kubeconfig')}</Tag>
      </Space>
      {gate.message ? <Typography.Text type="secondary">{shortText(String(gate.message), 96)}</Typography.Text> : null}
    </Space>
  );
}

function rollbackExecutionPlanView(row: AnyRow, t: (key: string) => string) {
  const plan = row.rollback_execution_plan || {};
  const requiredControls = Array.isArray(plan.required_controls) ? plan.required_controls : [];
  const disabledBackends = Array.isArray(plan.disabled_backends) ? plan.disabled_backends : [];
  const suppressedFields = Array.isArray(plan.suppressed_fields) ? plan.suppressed_fields : [];
  if (!plan.mode) {
    return <Tag>{translatedValue(row.rollback_execution_mode || 'read_only_preview', t)}</Tag>;
  }
  return (
    <Space direction="vertical" size={2}>
      <Space size={4} wrap>
        <Tag>{translatedValue(plan.mode, t)}</Tag>
        <Tag color={plan.plan_state === 'blocked' ? 'red' : 'blue'}>{t('value.plan')} {translatedValue(plan.plan_state || 'blocked', t)}</Tag>
        <Tag color={plan.prerequisite_state === 'metadata_available' ? 'blue' : 'gold'}>{t('value.metadata')} {translatedValue(plan.prerequisite_state || 'metadata_blocked', t)}</Tag>
        <Tag>{t('value.controls')} {requiredControls.length}</Tag>
        <Tag>{t('value.disabled_backends')} {disabledBackends.length}</Tag>
        <Tag>{t('value.suppressed')} {suppressedFields.length}</Tag>
      </Space>
      <Space size={4} wrap>
        <Tag>{plan.kubernetes_api_call_made === true ? t('value.k8s_called') : t('value.no_k8s_call')}</Tag>
        <Tag>{plan.helm_command_invoked === true ? t('value.helm_invoked') : t('value.helm_disabled')}</Tag>
        <Tag>{translatedValue(plan.rollback_mutation || 'disabled', t)}</Tag>
      </Space>
    </Space>
  );
}

function rollbackExecutionGateView(gate: AnyRow, t: (key: string) => string) {
  const plan = gate.rollback_execution_plan || {};
  const disabledBackends = Array.isArray(gate.disabled_backends) ? gate.disabled_backends : [];
  const suppressedFields = Array.isArray(gate.suppressed_fields) ? gate.suppressed_fields : [];
  return (
    <Space direction="vertical" size={2}>
      <Space size={4} wrap>
        <Tag color={gate.execution_gate_ready === true ? 'green' : 'red'}>{translatedValue(gate.execution_gate_state || 'gate_blocked', t)}</Tag>
        <Tag color={gate.readiness_state === 'previewable' ? 'blue' : 'gold'}>{t('value.readiness')} {translatedValue(gate.readiness_state || 'blocked', t)}</Tag>
        <Tag>{gate.revision_metadata_ready ? t('value.revision_metadata') : t('value.revision_missing')}</Tag>
        <Tag>{gate.target_metadata_ready ? t('value.target_metadata') : t('value.target_missing')}</Tag>
        <Tag>{gate.kubernetes_api_call_made ? t('value.k8s_called') : t('value.no_k8s_call')}</Tag>
        <Tag>{gate.helm_command_invoked ? t('value.helm_invoked') : t('value.helm_disabled')}</Tag>
        <Tag>{gate.rollback_started ? t('value.rollback_started') : t('value.no_rollback')}</Tag>
        <Tag>{translatedValue(gate.rollback_mutation || 'disabled', t)}</Tag>
        <Tag>{disabledBackends.length || plan.disabled_backends?.length || 0} {t('value.disabled_backends')}</Tag>
        <Tag>{suppressedFields.length || plan.suppressed_fields?.length || 0} {t('value.suppressed')}</Tag>
        <Tag>{gate.secret_included || gate.kubeconfig_included || gate.revision_value_included ? t('value.sensitive_material_present') : t('value.no_secrets_revision')}</Tag>
      </Space>
      {gate.message ? <Typography.Text type="secondary">{shortText(String(gate.message), 96)}</Typography.Text> : null}
    </Space>
  );
}

function deploymentStatusUnhealthy(status: any) {
  const value = String(status || '').toLowerCase();
  return ['failed', 'error', 'degraded', 'outofsync', 'missing', 'unknown'].includes(value);
}

function ConfigPage() {
  const { t } = useI18n();
  const projects = useLoad(() => api('/api/projects'), []);
  const projectRows = projects.data?.items || [];
  const projectPick = useSelectedRow(projectRows);
  const project = projectPick.selected;
  const [argoOpen, setArgoOpen] = useState(false);
  const [argoEdit, setArgoEdit] = useState<AnyRow | null>(null);
  const [credentialOpen, setCredentialOpen] = useState(false);
  const [argoSyncOpID, setArgoSyncOpID] = useState<string>();
  const [podLogForm] = Form.useForm();
  const [podLogPreview, setPodLogPreview] = useState<AnyRow>();
  const [podLogLoading, setPodLogLoading] = useState(false);
  const [podListLoading, setPodListLoading] = useState(false);
  const [podListResult, setPodListResult] = useState<AnyRow>();
  const [podLogRunLoading, setPodLogRunLoading] = useState(false);
  const [podLogRunResult, setPodLogRunResult] = useState<AnyRow>();
  const [podRestartLoading, setPodRestartLoading] = useState(false);
  const [podRestartResult, setPodRestartResult] = useState<AnyRow>();
  const [podLogSnapshotLoading, setPodLogSnapshotLoading] = useState(false);
  const [podLogSnapshotResult, setPodLogSnapshotResult] = useState<AnyRow>();
  const selectedPodName = Form.useWatch('pod_name', podLogForm);
  const selectedDeploymentName = Form.useWatch('deployment_name', podLogForm);
  const selectedDeploymentTargetID = Form.useWatch('deployment_target_id', podLogForm);
  const [kubernetesEnvironmentOpen, setKubernetesEnvironmentOpen] = useState(false);
  const [kubernetesEnvironmentForm] = Form.useForm();
  const [kubernetesImportOpen, setKubernetesImportOpen] = useState(false);
  const [kubernetesImportForm] = Form.useForm();
  const [kubernetesImportPreview, setKubernetesImportPreview] = useState<AnyRow>();
  const [kubernetesImportLoading, setKubernetesImportLoading] = useState(false);
  const [argoImportOpen, setArgoImportOpen] = useState(false);
  const [argoImportForm] = Form.useForm();
  const [argoImportPreview, setArgoImportPreview] = useState<AnyRow>();
  const [argoImportLoading, setArgoImportLoading] = useState(false);
  const [deploymentExecutionGateLoadingID, setDeploymentExecutionGateLoadingID] = useState('');
  const [deploymentExecutionGateResults, setDeploymentExecutionGateResults] = useState<Record<string, AnyRow>>({});
  const [rollbackExecutionGateLoadingID, setRollbackExecutionGateLoadingID] = useState('');
  const [rollbackExecutionGateResults, setRollbackExecutionGateResults] = useState<Record<string, AnyRow>>({});
  const [sshOpen, setSSHOpen] = useState(false);
  const [sshEdit, setSSHEdit] = useState<AnyRow | null>(null);
  const [commandOpen, setCommandOpen] = useState(false);
  const [sshSnapshotLoading, setSSHSnapshotLoading] = useState(false);
  const [sshSnapshotResult, setSSHSnapshotResult] = useState<AnyRow>();
  const [sshProofLoading, setSSHProofLoading] = useState(false);
  const [sshProofResult, setSSHProofResult] = useState<AnyRow>();
  const credentials = useLoad(() => project ? api(`/api/projects/${project.id}/connection-credentials`) : Promise.resolve({ items: [] }), [project?.id]);
  const credentialRows = credentials.data?.items || [];
  const credentialOptionLabel = (row: AnyRow) => `${row.name || row.id} · ${t(`option.${row.kind}`)} · ${row.secret_configured ? t('common.configured') : t('common.missing')}`;
  const argoCredentialOptions = credentialRows.filter((row: AnyRow) => row.kind === 'argo_token').map((row: AnyRow) => ({ value: row.id, label: credentialOptionLabel(row) }));
  const sshCredentialOptions = credentialRows.filter((row: AnyRow) => row.kind === 'ssh_key' || row.kind === 'ssh_password').map((row: AnyRow) => ({ value: row.id, label: credentialOptionLabel(row) }));
  const argoConnections = useLoad(() => project ? api(`/api/projects/${project.id}/argo/connections`) : Promise.resolve({ items: [] }), [project?.id]);
  const argoRows = argoConnections.data?.items || [];
  const argoPick = useSelectedRow(argoRows);
  const argoApps = useLoad(() => project ? api(`/api/projects/${project.id}/argo/apps`) : Promise.resolve({ items: [] }), [project?.id]);
  const kubernetesEnvironments = useLoad(() => project ? api(`/api/projects/${project.id}/kubernetes/environments`) : Promise.resolve({ items: [] }), [project?.id]);
  const kubernetesEnvironmentRows = kubernetesEnvironments.data?.items || [];
  const kubernetesEnvironmentPick = useSelectedRow(kubernetesEnvironmentRows);
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
    target_environment_proof_registration: sshRehearsal.data.target_environment_proof_registration,
    target_environment_proof_registered: sshRehearsal.data.target_environment_proof_registered,
    target_environment_proof_state: sshRehearsal.data.target_environment_proof_state,
    target_environment_proof_registered_at: sshRehearsal.data.target_environment_proof_registered_at,
    operator_approved_proof_recorded: sshRehearsal.data.operator_approved_proof_recorded,
    required_live_rehearsal: sshRehearsal.data.required_live_rehearsal,
    required_controls: sshRehearsal.data.required_controls,
    steps: sshRehearsal.data.steps,
    recent_evidence: sshRehearsal.data.recent_evidence
  } : null;
  const sshRuns = useLoad(() => project ? api(`/api/ssh-command-runs?project_id=${project.id}`) : Promise.resolve({ items: [] }), [project?.id]);
  useEffect(() => {
    setSSHSnapshotResult(undefined);
    setSSHProofResult(undefined);
    setKubernetesImportPreview(undefined);
  }, [sshPick.selectedID]);
  useEffect(() => {
    setArgoImportPreview(undefined);
  }, [kubernetesEnvironmentPick.selectedID]);
  useEffect(() => {
    setDeploymentExecutionGateLoadingID('');
    setDeploymentExecutionGateResults({});
    setRollbackExecutionGateLoadingID('');
    setRollbackExecutionGateResults({});
  }, [project?.id]);
  const deploymentPosture = buildDeploymentPosture(
    deploymentTargets.data?.items || [],
    deploymentRecords.data?.items || [],
    rollbackPoints.data?.items || [],
    t
  );
  const podListItems = Array.isArray(podListResult?.items) ? podListResult.items : [];
  const podOptions = podListItems.map((pod: AnyRow) => ({
    value: pod.name,
    label: `${pod.name} · ${translatedValue(pod.phase || 'unknown', t)} · ${pod.ready_containers || 0}/${pod.container_count || 0}`
  }));
  const selectedPodMetadata = podListItems.find((pod: AnyRow) => pod.name === selectedPodName);
  const containerOptions = (Array.isArray(selectedPodMetadata?.containers) ? selectedPodMetadata.containers : [])
    .map((container: string) => ({ value: container, label: container }));
  const rollbackGuardrail = buildRollbackGuardrail(rollbackPoints.data?.items || [], t);
  const deploymentExecutionGuardrail = buildDeploymentExecutionGuardrail(deploymentTargets.data?.items || [], t);
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
          message.success(t('config.argoAppsSynced'));
          setArgoSyncOpID(undefined);
          argoConnections.reload();
          argoApps.reload();
          deploymentTargets.reload();
          deploymentRecords.reload();
          rollbackPoints.reload();
        } else if (op.status === 'failed' || op.status === 'canceled') {
          if (!alive) return;
          message.error(op.error || t('config.argoAppSyncFailed'));
          setArgoSyncOpID(undefined);
          argoConnections.reload();
        } else if (attempts >= 150) {
          if (!alive) return;
          message.warning(t('config.argoSyncStillRunning'));
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
        credential_id: values.credential_id,
        config: {
          insecure_skip_verify: values.insecure_skip_verify === true || values.insecure_skip_verify === 'true'
        }
      })
    });
    argoConnections.reload();
  }
  async function updateArgoConnection(values: AnyRow) {
    if (!argoEdit?.id) return;
    await api(`/api/argo/connections/${argoEdit.id}`, {
      method: 'PATCH',
      body: JSON.stringify({
        name: values.name,
        server_url: values.server_url,
        auth_type: values.auth_type || values.argo_auth_type || 'token',
        credential_id: values.credential_id,
        config: {
          ...(argoEdit.config || {}),
          insecure_skip_verify: values.insecure_skip_verify === true || values.insecure_skip_verify === 'true'
        }
      })
    });
    message.success(t('config.argoConnectionSaved'));
    setArgoEdit(null);
    argoConnections.reload();
    argoApps.reload();
    deploymentTargets.reload();
    deploymentRecords.reload();
    rollbackPoints.reload();
  }
  async function deleteArgoConnection(row: AnyRow) {
    if (!row?.id) return;
    Modal.confirm({
      title: t('config.deleteArgoConnectionConfirm'),
      okText: t('common.delete'),
      cancelText: t('common.cancel'),
      okButtonProps: { danger: true },
      onOk: async () => {
        await api(`/api/argo/connections/${row.id}`, { method: 'DELETE' });
        message.success(t('config.argoConnectionDeleted'));
        if (argoPick.selectedID === row.id) argoPick.setSelectedID(undefined);
        argoConnections.reload();
        argoApps.reload();
        deploymentTargets.reload();
        deploymentRecords.reload();
        rollbackPoints.reload();
      }
    });
  }
  async function createConnectionCredential(values: AnyRow) {
    if (!project) return;
    await api(`/api/projects/${project.id}/connection-credentials`, {
      method: 'POST',
      body: JSON.stringify({
        name: values.name,
        kind: values.kind,
        secret_value: values.secret_value,
        public_value: values.public_value,
        metadata: {}
      })
    });
    credentials.reload();
  }
  async function createKubernetesEnvironment(values: AnyRow) {
    if (!project) return;
    await api(`/api/projects/${project.id}/kubernetes/environments`, {
      method: 'POST',
      body: JSON.stringify({
        name: values.name,
        environment: values.environment,
        cluster_name: values.cluster_name,
        namespace: values.namespace,
        kubeconfig_secret_ref: values.kubeconfig_secret_ref,
        service_account: values.service_account,
        token_subject_review_status: values.token_subject_review_status || 'not_reviewed',
        rbac_read_logs_status: values.rbac_read_logs_status || 'not_reviewed',
        rbac_restart_pods_status: values.rbac_restart_pods_status || 'not_reviewed',
        status: values.status || 'metadata_only',
        metadata: {}
      })
    });
    message.success(t('config.kubernetesEnvSaved'));
    setKubernetesEnvironmentOpen(false);
    kubernetesEnvironmentForm.resetFields();
    kubernetesEnvironments.reload();
    deploymentTargets.reload();
    if (podLogPreview) {
      const target = podLogPreview.deployment_target || {};
      const query = podLogPreview.query || {};
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
    }
  }
  async function openKubernetesImport() {
    if (!sshPick.selectedID) {
      message.error(t('config.selectSSHMachine'));
      return;
    }
    setKubernetesImportOpen(true);
    setKubernetesImportLoading(true);
    setKubernetesImportPreview(undefined);
    try {
      const result = await api(`/api/ssh-machines/${sshPick.selectedID}/kubernetes/import-preview`, { method: 'POST', body: '{}' });
      setKubernetesImportPreview(result);
      kubernetesImportForm.setFieldsValue(result.suggested_environment || {});
      if (result.status === 'ok') message.success(t('config.kubernetesImportReady'));
      else message.warning((result.discovery?.blocked_reasons || []).join(', ') || t('common.requestFailed'));
    } catch (error: any) {
      setKubernetesImportPreview(error.payload || { status: 'failed', error: error.message });
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setKubernetesImportLoading(false);
    }
  }
  async function importKubernetesFromSSH(values: AnyRow) {
    if (!sshPick.selectedID) return;
    setKubernetesImportLoading(true);
    try {
      const result = await api(`/api/ssh-machines/${sshPick.selectedID}/kubernetes/import`, {
        method: 'POST',
        body: JSON.stringify({
          name: values.name,
          environment: values.environment,
          kubeconfig_secret_ref: values.kubeconfig_secret_ref,
          service_account: values.service_account,
          status: values.status || 'metadata_only'
        })
      });
      setKubernetesImportPreview(result);
      message.success(t('config.kubernetesImportSaved'));
      setKubernetesImportOpen(false);
      kubernetesImportForm.resetFields();
      kubernetesEnvironments.reload();
      deploymentTargets.reload();
    } catch (error: any) {
      setKubernetesImportPreview(error.payload || { status: 'failed', error: error.message, blocked_reasons: [error.message || t('common.requestFailed')] });
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setKubernetesImportLoading(false);
    }
  }
  async function openArgoImport() {
    if (!kubernetesEnvironmentPick.selectedID) {
      message.error(t('form.addKubernetesEnvironment'));
      return;
    }
    setArgoImportOpen(true);
    setArgoImportLoading(true);
    setArgoImportPreview(undefined);
    try {
      const result = await api(`/api/kubernetes/environments/${kubernetesEnvironmentPick.selectedID}/argo/import-preview`, { method: 'POST', body: '{}' });
      setArgoImportPreview(result);
      const candidates = Array.isArray(result.candidates) ? result.candidates : [];
      const firstURL = candidates.find((item: AnyRow) => item.url)?.url || '';
      argoImportForm.setFieldsValue({ name: `${kubernetesEnvironmentPick.selected?.name || 'Kubernetes'} Argo CD`, server_url: firstURL, credential_id: argoCredentialOptions[0]?.value, insecure_skip_verify: false });
      if (result.status === 'ok') message.success(t('config.argoImportReady'));
      else message.warning((result.blocked_reasons || []).join(', ') || t('common.requestFailed'));
    } catch (error: any) {
      setArgoImportPreview(error.payload || { status: 'failed', error: error.message });
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setArgoImportLoading(false);
    }
  }
  async function importArgoFromKubernetes(values: AnyRow) {
    if (!kubernetesEnvironmentPick.selectedID) return;
    setArgoImportLoading(true);
    try {
      const result = await api(`/api/kubernetes/environments/${kubernetesEnvironmentPick.selectedID}/argo/import`, {
        method: 'POST',
        body: JSON.stringify({
          name: values.name,
          server_url: values.server_url,
          credential_id: values.credential_id,
          config: { insecure_skip_verify: values.insecure_skip_verify === true || values.insecure_skip_verify === 'true' }
        })
      });
      setArgoImportPreview(result);
      message.success(t('config.argoImportSaved'));
      setArgoImportOpen(false);
      argoImportForm.resetFields();
      argoConnections.reload();
    } catch (error: any) {
      setArgoImportPreview(error.payload || { status: 'failed', error: error.message, blocked_reasons: [error.message || t('common.requestFailed')] });
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setArgoImportLoading(false);
    }
  }
  async function syncArgoApps() {
    if (!argoPick.selectedID) {
      message.error(t('config.selectArgoConnection'));
      return;
    }
    try {
      const op = await api(`/api/argo/connections/${argoPick.selectedID}/apps/sync`, { method: 'POST', body: '{}' });
      setArgoSyncOpID(op.id);
      message.success(t('config.argoSyncQueued'));
      argoConnections.reload();
    } catch (error: any) {
      message.error(error.message);
    }
  }
  async function checkDeploymentExecutionGate(targetID: string) {
    if (!targetID || deploymentExecutionGateLoadingID) return;
    setDeploymentExecutionGateLoadingID(targetID);
    try {
      const result = await api(`/api/deployment-targets/${targetID}/execution-gate`, { method: 'POST', body: '{}' });
      setDeploymentExecutionGateResults((current) => ({ ...current, [targetID]: result }));
      message.warning(result.message || t('config.deploymentGateBlocked'));
    } catch (error: any) {
      message.error(error.message || t('config.deploymentGateFailed'));
    } finally {
      setDeploymentExecutionGateLoadingID('');
    }
  }
  async function checkRollbackExecutionGate(rollbackPointID: string) {
    if (!rollbackPointID || rollbackExecutionGateLoadingID) return;
    setRollbackExecutionGateLoadingID(rollbackPointID);
    try {
      const result = await api(`/api/rollback-points/${rollbackPointID}/execution-gate`, { method: 'POST', body: '{}' });
      setRollbackExecutionGateResults((current) => ({ ...current, [rollbackPointID]: result }));
      message.warning(result.message || t('config.rollbackGateBlocked'));
    } catch (error: any) {
      message.error(error.message || t('config.rollbackGateFailed'));
    } finally {
      setRollbackExecutionGateLoadingID('');
    }
  }
  async function refreshPodList(targetID?: string) {
    const selectedTargetID = targetID || podLogForm.getFieldValue('deployment_target_id');
    if (!selectedTargetID) {
      message.error(t('config.selectDeploymentTarget'));
      return;
    }
    setPodListLoading(true);
    try {
      const result = await api(`/api/deployment-targets/${selectedTargetID}/pods`, { method: 'POST', body: '{}' });
      setPodListResult(result);
      const items = Array.isArray(result.items) ? result.items : [];
      if (items.length > 0) {
        const currentPodName = podLogForm.getFieldValue('pod_name');
        const selectedPod = items.find((item: AnyRow) => item.name === currentPodName) || items[0];
        const firstContainer = Array.isArray(selectedPod.containers) ? selectedPod.containers[0] : undefined;
        podLogForm.setFieldsValue({
          pod_name: selectedPod.name,
          container_name: podLogForm.getFieldValue('container_name') || firstContainer
        });
        message.success(t('pod.listReady'));
      } else if (result.backend_state === 'blocked' || result.backend_state === 'disabled') {
        message.warning(result.message || t('pod.listBlocked'));
      } else {
        message.warning(result.message || t('pod.listFailed'));
      }
    } catch (error: any) {
      setPodListResult(undefined);
      message.error(error.message || t('pod.listFailed'));
    } finally {
      setPodListLoading(false);
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
      message.success(t('config.podLogPreviewReady'));
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
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
      message.success(result.approval ? t('config.podLogApprovalRequested') : t('config.podLogAuditQueued'));
    } catch (error: any) {
      setPodLogRunResult(undefined);
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setPodLogRunLoading(false);
    }
  }
  async function requestPodRestart() {
    if (!project) return;
    const deploymentTargetID = podLogForm.getFieldValue('deployment_target_id');
    const deploymentName = podLogForm.getFieldValue('deployment_name');
    if (!deploymentTargetID || !deploymentName) {
      message.error(t('common.required'));
      return;
    }
    setPodRestartLoading(true);
    try {
      const result = await api(`/api/projects/${project.id}/argo/pod-restarts`, {
        method: 'POST',
        body: JSON.stringify({
          deployment_target_id: deploymentTargetID,
          deployment_name: deploymentName
        })
      });
      setPodRestartResult(result);
      message.success(t('pod.restartRequested'));
    } catch (error: any) {
      setPodRestartResult(undefined);
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setPodRestartLoading(false);
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
        message.warning(result.message || t('config.podLogSnapshotNotReady'));
      } else {
        message.success(result.pod_log_audit_snapshot_written ? t('config.podLogSnapshotRecorded') : t('config.podLogSnapshotCurrent'));
      }
    } catch (error: any) {
      setPodLogSnapshotResult(undefined);
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setPodLogSnapshotLoading(false);
    }
  }
  async function createSSHMachine(values: AnyRow) {
    if (!project) return;
    await api(`/api/projects/${project.id}/ssh-machines`, {
      method: 'POST',
      body: JSON.stringify({
        name: values.name,
        host: values.host,
        port: Number(values.port || 22),
        username: values.username,
        auth_type: values.auth_type || values.ssh_auth_type || 'key',
        credential_id: values.credential_id,
        metadata: {}
      })
    });
    ssh.reload();
  }
  async function updateSSHMachine(values: AnyRow) {
    if (!sshEdit?.id) return;
    await api(`/api/ssh-machines/${sshEdit.id}`, {
      method: 'PATCH',
      body: JSON.stringify({
        name: values.name,
        host: values.host,
        port: Number(values.port || 22),
        username: values.username,
        auth_type: values.auth_type || values.ssh_auth_type || 'key',
        credential_id: values.credential_id,
        metadata: sshEdit.metadata || {}
      })
    });
    message.success(t('config.sshMachineSaved'));
    setSSHEdit(null);
    ssh.reload();
    sshRehearsal.reload();
  }
  async function deleteSSHMachine(row: AnyRow) {
    if (!row?.id) return;
    Modal.confirm({
      title: t('config.deleteSSHMachineConfirm'),
      okText: t('common.delete'),
      cancelText: t('common.cancel'),
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await api(`/api/ssh-machines/${row.id}`, { method: 'DELETE' });
          message.success(t('config.sshMachineDeleted'));
          if (sshPick.selectedID === row.id) sshPick.setSelectedID(undefined);
          ssh.reload();
          sshRuns.reload();
          sshRehearsal.reload();
        } catch (error: any) {
          message.error(error.message || t('common.requestFailed'));
        }
      }
    });
  }
  async function runSSHCommand(values: AnyRow) {
    if (!sshPick.selectedID) {
      message.error(t('config.selectSSHMachine'));
      return;
    }
    try {
      const result = await api(`/api/ssh-machines/${sshPick.selectedID}/commands`, {
        method: 'POST',
        body: JSON.stringify({ command: values.command, timeout_seconds: Number(values.timeout_seconds || 60) })
      });
      message.success(result.approval ? t('config.approvalRequested') : t('config.sshCommandQueued'));
      sshRuns.reload();
      sshRehearsal.reload();
    } catch (error: any) {
      message.error(error.message);
    }
  }
  async function verifySSHMachine() {
    if (!sshPick.selectedID) {
      message.error(t('config.selectSSHMachine'));
      return;
    }
    try {
      await api(`/api/ssh-machines/${sshPick.selectedID}/verify`, { method: 'POST', body: '{}' });
      message.success(t('config.sshVerifyQueued'));
      sshRuns.reload();
      sshRehearsal.reload();
    } catch (error: any) {
      message.error(error.message);
    }
  }
  async function recordSSHRehearsalSnapshot() {
    if (!sshPick.selectedID) {
      message.error(t('config.selectSSHMachine'));
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
        message.warning(result.message || t('config.sshSnapshotNotReady'));
      } else {
        message.success(result.ssh_rehearsal_snapshot_written ? t('config.sshSnapshotRecorded') : t('config.sshSnapshotCurrent'));
      }
    } catch (error: any) {
      setSSHSnapshotResult(undefined);
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setSSHSnapshotLoading(false);
    }
  }
  async function recordSSHTargetEnvironmentProof() {
    if (!sshPick.selectedID) {
      message.error(t('config.selectSSHMachine'));
      return;
    }
    setSSHProofLoading(true);
    try {
      const result = await api(`/api/ssh-machines/${sshPick.selectedID}/target-environment-proof`, {
        method: 'POST',
        body: '{}'
      });
      setSSHProofResult(result);
      sshRehearsal.reload();
      if (result.recording_ready === false) {
        message.warning(result.message || t('config.sshProofNotReady'));
      } else {
        message.success(result.proof_registered ? t('config.sshProofRecorded') : t('config.sshProofCurrent'));
      }
    } catch (error: any) {
      setSSHProofResult(undefined);
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setSSHProofLoading(false);
    }
  }
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>{t('title.argoSsh')}</Typography.Title>
      <EntitySelect label={t('common.project')} rows={projectRows} value={projectPick.selectedID} onChange={projectPick.setSelectedID} />
      <Tabs items={[
        { key: 'credentials', label: t('title.connectionCredentials'), children: <Space direction="vertical" size={16} className="full">
          <Toolbar title="Create connection credential" onCreate={() => setCredentialOpen(true)} disabled={!project} />
          <Table<AnyRow> rowKey="id" dataSource={credentialRows} pagination={false} columns={[
            { title: t('common.name'), dataIndex: 'name' },
            { title: t('common.type'), render: (_, row) => translatedValue(row.kind, t) },
            { title: t('field.public_value'), dataIndex: 'public_value', render: (value) => value ? shortText(String(value), 96) : '-' },
            { title: t('common.status'), render: (_, row) => <Tag color={row.secret_configured ? 'green' : 'red'}>{row.secret_configured ? t('common.configured') : t('common.missing')}</Tag> },
            { title: t('common.created'), dataIndex: 'created_at' }
          ]} />
        </Space> },
        { key: 'ssh', label: t('title.sshMachines'), children: <Space direction="vertical" size={16} className="full">
          <Toolbar title="SSH Machines" onCreate={() => setSSHOpen(true)} disabled={!project} />
          <EntitySelect label={t('title.sshMachines')} rows={sshRows} value={sshPick.selectedID} onChange={sshPick.setSelectedID} />
          <Space>
            <Button onClick={verifySSHMachine} disabled={!sshPick.selectedID}>{t('config.verify')}</Button>
            <Button type="primary" onClick={() => setCommandOpen(true)} disabled={!sshPick.selectedID}>{t('config.runCommand')}</Button>
            <Button onClick={() => { sshRuns.reload(); sshRehearsal.reload(); }} loading={sshRehearsal.loading} disabled={!project}>{t('config.refreshRuns')}</Button>
            <Button onClick={openKubernetesImport} loading={kubernetesImportLoading} disabled={!sshPick.selectedID}>{t('form.importKubernetesFromSSH')}</Button>
            <Button onClick={recordSSHTargetEnvironmentProof} loading={sshProofLoading} disabled={!sshPick.selectedID || sshRehearsalView?.rehearsal_state !== 'ready' || !sshRehearsalView?.target_environment_attestation_ready}>{t('config.recordProof')}</Button>
            <Button onClick={recordSSHRehearsalSnapshot} loading={sshSnapshotLoading} disabled={!sshPick.selectedID || !sshRehearsalView?.target_environment_attestation_ready}>{t('config.recordSnapshot')}</Button>
          </Space>
          {sshRehearsal.error && <Alert showIcon type="warning" message={t('config.sshPreviewUnavailable')} description={sshRehearsal.error} />}
          {sshRehearsalView && (
            <Card title={t('title.sshRehearsal')}>
              <Space direction="vertical" size={8} className="full">
                <Space wrap>
                  <Tag color={sshRehearsalView.rehearsal_state === 'ready' ? 'green' : sshRehearsalView.rehearsal_state === 'blocked' ? 'red' : 'gold'}>{translatedValue(sshRehearsalView.rehearsal_state, t)}</Tag>
                  {sshRehearsalView.auth_binding_plan ? <Tag color={sshRehearsalView.auth_binding_plan.binding_state === 'planned' ? 'gold' : sshRehearsalView.auth_binding_plan.binding_state === 'observed' ? 'green' : 'red'}>{t('value.auth')} {translatedValue(sshRehearsalView.auth_binding_plan.binding_state || 'blocked', t)}</Tag> : null}
                  {sshRehearsalView.verify_execution_plan ? <Tag color={sshRehearsalView.verify_execution_plan.verify_state === 'observed' ? 'green' : sshRehearsalView.verify_execution_plan.verify_state === 'planned' ? 'gold' : 'red'}>{t('value.verify')} {translatedValue(sshRehearsalView.verify_execution_plan.verify_state || 'blocked', t)}</Tag> : null}
                  {sshRehearsalView.exec_execution_plan ? <Tag color={sshRehearsalView.exec_execution_plan.exec_state === 'observed' ? 'green' : sshRehearsalView.exec_execution_plan.exec_state === 'planned' ? 'gold' : 'red'}>{t('value.exec')} {translatedValue(sshRehearsalView.exec_execution_plan.exec_state || 'blocked', t)}</Tag> : null}
                  {sshRehearsalView.target_environment_attestation_ready ? <Tag color="green">{t('value.target_proof_review_ready')}</Tag> : <Tag>{t('value.target_proof_pending')}</Tag>}
                  {sshRehearsalView.target_environment_proof_registered ? <Tag color="green">{t('value.target_proof_registered')}</Tag> : <Tag>{t('value.target_proof_unregistered')}</Tag>}
                  {sshRehearsalView.recent_evidence?.evidence_state ? <Tag color={sshRehearsalView.recent_evidence.evidence_state === 'recorded' ? 'green' : sshRehearsalView.recent_evidence.evidence_state === 'failed' ? 'red' : 'gold'}>{t('value.evidence')} {translatedValue(sshRehearsalView.recent_evidence.evidence_state, t)}</Tag> : null}
                </Space>
                {sshProofResult && (
                  <Space wrap>
                    <Tag color={sshProofResult.recording_state === 'recorded' ? 'green' : sshProofResult.recording_state === 'asset_missing' ? 'red' : 'gold'}>{t('value.proof')} {translatedValue(sshProofResult.recording_state || 'pending', t)}</Tag>
                    <Tag>{t(sshProofResult.asset_status_snapshot_written ? 'value.asset_status_written' : 'value.asset_status_unchanged')}</Tag>
                    <Tag>{t(sshProofResult.proof_registered ? 'value.proof_registered' : 'value.proof_not_written')}</Tag>
                    <Tag>{t(sshProofResult.stdout_included || sshProofResult.stderr_included ? 'value.output_included' : 'value.no_command_output')}</Tag>
                    <Tag>{t(sshProofResult.private_key_included ? 'value.key_included' : 'value.no_key_material')}</Tag>
                  </Space>
                )}
                {sshSnapshotResult && (
                  <Space wrap>
                    <Tag color={sshSnapshotResult.recording_state === 'recorded' ? 'green' : sshSnapshotResult.recording_state === 'asset_missing' ? 'red' : 'gold'}>{t('value.snapshot')} {translatedValue(sshSnapshotResult.recording_state || 'pending', t)}</Tag>
                    <Tag>{t(sshSnapshotResult.asset_status_snapshot_written ? 'value.asset_status_written' : 'value.asset_status_unchanged')}</Tag>
                    <Tag>{t(sshSnapshotResult.ssh_machine_asset_observed ? 'value.host_asset_observed' : 'value.host_asset_missing')}</Tag>
                    <Tag>{t(sshSnapshotResult.stdout_included || sshSnapshotResult.stderr_included ? 'value.output_included' : 'value.no_command_output')}</Tag>
                    <Tag>{t(sshSnapshotResult.private_key_included ? 'value.key_included' : 'value.no_key_material')}</Tag>
                  </Space>
                )}
              </Space>
            </Card>
          )}
          <Table<AnyRow> rowKey="id" dataSource={sshRows} pagination={false} columns={[
            { title: t('common.name'), dataIndex: 'name' },
            { title: t('field.host'), dataIndex: 'host' },
            { title: t('field.port'), dataIndex: 'port' },
            { title: t('config.sshHostUser'), dataIndex: 'username' },
            { title: t('common.auth'), render: (_, row) => translatedValue(row.auth_type, t) },
            { title: t('common.credential'), render: (_, row) => row.credential_name ? <Tag color={row.credential_configured ? 'green' : 'gold'}>{row.credential_name}</Tag> : <Tag color="red">{t('common.missing')}</Tag> },
            { title: t('common.action'), render: (_, row) => <Space><Button size="small" onClick={() => setSSHEdit(row)}>{t('common.edit')}</Button><Button size="small" danger onClick={() => deleteSSHMachine(row)}>{t('common.delete')}</Button></Space> }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={sshRuns.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: t('config.operationType'), render: (_, row) => <Tag color={row.operation_type === 'ssh.verify' ? 'cyan' : 'default'}>{row.operation_type || t('common.unknown')}</Tag> },
            { title: t('common.status'), render: (_, row) => <Tag color={row.status === 'completed' ? 'green' : row.status === 'failed' ? 'red' : 'blue'}>{translatedValue(row.status, t)}</Tag> },
            { title: t('field.command'), dataIndex: 'command' },
            { title: t('config.exit'), dataIndex: 'exit_code' },
            { title: t('config.failureReason'), render: (_, row) => <Typography.Text title={String(row.error_message || row.stderr || '')}>{shortText(row.error_message || row.stderr, 96)}</Typography.Text> },
            { title: t('common.created'), dataIndex: 'created_at' },
            { title: t('config.finished'), dataIndex: 'finished_at' }
          ]} />
        </Space> },
        { key: 'argo', label: t('title.argoApps'), children: <Space direction="vertical" size={16} className="full">
          <Toolbar title="Argo Connections" onCreate={() => setArgoOpen(true)} disabled={!project} />
          <EntitySelect label={t('title.argoConnections')} rows={argoRows} value={argoPick.selectedID} onChange={argoPick.setSelectedID} />
          <Space>
            <Button type="primary" loading={Boolean(argoSyncOpID)} onClick={syncArgoApps} disabled={!argoPick.selectedID || Boolean(argoSyncOpID)}>{t('config.syncApps')}</Button>
            <Button onClick={() => setKubernetesEnvironmentOpen(true)} disabled={!project}>{t('form.addKubernetesEnvironment')}</Button>
            <Button onClick={openArgoImport} loading={argoImportLoading} disabled={!kubernetesEnvironmentPick.selectedID}>{t('form.importArgoFromKubernetes')}</Button>
            <Button onClick={() => { argoConnections.reload(); argoApps.reload(); kubernetesEnvironments.reload(); deploymentTargets.reload(); deploymentRecords.reload(); rollbackPoints.reload(); }} disabled={!project}>{t('common.refresh')}</Button>
          </Space>
          <EntitySelect label={t('config.kubernetesEnv')} rows={kubernetesEnvironmentRows} value={kubernetesEnvironmentPick.selectedID} onChange={kubernetesEnvironmentPick.setSelectedID} />
          <div className="metricGrid">
            <Card><Typography.Text type="secondary">{t('config.deploymentTargets')}</Typography.Text><Typography.Title level={3}>{deploymentPosture.targets}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('config.unhealthy')}</Typography.Text><Typography.Title level={3}>{deploymentPosture.unhealthy}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('config.environments')}</Typography.Text><Typography.Title level={3}>{deploymentPosture.environments}</Typography.Title></Card>
            <Card><Typography.Text type="secondary">{t('config.rollbackPoints')}</Typography.Text><Typography.Title level={3}>{deploymentPosture.rollbackPoints}</Typography.Title></Card>
          </div>
          {deploymentPosture.targets > 0 && <Alert showIcon type={deploymentPosture.unhealthy > 0 ? 'warning' : 'success'} message={deploymentPosture.summary} />}
          {deploymentExecutionGuardrail && <Alert showIcon type={deploymentExecutionGuardrail.type} message={deploymentExecutionGuardrail.message} description={deploymentExecutionGuardrail.description} />}
          {rollbackGuardrail && <Alert showIcon type={rollbackGuardrail.type} message={rollbackGuardrail.message} description={rollbackGuardrail.description} />}
          <Card title={t('form.podLogQuery')}>
            <Space direction="vertical" size={12} className="full">
              <Form
                form={podLogForm}
                layout="inline"
                onFinish={previewPodLogs}
                initialValues={{ tail_lines: 200, since_seconds: 0 }}
                onValuesChange={(changed) => {
                  if (changed.deployment_target_id) {
                    setPodListResult(undefined);
                    setPodLogPreview(undefined);
                    setPodLogRunResult(undefined);
                    setPodLogSnapshotResult(undefined);
                    setPodRestartResult(undefined);
                    podLogForm.setFieldsValue({ pod_name: undefined, container_name: undefined, deployment_name: undefined });
                  }
                }}
              >
                <Form.Item name="deployment_target_id" label={fieldLabel('deployment_target_id', t)} rules={[{ required: true, message: t('common.required') }]}>
                  <Select placeholder={t('common.target')} style={{ width: 220 }} options={(deploymentTargets.data?.items || []).map((target: AnyRow) => ({ value: target.id, label: `${target.name || target.namespace} (${target.environment || 'env'})` }))} />
                </Form.Item>
                <Form.Item name="pod_name" label={fieldLabel('pod_name', t)} rules={[{ required: true, message: t('common.required') }]}>
                  <AutoComplete
                    placeholder={t('field.pod_name')}
                    style={{ width: 220 }}
                    options={podOptions}
                    filterOption={(inputValue, option) => String(option?.value || '').toLowerCase().includes(inputValue.toLowerCase())}
                    onSelect={(value) => {
                      const selected = podListItems.find((pod: AnyRow) => pod.name === value);
                      const firstContainer = Array.isArray(selected?.containers) ? selected.containers[0] : undefined;
                      podLogForm.setFieldsValue({ container_name: firstContainer });
                    }}
                  />
                </Form.Item>
                <Form.Item name="container_name" label={fieldLabel('container_name', t)}>
                  <AutoComplete
                    placeholder={t('field.container_name')}
                    style={{ width: 170 }}
                    options={containerOptions}
                    filterOption={(inputValue, option) => String(option?.value || '').toLowerCase().includes(inputValue.toLowerCase())}
                  />
                </Form.Item>
                <Form.Item name="deployment_name" label={fieldLabel('deployment_name', t)}>
                  <Input placeholder={t('field.deployment_name')} style={{ width: 190 }} />
                </Form.Item>
                <Form.Item name="tail_lines">
                  <Input type="number" min={1} max={200} placeholder={t('field.tail_lines')} style={{ width: 110 }} suffix={<Tooltip title={t('help.tail_lines')}><QuestionCircleOutlined className="fieldHelpIcon" /></Tooltip>} />
                </Form.Item>
                <Form.Item name="since_seconds">
                  <Input type="number" min={0} max={86400} placeholder={t('field.since_seconds')} style={{ width: 130 }} suffix={<Tooltip title={t('help.since_seconds')}><QuestionCircleOutlined className="fieldHelpIcon" /></Tooltip>} />
                </Form.Item>
                <Button htmlType="button" onClick={() => refreshPodList()} loading={podListLoading} disabled={!project || !(deploymentTargets.data?.items || []).length}>{t('pod.refreshPods')}</Button>
                <Button htmlType="submit" loading={podLogLoading} disabled={!project || !(deploymentTargets.data?.items || []).length}>{t('pod.preview')}</Button>
              </Form>
              <Space wrap>
                <Tooltip title={podListResult?.backend_plan?.ready ? t('k8s.backendHintReady') : t('k8s.backendHintBlocked')}>
                  <Tag color={podListResult?.backend_state === 'completed' ? 'green' : podListResult?.backend_state === 'blocked' || podListResult?.backend_state === 'disabled' ? 'gold' : podListResult?.backend_state === 'failed' ? 'red' : 'default'}>
                    {t('value.pod_metadata')} {translatedValue(podListResult?.backend_state || 'unknown', t)}
                  </Tag>
                </Tooltip>
                {podListResult ? <Tag>{podListResult.item_count || 0} {t('value.pod_metadata')}</Tag> : null}
                {podListResult ? <Tag>{podListResult.kubernetes_api_call ? t('value.k8s_called') : t('value.no_k8s_call')}</Tag> : null}
                {podListResult ? <Tag>{podListResult.raw_response_included || podListResult.log_body_included ? t('value.sensitive_material_present') : t('value.no_secrets_kubeconfig')}</Tag> : null}
              </Space>
              {podLogPreview && (
                <Space direction="vertical" size={8} className="full">
                  <Space wrap>
                    <Tag color="gold">{translatedValue(podLogPreview.query_state || 'blocked', t)}</Tag>
                    <Tag>{podLogPreview.execution_enabled ? t('value.execution_enabled') : t('value.execution_disabled')}</Tag>
                    <Tag>{podLogPreview.operation_request_enabled ? t('value.operation_request_ready') : t('value.operation_request_blocked')}</Tag>
                    <Tag>{podLogPreview.kubernetes_api_call ? t('value.k8s_called') : t('value.no_k8s_call')}</Tag>
                    <Tag>{podLogPreview.log_body_included ? t('value.log_body_included') : t('value.no_log_body')}</Tag>
                    {podLogPreview.retrieval_plan ? <Tag color={podLogPreview.retrieval_plan.plan_state === 'ready_for_approval' ? 'gold' : 'red'}>{t('value.retrieval')} {translatedValue(podLogPreview.retrieval_plan.plan_state || 'blocked', t)}</Tag> : null}
                    {podLogPreview.retrieval_plan ? <Tag>{podLogPreview.retrieval_plan.step_count || 0} {t('value.retrieval')}</Tag> : null}
                    {podLogPreview.audit_evidence ? <Tag color={podLogPreview.audit_evidence.evidence_state === 'recorded' ? 'green' : podLogPreview.audit_evidence.evidence_state === 'failed' ? 'red' : podLogPreview.audit_evidence.evidence_state === 'waiting_for_worker' ? 'blue' : 'default'}>{t('value.audit')} {translatedValue(podLogPreview.audit_evidence.evidence_state || 'not_requested', t)}</Tag> : null}
                    {podLogPreview.audit_evidence ? <Tag>{podLogPreview.audit_evidence.operation_count || 0} {t('value.audit_ops')}</Tag> : null}
                    {podLogPreview.audit_evidence?.operation_log_count ? <Tag>{podLogPreview.audit_evidence.operation_log_count} {t('value.audit_logs')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan ? <Tag color={podLogPreview.retrieval_plan.execution_plan.execution_state === 'ready_for_approval' ? 'gold' : 'red'}>{t('value.execute')} {translatedValue(podLogPreview.retrieval_plan.execution_plan.execution_state || 'blocked', t)}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.approval_request_plan ? <Tag color="gold">{t('value.approval')} {translatedValue(podLogPreview.retrieval_plan.execution_plan.approval_request_plan.request_state || 'blocked', t)}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.audit_worker_job_enabled ? t('value.audit_worker_ready') : t('value.no_audit_worker')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.audit_operation_observed ? t('value.audit_observed') : t('value.no_audit_observed')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.kubeconfig_binding_plan ? <Tag color={podLogPreview.retrieval_plan.execution_plan.kubeconfig_binding_plan.binding_state === 'planned' ? 'gold' : 'red'}>{t('value.kubeconfig')} {translatedValue(podLogPreview.retrieval_plan.execution_plan.kubeconfig_binding_plan.binding_state || 'blocked', t)}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.kubeconfig_readiness_plan ? <Tag color={podLogPreview.retrieval_plan.execution_plan.kubeconfig_readiness_plan.readiness_ready ? 'gold' : 'red'}>{t('value.kube_readiness')} {translatedValue(podLogPreview.retrieval_plan.execution_plan.kubeconfig_readiness_plan.readiness_state || 'blocked', t)}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.kubeconfig_readiness_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.kubeconfig_readiness_plan.namespace_scoped_kubeconfig_bound ? t('value.namespace_kubeconfig_bound') : t('value.no_namespace_kubeconfig')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.kubeconfig_readiness_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.kubeconfig_readiness_plan.log_access_metadata_ready ? t('value.log_metadata_reviewed') : t('value.log_metadata_pending')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.live_backend_plan ? <Tooltip title={podLogPreview.retrieval_plan.execution_plan.live_backend_plan.ready ? t('k8s.backendHintReady') : t('k8s.backendHintBlocked')}><Tag color={podLogPreview.retrieval_plan.execution_plan.live_backend_plan.ready ? 'green' : podLogPreview.retrieval_plan.execution_plan.live_backend_plan.enabled ? 'gold' : 'default'}>{podLogPreview.retrieval_plan.execution_plan.live_backend_plan.ready ? t('k8s.backendReady') : podLogPreview.retrieval_plan.execution_plan.live_backend_plan.enabled ? t('k8s.backendBlocked') : t('k8s.backendOff')}</Tag></Tooltip> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.pod_scope_plan ? <Tag color={podLogPreview.retrieval_plan.execution_plan.pod_scope_plan.scope_state === 'planned' ? 'gold' : 'red'}>{t('value.scope')} {translatedValue(podLogPreview.retrieval_plan.execution_plan.pod_scope_plan.scope_state || 'blocked', t)}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.log_capture_plan ? <Tag color={podLogPreview.retrieval_plan.execution_plan.log_capture_plan.capture_state === 'planned' ? 'gold' : 'red'}>{t('value.capture')} {translatedValue(podLogPreview.retrieval_plan.execution_plan.log_capture_plan.capture_state || 'blocked', t)}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.live_log_stream_plan ? <Tag color={podLogPreview.retrieval_plan.execution_plan.live_log_stream_plan.stream_ready_for_review ? 'green' : podLogPreview.retrieval_plan.execution_plan.live_log_stream_plan.metadata_ready ? 'gold' : 'red'}>{t('value.live_stream')} {translatedValue(podLogPreview.retrieval_plan.execution_plan.live_log_stream_plan.stream_state || 'blocked', t)}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.live_log_stream_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.live_log_stream_plan.stream_ready_for_review ? t('value.stream_review_ready') : t('value.stream_review_blocked')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.live_log_stream_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.live_log_stream_plan.live_log_stream_opened ? t('value.stream_opened') : t('value.no_live_stream')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.live_log_stream_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.live_log_stream_plan.log_body_included ? t('value.log_body_included') : t('value.no_log_body')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.secret_included ? t('value.secrets_included') : t('value.no_secrets')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.result_written ? t('value.result_written') : t('value.no_result_write')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.result_recording_plan ? <Tag>{translatedValue(podLogPreview.retrieval_plan.execution_plan.result_recording_plan.recording_state || 'blocked', t)} {t('value.recording')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.result_recording_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.result_recording_plan.sanitized_result_observed ? t('value.sanitized_result_observed') : t('value.no_sanitized_result')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.result_recording_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.result_recording_plan.kubeconfig_binding_recorded ? t('value.kubeconfig_recorded') : t('value.no_kubeconfig_record')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.result_recording_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.result_recording_plan.pod_scope_recorded ? t('value.scope_recorded') : t('value.no_scope_record')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan?.result_recording_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.result_recording_plan.log_capture_recorded ? t('value.capture_recorded') : t('value.no_capture_record')}</Tag> : null}
                    {podLogPreview.retrieval_plan?.execution_plan ? <Tag>{podLogPreview.retrieval_plan.execution_plan.disabled_backends?.length || 0} {t('value.disabled_backends')}</Tag> : null}
                  </Space>
                  <Space>
                    <Button type="primary" onClick={requestPodLogAudit} loading={podLogRunLoading} disabled={!podLogPreview.operation_request_enabled || !podLogPreview.retrieval_plan?.execution_plan?.audit_worker_job_enabled}>{t('pod.requestAudit')}</Button>
                    <Button danger onClick={requestPodRestart} loading={podRestartLoading} disabled={!project || !selectedDeploymentTargetID || !selectedDeploymentName}>{t('pod.requestRestart')}</Button>
                    <Button onClick={recordPodLogAuditSnapshot} loading={podLogSnapshotLoading} disabled={podLogPreview.audit_evidence?.evidence_state !== 'recorded'}>{t('pod.recordSnapshot')}</Button>
                    {podLogRunResult ? <Tag color={podLogRunResult.approval ? 'gold' : 'blue'}>{podLogRunResult.approval ? t('value.approval_requested') : t('value.operation_queued')}</Tag> : null}
                    {podRestartResult ? <Tag color={podRestartResult.approval ? 'gold' : 'blue'}>{podRestartResult.approval ? t('value.approval_requested') : t('value.operation_queued')}</Tag> : null}
                    {podLogRunResult?.worker_job_created ? <Tag>{t('value.worker_job_created')}</Tag> : null}
                    {podLogRunResult ? <Tag>{podLogRunResult.log_body_included ? t('value.log_body_included') : t('value.no_log_body')}</Tag> : null}
                    {podLogSnapshotResult ? <Tag color={podLogSnapshotResult.pod_log_audit_snapshot_written ? 'green' : podLogSnapshotResult.recording_state === 'asset_missing' ? 'red' : 'default'}>{t('value.snapshot')} {translatedValue(podLogSnapshotResult.recording_state || 'unknown', t)}</Tag> : null}
                    {podLogSnapshotResult ? <Tag>{podLogSnapshotResult.asset_status_snapshot_written ? t('value.asset_status_written') : t('value.no_asset_status_write')}</Tag> : null}
                    {podLogSnapshotResult ? <Tag>{podLogSnapshotResult.log_body_included ? t('value.log_body_included') : t('value.no_log_body')}</Tag> : null}
                  </Space>
                  {podLogPreview.audit_evidence?.redacted_log_preview ? (
                    <Card size="small" title={t('pod.redactedPreview')}>
                      <Space direction="vertical" size={8} className="full">
                        <Space wrap>
                          <Tag color="green">{podLogPreview.audit_evidence.preview_line_count || 0} {t('field.preview_lines')}</Tag>
                          {podLogPreview.audit_evidence.preview_truncated ? <Tag color="gold">{translatedValue('truncated', t)}</Tag> : null}
                          <Tag>{t('value.no_secrets')}</Tag>
                          <Tag>{t('value.no_kubeconfig')}</Tag>
                        </Space>
                        <Typography.Text type="secondary">{t('pod.redactedPreviewDescription')}</Typography.Text>
                        <pre className="logPreview">{podLogPreview.audit_evidence.redacted_log_preview}</pre>
                      </Space>
                    </Card>
                  ) : null}
                  {podLogRunResult ? <JSONBlock value={podLogRunResult} /> : null}
                  {podLogSnapshotResult ? <JSONBlock value={podLogSnapshotResult} /> : null}
                  <JSONBlock value={podLogPreview} />
                </Space>
              )}
            </Space>
          </Card>
          <Table<AnyRow> rowKey="id" dataSource={argoRows} pagination={false} columns={[
            { title: t('common.name'), dataIndex: 'name' },
            { title: t('common.server'), dataIndex: 'server_url' },
            { title: t('common.auth'), render: (_, row) => translatedValue(row.auth_type, t) },
            { title: t('common.credential'), render: (_, row) => row.credential_name ? <Tag color={row.credential_configured ? 'green' : 'gold'}>{row.credential_name}</Tag> : <Tag color="red">{t('common.missing')}</Tag> },
            { title: t('common.sync'), render: (_, row) => <Tag color={row.last_sync_status === 'completed' ? 'green' : row.last_sync_status === 'failed' ? 'red' : row.last_sync_status === 'running' ? 'blue' : 'default'}>{row.last_sync_status ? translatedValue(row.last_sync_status, t) : t('common.never')}</Tag> },
            { title: t('common.created'), dataIndex: 'created_at' },
            { title: t('common.action'), render: (_, row) => <Space><Button size="small" onClick={() => setArgoEdit(row)}>{t('common.edit')}</Button><Button size="small" danger onClick={() => deleteArgoConnection(row)}>{t('common.delete')}</Button></Space> }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={kubernetesEnvironments.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: t('config.kubernetesEnv'), dataIndex: 'name' },
            { title: t('common.environment'), dataIndex: 'environment' },
            { title: t('common.namespace'), dataIndex: 'namespace' },
            { title: t('common.cluster'), dataIndex: 'cluster_name' },
            { title: t('common.secretRef'), render: (_, row) => row.kubeconfig_secret_ref_present ? <Tag color="green">{t('common.configured')}</Tag> : <Tag color="red">{t('common.missing')}</Tag> },
            { title: t('config.tokenReview'), render: (_, row) => <Tag color={row.token_subject_review_ready ? 'green' : 'gold'}>{translatedValue(row.token_subject_review_status || 'not_reviewed', t)}</Tag> },
            { title: t('config.logsRbac'), render: (_, row) => <Tag color={row.rbac_read_logs_ready ? 'green' : 'gold'}>{translatedValue(row.rbac_read_logs_status || 'not_reviewed', t)}</Tag> },
            { title: t('common.metadata'), render: (_, row) => <Tag color={row.log_access_metadata_ready ? 'green' : 'gold'}>{row.log_access_metadata_ready ? t('value.metadata_reviewed') : translatedValue(row.status || 'metadata_only', t)}</Tag> }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={deploymentTargets.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: t('common.target'), dataIndex: 'name' },
            { title: t('common.environment'), dataIndex: 'environment' },
            { title: t('common.namespace'), dataIndex: 'namespace' },
            { title: t('common.cluster'), dataIndex: 'cluster_name' },
            { title: t('config.kubeEnv'), render: (_, row) => row.kubernetes_environment_id ? <Tag color={row.kubeconfig_secret_ref_present ? 'green' : 'gold'}>{row.kubernetes_environment_name || t('common.bound')}</Tag> : <Tag>{t('common.unbound')}</Tag> },
            { title: t('config.apps'), dataIndex: 'argo_app_count' },
            { title: t('common.execution'), render: (_, row) => <Space direction="vertical" size={4}>{deploymentExecutionReadinessView(row, t)}{deploymentExecutionGateResults[row.id] ? deploymentExecutionGateView(deploymentExecutionGateResults[row.id], t) : null}</Space> },
            { title: t('common.action'), render: (_, row) => <Button size="small" onClick={() => checkDeploymentExecutionGate(String(row.id || ''))} loading={deploymentExecutionGateLoadingID === row.id} disabled={!row.id || Boolean(deploymentExecutionGateLoadingID)}>{t('config.checkGate')}</Button> },
            { title: t('common.status'), render: (_, row) => <Tag color={argoStatusColor(row.status)}>{translatedValue(row.status, t)}</Tag> }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={deploymentRecords.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: t('config.deployment'), dataIndex: 'name' },
            { title: t('common.target'), dataIndex: 'deployment_target_name' },
            { title: t('common.environment'), dataIndex: 'environment' },
            { title: t('common.status'), render: (_, row) => <Tag color={argoStatusColor(row.status)}>{translatedValue(row.status, t)}</Tag> },
            { title: t('common.revision'), dataIndex: 'revision' },
            { title: t('common.observed'), dataIndex: 'observed_at' }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={rollbackPoints.data?.items || []} pagination={{ pageSize: 6 }} columns={[
            { title: t('config.rollbackPoint'), dataIndex: 'name' },
            { title: t('common.target'), dataIndex: 'deployment_target_name' },
            { title: t('common.environment'), dataIndex: 'environment' },
            { title: t('common.revision'), dataIndex: 'revision' },
            { title: t('common.readiness'), render: (_, row) => <Tag color={rollbackReadinessColor(row.rollback_readiness)}>{translatedValue(row.rollback_readiness || 'unknown', t)}</Tag> },
            { title: t('common.reason'), dataIndex: 'rollback_readiness_reason', render: (value) => value || '-' },
            { title: t('common.execution'), render: (_, row) => <Space direction="vertical" size={4}>{rollbackExecutionPlanView(row, t)}{rollbackExecutionGateResults[row.id] ? rollbackExecutionGateView(rollbackExecutionGateResults[row.id], t) : null}</Space> },
            { title: t('common.action'), render: (_, row) => <Button size="small" onClick={() => checkRollbackExecutionGate(String(row.id || ''))} loading={rollbackExecutionGateLoadingID === row.id} disabled={!row.id || Boolean(rollbackExecutionGateLoadingID)}>{t('config.checkGate')}</Button> },
            { title: t('common.status'), render: (_, row) => <Tag color={argoStatusColor(row.status)}>{translatedValue(row.status, t)}</Tag> },
            { title: t('common.captured'), dataIndex: 'captured_at' }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={argoApps.data?.items || []} pagination={{ pageSize: 8 }} columns={[
            { title: t('common.name'), dataIndex: 'name' },
            { title: t('common.target'), dataIndex: 'deployment_target_name' },
            { title: t('common.environment'), dataIndex: 'environment' },
            { title: t('common.namespace'), dataIndex: 'namespace' },
            { title: t('common.status'), render: (_, row) => <Tag color={argoStatusColor(row.status)}>{translatedValue(row.status, t)}</Tag> },
            { title: t('common.synced'), dataIndex: 'synced_at' },
            { title: t('common.updated'), dataIndex: 'updated_at' }
          ]} />
        </Space> }
      ]} />
      <CreateModal
        title="Create connection credential"
        open={credentialOpen}
        setOpen={setCredentialOpen}
        fields={[{ name: 'name', helpKey: 'help.name' }, { name: 'kind', metaKey: 'kind' }, 'secret_value', 'public_value']}
        initialValues={{ kind: 'ssh_key' }}
        onSubmit={createConnectionCredential}
      />
      <CreateModal
        title="Create Argo connection"
        open={argoOpen}
        setOpen={setArgoOpen}
        descriptionKey="argo.connectionModalDescription"
        fields={[{ name: 'name', helpKey: 'help.argo_connection_name' }, 'server_url', { name: 'auth_type', metaKey: 'argo_auth_type' }, { name: 'credential_id', input: 'select', optionItems: argoCredentialOptions, helpKey: 'help.credential_id', required: true }, 'insecure_skip_verify']}
        initialValues={{ auth_type: 'token', insecure_skip_verify: false }}
        onSubmit={createArgoConnection}
      />
      <CreateModal
        title="Argo Connections"
        open={Boolean(argoEdit)}
        setOpen={(open) => { if (!open) setArgoEdit(null); }}
        descriptionKey="argo.connectionModalDescription"
        fields={[{ name: 'name', helpKey: 'help.argo_connection_name' }, 'server_url', { name: 'auth_type', metaKey: 'argo_auth_type' }, { name: 'credential_id', input: 'select', optionItems: argoCredentialOptions, helpKey: 'help.credential_id', required: true }, 'insecure_skip_verify']}
        initialValues={{ ...argoEdit, insecure_skip_verify: Boolean(argoEdit?.config?.insecure_skip_verify) }}
        onSubmit={updateArgoConnection}
      />
      <Modal title={t('form.addKubernetesEnvironment')} open={kubernetesEnvironmentOpen} onCancel={() => setKubernetesEnvironmentOpen(false)} onOk={() => kubernetesEnvironmentForm.submit()} destroyOnHidden okText={t('common.ok')} cancelText={t('common.cancel')}>
        <Form form={kubernetesEnvironmentForm} layout="vertical" onFinish={createKubernetesEnvironment} initialValues={{ token_subject_review_status: 'not_reviewed', rbac_read_logs_status: 'not_reviewed', rbac_restart_pods_status: 'not_reviewed', status: 'metadata_only' }}>
          <Typography.Paragraph type="secondary">{t('k8s.modalDescription')}</Typography.Paragraph>
          {['name', 'environment', 'cluster_name', 'namespace', 'kubeconfig_secret_ref', 'service_account', 'token_subject_review_status', 'rbac_read_logs_status', 'rbac_restart_pods_status', 'status'].map((field) => (
            <Form.Item key={field} name={field} label={fieldLabel(field, t)} rules={fieldRules(field, t)} valuePropName={fieldValuePropName(field)}>
              {fieldInput(field, t)}
            </Form.Item>
          ))}
        </Form>
      </Modal>
      <Modal title={t('form.importKubernetesFromSSH')} open={kubernetesImportOpen} onCancel={() => setKubernetesImportOpen(false)} onOk={() => kubernetesImportForm.submit()} confirmLoading={kubernetesImportLoading} destroyOnHidden okText={t('common.ok')} cancelText={t('common.cancel')}>
        <Space direction="vertical" size={12} className="full">
          {kubernetesImportPreview ? (
            <Alert
              showIcon
              type={kubernetesImportPreview.status === 'ok' || kubernetesImportPreview.imported ? 'success' : 'warning'}
              message={translatedValue(kubernetesImportPreview.status || kubernetesImportPreview.discovery?.status || 'unknown', t)}
              description={(kubernetesImportPreview.discovery?.blocked_reasons || kubernetesImportPreview.blocked_reasons || []).join(', ') || kubernetesImportPreview.error || undefined}
            />
          ) : null}
          <Form form={kubernetesImportForm} layout="vertical" onFinish={importKubernetesFromSSH} initialValues={{ status: 'metadata_only' }}>
            {['name', 'environment', 'kubeconfig_secret_ref', 'service_account', 'status'].map((field) => (
              <Form.Item key={field} name={field} label={fieldLabel(field, t)} rules={fieldRules(field, t)} valuePropName={fieldValuePropName(field)}>
                {fieldInput(field, t)}
              </Form.Item>
            ))}
          </Form>
          {kubernetesImportPreview ? <JSONBlock value={kubernetesImportPreview} /> : null}
        </Space>
      </Modal>
      <Modal title={t('form.importArgoFromKubernetes')} open={argoImportOpen} onCancel={() => setArgoImportOpen(false)} onOk={() => argoImportForm.submit()} confirmLoading={argoImportLoading} destroyOnHidden okText={t('common.ok')} cancelText={t('common.cancel')}>
        <Space direction="vertical" size={12} className="full">
          {argoImportPreview ? (
            <Alert
              showIcon
              type={argoImportPreview.status === 'ok' || argoImportPreview.imported ? 'success' : 'warning'}
              message={translatedValue(argoImportPreview.status || 'unknown', t)}
              description={[...(argoImportPreview.blocked_reasons || []), ...(argoImportPreview.warnings || [])].join(', ') || argoImportPreview.error || undefined}
            />
          ) : null}
          <Form form={argoImportForm} layout="vertical" onFinish={importArgoFromKubernetes} initialValues={{ insecure_skip_verify: false }}>
            <Form.Item name="name" label={fieldLabel('name', t)} rules={fieldRules('name', t)}>{fieldInput('name', t)}</Form.Item>
            <Form.Item name="server_url" label={fieldLabel('server_url', t)} rules={fieldRules('server_url', t)}>
              {(Array.isArray(argoImportPreview?.candidates) && argoImportPreview.candidates.some((item: AnyRow) => item.url)) ? (
                <Select options={argoImportPreview.candidates.filter((item: AnyRow) => item.url).map((item: AnyRow) => ({ value: item.url, label: `${item.url} · ${item.namespace}/${item.name} · ${item.reason}` }))} />
              ) : fieldInput('server_url', t)}
            </Form.Item>
            <Form.Item name="credential_id" label={fieldLabel('credential_id', t)} rules={fieldRules('credential_id', t, { input: 'select', optionItems: argoCredentialOptions, required: true })}>
              {fieldInput('credential_id', t, { input: 'select', optionItems: argoCredentialOptions, required: true })}
            </Form.Item>
            <Form.Item name="insecure_skip_verify" valuePropName="checked">
              {fieldInput('insecure_skip_verify', t)}
            </Form.Item>
          </Form>
          {argoImportPreview ? <JSONBlock value={argoImportPreview} /> : null}
        </Space>
      </Modal>
      <CreateModal
        title="Create SSH machine"
        open={sshOpen}
        setOpen={setSSHOpen}
        descriptionKey="ssh.machineModalDescription"
        fields={[{ name: 'name', helpKey: 'help.ssh_machine_name' }, 'host', 'port', 'username', { name: 'auth_type', metaKey: 'ssh_auth_type' }, { name: 'credential_id', input: 'select', optionItems: sshCredentialOptions, helpKey: 'help.credential_id', required: true }]}
        initialValues={{ port: 22, auth_type: 'key' }}
        onSubmit={createSSHMachine}
      />
      <CreateModal
        title="SSH Machines"
        open={Boolean(sshEdit)}
        setOpen={(open) => { if (!open) setSSHEdit(null); }}
        descriptionKey="ssh.machineModalDescription"
        fields={[{ name: 'name', helpKey: 'help.ssh_machine_name' }, 'host', 'port', 'username', { name: 'auth_type', metaKey: 'ssh_auth_type' }, { name: 'credential_id', input: 'select', optionItems: sshCredentialOptions, helpKey: 'help.credential_id', required: true }]}
        initialValues={{ ...sshEdit, port: Number(sshEdit?.port || 22), auth_type: sshEdit?.auth_type || 'key' }}
        onSubmit={updateSSHMachine}
      />
      <CreateModal title="Run SSH command" open={commandOpen} setOpen={setCommandOpen} descriptionKey="ssh.commandModalDescription" fields={['command', 'timeout_seconds']} initialValues={{ timeout_seconds: 60 }} onSubmit={runSSHCommand} />
    </Space>
  );
}

function Toolbar({ title, onCreate, disabled = false }: { title: string; onCreate: () => void; disabled?: boolean }) {
  const { t } = useI18n();
  return <div className="toolbar"><Typography.Title level={2}>{translateTitle(title, t)}</Typography.Title><Button type="primary" onClick={onCreate} disabled={disabled}>{t('common.create')}</Button></div>;
}

const titleKeys: Record<string, string> = {
  'Projects': 'title.projects',
  'Git Remotes': 'title.gitRemotes',
  'SSH Machines': 'title.sshMachines',
  'Argo Connections': 'title.argoConnections',
  'Versions': 'title.versions',
  'AI Runtime': 'title.aiRuntime',
  'Agent Tasks': 'title.agentTasks',
  'Create project': 'form.createProject',
  'Create repository': 'form.createRepository',
  'Create AI runtime': 'form.createAIRuntime',
  'Create agent task': 'form.createAgentTask',
  'Provider Accounts': 'title.providerAccounts',
  'Create provider account': 'form.createProviderAccount',
  'Create remote': 'form.createRemote',
  'Create tag': 'form.createTag',
  'Save repo sync asset': 'form.saveRepoSyncAsset',
  'Edit repo sync asset': 'form.editRepoSyncAsset',
  'Create webhook': 'form.createWebhook',
  'Create Argo connection': 'form.createArgoConnection',
  'Create connection credential': 'form.createConnectionCredential',
  'Create SSH machine': 'form.createSSHMachine',
  'Run SSH command': 'form.runSSHCommand'
};

function translateTitle(title: string, t: (key: string) => string) {
  const key = titleKeys[title];
  return key ? t(key) : title;
}

function translatedValue(value: any, t: (key: string) => string) {
  const raw = String(value || '').trim();
  if (!raw) return '-';
  const translated = t(`value.${raw}`);
  return translated === `value.${raw}` ? raw.replaceAll('_', ' ') : translated;
}

type FieldMeta = {
  labelKey?: string;
  helpKey?: string;
  input?: 'text' | 'textarea' | 'password' | 'url' | 'number' | 'select' | 'checkbox';
  options?: string[];
  optionItems?: Array<{ value: string; label: React.ReactNode }>;
  required?: boolean;
  placeholder?: string;
};

type ModalField = string | (FieldMeta & { name: string; metaKey?: string });

function resolveModalField(field: ModalField) {
  if (typeof field === 'string') {
    return { name: field, meta: fieldMeta[field] || {} };
  }
  const base = fieldMeta[field.metaKey || field.name] || {};
  const { name, metaKey: _metaKey, ...override } = field;
  return { name, meta: { ...base, ...override } };
}

const fieldMeta: Record<string, FieldMeta> = {
  name: { required: true, helpKey: 'help.name' },
  title: { required: true, helpKey: 'help.agent_task_title' },
  slug: { helpKey: 'help.slug' },
  environment: { required: true, helpKey: 'help.environment', placeholder: 'test' },
  cluster_name: { required: true, helpKey: 'help.cluster_name' },
  namespace: { required: true, helpKey: 'help.namespace' },
  repo_key: { required: true, helpKey: 'help.repo_key', placeholder: 'service' },
  display_name: {},
  repo_role: { input: 'select', options: ['code', 'service', 'config'], helpKey: 'help.repo_role' },
  description: { input: 'textarea' },
  remote_key: { required: true, helpKey: 'help.remote_key', placeholder: 'github' },
  provider_type: { input: 'select', options: ['github', 'gitea', 'git'], helpKey: 'help.provider_type', required: true },
  provider: { input: 'select', options: ['github', 'gitea'], helpKey: 'help.provider_type', required: true },
  api_base_url: { input: 'url', helpKey: 'help.api_base_url', placeholder: 'https://api.github.com' },
  web_base_url: { input: 'url', helpKey: 'help.web_base_url', placeholder: 'https://github.com' },
  token_env: { required: true, helpKey: 'help.token_env', placeholder: 'GITHUB_TOKEN' },
  default_owner: { helpKey: 'help.default_owner', placeholder: 'org-or-owner' },
  visibility: { input: 'select', options: ['private', 'public', 'internal'], helpKey: 'help.visibility' },
  remote_url: { input: 'url', helpKey: 'help.remote_url', placeholder: 'git@github.com:org/repo.git' },
  web_url: { input: 'url', helpKey: 'help.web_url', placeholder: 'https://github.com/org/repo' },
  remote_role: { input: 'select', options: ['source', 'mirror', 'target', 'config', 'origin'], helpKey: 'help.remote_role' },
  urls: { input: 'textarea', helpKey: 'help.urls', placeholder: 'git@example.com:org/repo.git, https://example.com/org/repo.git' },
  default_branch: { helpKey: 'help.default_branch', placeholder: 'main' },
  tag_name: { required: true, helpKey: 'help.tag_name', placeholder: 'v1.0.0' },
  target_sha: { required: true, helpKey: 'help.target_sha' },
  branch: { helpKey: 'help.branch', placeholder: 'main' },
  tag_message: { input: 'textarea', helpKey: 'help.tag_message' },
  trigger_mode: { input: 'select', options: ['manual', 'webhook', 'push', 'manual_or_webhook'], helpKey: 'help.trigger_mode' },
  sync_mode: { input: 'select', options: ['selected_refs', 'all_refs'], helpKey: 'help.sync_mode' },
  transport: { input: 'select', options: ['ssh', 'https'], helpKey: 'help.transport' },
  driver: { input: 'select', options: ['projectops_worker_git_ssh'], helpKey: 'help.driver' },
  enabled: { input: 'checkbox' },
  metadata_json: { input: 'textarea', helpKey: 'help.metadata_json', placeholder: '{}' },
  secret_token: { input: 'password', helpKey: 'help.secret_token' },
  server_url: { input: 'url', required: true, helpKey: 'help.server_url', placeholder: 'https://argo.example.com' },
  auth_type: { input: 'select', options: ['token', 'key', 'password'], helpKey: 'help.auth_type' },
  argo_auth_type: { input: 'select', options: ['token'], labelKey: 'field.argo_auth_type', helpKey: 'help.argo_auth_type', required: true },
  ssh_auth_type: { input: 'select', options: ['key', 'password'], labelKey: 'field.ssh_auth_type', helpKey: 'help.ssh_auth_type', required: true },
  kind: { input: 'select', options: ['ssh_key', 'ssh_password', 'git_https_password', 'git_https_token', 'argo_token', 'provider_token', 'ai_provider_api_key'], helpKey: 'help.credential_kind', required: true },
  secret_value: { input: 'textarea', helpKey: 'help.secret_value', required: true },
  public_value: { input: 'textarea', helpKey: 'help.public_value' },
  credential_id: { input: 'select', helpKey: 'help.credential_id', required: true },
  token: { input: 'password', helpKey: 'help.token' },
  argo_token: { input: 'password', labelKey: 'field.token', helpKey: 'help.argo_token' },
  insecure_skip_verify: { input: 'checkbox', helpKey: 'help.insecure_skip_verify' },
  kubeconfig_secret_ref: { required: true, helpKey: 'help.kubeconfig_secret_ref', placeholder: 'assops/test/namespace-reader' },
  service_account: { helpKey: 'help.service_account', placeholder: 'system:serviceaccount:ns:assops-reader' },
  token_subject_review_status: { input: 'select', options: ['not_reviewed', 'reviewed', 'failed', 'waived'], helpKey: 'help.token_subject_review_status' },
  rbac_read_logs_status: { input: 'select', options: ['not_reviewed', 'reviewed', 'failed', 'waived'], helpKey: 'help.rbac_read_logs_status' },
  rbac_restart_pods_status: { input: 'select', options: ['not_reviewed', 'reviewed', 'failed', 'waived'], helpKey: 'help.rbac_restart_pods_status' },
  status: { input: 'select', options: ['metadata_only', 'ready', 'disabled'], helpKey: 'help.status' },
  pod_name: { required: true, helpKey: 'help.pod_name' },
  deployment_name: { required: true, helpKey: 'help.deployment_name' },
  container_name: { helpKey: 'help.container_name' },
  tail_lines: { input: 'number', helpKey: 'help.tail_lines', placeholder: '200' },
  since_seconds: { input: 'number', helpKey: 'help.since_seconds', placeholder: '0' },
  deployment_target_id: { input: 'select', required: true, helpKey: 'help.deployment_target_id' },
  host: { required: true, helpKey: 'help.host', placeholder: 'worker-host.example.com' },
  port: { input: 'number', helpKey: 'help.port', placeholder: '22' },
  username: { required: true, helpKey: 'help.username' },
  command: { input: 'textarea', required: true, helpKey: 'help.command' },
  timeout_seconds: { input: 'number', helpKey: 'help.timeout_seconds', placeholder: '60' },
  runtime_type: { input: 'select', options: ['codex-cli'], helpKey: 'help.runtime_type' },
  codex_binary: { helpKey: 'help.codex_binary', placeholder: 'codex' },
  model: { helpKey: 'help.model' },
  prompt: { input: 'textarea', required: true, helpKey: 'help.prompt' }
};

function fieldLabel(field: string, t: (key: string) => string, metaOverride?: FieldMeta) {
  const meta = { ...(fieldMeta[field] || {}), ...(metaOverride || {}) };
  const labelKey = meta.labelKey || `field.${field}`;
  const label = t(labelKey);
  const fallback = label === labelKey ? field.replaceAll('_', ' ') : label;
  if (!meta.helpKey) return fallback;
  return (
    <Space size={4}>
      <span>{fallback}</span>
      <Tooltip title={t(meta.helpKey)}>
        <QuestionCircleOutlined className="fieldHelpIcon" />
      </Tooltip>
    </Space>
  );
}

function fieldValuePropName(field: string) {
  return fieldMeta[field]?.input === 'checkbox' ? 'checked' : 'value';
}

function fieldRules(field: string, t: (key: string) => string = createTranslator('en'), metaOverride?: FieldMeta) {
  const meta = { ...(fieldMeta[field] || {}), ...(metaOverride || {}) };
  const rules: AnyRow[] = [];
  if (meta.required || field === 'name' || field === 'title' || field === 'command') rules.push({ required: true, message: t('common.required') });
  if (field === 'server_url' || field === 'web_url' || field === 'api_base_url' || field === 'web_base_url') rules.push({ type: 'url', message: t('common.validUrl') });
  return rules;
}

function fieldInput(field: string, t: (key: string) => string, metaOverride?: FieldMeta) {
  const meta = { ...(fieldMeta[field] || {}), ...(metaOverride || {}) };
  const placeholder = meta.placeholder;
  if (meta.input === 'checkbox') return <Checkbox>{t(`field.${field}`)}</Checkbox>;
  if (meta.input === 'select' && meta.options) {
    return <Select options={meta.options.map((value) => ({ value, label: t(`option.${value}`) }))} />;
  }
  if (meta.input === 'select' && meta.optionItems) return <Select options={meta.optionItems} />;
  if (meta.input === 'textarea' || field === 'command' || field.endsWith('_json')) return <Input.TextArea autoSize={{ minRows: 3, maxRows: 8 }} placeholder={placeholder} />;
  if (meta.input === 'url' || field === 'server_url' || field.endsWith('_url')) return <Input type="url" placeholder={placeholder} />;
  if (meta.input === 'password' || field === 'token' || field === 'password' || field.endsWith('_password')) return <Input.Password placeholder={placeholder} />;
  if (meta.input === 'number') return <Input type="number" placeholder={placeholder} />;
  return <Input placeholder={placeholder} />;
}

function CreateModal({ title, open, setOpen, fields, onSubmit, initialValues, descriptionKey }: { title: string; open: boolean; setOpen: (v: boolean) => void; fields: ModalField[]; onSubmit: (values: AnyRow) => Promise<any>; initialValues?: AnyRow; descriptionKey?: string }) {
  const { t } = useI18n();
  const [form] = Form.useForm();
  const [submitting, setSubmitting] = useState(false);
  async function submit(values: AnyRow) {
    setSubmitting(true);
    try {
      await onSubmit(values);
      setOpen(false);
      form.resetFields();
    } catch (error: any) {
      message.error(error.message || t('common.requestFailed'));
    } finally {
      setSubmitting(false);
    }
  }
  return (
    <Modal title={translateTitle(title, t)} open={open} onCancel={() => setOpen(false)} onOk={() => form.submit()} confirmLoading={submitting} okButtonProps={{ disabled: submitting }} destroyOnHidden okText={t('common.ok')} cancelText={t('common.cancel')}>
      <Form form={form} layout="vertical" onFinish={submit} initialValues={initialValues}>
        {descriptionKey ? <Typography.Paragraph type="secondary">{t(descriptionKey)}</Typography.Paragraph> : null}
        {fields.map((field) => {
          const resolved = resolveModalField(field);
          return (
          <Form.Item key={resolved.name} name={resolved.name} label={fieldLabel(resolved.name, t, resolved.meta)} rules={fieldRules(resolved.name, t, resolved.meta)} valuePropName={resolved.meta.input === 'checkbox' ? 'checked' : fieldValuePropName(resolved.name)}>
            {fieldInput(resolved.name, t, resolved.meta)}
          </Form.Item>
          );
        })}
      </Form>
    </Modal>
  );
}

function LanguageSwitch() {
  const { lang, setLang, t } = useI18n();
  return (
    <Select
      aria-label={t('app.language')}
      className="languageSwitch"
      size="small"
      value={lang}
      onChange={(value) => setLang(value as Language)}
      options={[
        { value: 'en', label: t('lang.en') },
        { value: 'zh', label: t('lang.zh') }
      ]}
    />
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
  const [lang, setLangState] = useState<Language>(getInitialLanguage);
  const [authed, setAuthed] = useState(Boolean(authToken()));
  const [page, setPage] = useState('dashboard');
  const t = useMemo(() => createTranslator(lang), [lang]);
  const setLang = (next: Language) => {
    localStorage.setItem('assops_lang', next);
    setLangState(next);
  };
  const i18nValue = useMemo(() => ({ lang, setLang, t }), [lang, t]);
  const menu = useMemo(() => [
    { key: 'dashboard', icon: <DashboardOutlined />, label: t('menu.dashboard') },
    { key: 'assets', icon: <AppstoreOutlined />, label: t('menu.assets') },
    { key: 'projects', icon: <AppstoreOutlined />, label: t('menu.projects') },
    { key: 'providers', icon: <ApiOutlined />, label: t('menu.providers') },
    { key: 'detail', icon: <BranchesOutlined />, label: t('menu.detail') },
    { key: 'remotes', icon: <ApiOutlined />, label: t('menu.remotes') },
    { key: 'operations', icon: <PlayCircleOutlined />, label: t('menu.operations') },
    { key: 'nodes', icon: <CloudServerOutlined />, label: t('menu.nodes') },
    { key: 'ai', icon: <CodeOutlined />, label: t('menu.ai') },
    { key: 'agent', icon: <RobotOutlined />, label: t('menu.agent') },
    { key: 'config', icon: <DeploymentUnitOutlined />, label: t('menu.config') }
  ], [t]);
  const content: Record<string, React.ReactNode> = {
    dashboard: <Dashboard />, assets: <AssetCenter />, projects: <Projects />, providers: <ProviderAccounts />, detail: <ProjectDetail />, remotes: <GitRemotes />, operations: <Operations />, nodes: <WorkerNodes />, ai: <AIRuntime />, agent: <AgentTasks />, config: <ConfigPage />
  };
  const body = !authed ? (
    <Login onLogin={() => setAuthed(true)} />
  ) : (
    <>
      <MobileAIHome setPage={setPage} />
      <Layout className="appShell">
        <Sider width={240} breakpoint="lg" collapsedWidth={0}><div className="brand"><SettingOutlined /> ASSOPS</div><Menu theme="dark" mode="inline" selectedKeys={[page]} items={menu} onClick={(e) => setPage(e.key)} /></Sider>
        <Layout>
          <Header className="topbar">
            <Typography.Text strong>{t('app.controlPlane')}</Typography.Text>
            <Space className="topbarActions">
              <LanguageSwitch />
              <Button onClick={() => { localStorage.removeItem('assops_token'); setAuthed(false); }}>{t('app.signOut')}</Button>
            </Space>
          </Header>
          <Content className="content">{content[page]}</Content>
        </Layout>
      </Layout>
    </>
  );
  return (
    <I18nContext.Provider value={i18nValue}>
      <ConfigProvider locale={lang === 'zh' ? zhCN : enUS} theme={{ token: { borderRadius: 6, colorPrimary: '#1677ff' } }}>
        {body}
      </ConfigProvider>
    </I18nContext.Provider>
  );
}

createRoot(document.getElementById('root')!).render(<App />);
