import { readFileSync } from 'node:fs';

const source = readFileSync(new URL('../src/main.tsx', import.meta.url), 'utf8');

function fail(message) {
  console.error(`i18n check failed: ${message}`);
  process.exitCode = 1;
}

function section(name) {
  const marker = `  ${name}: {`;
  const start = source.indexOf(marker);
  if (start === -1) {
    throw new Error(`dictionary section ${name} not found`);
  }
  const bodyStart = source.indexOf('{', start) + 1;
  let depth = 1;
  for (let i = bodyStart; i < source.length; i += 1) {
    const char = source[i];
    if (char === '{') depth += 1;
    if (char === '}') depth -= 1;
    if (depth === 0) return source.slice(bodyStart, i);
  }
  throw new Error(`dictionary section ${name} was not closed`);
}

function dictionaryKeys(name) {
  return new Set([...section(name).matchAll(/^\s*'([^']+)':/gm)].map((match) => match[1]));
}

const en = dictionaryKeys('en');
const zh = dictionaryKeys('zh');

for (const key of en) {
  if (!zh.has(key)) fail(`missing zh translation for ${key}`);
}
for (const key of zh) {
  if (!en.has(key)) fail(`missing en translation for ${key}`);
}

const ignoredPrefixes = ['value.'];
const usedLiteralKeys = new Set([
  ...source.matchAll(/\bt\('([^']+)'\)/g)
].map((match) => match[1]).filter((key) => !ignoredPrefixes.some((prefix) => key.startsWith(prefix))));

for (const key of usedLiteralKeys) {
  if (!en.has(key)) fail(`used translation key is missing from en dictionary: ${key}`);
  if (!zh.has(key)) fail(`used translation key is missing from zh dictionary: ${key}`);
}

const requiredFirstDeployableKeys = [
  'app.language',
  'form.createArgoConnection',
  'form.addKubernetesEnvironment',
  'form.podLogQuery',
  'field.argo_auth_type',
  'field.ssh_auth_type',
  'field.kubeconfig_secret_ref',
  'field.token_subject_review_status',
  'field.rbac_read_logs_status',
  'field.rbac_restart_pods_status',
  'field.deployment_name',
  'help.argo_auth_type',
  'help.ssh_auth_type',
  'help.server_url',
  'help.token',
  'help.insecure_skip_verify',
  'help.kubeconfig_secret_ref',
  'help.token_subject_review_status',
  'help.rbac_read_logs_status',
  'help.rbac_restart_pods_status',
  'help.deployment_name',
  'help.pod_name',
  'help.tail_lines',
  'help.since_seconds',
  'k8s.backendHintReady',
  'k8s.backendHintBlocked',
  'git.syncGitHubActions',
  'git.artifactDescription'
];

for (const key of requiredFirstDeployableKeys) {
  if (!en.has(key) || !zh.has(key)) fail(`first-deployable translation key is missing: ${key}`);
}

const requiredFieldMetaSnippets = [
  "argo_auth_type: { input: 'select', options: ['token']",
  "ssh_auth_type: { input: 'select', options: ['key', 'password']",
  "token_subject_review_status: { input: 'select', options: ['not_reviewed', 'reviewed', 'failed', 'waived']",
  "rbac_read_logs_status: { input: 'select', options: ['not_reviewed', 'reviewed', 'failed', 'waived']",
  "rbac_restart_pods_status: { input: 'select', options: ['not_reviewed', 'reviewed', 'failed', 'waived']",
  "status: { input: 'select', options: ['metadata_only', 'ready', 'disabled']"
];

for (const snippet of requiredFieldMetaSnippets) {
  if (!source.includes(snippet)) fail(`required Argo/Kubernetes select field metadata changed or disappeared: ${snippet}`);
}

const requiredHelpBindings = [
  "helpKey: 'help.argo_auth_type'",
  "helpKey: 'help.ssh_auth_type'",
  "helpKey: 'help.kubeconfig_secret_ref'",
  "helpKey: 'help.token_subject_review_status'",
  "helpKey: 'help.rbac_read_logs_status'",
  "helpKey: 'help.rbac_restart_pods_status'",
  "helpKey: 'help.deployment_name'",
  "helpKey: 'help.pod_name'",
  "helpKey: 'help.tail_lines'",
  "helpKey: 'help.since_seconds'"
];

for (const snippet of requiredHelpBindings) {
  if (!source.includes(snippet)) fail(`required help tooltip binding changed or disappeared: ${snippet}`);
}

if (process.exitCode) {
  process.exit(process.exitCode);
}

console.log(`i18n check passed: ${en.size} English keys, ${zh.size} Chinese keys, ${usedLiteralKeys.size} literal uses`);
