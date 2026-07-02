.PHONY: postgres compose-up compose-down compose-config-check compose-smoke compose-smoke-local-images compose-smoke-local-images-self-test docker-pg18-runtime-up docker-pg18-runtime-check cloudflare-docker-route-check cloudflare-docker-runtime-evidence github-ruleset-evidence first-deployable-external-audit first-deployable-external-audit-validate first-deployable-external-audit-validate-self-test first-deployable-external-audit-runbook first-deployable-external-audit-runbook-validate first-deployable-external-audit-runbook-self-test gateway worker node-worker web build test tool context db-automigrate db-migrate db-migrations db-seed-demo db-sync-assets db-backup-retain db-rehearse-restore api-smoke api-smoke-self-test helm-test-preflight helm-test-image-preflight helm-test-image-preflight-self-test helm-test-preflight-self-test helm-test-smoke helm-test-smoke-self-test helm-production-hardening-self-test workflow-safety-self-test provider-review-live-test-plan provider-review-live-test-plan-self-test production-backup-rehearsal-plan production-backup-rehearsal-plan-self-test helm-rollout-rehearsal-plan helm-rollout-rehearsal-plan-self-test first-deployable-handoff-plan first-deployable-handoff-plan-manifest-validate first-deployable-handoff-plan-self-test first-deployable-handoff-manifest-validate-self-test first-deployable-handoff-schema-self-test first-deployable-handoff-validate first-deployable-handoff-validate-self-test first-deployable-completion-audit first-deployable-completion-audit-self-test first-deployable-coverage-audit-self-test first-deployable-external-evidence-self-test first-deployable-external-evidence-validate first-deployable-external-evidence-validate-self-test first-deployable-external-evidence-complete-validate first-deployable-external-evidence-complete-validate-self-test release-rehearsal-plans-self-test rehearsal-make-targets-self-test first-deployable-prereqs first-deployable-check first-deployable-local-runtime-check release-images release-images-self-test release-validate-bundle release-validate-bundle-self-test release-helm-values release-helm-values-self-test release-helm-readiness-plan release-helm-readiness-plan-self-test release-helm-test-readiness-plan release-helm-test-readiness-plan-self-test release-promotion-plan release-promotion-plan-self-test release-backup-schedule-plan release-backup-schedule-plan-self-test release-branch-protection-plan release-branch-protection-plan-self-test helm-lint helm-template helm-smoke

postgres:
	docker compose -f deploy/docker-compose.yml up -d postgres

compose-up:
	docker compose -f deploy/docker-compose.yml up --build

compose-down:
	docker compose -f deploy/docker-compose.yml down

compose-config-check:
	bash scripts/compose-config-check.sh

compose-smoke:
	bash scripts/compose-smoke.sh

compose-smoke-local-images:
	bash scripts/compose-smoke-local-images.sh

compose-smoke-local-images-self-test:
	bash scripts/compose-smoke-local-images-self-test.sh

docker-pg18-runtime-up:
	bash scripts/docker-pg18-runtime-up.sh

docker-pg18-runtime-check:
	bash scripts/docker-pg18-runtime-check.sh

cloudflare-docker-route-check:
	ASSOPS_REQUIRE_CLOUDFLARE_API_JSON=true bash scripts/docker-pg18-runtime-check.sh

cloudflare-docker-runtime-evidence:
	bash scripts/cloudflare-docker-runtime-evidence.sh

github-ruleset-evidence:
	bash scripts/github-ruleset-evidence.sh

first-deployable-external-audit:
	bash scripts/first-deployable-external-audit.sh
	bash scripts/first-deployable-external-audit-validate.sh "$${ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_AUDIT_OUTPUT:-.assops/release-notes/first-deployable-external-audit.json}"

first-deployable-external-audit-validate:
	bash scripts/first-deployable-external-audit-validate.sh $(if $(AUDIT_FILE),"$(AUDIT_FILE)")

first-deployable-external-audit-validate-self-test:
	bash scripts/first-deployable-external-audit-validate-self-test.sh

first-deployable-external-audit-runbook:
	bash scripts/first-deployable-external-audit-runbook.sh $(if $(AUDIT_FILE),"$(AUDIT_FILE)")

first-deployable-external-audit-runbook-validate:
	bash scripts/first-deployable-external-audit-runbook-validate.sh $(if $(RUNBOOK_FILE),"$(RUNBOOK_FILE)")

