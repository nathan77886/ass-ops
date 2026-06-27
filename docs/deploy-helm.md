# Helm Deployment Runbook

This chart is a first Kubernetes-shaped deployment for ASSOPS. It renders gateway, control worker, node worker, web, migration job, optional PostgreSQL, PVCs, Services, and optional Ingress resources.

It is a manifest baseline, not an instruction to deploy into an unspecified cluster. Confirm the target namespace, storage classes, TLS, ingress, and secret handling before running any cluster-mutating command.

## Render Locally

The values below are placeholders. Do not pass real production secrets through shell history or commit rendered Secret manifests.

```bash
helm lint deploy/helm/assops
cat >/tmp/assops-dev-values.yaml <<'YAML'
secret:
  databaseURL: postgres://assops:change-me@assops-postgres:5432/assops?sslmode=disable
  jwtSecret: change-me-jwt
  webhookSecretKey: change-me-webhook
  adminEmail: admin@example.com
  adminPassword: change-me-admin
YAML
helm template assops deploy/helm/assops -f /tmp/assops-dev-values.yaml
```

The chart includes `values.schema.json`, so `helm lint` also validates common value shape mistakes such as invalid image pull policies, non-numeric worker intervals, and missing external secret names when `secret.create=false`.
CI renders the default values, `values.test.example.yaml`, and `values.production.example.yaml`, then runs a disposable kind smoke test that builds the gateway, worker, node-worker, and web images from the pull request, loads them into a temporary cluster, installs the chart with the built-in PostgreSQL, waits for the migration hook and Deployments, and probes the web Service `/healthz`. The smoke probe parses the health JSON and verifies the gateway component plus release metadata so a green run proves the chart passed build identity through the deployed gateway.

## First Test Environment

For a private test namespace, start from `deploy/helm/assops/values.test.example.yaml` and layer `deploy/helm/assops/values.test.private.example.yaml` as a copyable overlay template. The checked-in test values expect an environment-owned `assops-test-secret`; the private overlay demonstrates the chart-managed PostgreSQL and private registry image settings but still uses placeholders that must be replaced outside the repository.

The external test Secret must provide the same application keys as production:

- `DATABASE_URL`
- `ASSOPS_JWT_SECRET`
- `ASSOPS_WEBHOOK_SECRET_KEY`
- `ASSOPS_ADMIN_EMAIL`
- `ASSOPS_ADMIN_PASSWORD`
- `ASSOPS_APPROVAL_WEBHOOK_TOKEN`
- `ASSOPS_GITHUB_ACTIONS_READ_TOKEN`
- `ASSOPS_ARGO_READ_TOKEN`
- `ASSOPS_GITHUB_TEMPLATE_TOKEN`
- `ASSOPS_GITEA_TEMPLATE_TOKEN`

For a disposable namespace that uses the chart-managed PostgreSQL, enable `postgres.enabled`, `secret.create`, `secret.databaseURL`, and `postgres.password` only in a private values overlay.

Run the local first-deployable gate before attempting a test-cluster install:

```bash
make first-deployable-prereqs
make first-deployable-check
```

This installs web dependencies from the lockfile, then checks Go tests, Compose manifests, the web multilingual build gate, the Helm test readiness plan, Helm rendering with the checked-in default, test, private-test, and production values, and every checked-in first-deployable self-test target. It does not contact a Kubernetes cluster.

Self-test targets covered by `first-deployable-check`:

- `api-smoke-self-test`
- `compose-smoke-local-images-self-test`
- `first-deployable-completion-audit-self-test`
- `first-deployable-coverage-audit-self-test`
- `first-deployable-external-audit-runbook-self-test`
- `first-deployable-external-audit-validate-self-test`
- `first-deployable-external-evidence-complete-validate-self-test`
- `first-deployable-external-evidence-self-test`
- `first-deployable-external-evidence-validate-self-test`
- `first-deployable-handoff-manifest-validate-self-test`
- `first-deployable-handoff-plan-self-test`
- `first-deployable-handoff-schema-self-test`
- `first-deployable-handoff-validate-self-test`
- `helm-production-hardening-self-test`
- `helm-rollout-rehearsal-plan-self-test`
- `helm-test-image-preflight-self-test`
- `helm-test-preflight-self-test`
- `helm-test-smoke-self-test`
- `production-backup-rehearsal-plan-self-test`
- `provider-review-live-test-plan-self-test`
- `rehearsal-make-targets-self-test`
- `release-backup-schedule-plan-self-test`
- `release-branch-protection-plan-self-test`
- `release-helm-readiness-plan-self-test`
- `release-helm-test-readiness-plan-self-test`
- `release-helm-values-self-test`
- `release-images-self-test`
- `release-promotion-plan-self-test`
- `release-rehearsal-plans-self-test`
- `release-validate-bundle-self-test`
- `workflow-safety-self-test`

