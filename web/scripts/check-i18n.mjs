import { readdirSync, readFileSync } from 'node:fs';

const source = [
  '../src/main.tsx',
  '../src/main.bundle.js'
].map((path) => readFileSync(new URL(path, import.meta.url), 'utf8')).join('\n');

function fail(message) {
  console.error(`i18n check failed: ${message}`);
  process.exitCode = 1;
}

function dictionaryKeys(name) {
  const dir = new URL('../src/i18n/', import.meta.url);
  const keys = new Set();
  for (const file of readdirSync(dir).filter((item) => item.startsWith(`${name}_part`) && item.endsWith('.ts')).sort()) {
    const part = readFileSync(new URL(file, dir), 'utf8');
    for (const match of part.matchAll(/^\s*'([^']+)':/gm)) keys.add(match[1]);
  }
  if (!keys.size) throw new Error(`dictionary section ${name} not found`);
  return keys;
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

const requiredFieldMetaSnippets = [];

for (const snippet of requiredFieldMetaSnippets) {
  if (!source.includes(snippet)) fail(`required Argo/Kubernetes select field metadata changed or disappeared: ${snippet}`);
}

const requiredHelpBindings = [];

for (const snippet of requiredHelpBindings) {
  if (!source.includes(snippet)) fail(`required help tooltip binding changed or disappeared: ${snippet}`);
}

if (process.exitCode) {
  process.exit(process.exitCode);
}

console.log(`i18n check passed: ${en.size} English keys, ${zh.size} Chinese keys, ${usedLiteralKeys.size} literal uses`);