first-deployable-external-audit-runbook-self-test:
	bash scripts/first-deployable-external-audit-runbook-self-test.sh

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

db-automigrate:
	go run ./backend/cmd/assops-tool db automigrate

db-migrate: db-automigrate

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

helm-test-preflight:
	bash scripts/helm-test-preflight.sh

helm-test-image-preflight:
	bash scripts/helm-test-image-preflight.sh

helm-test-image-preflight-self-test:
	bash scripts/helm-test-image-preflight-self-test.sh

helm-test-preflight-self-test:
	bash scripts/helm-test-preflight-self-test.sh

helm-test-smoke:
	bash scripts/helm-test-smoke.sh

helm-test-smoke-self-test:
	bash scripts/helm-test-smoke-self-test.sh

helm-production-hardening-self-test:
	bash scripts/helm-production-hardening-self-test.sh

workflow-safety-self-test:
	bash scripts/workflow-safety-self-test.sh

provider-review-live-test-plan:
	bash scripts/provider-review-live-test-plan.sh

provider-review-live-test-plan-self-test:
	bash scripts/provider-review-live-test-plan-self-test.sh

production-backup-rehearsal-plan:
	ASSOPS_PRODUCTION_BACKUP_REHEARSAL_REPO="$${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_REPO:-$(REPO)}" \
	ASSOPS_PRODUCTION_BACKUP_REHEARSAL_ENV="$${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_ENV:-$(ENV)}" \
	ASSOPS_PRODUCTION_BACKUP_REHEARSAL_RUNNER="$${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_RUNNER:-$(RUNNER)}" \
	ASSOPS_PRODUCTION_BACKUP_REHEARSAL_CRON="$${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_CRON:-$(CRON)}" \
	ASSOPS_PRODUCTION_BACKUP_REHEARSAL_BACKUP_SOURCE="$${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_BACKUP_SOURCE:-$(BACKUP_SOURCE)}" \
	ASSOPS_PRODUCTION_BACKUP_REHEARSAL_RETENTION_DAYS="$${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_RETENTION_DAYS:-$(RETENTION_DAYS)}" \
	ASSOPS_PRODUCTION_RETAINED_BACKUP_ARTIFACT="$${ASSOPS_PRODUCTION_RETAINED_BACKUP_ARTIFACT:-$(RETAINED_ARTIFACT)}" \
	ASSOPS_PRODUCTION_RESTORE_REHEARSAL_REPORT_NAME="$${ASSOPS_PRODUCTION_RESTORE_REHEARSAL_REPORT_NAME:-$(RESTORE_REPORT_NAME)}" \
	ASSOPS_PRODUCTION_BACKUP_REHEARSAL_PLAN_OUTPUT="$${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_PLAN_OUTPUT:-$(OUTPUT)}" \
	bash scripts/production-backup-rehearsal-plan.sh

production-backup-rehearsal-plan-self-test:
	bash scripts/production-backup-rehearsal-plan-self-test.sh

helm-rollout-rehearsal-plan:
	ASSOPS_HELM_ROLLOUT_REHEARSAL_REPO="$${ASSOPS_HELM_ROLLOUT_REHEARSAL_REPO:-$(REPO)}" \
	ASSOPS_HELM_ROLLOUT_REHEARSAL_GHCR_OWNER="$${ASSOPS_HELM_ROLLOUT_REHEARSAL_GHCR_OWNER:-$(GHCR_OWNER)}" \
	ASSOPS_HELM_ROLLOUT_REHEARSAL_VERSION="$${ASSOPS_HELM_ROLLOUT_REHEARSAL_VERSION:-$(VERSION)}" \
	ASSOPS_HELM_ROLLOUT_REHEARSAL_NAMESPACE="$${ASSOPS_HELM_ROLLOUT_REHEARSAL_NAMESPACE:-$(NAMESPACE)}" \
	ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE="$${ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE:-$(RELEASE)}" \
	ASSOPS_HELM_ROLLOUT_REHEARSAL_ENVIRONMENT="$${ASSOPS_HELM_ROLLOUT_REHEARSAL_ENVIRONMENT:-$(ENV)}" \
	ASSOPS_HELM_ROLLOUT_REHEARSAL_ENV_VALUES="$${ASSOPS_HELM_ROLLOUT_REHEARSAL_ENV_VALUES:-$(ENV_VALUES)}" \
	ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE_VALUES="$${ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE_VALUES:-$(RELEASE_VALUES)}" \
	ASSOPS_HELM_ROLLOUT_REHEARSAL_PREVIOUS_VALUES="$${ASSOPS_HELM_ROLLOUT_REHEARSAL_PREVIOUS_VALUES:-$(PREVIOUS_VALUES)}" \
	ASSOPS_HELM_ROLLOUT_REHEARSAL_RESTORE_REPORT="$${ASSOPS_HELM_ROLLOUT_REHEARSAL_RESTORE_REPORT:-$(RESTORE_REPORT)}" \
	ASSOPS_HELM_ROLLOUT_REHEARSAL_PLAN_OUTPUT="$${ASSOPS_HELM_ROLLOUT_REHEARSAL_PLAN_OUTPUT:-$(OUTPUT)}" \
	bash scripts/helm-rollout-rehearsal-plan.sh