The `first-deployable-coverage-audit-self-test` target fails if a Make self-test target is missing from this list, from `first-deployable-check`, or from the local completion audit.
The prerequisite target is intentionally cheaper: it only verifies that `go`, `pnpm`, Helm v3, Docker, `curl`, and `python3` are on `PATH`, then prints a targeted message when one is missing.

When the Docker registry mirror cannot resolve the normal Compose build base images, run the full local runtime proof through already-local base images instead:

```bash
make first-deployable-local-runtime-check
```

That target runs `first-deployable-check`, builds current Compose smoke images from local Go binaries and `web/dist`, then runs `compose-smoke` with `ASSOPS_COMPOSE_SMOKE_BUILD=false`. It proves the disposable local runtime path, but it does not publish release images, contact a registry, install Helm, or mutate Kubernetes.

Generate a local no-call readiness plan for the checked-in test values before touching a cluster:

```bash
go run ./backend/cmd/assops-tool release helm-test-readiness-plan \
  deploy/helm/assops/values.test.example.yaml \
  .assops/release-notes/helm-test-readiness-plan.md
```

Create the namespace-scoped kubeconfig Secret out of band. The key name becomes the UI `kubeconfig_secret_ref` value:

```bash
kubectl -n assops-test create secret generic assops-kubeconfigs \
  --from-file=test-assops-reader.yaml=/path/to/namespace-scoped-kubeconfig.yaml
```

Use the same key in the ASSOPS Kubernetes environment form:

```text
kubeconfig_secret_ref = test-assops-reader.yaml
```

Render or install with the test example plus your private override:

```bash
helm template assops deploy/helm/assops \
  -n assops-test \
  -f deploy/helm/assops/values.test.example.yaml \
  -f /path/to/private-test-values.yaml
```

With the test example, gateway and worker mount `assops-kubeconfigs` read-only at `/etc/assops/kubeconfigs`, `ASSOPS_KUBERNETES_LOGS_ENABLED=true`, `ASSOPS_KUBERNETES_LOG_PREVIEW_ENABLED=false`, and `ASSOPS_KUBERNETES_RESTARTS_ENABLED=true`. Pod-log audit results remain sanitized metadata only by default. Rollout restarts are still approval-gated and require the Kubernetes environment row to be `ready` with reviewed token-subject and restart RBAC metadata before the worker can run the server dry-run and `kubectl rollout restart`. Keep restarts disabled in any shared environment until that namespace-scoped RBAC review is complete.

Provider-review live execution remains off in the checked-in test values. In a private test overlay, set `env.providerReviewExecutionEnabled: "true"`, `env.providerReviewMutationArmed: "true"`, and provide `ASSOPS_GITHUB_TEMPLATE_TOKEN` in the external Secret only after the target GitHub repository, branch/review policy, and operator approval path are reviewed. `ASSOPS_GITEA_TEMPLATE_TOKEN` is still useful for Gitea template repository creation, but the first deployable live review path is GitHub-only and uses the Approval audit `Execute live review` action to create a review branch plus pull request. Failed live executions record only the execution phase, status class, retryable flag, and `review_branch_delete_required` cleanup hint when automatic review-branch cleanup fails; provider request/response bodies, headers, token values, repository refs, raw branch names, and file content stay suppressed. When that cleanup hint is present, the Approval audit `Cleanup live review` action calls `POST /api/provider-review-attempts/{id}/cleanup-live`, deletes only the recorded review branch, and records sanitized cleanup status while preserving the original failed execution state for audit.

Before that real GitHub rehearsal, render the private overlay locally and produce the no-network test plan:

```bash
ASSOPS_PROVIDER_REVIEW_LIVE_TEST_VALUES=deploy/helm/assops/values.test.example.yaml \
ASSOPS_PROVIDER_REVIEW_LIVE_TEST_EXTRA_VALUES=/path/to/private-test-values.yaml \
ASSOPS_PROVIDER_REVIEW_LIVE_TEST_OUTPUT=.assops/provider-review-live-test-plan.md \
make provider-review-live-test-plan
```

This check verifies that the rendered ConfigMap arms both provider-review execution switches, that gateway/worker pods still reference the application Secret, and that the render does not contain common secret-shaped literals. It does not read `ASSOPS_GITHUB_TEMPLATE_TOKEN`, call GitHub, inspect the cluster Secret, push images, or contact Kubernetes. If the chart uses an external application Secret, verify out of band that the Secret contains `ASSOPS_GITHUB_TEMPLATE_TOKEN` before using `Execute live review`.

Set `env.version`, `env.commit`, and `env.buildTime` in the private overlay when deploying a tagged test build. The gateway, control worker, and node worker expose these values from `/healthz`, and the chart web service proxies `/healthz` to the gateway so the running build can be checked without opening worker ports.

Before installing, run the read-only test-environment preflight from a machine that has access to the target test cluster:

```bash
ASSOPS_HELM_PREFLIGHT_NAMESPACE=assops-test \
ASSOPS_HELM_PREFLIGHT_RELEASE=assops \
ASSOPS_HELM_PREFLIGHT_APP_SECRET=assops-test-secret \
ASSOPS_HELM_PREFLIGHT_KUBECONFIG_SECRET=assops-kubeconfigs \
ASSOPS_HELM_PREFLIGHT_KUBECONFIG_KEY=test-assops-reader.yaml \
ASSOPS_HELM_PREFLIGHT_EXTRA_VALUES=/path/to/private-test-values.yaml \
make helm-test-preflight
```

The preflight lints and renders the chart with `values.test.example.yaml` plus optional colon-separated `ASSOPS_HELM_PREFLIGHT_EXTRA_VALUES`, checks that the namespace exists, verifies the external application Secret has every required key, verifies the kubeconfig Secret contains the key that the UI should store as `kubeconfig_secret_ref`, and runs read-only `kubectl auth can-i` checks with that kubeconfig for `get pods`, `get pods/log`, and `patch deployments`. It only uses `helm lint`, `helm template`, `kubectl get`, and `kubectl auth can-i`; it does not install, upgrade, delete, patch, restart, port-forward, or output Secret values. For RBAC validation it decodes the kubeconfig Secret key into a private temporary file, removes it on normal exit or interrupt/terminate signals, and may require manual cleanup only if the process is killed with `SIGKILL` or by the host OOM killer. Set `ASSOPS_HELM_PREFLIGHT_CHECK_KUBECONFIG_RBAC=false` to skip the kubeconfig RBAC probe, or `ASSOPS_HELM_PREFLIGHT_CHECK_RESTART_RBAC=false` when a test namespace intentionally supports log audits but not rollout restarts.

Also verify that the rendered images are available to the registry credentials you will use for the test cluster:

```bash
ASSOPS_HELM_IMAGE_PREFLIGHT_VALUES=deploy/helm/assops/values.test.example.yaml \
ASSOPS_HELM_IMAGE_PREFLIGHT_EXTRA_VALUES=/path/to/private-test-values.yaml \
make helm-test-image-preflight
```

This renders the same chart values and runs `docker manifest inspect` for each referenced image with a per-image timeout. If the check reports GHCR `denied`, Docker Hub timeouts, or local image names such as `assops/gateway:local`, publish the images to an environment-accessible registry, run `docker login`, or configure image pull secrets before attempting `helm upgrade --install`.

For private registries, create the image pull Secret out of band in the target namespace, then reference only the Secret name from the private values overlay:

```bash
kubectl -n assops-test create secret docker-registry assops-registry-pull \
  --docker-server=ghcr.io \
  --docker-username='<registry-user>' \
  --docker-password='<registry-token>'
```

```yaml
image:
  pullSecrets:
    - name: assops-registry-pull
```

