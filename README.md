# ASSOPS

ASSOPS 是面向项目运维的控制平面，用于管理项目、Git 远端、同步任务、审批审计、Argo / Kubernetes 元数据、SSH 操作和节点 Agent。

## 功能

- 项目、仓库、Git 远端和 Repo Sync 资产管理。
- Gateway HTTP API、控制 Worker、Node Agent。
- PostgreSQL 持久化。
- 审批门控的高风险操作审计。
- GitHub Actions、Argo CD、Kubernetes、SSH 等运行时集成。
- 面向 AI Agent 的项目上下文生成。

## 组件

| 组件 | 说明 | 部署方式 |
| --- | --- | --- |
| `gateway` | HTTP API 和控制入口 | 仅支持 Docker / Kubernetes 镜像部署 |
| `worker` | 后台控制 Worker | 仅支持 Docker / Kubernetes 镜像部署 |
| `node-worker` | 节点 Agent / 执行节点 | 支持镜像部署，也支持非镜像本地进程部署 |
| Web | 前端控制台 | 不由本仓库 GitHub Actions 构建镜像，按环境自行部署 |
| PostgreSQL | 主数据库 | 外部数据库或部署编排内数据库 |

## 镜像

本仓库 GitHub Actions 只构建并发布运行时镜像：

```text
ghcr.io/<owner>/assops-gateway:<tag>
ghcr.io/<owner>/assops-worker:<tag>
ghcr.io/<owner>/assops-node-worker:<tag>
```

推送 `v*` tag 或手动运行 `Build Images` workflow 后，从当前仓库的 GHCR 包拉取镜像部署。

## 部署

部署文档：

- [中文部署文档](docs/deployment.md)
- [English deployment guide](docs/deployment.en.md)

核心要求：

- `gateway` 和 `worker` 必须从 GHCR 镜像部署。
- `node-worker` 可从 GHCR 镜像部署，也可直接运行二进制或本地进程。
- Docker / Kubernetes 部署时使用当前仓库发布的 GHCR 镜像。
- Web 不在当前 GitHub Actions 中构建镜像。

## 必填配置

```bash
DATABASE_URL='postgres://assops:password@postgres:5432/assops?sslmode=disable'
ASSOPS_GATEWAY_URL='https://assops.example.com'
ASSOPS_JWT_SECRET='change-me'
ASSOPS_WEBHOOK_SECRET_KEY='change-me'
ASSOPS_ADMIN_EMAIL='admin@example.com'
ASSOPS_ADMIN_PASSWORD='change-me'
```

常用可选配置：

```bash
ASSOPS_GITHUB_ACTIONS_READ_TOKEN=''
ASSOPS_ARGO_READ_TOKEN=''
ASSOPS_KUBERNETES_LOGS_ENABLED='false'
ASSOPS_KUBERNETES_RESTARTS_ENABLED='false'
ASSOPS_SSH_KEY_DIR='/etc/assops/ssh/keys'
ASSOPS_SSH_KNOWN_HOSTS_DIR='/etc/assops/ssh/known_hosts'
```

## 本地快速启动

仅用于本地验证：

```bash
docker compose -f deploy/docker-compose.yml up --build
```

默认本地账号：

```text
admin@assops.local
admin1234
```

## License

按仓库 License 文件为准。
