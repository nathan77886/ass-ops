# ASSOPS

ASSOPS is an operations control plane for projects, Git remotes, sync jobs, approval audits, Argo / Kubernetes metadata, SSH operations, and node agents.

## Features

- Project, repository, Git remote, and Repo Sync asset management.
- Gateway HTTP API, control worker, and node agent.
- PostgreSQL persistence.
- Approval-gated audit flow for high-risk operations.
- Runtime integrations for GitHub Actions, Argo CD, Kubernetes, and SSH.
- AI-agent-readable project context generation.

## Components

| Component | Description | Deployment |
| --- | --- | --- |
| `gateway` | HTTP API and control entrypoint | Docker / Kubernetes image only |
| `worker` | Background control worker | Docker / Kubernetes image only |
| `node-worker` | Node agent / execution node | Image deployment or non-image local process |
| Web | Console UI | Not built as an image by this repository's GitHub Actions |
| PostgreSQL | Primary database | External database or deployment-managed database |

## Images

This repository's GitHub Actions only builds and publishes runtime images:

```text
ghcr.io/<owner>/assops-gateway:<tag>
ghcr.io/<owner>/assops-worker:<tag>
ghcr.io/<owner>/assops-node-worker:<tag>
```

Push a `v*` tag or run the `Build Images` workflow manually, then deploy by pulling images from this repository's GHCR packages.

## Deployment

Deployment guides:

- [中文部署文档](docs/deployment.md)
- [English deployment guide](docs/deployment.en.md)

Core rules:

- `gateway` and `worker` must be deployed from GHCR images.
- `node-worker` can be deployed from a GHCR image or run as a local process / binary.
- Docker / Kubernetes deployments should pull runtime images from this repository's GHCR packages.
- Web images are not built by the current GitHub Actions workflow.

## Required Configuration

```bash
DATABASE_URL='postgres://assops:password@postgres:5432/assops?sslmode=disable'
ASSOPS_GATEWAY_URL='https://assops.example.com'
ASSOPS_JWT_SECRET='change-me'
ASSOPS_WEBHOOK_SECRET_KEY='change-me'
ASSOPS_ADMIN_EMAIL='admin@example.com'
ASSOPS_ADMIN_PASSWORD='change-me'
```

Common optional configuration:

```bash
ASSOPS_GITHUB_ACTIONS_READ_TOKEN=''
ASSOPS_ARGO_READ_TOKEN=''
ASSOPS_KUBERNETES_LOGS_ENABLED='false'
ASSOPS_KUBERNETES_RESTARTS_ENABLED='false'
ASSOPS_SSH_KEY_DIR='/etc/assops/ssh/keys'
ASSOPS_SSH_KNOWN_HOSTS_DIR='/etc/assops/ssh/known_hosts'
```

## Local Quick Start

For local verification only:

```bash
docker compose -f deploy/docker-compose.yml up --build
```

Default local account:

```text
admin@assops.local
admin1234
```

## License

See the repository license file.