The normal image publication path is the `Release Candidate` GitHub Actions workflow: push a `v*` tag and wait for the workflow to publish `ghcr.io/<owner>/assops-{gateway,worker,node-worker,web}:<version>`. After the four images are pushed, the workflow generates a release image overlay and runs the same Helm image preflight against GHCR manifests, so a tag should not be promoted to a test Helm install until that job is green. For a private test release from a trusted workstation, build the same image names locally first:

```bash
ASSOPS_RELEASE_IMAGE_OWNER=nathan77886 \
ASSOPS_RELEASE_IMAGE_VERSION=v0.1.0 \
make release-images
```

After `docker login ghcr.io`, push only when you intend to publish:

```bash
ASSOPS_RELEASE_IMAGE_OWNER=nathan77886 \
ASSOPS_RELEASE_IMAGE_VERSION=v0.1.0 \
ASSOPS_RELEASE_IMAGE_PUSH=true \
make release-images
```

Then rerun `make helm-test-image-preflight` with the private test overlay that points the chart to the published tag.
`make release-images-self-test` is a no-Docker-daemon contract test for the local publication script; it verifies lowercased GHCR owner names, all four component tags, default no-push behavior, explicit push behavior, and invalid push flag rejection.

After the external application Secret, database, kubeconfig Secret, image pull access, and private values overlay are ready, install or upgrade the test release:

```bash
helm upgrade --install assops deploy/helm/assops \
  -n assops-test \
  --create-namespace \
  --wait \
  --wait-for-jobs \
  --timeout 10m \
  -f deploy/helm/assops/values.test.example.yaml \
  -f /path/to/private-test-values.yaml
```

Verify the test workloads, gateway API, worker health, and node-worker health:

```bash
ASSOPS_HELM_SMOKE_NAMESPACE=assops-test \
ASSOPS_HELM_SMOKE_RELEASE=assops \
ASSOPS_ADMIN_EMAIL=admin@example.com \
ASSOPS_ADMIN_PASSWORD='<admin-password>' \
make helm-test-smoke
```

The smoke command waits for all four Deployments, verifies worker and node-worker health Service endpoints, opens local port-forwards, runs the gateway API smoke through the web Service, and checks the worker and node-worker `/healthz` payloads. It is read-only: it does not apply manifests, mutate Kubernetes resources, or create ASSOPS rows.
By default it derives Helm object names the same way the chart does: release `assops` maps to `assops-*`, while a release such as `test` maps to `test-assops-*`. Set `ASSOPS_HELM_SMOKE_FULLNAME` if you use `fullnameOverride`.

For a fully disposable local cluster rehearsal, `make helm-smoke` creates a kind cluster, builds the four runtime images, installs the chart, waits for gateway/worker/node-worker/web rollouts, and probes `/healthz`. If the default kind node image is slow or blocked in your network, pre-pull an approved mirror and run with `KIND_NODE_IMAGE=<registry>/kindest-node:v1.31.0 make helm-smoke`.

For manual diagnosis, the equivalent checks are:

```bash
kubectl -n assops-test rollout status deployment/assops-gateway --timeout=180s
kubectl -n assops-test rollout status deployment/assops-worker --timeout=180s
kubectl -n assops-test rollout status deployment/assops-node-worker --timeout=180s
kubectl -n assops-test rollout status deployment/assops-web --timeout=180s
kubectl -n assops-test get endpoints assops-worker-health
kubectl -n assops-test get endpoints assops-node-worker-health

kubectl -n assops-test port-forward svc/assops-web 18080:80
curl -fsS http://127.0.0.1:18080/healthz
```

The health response should include `ok: true`, `component: gateway`, and the `version`, `commit`, and `build_time` values from the private overlay. If the response still shows `test`, `local`, or `unknown`, the release image overlay or private metadata values were not applied.
The worker health Services are internal ClusterIP endpoints for rollout verification and emergency port-forward checks; they expose only `/healthz` for the control worker and node worker.

Run the gateway API smoke through the same web Service port-forward:

```bash
ASSOPS_GATEWAY_URL=http://127.0.0.1:18080 \
ASSOPS_ADMIN_EMAIL=admin@example.com \
ASSOPS_ADMIN_PASSWORD='<admin-password>' \
make api-smoke
```

The smoke check verifies `/healthz`, login, project listing, and worker queue summary through the deployed gateway without creating or modifying rows.

## Production Values

Prefer an external secret for production. Start from `deploy/helm/assops/values.production.example.yaml` and commit or store an environment-specific overlay outside the chart defaults:

```yaml
secret:
  create: false
  name: assops-secret
postgres:
  enabled: false
gatewayURL: https://assops.example.com
ingress:
  enabled: true
  className: nginx
  host: assops.example.com
  tlsSecretName: assops-tls
```

The external secret must provide:

- `DATABASE_URL`
- `ASSOPS_JWT_SECRET`
- `ASSOPS_WEBHOOK_SECRET_KEY`
- `ASSOPS_ADMIN_EMAIL`
- `ASSOPS_ADMIN_PASSWORD`
- `ASSOPS_APPROVAL_WEBHOOK_TOKEN`
- `ASSOPS_GITHUB_ACTIONS_READ_TOKEN`
- `ASSOPS_ARGO_READ_TOKEN`
- `ASSOPS_GITHUB_TEMPLATE_TOKEN`
- `ASSOPS_GITEA_TEMPLATE_TOKEN`

Kubernetes pod-log audit settings are non-secret chart values under `env`:

```yaml
env:
  kubernetesLogsEnabled: "true"
  kubernetesLogPreviewEnabled: "false"
  kubernetesRestartsEnabled: "false"
  kubeconfigSecretDir: /etc/assops/kubeconfigs
  kubectlPath: kubectl
```

Provider-review live execution settings are also non-secret chart values under `env` and should stay `"false"` outside a reviewed private test overlay:

```yaml
env:
  providerReviewExecutionEnabled: "false"
  providerReviewMutationArmed: "false"
```

Before any protected production rollout, generate the offline Helm rollout rehearsal plan with the reviewed production overlay and release-image overlay:

```bash
ASSOPS_HELM_ROLLOUT_REHEARSAL_REPO=<owner>/<repo> \
ASSOPS_HELM_ROLLOUT_REHEARSAL_GHCR_OWNER=<owner> \
ASSOPS_HELM_ROLLOUT_REHEARSAL_VERSION=v0.1.0 \
ASSOPS_HELM_ROLLOUT_REHEARSAL_NAMESPACE=assops \
ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE=assops \
ASSOPS_HELM_ROLLOUT_REHEARSAL_ENVIRONMENT=production \
ASSOPS_HELM_ROLLOUT_REHEARSAL_ENV_VALUES=/backups/release-notes/values.production.reviewed.yaml \
ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE_VALUES=/backups/release-notes/helm-values-v0.1.0.yaml \
ASSOPS_HELM_ROLLOUT_REHEARSAL_PREVIOUS_VALUES=/backups/release-notes/helm-values-previous.yaml \
ASSOPS_HELM_ROLLOUT_REHEARSAL_RESTORE_REPORT=/backups/release-notes/restore-rehearsal-YYYYMMDD-HHMMSS.json \
ASSOPS_HELM_ROLLOUT_REHEARSAL_PLAN_OUTPUT=/backups/release-notes/helm-rollout-rehearsal-plan-v0.1.0.md \
make helm-rollout-rehearsal-plan
```

The check validates only non-secret local inputs and file shapes, rejects common secret-shaped paths or names, and writes a checklist without reading kubeconfigs, reading Secret data, calling GitHub, contacting registries, running Helm, invoking Argo, or mutating Kubernetes.

`kubernetesLogPreviewEnabled` should stay `false` by default. In a private test overlay, setting it to `"true"` lets approved pod-log audit operations return a short best-effort redacted preview from `operation_runs.result`; the worker caps `kubectl logs --tail` at 200 lines and the stored preview at 64 KiB. Raw logs, kubeconfig content, Kubernetes responses, and preview text are still excluded from operation logs and asset status snapshots.

For test or shared environments, prefer mounting reviewed namespace-scoped kubeconfig files from an existing Kubernetes Secret instead of preloading the chart PVC. Each Secret key is exposed as a file below `env.kubeconfigSecretDir`, so the UI `kubeconfig_secret_ref` should match the key or a relative path in that mounted Secret.

```yaml
persistence:
  kubeconfigs:
    existingSecretName: assops-kubeconfigs
```

When `persistence.kubeconfigs.existingSecretName` is set, the chart mounts that Secret read-only into gateway and worker pods and skips creating the kubeconfigs PVC.