helm-rollout-rehearsal-plan-self-test:
	bash scripts/helm-rollout-rehearsal-plan-self-test.sh

first-deployable-handoff-plan:
	bash scripts/first-deployable-handoff-plan.sh

first-deployable-handoff-plan-manifest-validate:
	bash scripts/first-deployable-handoff-manifest-validate.sh

first-deployable-handoff-plan-self-test:
	bash scripts/first-deployable-handoff-plan-self-test.sh

first-deployable-handoff-manifest-validate-self-test:
	bash scripts/first-deployable-handoff-manifest-validate-self-test.sh

first-deployable-handoff-schema-self-test:
	bash scripts/first-deployable-handoff-schema-self-test.sh

first-deployable-handoff-validate:
	bash scripts/first-deployable-handoff-validate.sh

first-deployable-handoff-validate-self-test:
	bash scripts/first-deployable-handoff-validate-self-test.sh

first-deployable-completion-audit:
	bash scripts/first-deployable-completion-audit.sh

first-deployable-completion-audit-self-test:
	bash scripts/first-deployable-completion-audit-self-test.sh

first-deployable-coverage-audit-self-test:
	bash scripts/first-deployable-coverage-audit-self-test.sh

first-deployable-external-evidence-self-test:
	bash scripts/first-deployable-external-evidence-self-test.sh

first-deployable-external-evidence-validate:
	bash scripts/first-deployable-external-evidence-validate.sh $(if $(EVIDENCE_FILE),"$(EVIDENCE_FILE)")

first-deployable-external-evidence-validate-self-test:
	bash scripts/first-deployable-external-evidence-validate-self-test.sh

first-deployable-external-evidence-complete-validate:
	ASSOPS_FIRST_DEPLOYABLE_REQUIRE_ALL_VERIFIED=true bash scripts/first-deployable-external-evidence-validate.sh $(if $(EVIDENCE_FILE),"$(EVIDENCE_FILE)")

first-deployable-external-evidence-complete-validate-self-test:
	bash scripts/first-deployable-external-evidence-complete-validate-self-test.sh

release-rehearsal-plans-self-test:
	bash scripts/release-rehearsal-plans-self-test.sh

rehearsal-make-targets-self-test:
	bash scripts/rehearsal-make-targets-self-test.sh

first-deployable-prereqs:
	@set -e; \
	missing=""; \
	for bin in go pnpm helm curl python3 docker; do \
		command -v "$$bin" >/dev/null || missing="$$missing $$bin"; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "Missing first-deployable-check tools:$$missing"; \
		echo "Install Go, pnpm, Helm v3, Docker Compose, curl, and Python 3 before running make first-deployable-check."; \
		echo "Helm and Docker Compose are required because the deployable gate renders Helm and validates Compose manifests."; \
		exit 1; \
	fi; \
	echo "first-deployable prerequisites present"

