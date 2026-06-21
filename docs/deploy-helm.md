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
CI renders both the default values and `values.production.example.yaml`, then runs a disposable kind smoke test that builds the gateway, worker, node-worker, and web images from the pull request, loads them into a temporary cluster, installs the chart with the built-in PostgreSQL, waits for the migration hook and Deployments, and probes the web Service `/healthz`.

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

If you install with a release name other than `assops` while using the built-in PostgreSQL, override `secret.databaseURL` so the host matches `<release-name>-assops-postgres`.

Render the production-shaped example together with a release image overlay:

```bash
go run ./backend/cmd/assops-tool release helm-values nathan77886 v0.1.0 /tmp/assops-release-values.yaml
helm template assops deploy/helm/assops \
  -f deploy/helm/assops/values.production.example.yaml \
  -f /tmp/assops-release-values.yaml
```

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
- Web uses a chart-rendered nginx config so `/api` and `/healthz` route to the chart gateway Service.
