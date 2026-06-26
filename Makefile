.PHONY: postgres compose-up compose-down gateway worker node-worker web build test tool context db-migrate db-migrations db-seed-demo db-sync-assets db-backup-retain db-rehearse-restore api-smoke api-smoke-self-test helm-test-smoke helm-test-smoke-self-test first-deployable-check release-validate-bundle release-helm-values release-helm-test-readiness-plan release-promotion-plan release-backup-schedule-plan helm-lint helm-template helm-smoke

postgres:
	docker compose -f deploy/docker-compose.yml up -d postgres

compose-up:
	docker compose -f deploy/docker-compose.yml up --build

compose-down:
	docker compose -f deploy/docker-compose.yml down

gateway:
	go run ./backend/cmd/gateway

worker:
	go run ./backend/cmd/worker

node-worker:
	go run ./backend/cmd/node-worker

web:
	cd web && pnpm install && pnpm dev

build:
	go build ./...
	cd web && pnpm install && pnpm build

test:
	go test ./...

tool:
	go run ./backend/cmd/assops-tool --help

context:
	go run ./backend/cmd/assops-tool project brief

db-migrate:
	go run ./backend/cmd/assops-tool db migrate

db-migrations:
	go run ./backend/cmd/assops-tool db migrations

db-seed-demo:
	go run ./backend/cmd/assops-tool db seed-demo

db-sync-assets:
	go run ./backend/cmd/assops-tool db sync-assets

db-backup-retain:
	go run ./backend/cmd/assops-tool db backup-retain .assops/backups 3

db-rehearse-restore:
	@test -n "$(BACKUP)" || (echo "BACKUP=/path/to/assops.dump is required" && exit 1)
	@test -f "$(BACKUP)" || (echo "BACKUP=$(BACKUP) does not exist" && exit 1)
	@test -n "$(TARGET_DATABASE_URL)" || (echo "TARGET_DATABASE_URL=postgres://.../assops_restore_test is required" && exit 1)
	go run ./backend/cmd/assops-tool db rehearse-restore "$(BACKUP)" "$(TARGET_DATABASE_URL)" $(if $(REHEARSAL_REPORT),"$(REHEARSAL_REPORT)")

api-smoke:
	bash scripts/api-smoke.sh

api-smoke-self-test:
	bash scripts/api-smoke-self-test.sh

helm-test-smoke:
	bash scripts/helm-test-smoke.sh

helm-test-smoke-self-test:
	bash scripts/helm-test-smoke-self-test.sh

first-deployable-check:
	@set -e; \
	for bin in go pnpm helm curl python3; do \
		command -v "$$bin" >/dev/null || { echo "$$bin is required for first-deployable-check"; exit 1; }; \
	done; \
	go test ./...; \
	pnpm -C web install --frozen-lockfile; \
	pnpm -C web run i18n:check; \
	pnpm -C web build; \
	bash scripts/api-smoke-self-test.sh; \
	go run ./backend/cmd/assops-tool release helm-test-readiness-plan \
		deploy/helm/assops/values.test.example.yaml \
		/tmp/assops-helm-test-readiness-plan.md; \
	helm lint deploy/helm/assops; \
	helm template assops deploy/helm/assops \
		-f deploy/helm/assops/values.test.example.yaml \
		>/tmp/assops-test-rendered.yaml; \
	echo "first-deployable-check passed"

release-validate-bundle:
	@test -n "$(ARTIFACT_DIR)" || (echo "ARTIFACT_DIR=/path/to/release-artifacts is required" && exit 1)
	@test -d "$(ARTIFACT_DIR)" || (echo "ARTIFACT_DIR=$(ARTIFACT_DIR) does not exist" && exit 1)
	@test -n "$(REHEARSAL_REPORT)" || (echo "REHEARSAL_REPORT=/path/to/restore-rehearsal.json is required" && exit 1)
	@test -f "$(REHEARSAL_REPORT)" || (echo "REHEARSAL_REPORT=$(REHEARSAL_REPORT) does not exist" && exit 1)
	go run ./backend/cmd/assops-tool release validate-bundle "$(ARTIFACT_DIR)" "$(REHEARSAL_REPORT)"

release-helm-values:
	@test -n "$(GHCR_OWNER)" || (echo "GHCR_OWNER=github-owner-or-org is required" && exit 1)
	@test -n "$(VERSION)" || (echo "VERSION=v0.1.0 is required" && exit 1)
	go run ./backend/cmd/assops-tool release helm-values "$(GHCR_OWNER)" "$(VERSION)" $(if $(OUTPUT),"$(OUTPUT)")

release-helm-test-readiness-plan:
	go run ./backend/cmd/assops-tool release helm-test-readiness-plan deploy/helm/assops/values.test.example.yaml $(if $(OUTPUT),"$(OUTPUT)")

release-promotion-plan:
	@test -n "$(REPO)" || (echo "REPO=owner/repo is required" && exit 1)
	@test -n "$(GHCR_OWNER)" || (echo "GHCR_OWNER=github-owner-or-org is required" && exit 1)
	@test -n "$(VERSION)" || (echo "VERSION=v0.1.0 is required" && exit 1)
	@test -n "$(ARTIFACT_DIR)" || (echo "ARTIFACT_DIR=/path/to/release-artifacts is required" && exit 1)
	@test -n "$(REHEARSAL_REPORT)" || (echo "REHEARSAL_REPORT=/path/to/restore-rehearsal.json is required" && exit 1)
	@test -n "$(HELM_VALUES)" || (echo "HELM_VALUES=/path/to/helm-values.yaml is required" && exit 1)
	go run ./backend/cmd/assops-tool release promotion-plan "$(REPO)" "$(GHCR_OWNER)" "$(VERSION)" "$(ARTIFACT_DIR)" "$(REHEARSAL_REPORT)" "$(HELM_VALUES)" $(if $(OUTPUT),"$(OUTPUT)")