If you install with a release name other than `assops` while using the built-in PostgreSQL, override `secret.databaseURL` so the host matches `<release-name>-assops-postgres`.

Render the production-shaped example together with a release image overlay:

```bash
ASSOPS_RELEASE_COMMIT="$(git rev-parse --short=12 HEAD)" \
ASSOPS_RELEASE_BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  go run ./backend/cmd/assops-tool release helm-values nathan77886 v0.1.0 /tmp/assops-release-values.yaml
helm template assops deploy/helm/assops \
  -f deploy/helm/assops/values.production.example.yaml \
  -f /tmp/assops-release-values.yaml
```

The generated release overlay sets image tags and the `/healthz` metadata values under `env.version`, `env.commit`, and `env.buildTime`.

Generate a local environment-readiness plan before any cluster-mutating promotion. This reads only the reviewed values overlay and checks that production-shaped guardrails are present: external Secret, external PostgreSQL, HTTPS/TLS ingress, ServiceAccount token isolation, NetworkPolicy, PodDisruptionBudget, persistent volumes, resource requests/limits, and non-root/drop-capability runtime posture.

```bash
assops-tool release helm-readiness-plan \
  deploy/helm/assops/values.production.example.yaml \
  .assops/release-notes/helm-readiness-plan.md
```

The production example sets every ASSOPS PVC to an explicit placeholder storage class (`assops-retain`). Replace it with an environment-reviewed class before rollout; do not rely on the cluster default storage class for retained context, bare repositories, SSH material, or backups.

The GitHub `Promote Production` workflow accepts an `environment_values` input. Point it at the reviewed production overlay; the workflow then adds the generated release image overlay on top.

For GitHub-based promotion, review `deploy/k8s/promotion-rbac.yaml` as a namespace-scoped starting point for the kubeconfig stored in `KUBE_CONFIG_B64`. Avoid using cluster-admin credentials for the promotion workflow.

`values.production.example.yaml` also enables a stricter runtime posture for Go workloads and migration jobs: RuntimeDefault seccomp, non-root UID/GID, dropped Linux capabilities, no privilege escalation, and read-only root filesystems. The web container has a separate security context because nginx-based images may need image-specific tuning around port binding and writable runtime paths.

The production example also enables chart-managed NetworkPolicies. The first-version policy keeps web ingress configurable by CIDR, allows gateway traffic only from web and node-worker pods, and limits the optional in-chart PostgreSQL service to ASSOPS application pods. Review these defaults against your ingress controller and CNI behavior before rollout.

The production example includes conservative global CPU and memory requests/limits so scheduler behavior is explicit. Treat them as a starting point; split per-component resource sizing after load testing gateway, worker, node-worker, web, and migration behavior.

The production example enables PodDisruptionBudgets for gateway, worker, node-worker, and web with `minAvailable: 1`. With the default single replica this intentionally blocks voluntary disruption; raise `replicaCount` or adjust the PDB before planned node maintenance.

Application Pods do not need Kubernetes API credentials. The production example creates a chart ServiceAccount but sets `automountServiceAccountToken: false`; keep promotion workflow credentials separate through the namespace-scoped `KUBE_CONFIG_B64` path.

## Safety Notes

- The migration job is a Helm post-install/post-upgrade hook that runs `assops-tool db migrate`; gateway startup also runs the same locked migration path.
- The chart does not run `db restore`, `kubectl apply`, `helm upgrade`, or any rollback action by itself.
- The default PostgreSQL is suitable for demos only. Use managed PostgreSQL for shared environments.
- Default PVCs use `ReadWriteOnce`; use a single-node cluster, a compatible scheduler placement, or `ReadWriteMany` storage before scaling beyond one node.
- SSH material is mounted from the `assops-ssh` PVC and read-only in application pods; load key files into that volume out of band.
- Kubeconfig material for pod-log metadata audits is mounted read-only in gateway/worker pods from either `persistence.kubeconfigs.existingSecretName` or the `assops-kubeconfigs` PVC. Store only reviewed namespace-scoped kubeconfig files there, and keep the UI `kubeconfig_secret_ref` as a relative path below `/etc/assops/kubeconfigs`.
- Web uses a chart-rendered nginx config so `/api` and `/healthz` route to the chart gateway Service. `/healthz` includes `component`, `version`, `commit`, and `build_time` for deployment verification.
