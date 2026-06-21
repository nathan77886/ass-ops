# ASSOPS

ASSOPS is an operations control-plane MVP for projects, Git remotes, worker jobs, node workers, AI runtime context, agent plans, Argo app metadata, and SSH machine metadata.

## Stack

- Backend: Go HTTP API
- Worker: Go control-worker
- Node Worker: Go polling worker
- Tool: Go CLI
- Frontend: Vite + React + TypeScript + Ant Design
- Database: PostgreSQL
- Local deploy: Docker Compose

## Quick Start

```bash
docker compose -f deploy/docker-compose.yml up -d postgres
go run ./backend/cmd/gateway
go run ./backend/cmd/worker
go run ./backend/cmd/node-worker
cd web && pnpm install && pnpm dev
```

Open `http://localhost:5173`.

Default login:

- Email: `admin@assops.local`
- Password: `admin1234`

Override with:

```bash
ASSOPS_ADMIN_EMAIL=you@example.com ASSOPS_ADMIN_PASSWORD='change-me' go run ./backend/cmd/gateway
```

## Useful Commands

```bash
make postgres
make gateway
make worker
make node-worker
make web
make test
make build
```

## Context Files

Create a project in the UI, then generate context from Project Detail or call:

```bash
curl -X POST http://localhost:8080/api/projects/$PROJECT_ID/context/generate \
  -H "Authorization: Bearer $ASSOPS_TOKEN"
```

Generated files are written under `.assops/context/<project-id>/`:

- `ASSOPS_CONTEXT.md`
- `assops-context.json`
- `tool-manifest.json`

The CLI can read those files:

```bash
go run ./backend/cmd/assops-tool project brief
go run ./backend/cmd/assops-tool repo remotes
go run ./backend/cmd/assops-tool remote actions
go run ./backend/cmd/assops-tool plan validate
```

## MVP Adapter Boundaries

Implemented as real local code:

- Auth login and current user
- PostgreSQL migration and admin seed
- Project, repository, GitRemote CRUD
- Operation runs, worker jobs, operation logs
- Control-worker queue consumption
- Node-worker register, heartbeat, claim, log upload, complete/fail
- AI runtime CRUD and verify marker
- Agent task, generated plan, approve plan, execute-plan operation enqueue
- Argo connection, Argo app list, SSH machine CRUD
- ContextBuilder writing ASSOPS context files
- assops-tool local context commands and operations API query

Adapter/mock in this MVP:

- Real Git sync and tag execution
- Real GitHub Actions API calls
- Real SSH command execution
- Real Argo API calls
- Real Codex CLI process launch

## Remote Repository Tomorrow

When the remote repository exists:

```bash
git status
git add .
git commit -m "bootstrap assops mvp skeleton"
git remote add origin <REMOTE_URL>
git push -u origin master
```

Use `main` instead of `master` if the remote is created with `main` as the default branch.

