import React, { useEffect, useMemo, useState } from 'react';
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
      setError(err.message);
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

function templateProvisionGuidance(row: AnyRow) {
  const details = row.result?.details || {};
  const reconciliation = details.repository_reconciliation || {};
  if (row.result?.repository_provisioned) {
    return {
      status: 'ready',
      color: 'green',
      title: 'Repository provisioned',
      detail: 'Starter files were pushed and the repository metadata is linked to this template run.',
      next: 'Continue with RepoSync or deployment wiring.'
    };
  }
  if (row.status === 'provisioning' || row.status === 'running' || row.status === 'queued') {
    return {
      status: 'waiting',
      color: 'blue',
      title: 'Provisioning in progress',
      detail: 'The worker is still reconciling repository provisioning for this template run.',
      next: 'Wait for the run to finish before retrying.'
    };
  }
  if (reconciliation.kind) {
    const branchStrategy = reconciliation.branch_strategy || {};
    const branchStrategyReady = reconciliation.kind === 'protected_branch' && branchStrategy.strategy_status === 'planned';
    const titles: Record<string, string> = {
      existing_repository: 'Existing repository needs reconciliation',
      protected_branch: branchStrategyReady ? 'Protected branch strategy ready' : 'Protected branch guard is active',
      missing_token: 'Provider token is not configured'
    };
    return {
      status: reconciliation.kind === 'missing_token' ? 'token' : branchStrategyReady ? 'branch strategy' : 'manual reconcile',
      color: reconciliation.kind === 'missing_token' ? 'red' : 'gold',
      title: titles[String(reconciliation.kind)] || 'Repository needs reconciliation',
      detail: String((branchStrategyReady ? reconciliation.action_required : branchStrategy.message) || reconciliation.action_required || details.reason || row.result?.repository_provision_reason || 'Repository provisioning needs operator review.'),
      next: String(reconciliation.retry_after || 'Retry after the missing provider condition is fixed.')
    };
  }
  if (details.repository_exists && details.starter_push_skipped) {
    return {
      status: 'manual reconcile',
      color: 'gold',
      title: 'Existing repository needs reconciliation',
      detail: 'Starter files were skipped because the external repository already exists.',
      next: 'Review the repository contents, then set allow_existing_repository_push only when it is safe to write starter files.'
    };
  }
  if (details.starter_push_skipped) {
    return {
      status: 'manual reconcile',
      color: 'gold',
      title: 'Protected branch guard is active',
      detail: String(row.result?.repository_provision_reason || details.reason || 'Starter files were skipped by a template remote protection guard.'),
      next: 'Configure a provider-specific branch strategy or set allow_protected_branch_push only after branch protection rules are reviewed.'
    };
  }
  if (details.token_configured === false) {
    return {
      status: 'token',
      color: 'red',
      title: 'Provider token is not configured',
      detail: 'The selected provider account token environment variable is missing at runtime.',
      next: 'Rotate the provider account to a configured token env, run Check, then retry provisioning.'
    };
  }
  if (details.provider_status || details.provider_error) {
    return {
      status: 'provider',
      color: 'red',
      title: details.provider_status ? `Provider returned HTTP ${details.provider_status}` : 'Provider API error',
      detail: shortText(details.provider_error || row.result?.repository_provision_reason, 96),
      next: 'Use the provider account Check action and provider diagnostics before retrying.'
    };
  }
  if (row.result?.repository_provision_reason) {
    return {
      status: 'review',
      color: 'gold',
      title: 'Repository needs review',
      detail: shortText(row.result.repository_provision_reason, 96),
      next: 'Review the template remote metadata and retry after the missing condition is fixed.'
    };
  }
  return {
    status: 'pending',
    color: 'default',
    title: 'Provisioning not attempted',
    detail: 'No repository provisioning result has been recorded for this run yet.',
    next: 'Start or retry the template run when the provider account and template remotes are ready.'
  };
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

function countByField(rows: AnyRow[] = [], field: string) {
  return rows.reduce<Record<string, number>>((acc, row) => {
    const key = String(row[field] || '').trim();
    if (key) acc[key] = (acc[key] || 0) + 1;
    return acc;
  }, {});
}

function readinessState(done: boolean, evidence: React.ReactNode, hasPartialEvidence?: boolean) {
  if (done) return { status: 'ready', color: 'green', evidence };
  if (hasPartialEvidence ?? Boolean(evidence)) return { status: 'partial', color: 'gold', evidence };
  return { status: 'missing', color: 'red', evidence };
}

function firstVersionReadinessRows(assets: AnyRow[] = [], operations: AnyRow[] = [], approvalSummary: AnyRow = {}) {
  const assetCounts = countByField(assets, 'asset_type');
  const operationCounts = countByField(operations, 'operation_type');
  const syncTriggered = (operationCounts['repo.sync'] || 0) + (operationCounts['repo.sync_remote'] || 0);
  const webhookReady = (assetCounts.webhook_connection || 0) > 0;
  const sshRuns = (operationCounts['ssh.exec'] || 0) + (operationCounts['ssh.command'] || 0);
  const argoEvidence = (assetCounts.argo_connection || 0) + (assetCounts.deployment_target || 0) + (operationCounts['argo.apps.sync'] || 0);
  const approvalEvidence = Number(approvalSummary.total || 0);
  const pendingApprovalOps = operations.filter((row) => String(row.status || '') === 'pending_approval').length;
  const contextEvidence = (assetCounts.agent_task || 0) + (assetCounts.ai_runtime || 0);
  return [
    {
      key: 'project',
      label: 'Create/import project asset',
      next: 'Create a project or run the demo seed.',
      ...readinessState((assetCounts.project || 0) > 0, assetCounts.project || 0)
    },
    {
      key: 'repositories',
      label: 'Attach source and mirror repositories',
      next: 'Add repository metadata and at least two Git remotes.',
      ...readinessState((assetCounts.repository || 0) > 0 && (assetCounts.git_remote || 0) >= 2, `${assetCounts.repository || 0} repos / ${assetCounts.git_remote || 0} remotes`, (assetCounts.repository || 0) > 0 || (assetCounts.git_remote || 0) > 0)
    },
    {
      key: 'repo_sync',
      label: 'Define RepoSyncAsset',
      next: 'Create a RepoSyncAsset between source and mirror remotes.',
      ...readinessState((assetCounts.repo_sync || 0) > 0, assetCounts.repo_sync || 0)
    },
    {
      key: 'sync_trigger',
      label: 'Trigger sync manually and from webhook',
      next: 'Run a manual sync and configure a Gitea webhook connection.',
      ...readinessState(syncTriggered > 0 && webhookReady, `${syncTriggered} sync ops / ${assetCounts.webhook_connection || 0} webhooks`, syncTriggered > 0 || webhookReady)
    },
    {
      key: 'github_actions',
      label: 'See GitHub Actions state',
      next: 'Sync GitHub Actions for the mirror remote or receive workflow_run webhooks.',
      ...readinessState((assetCounts.pipeline_run || 0) > 0, assetCounts.pipeline_run || 0)
    },
    {
      key: 'ssh',
      label: 'Register SSH machines and audited commands',
      next: 'Register an SSH machine and run an approval-gated command.',
      ...readinessState((assetCounts.host || 0) > 0 && sshRuns > 0, `${assetCounts.host || 0} hosts / ${sshRuns} commands`, (assetCounts.host || 0) > 0 || sshRuns > 0)
    },
    {
      key: 'argo',
      label: 'Sync Argo apps to deployment targets',
      next: 'Create an Argo connection, sync apps, and inspect deployment targets.',
      ...readinessState((assetCounts.argo_connection || 0) > 0 && (assetCounts.deployment_target || 0) > 0 && (operationCounts['argo.apps.sync'] || 0) > 0, `${assetCounts.deployment_target || 0} targets / ${assetCounts.argo_connection || 0} Argo connections / ${operationCounts['argo.apps.sync'] || 0} sync ops`, argoEvidence > 0)
    },
    {
      key: 'operations',
      label: 'View operation history and logs',
      next: 'Run any controlled operation and inspect its logs.',
      ...readinessState((assetCounts.operation_run || operations.length || 0) > 0, assetCounts.operation_run || operations.length || 0)
    },
    {
      key: 'approval',
      label: 'Enforce approval for high-risk operations',
      next: 'Queue a high-risk action that creates an approval request.',
      ...readinessState(approvalEvidence > 0 || pendingApprovalOps > 0, `${approvalEvidence} approvals / ${pendingApprovalOps} pending ops`, approvalEvidence > 0 || pendingApprovalOps > 0)
    },
    {
      key: 'context',
      label: 'Generate AI-readable context from graph',
      next: 'Create an agent task or AI runtime after syncing the canonical asset ledger.',
      ...readinessState(contextEvidence > 0, contextEvidence)
    }
  ];
}

function cleanedList(values: string[] = []) {
  return values.map((value) => value.trim()).filter(Boolean);
}

function JSONBlock({ value }: { value: any }) {
  return <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{JSON.stringify(value || {}, null, 2)}</pre>;
}

function parseJSONField(value?: string) {
  const text = (value || '').trim();
  if (!text) return {};
  return JSON.parse(text);
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
  const approvalSummary = useLoad(() => api('/api/operation-approvals/summary'), []);
  const readinessRows = firstVersionReadinessRows(assets.data?.items || [], ops.data?.items || [], approvalSummary.data || {});
  const readinessCounts = countByField(readinessRows, 'status');
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>Dashboard</Typography.Title>
      <div className="metricGrid">
        <Card><Typography.Text type="secondary">Gateway</Typography.Text><Typography.Title level={3}>Online</Typography.Title></Card>
        <Card><Typography.Text type="secondary">Recent operations</Typography.Text><Typography.Title level={3}>{ops.data?.items?.length || 0}</Typography.Title></Card>
        <Card><Typography.Text type="secondary">Ready checks</Typography.Text><Typography.Title level={3}>{readinessCounts.ready || 0}/{readinessRows.length}</Typography.Title></Card>
        <Card><Typography.Text type="secondary">Needs evidence</Typography.Text><Typography.Title level={3}>{(readinessCounts.partial || 0) + (readinessCounts.missing || 0)}</Typography.Title></Card>
      </div>
      <Card title="First-Version Readiness">
        <Table<AnyRow>
          rowKey="key"
          dataSource={readinessRows}
          pagination={false}
          size="small"
          columns={[
            { title: 'Status', render: (_, row) => <Tag color={row.color}>{row.status}</Tag> },
            { title: 'Demo proof', dataIndex: 'label' },
            { title: 'Evidence', render: (_, row) => <Typography.Text>{String(row.evidence)}</Typography.Text> },
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
          { title: 'Action', render: (_, row) => canRetryTemplateProvision(row) ? <Button size="small" title={templateProvisionRetryTitle(row)} onClick={() => retryTemplateProvision(row)}>Retry provision</Button> : '-' }
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
        <Tag color={guidance.color}>{guidance.status}</Tag>
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
        <Typography.Text strong>{guidance.next}</Typography.Text>
      </Space>}
    />
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
  return (
    <Space direction="vertical" size={16} className="full">
      <Toolbar title="Provider Accounts" onCreate={() => setOpen(true)} />
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
  const [repoOpen, setRepoOpen] = useState(false);
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
          <Toolbar title="Git repositories" onCreate={() => setRepoOpen(true)} />
          <Table<AnyRow> rowKey="id" dataSource={repos.data?.items || []} pagination={false} columns={[
            { title: 'Name', dataIndex: 'name' },
            { title: 'Key', dataIndex: 'repo_key' },
            { title: 'Role', dataIndex: 'repo_role' },
            { title: 'Status', render: (_, row) => <Tag>{row.status || 'active'}</Tag> },
            { title: 'Default branch', dataIndex: 'default_branch' }
          ]} />
          <CreateModal title="Create repository" open={repoOpen} setOpen={setRepoOpen} fields={['name', 'repo_key', 'display_name', 'repo_role', 'description', 'default_branch']} onSubmit={(v) => api(`/api/projects/${project.id}/git-repositories`, { method: 'POST', body: JSON.stringify(v) }).then(repos.reload)} />
        </>
      )}
    </Space>
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
            { title: 'Action', render: (_, row) => <Button size="small" onClick={() => rotateWebhookSecret(row.id)}>Rotate secret</Button> }
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
          { title: 'Tag', dataIndex: 'tag_name' },
          { title: 'Target SHA', dataIndex: 'target_sha' },
          { title: 'Target', dataIndex: 'target_remote_id' },
          { title: 'Created', dataIndex: 'created_at' }
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
        { title: 'Notify', render: (_, row) => approvalRoles(row.notification_channels).join(', ') },
        { title: 'Escalate', render: (_, row) => row.escalation_after_minutes ? `${row.escalation_after_minutes}m -> ${approvalRoles(row.escalation_channels).join(', ') || '-'}` : '-' },
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
  const hasQueueRisk = (data.stale_nodes || 0) > 0 || (data.aged_queued_jobs || 0) > 0 || (data.stale_running_jobs || 0) > 0 || (data.failed_24h || 0) > 0;
  return (
    <Space direction="vertical" size={16} className="full">
      <Typography.Title level={2}>Worker Nodes</Typography.Title>
      <Alert showIcon message="Node workers register through /api/worker-nodes/register. Start one with go run ./backend/cmd/node-worker." />
      {summary.error && <Alert showIcon type="error" message={summary.error} />}
      {hasQueueRisk && <Alert showIcon type="warning" message="Worker queue needs attention" description={`${data.stale_nodes || 0} stale nodes, ${data.aged_queued_jobs || 0} aged queued jobs, ${data.stale_running_jobs || 0} stale running jobs, ${data.failed_24h || 0} failures in 24h.`} />}
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
  const taskDetail = useLoad(() => taskID ? api(`/api/agent/tasks/${taskID}`) : Promise.resolve(null), [taskID]);
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
  function latestPlanApproved(row: AnyRow) {
    return row.latest_plan_status === 'approved' || row.plans?.[0]?.status === 'approved';
  }
  function toolCallSummary(row: AnyRow) {
    const input = row.input || {};
    const output = row.output || {};
    if (row.tool_name === 'runtime.check') {
      return <Space size={4} wrap>
        <Tag color={output.readiness === 'verified' ? 'green' : output.readiness === 'missing' ? 'red' : 'gold'}>{output.readiness || input.status || 'unknown'}</Tag>
        <Typography.Text>{input.runtime_name || input.codex_binary || 'No runtime'}</Typography.Text>
      </Space>;
    }
    if (row.tool_name === 'patch.prepare') {
      const guardrail = output.patch_workflow_guardrail || {};
      const reasons = Array.isArray(guardrail.blocked_reasons) ? guardrail.blocked_reasons : [];
      return <Space size={4} wrap>
        <Tag color="gold">{guardrail.execution_mode || input.mode || 'simulation_only'}</Tag>
        <Tag color={guardrail.repository_mutation_allowed === true ? 'red' : 'green'}>{guardrail.repository_mutation_allowed === true ? 'Repo mutation allowed' : 'Repo mutation blocked'}</Tag>
        {reasons.length ? <Typography.Text>{reasons.length} blocked reason{reasons.length === 1 ? '' : 's'}</Typography.Text> : <Typography.Text>{output.message || 'Mutation disabled'}</Typography.Text>}
        {guardrail.next_step ? <Typography.Text type="secondary">{shortText(guardrail.next_step, 96)}</Typography.Text> : null}
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
          </Space>
          <Table<AnyRow> rowKey="id" size="small" dataSource={taskDetail.data.plans || []} pagination={{ pageSize: 4 }} columns={[
            { title: 'Status', render: (_, row) => <Tag color={row.status === 'approved' ? 'green' : 'blue'}>{row.status}</Tag> },
            { title: 'Created', dataIndex: 'created_at' },
            { title: 'Plan', render: (_, row) => <Typography.Paragraph className="mono-pre">{row.content}</Typography.Paragraph> }
          ]} />
          <Typography.Title level={5}>Tool-call audit</Typography.Title>
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
  const [sshOpen, setSSHOpen] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);
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
  const sshRuns = useLoad(() => project ? api(`/api/ssh-command-runs?project_id=${project.id}`) : Promise.resolve({ items: [] }), [project?.id]);
  const deploymentPosture = buildDeploymentPosture(
    deploymentTargets.data?.items || [],
    deploymentRecords.data?.items || [],
    rollbackPoints.data?.items || []
  );
  const rollbackGuardrail = buildRollbackGuardrail(rollbackPoints.data?.items || []);
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
    } catch (error: any) {
      message.error(error.message);
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
            <Button type="primary" onClick={() => setCommandOpen(true)} disabled={!sshPick.selectedID}>Run command</Button>
            <Button onClick={sshRuns.reload} disabled={!project}>Refresh runs</Button>
          </Space>
          <Table rowKey="id" dataSource={sshRows} pagination={false} columns={[
            { title: 'Name', dataIndex: 'name' },
            { title: 'Host', dataIndex: 'host' },
            { title: 'Port', dataIndex: 'port' },
            { title: 'User', dataIndex: 'username' },
            { title: 'Auth', dataIndex: 'auth_type' }
          ]} />
          <Table<AnyRow> rowKey="id" dataSource={sshRuns.data?.items || []} pagination={{ pageSize: 6 }} columns={[
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
          {rollbackGuardrail && <Alert showIcon type={rollbackGuardrail.type} message={rollbackGuardrail.message} description={rollbackGuardrail.description} />}
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
            { title: 'Mode', dataIndex: 'rollback_execution_mode', render: (value) => <Tag>{value || 'read_only_preview'}</Tag> },
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
  return (
    <Modal title={title} open={open} onCancel={() => setOpen(false)} onOk={() => form.submit()} destroyOnHidden>
      <Form form={form} layout="vertical" onFinish={(values) => onSubmit(values).then(() => { setOpen(false); form.resetFields(); })}>
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