first-deployable-check: first-deployable-prereqs
	@set -e; \
	go test ./...; \
	bash scripts/compose-config-check.sh; \
	pnpm -C web install --frozen-lockfile; \
	pnpm -C web run i18n:check; \
	pnpm -C web build; \
	bash scripts/api-smoke-self-test.sh; \
	bash scripts/compose-smoke-local-images-self-test.sh; \
	bash scripts/helm-test-preflight-self-test.sh; \
	bash scripts/helm-test-image-preflight-self-test.sh; \
	bash scripts/helm-test-smoke-self-test.sh; \
	bash scripts/helm-production-hardening-self-test.sh; \
	bash scripts/workflow-safety-self-test.sh; \
	bash scripts/provider-review-live-test-plan-self-test.sh; \
	bash scripts/production-backup-rehearsal-plan-self-test.sh; \
	bash scripts/helm-rollout-rehearsal-plan-self-test.sh; \
	bash scripts/first-deployable-handoff-plan-self-test.sh; \
	bash scripts/first-deployable-handoff-manifest-validate-self-test.sh; \
	bash scripts/first-deployable-handoff-schema-self-test.sh; \
	bash scripts/first-deployable-handoff-validate-self-test.sh; \
	bash scripts/first-deployable-completion-audit-self-test.sh; \
	bash scripts/first-deployable-coverage-audit-self-test.sh; \
	bash scripts/first-deployable-external-evidence-self-test.sh; \
	bash scripts/first-deployable-external-audit-validate-self-test.sh; \
	bash scripts/first-deployable-external-audit-runbook-self-test.sh; \
	bash scripts/first-deployable-external-evidence-validate-self-test.sh; \
	bash scripts/first-deployable-external-evidence-complete-validate-self-test.sh; \
	bash scripts/release-rehearsal-plans-self-test.sh; \
	bash scripts/rehearsal-make-targets-self-test.sh; \
	bash scripts/release-images-self-test.sh; \
	bash scripts/release-helm-values-self-test.sh; \
	bash scripts/release-helm-readiness-plan-self-test.sh; \
	bash scripts/release-validate-bundle-self-test.sh; \
	bash scripts/release-promotion-plan-self-test.sh; \
	bash scripts/release-backup-schedule-plan-self-test.sh; \
	bash scripts/release-helm-test-readiness-plan-self-test.sh; \
	bash scripts/release-branch-protection-plan-self-test.sh; \
	go run ./backend/cmd/assops-tool release helm-test-readiness-plan \
		deploy/helm/assops/values.test.example.yaml \
		/tmp/assops-helm-test-readiness-plan.md; \
	helm lint deploy/helm/assops; \
	helm lint deploy/helm/assops \
		-f deploy/helm/assops/values.test.example.yaml \
		-f deploy/helm/assops/values.test.private.example.yaml; \
	helm template assops deploy/helm/assops \
		>/tmp/assops-default-rendered.yaml; \
	helm template assops deploy/helm/assops \
		-f deploy/helm/assops/values.test.example.yaml \
		>/tmp/assops-test-rendered.yaml; \
	helm template assops deploy/helm/assops \
		-f deploy/helm/assops/values.test.example.yaml \
		-f deploy/helm/assops/values.test.private.example.yaml \
		>/tmp/assops-private-test-rendered.yaml; \
	helm template assops deploy/helm/assops \
		-f deploy/helm/assops/values.production.example.yaml \
		>/tmp/assops-production-rendered.yaml; \
	echo "first-deployable-check passed"

first-deployable-local-runtime-check: first-deployable-check
	@set -e; \
	bash scripts/compose-smoke-local-images.sh; \
	ASSOPS_COMPOSE_SMOKE_BUILD=false bash scripts/compose-smoke.sh; \
	echo "first-deployable-local-runtime-check passed"

release-images:
	bash scripts/release-images.sh

release-images-self-test:
	bash scripts/release-images-self-test.sh

release-validate-bundle:
	@test -n "$(ARTIFACT_DIR)" || (echo "ARTIFACT_DIR=/path/to/release-artifacts is required" && exit 1)
	@test -d "$(ARTIFACT_DIR)" || (echo "ARTIFACT_DIR=$(ARTIFACT_DIR) does not exist" && exit 1)
	@test -n "$(REHEARSAL_REPORT)" || (echo "REHEARSAL_REPORT=/path/to/restore-rehearsal.json is required" && exit 1)
	@test -f "$(REHEARSAL_REPORT)" || (echo "REHEARSAL_REPORT=$(REHEARSAL_REPORT) does not exist" && exit 1)
	go run ./backend/cmd/assops-tool release validate-bundle "$(ARTIFACT_DIR)" "$(REHEARSAL_REPORT)"

release-validate-bundle-self-test:
	bash scripts/release-validate-bundle-self-test.sh

release-helm-values:
	@test -n "$(GHCR_OWNER)" || (echo "GHCR_OWNER=github-owner-or-org is required" && exit 1)
	@test -n "$(VERSION)" || (echo "VERSION=v0.1.0 is required" && exit 1)
	go run ./backend/cmd/assops-tool release helm-values "$(GHCR_OWNER)" "$(VERSION)" $(if $(OUTPUT),"$(OUTPUT)")

