# ASSOPS Deployment Guide

This guide explains how to deploy ASSOPS by pulling images from this repository's GHCR packages.

## Images

GitHub Actions publishes only 3 runtime images:

```text
ghcr.io/<owner>/assops-gateway:<tag>
ghcr.io/<owner>/assops-worker:<tag>
ghcr.io/<owner>/assops-node-worker:<tag>
```

`<owner>` is the lower-case repository owner or organization name. `<tag>` is usually a `v*` release tag or a short commit SHA tag.

`gateway` and `worker` are Docker / Kubernetes image-only services. `node-worker` can be deployed as an image or run as a local process on a node.

## Required Configuration

```bash
DATABASE_URL='postgres://assops:password@postgres:5432/assops?sslmode=disable'
ASSOPS_GATEWAY_URL='https://assops.example.com'
ASSOPS_JWT_SECRET='change-me'
ASSOPS_WEBHOOK_SECRET_KEY='change-me'
ASSOPS_ADMIN_EMAIL='admin@example.com'
ASSOPS_ADMIN_PASSWORD='change-me'
```

Use strong random values in production. Do not commit real secrets, tokens, cookies, database URLs, or kubeconfigs.

## Docker Deployment

Example:

```yaml
services:
  gateway:
    image: ghcr.io/<owner>/assops-gateway:<tag>
    restart: unless-stopped
    environment:
      DATABASE_URL: postgres://assops:password@postgres:5432/assops?sslmode=disable
      ASSOPS_GATEWAY_URL: https://assops.example.com
      ASSOPS_JWT_SECRET: change-me
      ASSOPS_WEBHOOK_SECRET_KEY: change-me
      ASSOPS_ADMIN_EMAIL: admin@example.com
      ASSOPS_ADMIN_PASSWORD: change-me
    ports:
      - "8080:8080"

  worker:
    image: ghcr.io/<owner>/assops-worker:<tag>
    restart: unless-stopped
    environment:
      DATABASE_URL: postgres://assops:password@postgres:5432/assops?sslmode=disable
      ASSOPS_GATEWAY_URL: https://assops.example.com
      ASSOPS_JWT_SECRET: change-me
      ASSOPS_WEBHOOK_SECRET_KEY: change-me
    volumes:
      - /usr/local/bin/codex:/usr/local/bin/codex:ro

  node-worker:
    image: ghcr.io/<owner>/assops-node-worker:<tag>
    restart: unless-stopped
    command: ["-name", "node-1", "-kind", "local", "-capabilities", "echo,git,ssh,ai"]
    environment:
      ASSOPS_GATEWAY_URL: http://gateway:8080
```

## Git Connection Tokens and AI Runtimes

A Git connection token is the API access configuration for a Git platform such as GitHub or Gitea, including API base URL, web URL, token environment variable name, and default owner. It is used only for Git platform connections and API operations, such as creating repositories, reading branches, syncing Actions, and creating reviews. It is not used for AI execution. Store only the token environment variable name in ASSOPS, never the token value.

An AI runtime is the local AI executor the worker calls. The default runtime is Codex CLI:

```text
runtime_type: codex-cli
codex_binary: codex
```

The worker only needs the `codex_binary` executable to be available inside the container so the runtime can be registered and verified. The simplest setup is to mount the host `codex` binary into a directory on the container `PATH`:

```yaml
services:
  worker:
    volumes:
      - /usr/local/bin/codex:/usr/local/bin/codex:ro
```

If the host path is not `/usr/local/bin/codex`, check it first:

```bash
command -v codex
```

Then mount that path into the container, for example:

```yaml
services:
  worker:
    volumes:
      - /opt/codex/bin/codex:/usr/local/bin/codex:ro
```

Verify from inside the container:

```bash
docker compose exec worker codex --version
```

For Kubernetes / Helm deployments, provide `codex` to the worker container with a Secret, ConfigMap, hostPath, CSI volume, or custom image, and make sure `codex_binary` points to the in-container path. hostPath example:

```yaml
volumes:
  - name: codex-cli
    hostPath:
      path: /usr/local/bin/codex
      type: File
containers:
  - name: worker
    volumeMounts:
      - name: codex-cli
        mountPath: /usr/local/bin/codex
        readOnly: true
```

If you do not want to rely on `PATH`, set `codex_binary` to an absolute path when creating the AI runtime, such as `/usr/local/bin/codex`.

For private GHCR packages, log in first:

```bash
docker login ghcr.io
docker pull ghcr.io/<owner>/assops-gateway:<tag>
docker pull ghcr.io/<owner>/assops-worker:<tag>
docker pull ghcr.io/<owner>/assops-node-worker:<tag>
```

## Kubernetes / Helm Deployment

Point Helm values at GHCR images:

```yaml
image:
  registry: ghcr.io
  pullPolicy: IfNotPresent
  pullSecrets: []
  gateway:
    repository: <owner>/assops-gateway
    tag: <tag>
  worker:
    repository: <owner>/assops-worker
    tag: <tag>
  nodeWorker:
    repository: <owner>/assops-node-worker
    tag: <tag>
```

The application Secret must include at least these keys:

```text
DATABASE_URL
ASSOPS_JWT_SECRET
ASSOPS_WEBHOOK_SECRET_KEY
ASSOPS_ADMIN_EMAIL
ASSOPS_ADMIN_PASSWORD
```

Install example:

```bash
helm upgrade --install assops deploy/helm/assops \
  --namespace assops \
  --create-namespace \
  --wait \
  --wait-for-jobs \
  -f /path/to/values.yaml
```

Private GHCR packages require an image pull Secret in the target namespace:

```bash
kubectl -n assops create secret docker-registry ghcr-pull \
  --docker-server=ghcr.io \
  --docker-username='<github-user>' \
  --docker-password='<github-token>'
```

```yaml
image:
  pullSecrets:
    - name: ghcr-pull
```

## Non-Image Node Agent

`node-worker` can run directly on a target node:

```bash
ASSOPS_GATEWAY_URL='https://assops.example.com' \
ASSOPS_NODE_WORKER_HEALTH_ADDR=':8082' \
node-worker -name node-1 -kind local -capabilities echo,git,ssh,ai
```

For non-image deployment, the environment owns binary distribution, process supervision, logs, and upgrades.

## Web

Web images are not built by this repository's GitHub Actions. Deploy the static frontend or your existing frontend release pipeline separately, and point it at `ASSOPS_GATEWAY_URL`.

## Common Optional Configuration

```bash
ASSOPS_APPROVAL_WEBHOOK_URL=''
ASSOPS_APPROVAL_WEBHOOK_TOKEN=''
ASSOPS_GITHUB_ACTIONS_READ_TOKEN=''
ASSOPS_ARGO_READ_TOKEN=''
ASSOPS_KUBERNETES_LOGS_ENABLED='false'
ASSOPS_KUBERNETES_RESTARTS_ENABLED='false'
ASSOPS_SSH_KEY_DIR='/etc/assops/ssh/keys'
ASSOPS_SSH_KNOWN_HOSTS_DIR='/etc/assops/ssh/known_hosts'
```
