#!/usr/bin/env bash
set -euo pipefail

python3 - <<'PY'
from pathlib import Path
import sys

import yaml

root = Path('.github/workflows')
required = {
    'ci.yml',
    'release.yml',
    'production-retained-backup.yml',
    'production-restore-rehearsal.yml',
    'promote-production.yml',
    'restore-rehearsal.yml',
}

errors = []

def load(name):
    path = root / name
    if not path.exists():
        errors.append(f'missing workflow: {name}')
        return {}, ''
    text = path.read_text(encoding='utf-8')
    try:
        data = yaml.load(text, Loader=yaml.BaseLoader) or {}
    except Exception as exc:  # pragma: no cover - shell script diagnostic
        errors.append(f'{name} is not valid YAML: {exc}')
        data = {}
    return data, text

def has(text, needle, name):
    if needle not in text:
        errors.append(f'{name} missing expected text: {needle}')

def job(data, name, job_name):
    value = (data.get('jobs') or {}).get(job_name)
    if not isinstance(value, dict):
        errors.append(f'{name} missing job: {job_name}')
        return {}
    return value

for workflow in required:
    load(workflow)

ci, ci_text = load('ci.yml')
ci_jobs = ci.get('jobs') or {}
for expected in ['workflows', 'secrets', 'go', 'web', 'compose', 'smoke-scripts']:
    if expected not in ci_jobs:
        errors.append(f'ci.yml missing job: {expected}')
has(ci_text, 'go install github.com/rhysd/actionlint/cmd/actionlint@v1.7.7', 'ci.yml')
has(ci_text, 'run: actionlint', 'ci.yml')
has(ci_text, 'gitleaks/gitleaks-action@v3', 'ci.yml')
has(ci_text, 'make compose-config-check', 'ci.yml')

release, release_text = load('release.yml')
release_jobs = release.get('jobs') or {}
for expected in ['artifacts', 'docker-smoke', 'docker-publish', 'published-image-preflight']:
    if expected not in release_jobs:
        errors.append(f'release.yml missing job: {expected}')
docker_publish = job(release, 'release.yml', 'docker-publish')
if docker_publish.get('if') != "startsWith(github.ref, 'refs/tags/v')":
    errors.append('release.yml docker-publish must be tag-gated')
published_preflight = job(release, 'release.yml', 'published-image-preflight')
if published_preflight.get('if') != "startsWith(github.ref, 'refs/tags/v')":
    errors.append('release.yml published-image-preflight must be tag-gated')
has(release_text, 'docker/login-action@v3', 'release.yml')
has(release_text, 'push: true', 'release.yml')
has(release_text, 'make helm-test-image-preflight', 'release.yml')
has(release_text, 'actions/attest@v4', 'release.yml')

retained, retained_text = load('production-retained-backup.yml')
retained_publish = job(retained, 'production-retained-backup.yml', 'publish')
if retained_publish.get('if') != "${{ github.event_name == 'workflow_dispatch' || vars.ASSOPS_PRODUCTION_RETAINED_BACKUP_ENABLED == 'true' }}":
    errors.append('production-retained-backup.yml publish job must stay default-off for schedules')
if 'environment' not in retained_publish:
    errors.append('production-retained-backup.yml publish job must use GitHub environment')
has(retained_text, 'DATABASE_URL: ${{ secrets.ASSOPS_ACTIVE_DATABASE_URL }}', 'production-retained-backup.yml')
has(retained_text, 'set +x', 'production-retained-backup.yml')
has(retained_text, "-iname '*.pem'", 'production-retained-backup.yml')
has(retained_text, 'actions/upload-artifact@v4', 'production-retained-backup.yml')
has(retained_text, 'Retained backup artifact must contain exactly one assops-*.dump file', 'production-retained-backup.yml')

restore, restore_text = load('production-restore-rehearsal.yml')
restore_job = job(restore, 'production-restore-rehearsal.yml', 'rehearse')
if restore_job.get('if') != "${{ github.event_name == 'workflow_dispatch' || vars.ASSOPS_PRODUCTION_RESTORE_REHEARSAL_ENABLED == 'true' }}":
    errors.append('production-restore-rehearsal.yml rehearse job must stay default-off for schedules')
if 'environment' not in restore_job:
    errors.append('production-restore-rehearsal.yml rehearse job must use GitHub environment')
has(restore_text, 'TARGET_DATABASE_URL: ${{ secrets.ASSOPS_REHEARSAL_DATABASE_URL }}', 'production-restore-rehearsal.yml')
has(restore_text, 'Use either backup_artifact_name or backup_path, not both', 'production-restore-rehearsal.yml')
has(restore_text, 'backup_artifact_name or backup_path is required', 'production-restore-rehearsal.yml')
has(restore_text, 'Retained backup artifact must contain exactly one assops-*.dump file', 'production-restore-rehearsal.yml')
has(restore_text, 'target_database must not include a password', 'production-restore-rehearsal.yml')
has(restore_text, 'actions/upload-artifact@v4', 'production-restore-rehearsal.yml')

promote, promote_text = load('promote-production.yml')
promote_jobs = promote.get('jobs') or {}
for expected in ['preflight', 'deploy']:
    if expected not in promote_jobs:
        errors.append(f'promote-production.yml missing job: {expected}')
deploy = job(promote, 'promote-production.yml', 'deploy')
if deploy.get('if') != '${{ inputs.deploy }}':
    errors.append('promote-production.yml deploy job must require deploy input')
if deploy.get('environment') != 'production':
    errors.append('promote-production.yml deploy job must use production environment')
has(promote_text, 'gh attestation verify', 'promote-production.yml')
has(promote_text, 'helm lint deploy/helm/assops', 'promote-production.yml')
has(promote_text, 'helm template', 'promote-production.yml')
has(promote_text, 'helm upgrade --install', 'promote-production.yml')
has(promote_text, 'KUBE_CONFIG_B64: ${{ secrets.KUBE_CONFIG_B64 }}', 'promote-production.yml')

restore_ci, restore_ci_text = load('restore-rehearsal.yml')
has(restore_ci_text, 'Do not reuse this workflow shape with production database credentials.', 'restore-rehearsal.yml')
has(restore_ci_text, 'postgres:16', 'restore-rehearsal.yml')

if errors:
    for item in errors:
        print(item, file=sys.stderr)
    raise SystemExit(1)

print('workflow-safety self-test passed')
PY