release-backup-schedule-plan:
	@test -n "$(REPO)" || (echo "REPO=owner/repo is required" && exit 1)
	@test -n "$(ENV)" || (echo "ENV=production is required" && exit 1)
	@test -n "$(RUNNER)" || (echo "RUNNER=ubuntu-latest or self-hosted label is required" && exit 1)
	@test -n "$(CRON)" || (echo "CRON='17 3 * * 1' is required" && exit 1)
	@test -n "$(BACKUP_SOURCE)" || (echo "BACKUP_SOURCE=artifact:name or path:/mounted/assops.dump is required" && exit 1)
	@test -n "$(RETENTION_DAYS)" || (echo "RETENTION_DAYS=14 is required" && exit 1)
	go run ./backend/cmd/assops-tool release backup-schedule-plan "$(REPO)" "$(ENV)" "$(RUNNER)" "$(CRON)" "$(BACKUP_SOURCE)" "$(RETENTION_DAYS)" $(if $(OUTPUT),"$(OUTPUT)")

helm-lint:
	helm lint deploy/helm/assops

helm-template:
	helm template assops deploy/helm/assops

helm-smoke:
	@set -e; \
	cluster="$${KIND_CLUSTER:-assops-smoke}"; \
	namespace="$${HELM_SMOKE_NAMESPACE:-assops-smoke}"; \
	release="$${HELM_SMOKE_RELEASE:-assops}"; \
	for bin in docker kind helm kubectl curl; do \
		command -v "$$bin" >/dev/null || { echo "$$bin is required"; exit 1; }; \
	done; \
	cleanup() { kind delete cluster --name "$$cluster" >/dev/null 2>&1 || true; }; \
	trap cleanup EXIT; \
	kind delete cluster --name "$$cluster" >/dev/null 2>&1 || true; \
	kind create cluster --name "$$cluster" --wait 120s; \
	docker build --target gateway -t assops/gateway:ci .; \
	docker build --target worker -t assops/worker:ci .; \
	docker build --target node-worker -t assops/node-worker:ci .; \
	docker build --target web -t assops/web:ci .; \
	kind load docker-image --name "$$cluster" assops/gateway:ci; \
	kind load docker-image --name "$$cluster" assops/worker:ci; \
	kind load docker-image --name "$$cluster" assops/node-worker:ci; \
	kind load docker-image --name "$$cluster" assops/web:ci; \
	helm upgrade --install "$$release" deploy/helm/assops \
		--namespace "$$namespace" \
		--create-namespace \
		--wait \
		--wait-for-jobs \
		--timeout 10m \
		--set image.registry= \
		--set image.pullPolicy=IfNotPresent \
		--set image.gateway.tag=ci \
		--set image.worker.tag=ci \
		--set image.nodeWorker.tag=ci \
		--set image.web.tag=ci \
		--set env.version=ci \
		--set env.commit=local-smoke \
		--set env.buildTime=local-smoke \
		--set persistence.context.enabled=false \
		--set persistence.bareRepos.enabled=false \
		--set persistence.ssh.enabled=false \
		--set persistence.kubeconfigs.enabled=false \
		--set persistence.backups.enabled=false \
		--set postgres.storageSize=1Gi \
		--set secret.jwtSecret=ci-jwt-secret \
		--set secret.webhookSecretKey=ci-webhook-secret \
		--set secret.adminEmail=admin@assops.local \
		--set secret.adminPassword=ci-admin-password; \
	kubectl -n "$$namespace" rollout status "deployment/$${release}-gateway" --timeout=120s; \
	kubectl -n "$$namespace" rollout status "deployment/$${release}-worker" --timeout=120s; \
	kubectl -n "$$namespace" rollout status "deployment/$${release}-node-worker" --timeout=120s; \
	kubectl -n "$$namespace" rollout status "deployment/$${release}-web" --timeout=120s; \
	kubectl -n "$$namespace" get endpoints "$${release}-worker-health" -o jsonpath='{.subsets[*].addresses[*].ip}' | grep -q .; \
	kubectl -n "$$namespace" get endpoints "$${release}-node-worker-health" -o jsonpath='{.subsets[*].addresses[*].ip}' | grep -q .; \
	kubectl -n "$$namespace" port-forward "svc/$${release}-web" 18080:80 >/tmp/assops-web-port-forward.log 2>&1 & \
	pf_pid="$$!"; \
	trap 'kill "$$pf_pid" 2>/dev/null || true; cleanup' EXIT; \
	for _ in $$(seq 1 30); do \
		if response="$$(curl -fsS http://127.0.0.1:18080/healthz)"; then \
			printf '%s\n' "$$response"; \
			printf '%s' "$$response" | grep -q '"component":"gateway"'; \
			printf '%s' "$$response" | grep -q '"version":"ci"'; \
			printf '%s' "$$response" | grep -q '"commit":"local-smoke"'; \
			printf '%s' "$$response" | grep -q '"build_time":"local-smoke"'; \
			exit 0; \
		fi; \
		sleep 2; \
	done; \
	cat /tmp/assops-web-port-forward.log; \
	exit 1