release-helm-values-self-test:
	bash scripts/release-helm-values-self-test.sh

release-helm-readiness-plan:
	go run ./backend/cmd/assops-tool release helm-readiness-plan deploy/helm/assops/values.production.example.yaml $(if $(OUTPUT),"$(OUTPUT)")

release-helm-readiness-plan-self-test:
	bash scripts/release-helm-readiness-plan-self-test.sh

release-helm-test-readiness-plan:
	go run ./backend/cmd/assops-tool release helm-test-readiness-plan deploy/helm/assops/values.test.example.yaml $(if $(OUTPUT),"$(OUTPUT)")

release-helm-test-readiness-plan-self-test:
	bash scripts/release-helm-test-readiness-plan-self-test.sh

release-promotion-plan:
	@test -n "$(REPO)" || (echo "REPO=owner/repo is required" && exit 1)
	@test -n "$(GHCR_OWNER)" || (echo "GHCR_OWNER=github-owner-or-org is required" && exit 1)
	@test -n "$(VERSION)" || (echo "VERSION=v0.1.0 is required" && exit 1)
	@test -n "$(ARTIFACT_DIR)" || (echo "ARTIFACT_DIR=/path/to/release-artifacts is required" && exit 1)
	@test -n "$(REHEARSAL_REPORT)" || (echo "REHEARSAL_REPORT=/path/to/restore-rehearsal.json is required" && exit 1)
	@test -n "$(HELM_VALUES)" || (echo "HELM_VALUES=/path/to/helm-values.yaml is required" && exit 1)
	go run ./backend/cmd/assops-tool release promotion-plan "$(REPO)" "$(GHCR_OWNER)" "$(VERSION)" "$(ARTIFACT_DIR)" "$(REHEARSAL_REPORT)" "$(HELM_VALUES)" $(if $(OUTPUT),"$(OUTPUT)")

release-promotion-plan-self-test:
	bash scripts/release-promotion-plan-self-test.sh

release-backup-schedule-plan:
	@test -n "$(REPO)" || (echo "REPO=owner/repo is required" && exit 1)
	@test -n "$(ENV)" || (echo "ENV=production is required" && exit 1)
	@test -n "$(RUNNER)" || (echo "RUNNER=ubuntu-latest or self-hosted label is required" && exit 1)
	@test -n "$(CRON)" || (echo "CRON='17 3 * * 1' is required" && exit 1)
	@test -n "$(BACKUP_SOURCE)" || (echo "BACKUP_SOURCE=artifact:name or path:/mounted/assops.dump is required" && exit 1)
	@test -n "$(RETENTION_DAYS)" || (echo "RETENTION_DAYS=14 is required" && exit 1)
	go run ./backend/cmd/assops-tool release backup-schedule-plan "$(REPO)" "$(ENV)" "$(RUNNER)" "$(CRON)" "$(BACKUP_SOURCE)" "$(RETENTION_DAYS)" $(if $(OUTPUT),"$(OUTPUT)")

release-backup-schedule-plan-self-test:
	bash scripts/release-backup-schedule-plan-self-test.sh

release-branch-protection-plan:
	@test -n "$(REPO)" || (echo "REPO=owner/repo is required" && exit 1)
	go run ./backend/cmd/assops-tool release branch-protection-plan "$(REPO)" .github/rulesets/main-required-checks.json .github/CODEOWNERS $(if $(OUTPUT),"$(OUTPUT)")

release-branch-protection-plan-self-test:
	bash scripts/release-branch-protection-plan-self-test.sh

helm-lint:
	helm lint deploy/helm/assops

helm-template:
	helm template assops deploy/helm/assops

helm-smoke:
	@set -e; \
	cluster="$${KIND_CLUSTER:-assops-smoke}"; \
	node_image="$${KIND_NODE_IMAGE:-kindest/node:v1.31.0}"; \
	namespace="$${HELM_SMOKE_NAMESPACE:-assops-smoke}"; \
	release="$${HELM_SMOKE_RELEASE:-assops}"; \
	for bin in docker kind helm kubectl curl; do \
		command -v "$$bin" >/dev/null || { echo "$$bin is required"; exit 1; }; \
	done; \
	cleanup() { kind delete cluster --name "$$cluster" >/dev/null 2>&1 || true; }; \
	trap cleanup EXIT; \
	kind delete cluster --name "$$cluster" >/dev/null 2>&1 || true; \
	kind create cluster --name "$$cluster" --image "$$node_image" --wait 120s; \
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
