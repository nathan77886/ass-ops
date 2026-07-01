# ASSOPS 部署文档

本文档说明如何从当前仓库的 GHCR 包拉取镜像并部署 ASSOPS。

## 镜像

GitHub Actions 只发布 3 个运行时镜像：

```text
ghcr.io/<owner>/assops-gateway:<tag>
ghcr.io/<owner>/assops-worker:<tag>
ghcr.io/<owner>/assops-node-worker:<tag>
```

`<owner>` 是仓库 owner 或组织名的小写形式，`<tag>` 通常是 `v*` release tag 或 commit SHA 短标签。

`gateway` 和 `worker` 只支持 Docker / Kubernetes 镜像部署。`node-worker` 可以使用镜像部署，也可以在节点上以本地进程方式运行。

## 必填配置

```bash
DATABASE_URL='postgres://assops:password@postgres:5432/assops?sslmode=disable'
ASSOPS_GATEWAY_URL='https://assops.example.com'
ASSOPS_JWT_SECRET='change-me'
ASSOPS_WEBHOOK_SECRET_KEY='change-me'
ASSOPS_ADMIN_EMAIL='admin@example.com'
ASSOPS_ADMIN_PASSWORD='change-me'
```

生产环境必须使用强随机值，不要提交真实密钥、Token、Cookie、数据库连接串或 kubeconfig。

## Docker 部署

示例：

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

  node-worker:
    image: ghcr.io/<owner>/assops-node-worker:<tag>
    restart: unless-stopped
    command: ["-name", "node-1", "-kind", "local", "-capabilities", "echo,git,ssh,ai"]
    environment:
      ASSOPS_GATEWAY_URL: http://gateway:8080
```

如 GHCR 包为私有，需要先登录：

```bash
docker login ghcr.io
docker pull ghcr.io/<owner>/assops-gateway:<tag>
docker pull ghcr.io/<owner>/assops-worker:<tag>
docker pull ghcr.io/<owner>/assops-node-worker:<tag>
```

## Kubernetes / Helm 部署

使用 Helm values 指向 GHCR 镜像：

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

应用 Secret 至少需要这些 key：

```text
DATABASE_URL
ASSOPS_JWT_SECRET
ASSOPS_WEBHOOK_SECRET_KEY
ASSOPS_ADMIN_EMAIL
ASSOPS_ADMIN_PASSWORD
```

安装示例：

```bash
helm upgrade --install assops deploy/helm/assops \
  --namespace assops \
  --create-namespace \
  --wait \
  --wait-for-jobs \
  -f /path/to/values.yaml
```

私有 GHCR 包需要在命名空间内创建镜像拉取 Secret，并在 values 中引用：

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

## Node Agent 非镜像部署

`node-worker` 可在目标节点直接运行：

```bash
ASSOPS_GATEWAY_URL='https://assops.example.com' \
ASSOPS_NODE_WORKER_HEALTH_ADDR=':8082' \
node-worker -name node-1 -kind local -capabilities echo,git,ssh,ai
```

非镜像部署时由环境自行维护二进制、进程守护、日志和升级策略。

## Web

Web 不由本仓库 GitHub Actions 构建镜像。按你的环境自行部署静态前端或已有前端发布链路，并确保 API 指向 `ASSOPS_GATEWAY_URL`。

## 常用可选配置

```bash
ASSOPS_APPROVAL_WEBHOOK_URL=''
ASSOPS_APPROVAL_WEBHOOK_TOKEN=''
ASSOPS_GITHUB_ACTIONS_READ_TOKEN=''
ASSOPS_ARGO_READ_TOKEN=''
ASSOPS_KUBERNETES_LOGS_ENABLED='false'
ASSOPS_KUBERNETES_RESTARTS_ENABLED='false'
ASSOPS_KUBECONFIG_SECRET_DIR='/etc/assops/kubeconfigs'
ASSOPS_KUBECTL_PATH='kubectl'
ASSOPS_SSH_KEY_DIR='/etc/assops/ssh/keys'
ASSOPS_SSH_KNOWN_HOSTS_DIR='/etc/assops/ssh/known_hosts'
```
