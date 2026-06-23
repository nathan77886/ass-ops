package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"assops/backend/internal/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "assops-tool:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := app.LoadConfig()
	api := flag.String("api", cfg.GatewayURL, "ASSOPS gateway URL")
	token := flag.String("token", os.Getenv("ASSOPS_TOKEN"), "gateway bearer token")
	contextDir := flag.String("context-dir", cfg.ContextDir, "local context directory")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "db":
		return runDBCommand(cfg, args[1:])
	case "project":
		if len(args) == 2 && args[1] == "brief" {
			return readContextBrief(*contextDir)
		}
		if len(args) == 2 && args[1] == "readiness" {
			return getProjectReadiness(*api, *token)
		}
	case "repo":
		if len(args) == 2 && args[1] == "remotes" {
			return readContextKey(*contextDir, "remotes")
		}
	case "remote":
		if len(args) == 2 && args[1] == "actions" {
			return printJSON(map[string]any{"actions": []string{"repo.sync", "repo.tag", "github.actions.sync"}})
		}
	case "operations":
		if len(args) == 2 && args[1] == "recent" {
			return getAPI(*api, *token, "/api/operations")
		}
	case "plan":
		if len(args) == 2 && args[1] == "validate" {
			return printJSON(map[string]any{"valid": true, "message": "MVP validation accepts adapter plans"})
		}
	case "release":
		if len(args) == 4 && args[1] == "validate-bundle" {
			result, err := validateReleaseBundle(args[2], args[3])
			if err != nil {
				return err
			}
			return printJSON(result)
		}
		if (len(args) == 4 || len(args) == 5) && args[1] == "helm-values" {
			values, err := releaseHelmValues(args[2], args[3])
			if err != nil {
				return err
			}
			if len(args) == 5 {
				return writeTextFile(args[4], values)
			}
			fmt.Print(values)
			return nil
		}
		if (len(args) == 8 || len(args) == 9) && args[1] == "promotion-plan" {
			plan, err := releasePromotionPlan(args[2], args[3], args[4], args[5], args[6], args[7])
			if err != nil {
				return err
			}
			if len(args) == 9 {
				return writeTextFile(args[8], plan)
			}
			fmt.Print(plan)
			return nil
		}
		if (len(args) == 8 || len(args) == 9) && args[1] == "backup-schedule-plan" {
			plan, err := releaseBackupSchedulePlan(args[2], args[3], args[4], args[5], args[6], args[7])
			if err != nil {
				return err
			}
			if len(args) == 9 {
				return writeTextFile(args[8], plan)
			}
			fmt.Print(plan)
			return nil
		}
	}
	return usage()
}

func usage() error {
	fmt.Fprintln(os.Stderr, "usage: assops-tool [--api URL] [--token TOKEN] <db migrate|db migrations|db seed-demo|db sync-assets|db backup FILE|db backup-retain DIR KEEP|db inspect-backup FILE|db restore FILE|db rehearse-restore FILE TARGET_DATABASE_URL [REPORT_FILE]|project brief|project readiness|repo remotes|remote actions|operations recent|plan validate|release validate-bundle ARTIFACT_DIR REHEARSAL_REPORT|release helm-values GHCR_OWNER VERSION [OUTPUT_FILE]|release promotion-plan OWNER/REPO GHCR_OWNER VERSION ARTIFACT_DIR REHEARSAL_REPORT HELM_VALUES [OUTPUT_FILE]|release backup-schedule-plan OWNER/REPO ENV RUNNER CRON BACKUP_SOURCE RETENTION_DAYS [OUTPUT_FILE]>")
	return fmt.Errorf("unknown command")
}

func runDBCommand(cfg app.Config, args []string) error {
	if len(args) == 0 {
		return usage()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	switch args[0] {
	case "migrate":
		store, err := app.OpenStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.ApplyMigrations(ctx, "backend/migrations"); err != nil {
			return err
		}
		fmt.Println("migrations applied")
		return nil
	case "migrations":
		store, err := app.OpenStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close()
		records, err := store.ListMigrations(ctx)
		if err != nil {
			return err
		}
		return printJSON(records)
	case "seed-demo":
		store, err := app.OpenStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.ApplyMigrations(ctx, "backend/migrations"); err != nil {
			return err
		}
		result, err := store.SeedDemoData(ctx, cfg)
		if err != nil {
			return err
		}
		return printJSON(result)
	case "sync-assets":
		store, err := app.OpenStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.ApplyMigrations(ctx, "backend/migrations"); err != nil {
			return err
		}
		result, err := store.SyncCanonicalAssetsReport(ctx)
		if err != nil {
			return err
		}
		return printJSON(result)
	case "backup":
		if len(args) != 2 {
			return usage()
		}
		dbURL, env, secrets, err := postgresProcessDatabaseURL(cfg.DatabaseURL)
		if err != nil {
			return err
		}
		return runExternalDBTool(ctx, env, secrets, "pg_dump", "-Fc", "--no-owner", "--no-password", "--file", args[1], dbURL)
	case "backup-retain":
		if len(args) != 3 {
			return usage()
		}
		keep, err := strconv.Atoi(args[2])
		if err != nil || keep < 1 {
			return fmt.Errorf("backup retention KEEP must be a positive integer")
		}
		if err := os.MkdirAll(args[1], 0o750); err != nil {
			return fmt.Errorf("creating backup directory: %w", err)
		}
		unlock, err := acquireBackupDirLock(args[1])
		if err != nil {
			return err
		}
		defer unlock()
		backupPath, err := nextBackupPath(args[1], time.Now().UTC())
		if err != nil {
			return err
		}
		dbURL, env, secrets, err := postgresProcessDatabaseURL(cfg.DatabaseURL)
		if err != nil {
			return err
		}
		if err := runExternalDBTool(ctx, env, secrets, "pg_dump", "-Fc", "--no-owner", "--no-password", "--file", backupPath, dbURL); err != nil {
			_ = os.Remove(backupPath)
			return err
		}
		pruned, err := pruneBackups(args[1], keep)
		if err != nil {
			return err
		}
		return printJSON(map[string]any{"backup": backupPath, "keep": keep, "pruned": pruned})
	case "inspect-backup":
		if len(args) != 2 {
			return usage()
		}
		return runExternalDBTool(ctx, nil, nil, "pg_restore", "--list", args[1])
	case "restore":
		if len(args) != 2 {
			return usage()
		}
		if err := confirmDestructiveRestore(cfg.DatabaseURL, os.Getenv("ASSOPS_CONFIRM_DB_RESTORE")); err != nil {
			return err
		}
		dbURL, env, secrets, err := postgresProcessDatabaseURL(cfg.DatabaseURL)
		if err != nil {
			return err
		}
		return runExternalDBTool(ctx, env, secrets, "pg_restore", "--clean", "--if-exists", "--no-owner", "--no-password", "--dbname", dbURL, args[1])
	case "rehearse-restore":
		if len(args) != 3 && len(args) != 4 {
			return usage()
		}
		reportPath := ""
		if len(args) == 4 {
			reportPath = args[3]
		}
		return rehearseRestore(ctx, cfg, args[1], args[2], reportPath)
	default:
		return usage()
	}
}

func rehearseRestore(ctx context.Context, cfg app.Config, backupPath, targetDatabaseURL, reportPath string) error {
	if err := validateRestoreRehearsalTarget(cfg.DatabaseURL, targetDatabaseURL, os.Getenv("ASSOPS_ALLOW_RESTORE_REHEARSAL_TARGET") == "1"); err != nil {
		return err
	}
	targetDBURL, env, secrets, err := postgresProcessDatabaseURL(targetDatabaseURL)
	if err != nil {
		return err
	}
	inspectOutput, err := runExternalDBToolOutput(ctx, nil, nil, "pg_restore", "--list", backupPath)
	if err != nil {
		return err
	}
	restoreOutput, err := runExternalDBToolOutput(ctx, env, secrets, "pg_restore", "--clean", "--if-exists", "--no-owner", "--no-password", "--dbname", targetDBURL, backupPath)
	if err != nil {
		return err
	}
	rehearsalCfg := cfg
	rehearsalCfg.DatabaseURL = targetDatabaseURL
	store, err := app.OpenStore(ctx, rehearsalCfg)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.ApplyMigrations(ctx, "backend/migrations"); err != nil {
		return err
	}
	records, err := store.ListMigrations(ctx)
	if err != nil {
		return err
	}
	report := map[string]any{
		"backup":               backupPath,
		"target_database":      redactedDatabaseURL(targetDatabaseURL),
		"inspect_line_count":   countNonEmptyLines(inspectOutput),
		"backup_object_counts": pgRestoreListObjectCounts(inspectOutput),
		"restore_output_lines": countNonEmptyLines(restoreOutput),
		"migrations":           records,
		"rehearsed_at":         time.Now().UTC().Format(time.RFC3339),
	}
	if reportPath != "" {
		if err := writeJSONReport(reportPath, report); err != nil {
			return err
		}
	}
	return printJSON(report)
}

func writeJSONReport(path string, value any) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("report path is required")
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("creating report directory: %w", err)
		}
	}
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}
	return nil
}

func writeTextFile(path, value string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("output path is required")
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("creating output directory: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	return nil
}

func releaseHelmValues(owner, version string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	version = strings.TrimSpace(version)
	if owner == "" {
		return "", fmt.Errorf("GHCR owner is required")
	}
	if strings.Contains(owner, "/") {
		return "", fmt.Errorf("GHCR owner must be an owner or organization name, not owner/repo")
	}
	if !isContainerPathSegment(owner) {
		return "", fmt.Errorf("GHCR owner contains unsupported characters")
	}
	if version == "" {
		return "", fmt.Errorf("release version is required")
	}
	if strings.ContainsAny(version, " \t\r\n") {
		return "", fmt.Errorf("release version must not contain whitespace")
	}
	return fmt.Sprintf(`image:
  registry: ghcr.io
  pullPolicy: IfNotPresent
  gateway:
    repository: %s/assops-gateway
    tag: %s
  worker:
    repository: %s/assops-worker
    tag: %s
  nodeWorker:
    repository: %s/assops-node-worker
    tag: %s
  web:
    repository: %s/assops-web
    tag: %s
`, owner, version, owner, version, owner, version, owner, version), nil
}

func releasePromotionPlan(repo, owner, version, artifactDir, rehearsalReport, helmValuesPath string) (string, error) {
	repo = strings.TrimSpace(repo)
	owner = strings.ToLower(strings.TrimSpace(owner))
	version = strings.TrimSpace(version)
	artifactDir = strings.TrimSpace(artifactDir)
	rehearsalReport = strings.TrimSpace(rehearsalReport)
	helmValuesPath = strings.TrimSpace(helmValuesPath)
	if !isOwnerRepo(repo) {
		return "", fmt.Errorf("repository must be owner/repo")
	}
	if _, err := releaseHelmValues(owner, version); err != nil {
		return "", err
	}
	if helmValuesPath == "" {
		return "", fmt.Errorf("Helm values path is required")
	}
	helmValuesDigest, err := validateReleaseHelmValuesFile(helmValuesPath, owner, version)
	if err != nil {
		return "", err
	}
	bundle, err := validateReleaseBundle(artifactDir, rehearsalReport)
	if err != nil {
		return "", err
	}
	artifacts, _ := bundle["artifacts"].(map[string]any)
	binaries := stringSliceFromAny(artifacts["binaries"])
	web := stringSliceFromAny(artifacts["web"])
	helm := stringSliceFromAny(artifacts["helm"])
	if len(binaries) == 0 || len(web) == 0 || len(helm) == 0 {
		return "", fmt.Errorf("release bundle artifact summary is incomplete")
	}
	images := []string{
		fmt.Sprintf("ghcr.io/%s/assops-gateway:%s", owner, version),
		fmt.Sprintf("ghcr.io/%s/assops-worker:%s", owner, version),
		fmt.Sprintf("ghcr.io/%s/assops-node-worker:%s", owner, version),
		fmt.Sprintf("ghcr.io/%s/assops-web:%s", owner, version),
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Promotion Plan %s\n\n", version)
	fmt.Fprintf(&b, "Repository: `%s`\n\n", repo)
	fmt.Fprintf(&b, "Artifact directory: `%s`\n\n", artifactDir)
	fmt.Fprintf(&b, "Restore rehearsal report: `%s`\n\n", rehearsalReport)
	fmt.Fprintf(&b, "Helm values overlay: `%s`\n\n", helmValuesPath)
	fmt.Fprintf(&b, "Helm values sha256: `%s`\n\n", helmValuesDigest)
	fmt.Fprintf(&b, "## Required Gates\n\n")
	fmt.Fprintf(&b, "- Release bundle checksum validation passed locally.\n")
	fmt.Fprintf(&b, "- Restore rehearsal report is present, redacted, and includes migrations/object counts.\n")
	fmt.Fprintf(&b, "- GitHub artifact attestations must be verified before promotion.\n")
	fmt.Fprintf(&b, "- GHCR image attestations must be verified before rollout.\n")
	fmt.Fprintf(&b, "- Helm values overlay pins all workloads to `%s`.\n\n", version)
	fmt.Fprintf(&b, "## Release Artifacts\n\n")
	for _, name := range append(append(binaries, web...), helm...) {
		fmt.Fprintf(&b, "- `%s`\n", filepath.Join(artifactDir, name))
	}
	fmt.Fprintf(&b, "- `%s`\n\n", filepath.Join(artifactDir, "SHA256SUMS"))
	fmt.Fprintf(&b, "## Images\n\n")
	for _, image := range images {
		fmt.Fprintf(&b, "- `%s`\n", image)
	}
	fmt.Fprintf(&b, "\n## Verification Commands\n\n```bash\n")
	fmt.Fprintf(&b, "assops-tool release validate-bundle %q %q\n", artifactDir, rehearsalReport)
	for _, name := range append(append([]string{"SHA256SUMS"}, binaries...), append(web, helm...)...) {
		fmt.Fprintf(&b, "gh attestation verify %q --repo %s\n", filepath.Join(artifactDir, name), repo)
	}
	for _, image := range images {
		fmt.Fprintf(&b, "gh attestation verify %q --repo %s\n", "oci://"+image, repo)
	}
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "## Rollout Guardrails\n\n")
	fmt.Fprintf(&b, "- The production promotion workflow defaults to preflight-only; do not set `rollout=true` until the protected environment, namespace-scoped kubeconfig, previous values overlay, rollback point, and operator approval have been reviewed.\n")
	fmt.Fprintf(&b, "- Application pods should not need Kubernetes API credentials; keep rollout credentials isolated to the protected promotion workflow.\n\n")
	fmt.Fprintf(&b, "## Rollout Commands\n\n```bash\n")
	fmt.Fprintf(&b, "helm template assops deploy/helm/assops -f deploy/helm/assops/values.yaml -f %q >/tmp/assops-rendered.yaml\n", helmValuesPath)
	fmt.Fprintf(&b, "helm upgrade --install assops deploy/helm/assops -f deploy/helm/assops/values.yaml -f %q\n", helmValuesPath)
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "## Rollback Note\n\n")
	fmt.Fprintf(&b, "Keep the previous Helm values overlay and database backup path with the release notes before rollout.\n")
	return b.String(), nil
}

func releaseBackupSchedulePlan(repo, environment, runner, cronExpr, backupSource, retentionDays string) (string, error) {
	repo = strings.TrimSpace(repo)
	environment = strings.TrimSpace(environment)
	runner = strings.TrimSpace(runner)
	cronExpr = strings.TrimSpace(cronExpr)
	backupSource = strings.TrimSpace(backupSource)
	retentionDays = strings.TrimSpace(retentionDays)
	if !isOwnerRepo(repo) {
		return "", fmt.Errorf("repository must be owner/repo")
	}
	if !isSafeWorkflowInput(environment) {
		return "", fmt.Errorf("environment must contain only letters, numbers, dot, underscore, or hyphen")
	}
	if !isSafeWorkflowInput(runner) {
		return "", fmt.Errorf("runner must contain only letters, numbers, dot, underscore, or hyphen")
	}
	if !isSafeCronExpression(cronExpr) {
		return "", fmt.Errorf("cron must be a five-field GitHub Actions cron expression without quotes or shell metacharacters")
	}
	retention, err := strconv.Atoi(retentionDays)
	if err != nil || retention < 1 || retention > 90 {
		return "", fmt.Errorf("artifact retention days must be between 1 and 90")
	}
	sourceKind, sourceValue, err := parseBackupScheduleSource(backupSource)
	if err != nil {
		return "", err
	}
	var artifactInput, pathInput, runnerNote string
	switch sourceKind {
	case "artifact":
		artifactInput = sourceValue
		runnerNote = "Any runner with repository Actions artifact read access can download the latest unexpired retained backup artifact by name."
	case "path":
		pathInput = sourceValue
		if runner == "ubuntu-latest" {
			return "", fmt.Errorf("backup path sources require a self-hosted runner that mounts the retained backup store")
		}
		runnerNote = "The selected runner must be self-hosted and mount the retained backup store read-only."
	default:
		return "", fmt.Errorf("unsupported backup source kind %q", sourceKind)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Production Backup Schedule Plan\n\n")
	fmt.Fprintf(&b, "Repository: `%s`\n\n", repo)
	fmt.Fprintf(&b, "Workflow: `.github/workflows/production-restore-rehearsal.yml`\n\n")
	fmt.Fprintf(&b, "GitHub environment: `%s`\n\n", environment)
	fmt.Fprintf(&b, "Runner: `%s`\n\n", runner)
	fmt.Fprintf(&b, "Cron: `%s`\n\n", cronExpr)
	fmt.Fprintf(&b, "Artifact retention: `%d days`\n\n", retention)
	fmt.Fprintf(&b, "## Backup Source\n\n")
	if sourceKind == "artifact" {
		fmt.Fprintf(&b, "- Use workflow artifact `%s` containing exactly one retained `assops-*.dump` file.\n", sourceValue)
	} else {
		fmt.Fprintf(&b, "- Use runner-local backup path `%s`.\n", sourceValue)
	}
	fmt.Fprintf(&b, "- %s\n\n", runnerNote)
	fmt.Fprintf(&b, "## Retained Backup Publication Contract\n\n")
	fmt.Fprintf(&b, "- Publication must be produced by the environment-owned retained backup job after `assops-tool db backup-retain` succeeds.\n")
	fmt.Fprintf(&b, "- The published source must contain exactly one `assops-*.dump` backup for the rehearsal run.\n")
	fmt.Fprintf(&b, "- Publication metadata must record the backup timestamp, source environment, retention window, and checksum location without embedding database URLs or credentials.\n")
	fmt.Fprintf(&b, "- The rehearsal workflow is a consumer only; it must not create, rotate, delete, or overwrite retained backups.\n")
	if sourceKind == "artifact" {
		fmt.Fprintf(&b, "- The checked-in `.github/workflows/production-retained-backup.yml` workflow can publish artifact `%s`, but it is disabled until the protected environment secrets, runner, artifact retention, and repository variable gate are configured.\n", sourceValue)
		fmt.Fprintf(&b, "- GitHub artifact publication uploads a raw `pg_dump` custom-format file to private repository Actions storage; external storage, additional encryption, and large-database handling remain environment-owned.\n")
		fmt.Fprintf(&b, "- Artifact `%s` must stay unexpired for at least `%d days` and remain private to repository Actions readers until the rehearsal report is attached to release notes.\n", sourceValue, retention)
		fmt.Fprintf(&b, "- The artifact producer should upload only the dump and non-secret manifest/checksum metadata; do not include `.env`, database URLs, kubeconfigs, or raw logs.\n\n")
	} else {
		fmt.Fprintf(&b, "- Path `%s` must be mounted read-only on runner `%s`; the workflow only reads this file path and never publishes the backup itself.\n", sourceValue, runner)
		fmt.Fprintf(&b, "- The mounted backup store owner must handle backup retention, checksum publication, and deletion outside this workflow.\n\n")
	}
	fmt.Fprintf(&b, "## Required GitHub Environment Secrets\n\n")
	fmt.Fprintf(&b, "- `ASSOPS_REHEARSAL_DATABASE_URL`: disposable restore database URL; the database name must include rehearsal, restore, test, tmp, scratch, or disposable.\n")
	fmt.Fprintf(&b, "- `ASSOPS_REHEARSAL_DATABASE_PASSWORD`: optional password passed through `PGPASSWORD` when the URL omits a password.\n")
	fmt.Fprintf(&b, "- `ASSOPS_ACTIVE_DATABASE_URL`: optional guard value so `assops-tool` can reject a rehearsal target that equals the active database.\n\n")
	fmt.Fprintf(&b, "## Manual Dispatch Check\n\n")
	fmt.Fprintf(&b, "Run this once before enabling the scheduled workflow gate:\n\n```bash\n")
	fmt.Fprintf(&b, "gh workflow run production-restore-rehearsal.yml --repo %s \\\n", repo)
	fmt.Fprintf(&b, "  -f github_environment=%q \\\n", environment)
	fmt.Fprintf(&b, "  -f runner=%q \\\n", runner)
	if artifactInput != "" {
		fmt.Fprintf(&b, "  -f backup_artifact_name=%q \\\n", artifactInput)
		fmt.Fprintf(&b, "  -f backup_path='' \\\n")
	} else {
		fmt.Fprintf(&b, "  -f backup_artifact_name='' \\\n")
		fmt.Fprintf(&b, "  -f backup_path=%q \\\n", pathInput)
	}
	fmt.Fprintf(&b, "  -f artifact_retention_days=%q\n", strconv.Itoa(retention))
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "## Scheduled Workflow Variables\n\n")
	fmt.Fprintf(&b, "The workflow already contains a scheduled trigger. After the manual dispatch succeeds, set these repository variables to enable it:\n\n")
	fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_ENABLED=true`\n")
	fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_ENVIRONMENT=%s`\n", environment)
	fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_RUNNER=%s`\n", runner)
	if artifactInput != "" {
		fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_ARTIFACT=%s`\n", artifactInput)
		fmt.Fprintf(&b, "- Leave `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_PATH` unset.\n")
	} else {
		fmt.Fprintf(&b, "- Leave `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_ARTIFACT` unset.\n")
		fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_PATH=%s`\n", pathInput)
	}
	fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_REPORT_NAME=production-restore-rehearsal-scheduled`\n")
	fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_RETENTION_DAYS=%d`\n\n", retention)
	fmt.Fprintf(&b, "The checked-in schedule is `%s`; update the workflow cron separately only if the environment needs a different window.\n\n", cronExpr)
	fmt.Fprintf(&b, "Keep the workflow protected by the `%s` environment and preserve the one-source-only guard between the artifact and path variables.\n", environment)
	return b.String(), nil
}

func parseBackupScheduleSource(value string) (string, string, error) {
	kind, raw, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return "", "", fmt.Errorf("backup source must use artifact:NAME or path:/mounted/assops-*.dump")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("backup source value is required")
	}
	switch kind {
	case "artifact":
		if !isSafeWorkflowInput(raw) {
			return "", "", fmt.Errorf("backup artifact name must contain only letters, numbers, dot, underscore, or hyphen")
		}
	case "path":
		if !isSafeBackupPath(raw) {
			return "", "", fmt.Errorf("backup path contains unsupported characters")
		}
	default:
		return "", "", fmt.Errorf("backup source must use artifact:NAME or path:/mounted/assops-*.dump")
	}
	return kind, raw, nil
}

func isSafeWorkflowInput(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func isSafeBackupPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "..") {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '/', '.', '_', '@', '=', ':', '+', '-':
			continue
		default:
			return false
		}
	}
	return true
}

func isSafeCronExpression(value string) bool {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) != 5 {
		return false
	}
	for _, field := range fields {
		if field == "" {
			return false
		}
		for _, r := range field {
			if (r >= '0' && r <= '9') || r == '*' || r == ',' || r == '-' || r == '/' {
				continue
			}
			return false
		}
	}
	return true
}

func validateReleaseHelmValuesFile(path, owner, version string) (string, error) {
	expected, err := releaseHelmValues(owner, version)
	if err != nil {
		return "", err
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("checking Helm values path: %w", err)
	}
	if string(actual) != expected {
		return "", fmt.Errorf("Helm values overlay does not match GHCR owner/version; regenerate it with release helm-values")
	}
	sum := sha256.Sum256(actual)
	return fmt.Sprintf("%x", sum), nil
}

func isOwnerRepo(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	return isContainerPathSegment(strings.ToLower(parts[0])) && isContainerPathSegment(strings.ToLower(parts[1]))
}

func stringSliceFromAny(value any) []string {
	items, ok := value.([]string)
	if ok {
		return items
	}
	rawItems, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func isContainerPathSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= '0' && char <= '9':
		case char == '-', char == '_', char == '.':
		default:
			return false
		}
	}
	return true
}

func validateReleaseBundle(artifactDir, rehearsalReport string) (map[string]any, error) {
	artifactDir = strings.TrimSpace(artifactDir)
	rehearsalReport = strings.TrimSpace(rehearsalReport)
	if artifactDir == "" {
		return nil, fmt.Errorf("release artifact directory is required")
	}
	if rehearsalReport == "" {
		return nil, fmt.Errorf("restore rehearsal report path is required")
	}
	info, err := os.Stat(artifactDir)
	if err != nil {
		return nil, fmt.Errorf("checking release artifact directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("release artifact path is not a directory: %s", artifactDir)
	}
	checksums, err := readChecksumFile(filepath.Join(artifactDir, "SHA256SUMS"))
	if err != nil {
		return nil, err
	}
	for name, expected := range checksums {
		actual, err := sha256File(filepath.Join(artifactDir, name))
		if err != nil {
			return nil, fmt.Errorf("verifying checksum for %s: %w", name, err)
		}
		if actual != expected {
			return nil, fmt.Errorf("checksum mismatch for %s", name)
		}
	}
	artifacts, err := releaseArtifactSummary(artifactDir, checksums)
	if err != nil {
		return nil, err
	}
	report, err := validateRehearsalReport(rehearsalReport)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"valid":             true,
		"artifact_dir":      artifactDir,
		"checksum_file":     filepath.Join(artifactDir, "SHA256SUMS"),
		"checksum_entries":  len(checksums),
		"checksum_verified": len(checksums),
		"artifacts":         artifacts,
		"rehearsal_report":  report,
	}, nil
}

func readChecksumFile(path string) (map[string]string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading SHA256SUMS: %w", err)
	}
	checksums := map[string]string{}
	for index, rawLine := range strings.Split(string(bytes), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid SHA256SUMS line %d", index+1)
		}
		hash := strings.ToLower(fields[0])
		name := strings.TrimPrefix(fields[1], "*")
		if !isSHA256Hex(hash) {
			return nil, fmt.Errorf("invalid SHA256 hash on line %d", index+1)
		}
		if err := validateChecksumPath(name); err != nil {
			return nil, fmt.Errorf("invalid SHA256SUMS path on line %d: %w", index+1, err)
		}
		if _, exists := checksums[name]; exists {
			return nil, fmt.Errorf("duplicate SHA256SUMS entry for %s", name)
		}
		checksums[name] = hash
	}
	if len(checksums) == 0 {
		return nil, fmt.Errorf("SHA256SUMS has no entries")
	}
	return checksums, nil
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}

func validateChecksumPath(name string) error {
	if name == "" {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("absolute paths are not allowed")
	}
	clean := filepath.Clean(name)
	if clean == "." || clean != name || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		return fmt.Errorf("path must be a clean relative file path")
	}
	return nil
}

func sha256File(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("symlink artifacts are not allowed")
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("artifact is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func releaseArtifactSummary(artifactDir string, checksums map[string]string) (map[string]any, error) {
	patterns := map[string]string{
		"binaries": "*-linux-amd64.tar.gz",
		"web":      "assops-web-*.tar.gz",
		"helm":     "assops-*.tgz",
	}
	summary := map[string]any{}
	for key, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(artifactDir, pattern))
		if err != nil {
			return nil, err
		}
		var names []string
		for _, match := range matches {
			info, err := os.Lstat(match)
			if err != nil {
				return nil, fmt.Errorf("checking release artifact %s: %w", match, err)
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				continue
			}
			name := filepath.Base(match)
			if _, ok := checksums[name]; !ok {
				return nil, fmt.Errorf("release artifact %s is missing from SHA256SUMS", name)
			}
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return nil, fmt.Errorf("release bundle missing %s artifact matching %s", key, pattern)
		}
		summary[key] = names
	}
	return summary, nil
}

func validateRehearsalReport(path string) (map[string]any, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading restore rehearsal report: %w", err)
	}
	var report map[string]any
	if err := json.Unmarshal(bytes, &report); err != nil {
		return nil, fmt.Errorf("parsing restore rehearsal report: %w", err)
	}
	backup, err := reportString(report, "backup")
	if err != nil {
		return nil, err
	}
	targetDatabase, err := reportString(report, "target_database")
	if err != nil {
		return nil, err
	}
	if err := validateReportDatabaseURL(targetDatabase); err != nil {
		return nil, err
	}
	rehearsedAt, err := reportString(report, "rehearsed_at")
	if err != nil {
		return nil, err
	}
	if _, err := time.Parse(time.RFC3339, rehearsedAt); err != nil {
		return nil, fmt.Errorf("restore rehearsal report rehearsed_at must be RFC3339: %w", err)
	}
	migrations, ok := report["migrations"].([]any)
	if !ok || len(migrations) == 0 {
		return nil, fmt.Errorf("restore rehearsal report migrations must be a non-empty array")
	}
	counts, ok := report["backup_object_counts"].(map[string]any)
	if !ok || len(counts) == 0 {
		return nil, fmt.Errorf("restore rehearsal report backup_object_counts must be a non-empty object")
	}
	return map[string]any{
		"path":                path,
		"backup":              backup,
		"target_database":     targetDatabase,
		"migration_count":     len(migrations),
		"backup_object_types": len(counts),
		"rehearsed_at":        rehearsedAt,
	}, nil
}

func reportString(report map[string]any, key string) (string, error) {
	value, ok := report[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("restore rehearsal report %s must be a non-empty string", key)
	}
	return value, nil
}

func validateReportDatabaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return fmt.Errorf("restore rehearsal report target_database must be URL-style")
	}
	if _, hasPassword := parsed.User.Password(); hasPassword {
		return fmt.Errorf("restore rehearsal report target_database must not include a password")
	}
	return nil
}

func validateRestoreRehearsalTarget(currentDatabaseURL, targetDatabaseURL string, allowOverride bool) error {
	current, err := normalizeDatabaseURLForCompare(currentDatabaseURL)
	if err != nil && strings.TrimSpace(currentDatabaseURL) != "" {
		return fmt.Errorf("invalid DATABASE_URL: %w", err)
	}
	target, err := normalizeDatabaseURLForCompare(targetDatabaseURL)
	if err != nil {
		return fmt.Errorf("invalid restore rehearsal target database URL: %w", err)
	}
	if target == "" {
		return fmt.Errorf("restore rehearsal target database URL is required")
	}
	if current != "" && target == current {
		return fmt.Errorf("restore rehearsal target must not equal DATABASE_URL")
	}
	if allowOverride {
		return nil
	}
	dbName, err := databaseNameFromURL(targetDatabaseURL)
	if err != nil {
		return err
	}
	lowerName := strings.ToLower(dbName)
	for _, token := range []string{"rehears", "restore", "test", "tmp", "scratch", "disposable"} {
		if strings.Contains(lowerName, token) {
			return nil
		}
	}
	return fmt.Errorf("restore rehearsal target database name %q must look disposable; include rehearsal/test/tmp/restore/scratch or set ASSOPS_ALLOW_RESTORE_REHEARSAL_TARGET=1", dbName)
}

func confirmDestructiveRestore(databaseURL, confirmation string) error {
	dbName, err := databaseNameFromURL(databaseURL)
	if err != nil {
		return err
	}
	if confirmation != dbName {
		return fmt.Errorf("db restore is destructive; set ASSOPS_CONFIRM_DB_RESTORE=%s to confirm target database", dbName)
	}
	return nil
}

func normalizeDatabaseURLForCompare(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "", fmt.Errorf("database URL must be URL-style")
	}
	host := canonicalDBHost(parsed.Hostname())
	port := parsed.Port()
	if port == "" {
		port = defaultDatabasePort(parsed.Scheme)
	}
	dbName := strings.Trim(strings.TrimSpace(parsed.Path), "/")
	if dbName == "" {
		return "", fmt.Errorf("database URL must include a database name")
	}
	return strings.Join([]string{strings.ToLower(parsed.Scheme), host, port, dbName}, "|"), nil
}

func canonicalDBHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch host {
	case "localhost", "::1", "[::1]", "0.0.0.0":
		return "127.0.0.1"
	default:
		return host
	}
}

func defaultDatabasePort(scheme string) string {
	switch strings.ToLower(scheme) {
	case "postgres", "postgresql":
		return "5432"
	default:
		return ""
	}
}

func databaseNameFromURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "", fmt.Errorf("database URL must be URL-style")
	}
	name := strings.Trim(strings.TrimSpace(parsed.Path), "/")
	if name == "" {
		return "", fmt.Errorf("database URL must include a database name")
	}
	return name, nil
}

func redactedDatabaseURL(raw string) string {
	redacted, _, _, err := postgresProcessDatabaseURL(raw)
	if err != nil {
		return "<invalid>"
	}
	return redacted
}

func countNonEmptyLines(output string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func pgRestoreListObjectCounts(output string) map[string]int {
	counts := map[string]int{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// pg_restore --list TOC lines are stable as:
		// dumpID; catalogOID objectOID OBJECT_TYPE schema name owner
		objectType := fields[3]
		counts[objectType]++
	}
	return counts
}

func acquireBackupDirLock(dir string) (func(), error) {
	lockPath := filepath.Join(dir, ".assops-backup.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("backup directory is already locked: %s", lockPath)
		}
		return nil, fmt.Errorf("creating backup lock: %w", err)
	}
	_, _ = fmt.Fprintf(file, "pid=%d\n", os.Getpid())
	return func() {
		_ = file.Close()
		_ = os.Remove(lockPath)
	}, nil
}

func nextBackupPath(dir string, now time.Time) (string, error) {
	base := "assops-" + now.UTC().Format("20060102-150405") + ".dump"
	path := filepath.Join(dir, base)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path, nil
	} else if err != nil {
		return "", fmt.Errorf("checking backup path: %w", err)
	}
	for i := 1; i < 1000; i++ {
		path = filepath.Join(dir, fmt.Sprintf("assops-%s-%03d.dump", now.UTC().Format("20060102-150405"), i))
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		} else if err != nil {
			return "", fmt.Errorf("checking backup path: %w", err)
		}
	}
	return "", fmt.Errorf("could not allocate unique backup filename in %s", dir)
}

type backupFile struct {
	path    string
	name    string
	modTime time.Time
}

func pruneBackups(dir string, keep int) ([]string, error) {
	if keep < 1 {
		return nil, fmt.Errorf("backup retention KEEP must be a positive integer")
	}
	backups, err := listManagedBackups(dir)
	if err != nil {
		return nil, err
	}
	if len(backups) <= keep {
		return []string{}, nil
	}
	var pruned []string
	for _, backup := range backups[keep:] {
		if err := os.Remove(backup.path); err != nil {
			return nil, fmt.Errorf("pruning backup %s: %w", backup.path, err)
		}
		pruned = append(pruned, backup.path)
	}
	return pruned, nil
}

func listManagedBackups(dir string) ([]backupFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("listing backup directory: %w", err)
	}
	var backups []backupFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "assops-") || !strings.HasSuffix(name, ".dump") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("reading backup file info: %w", err)
		}
		backups = append(backups, backupFile{path: filepath.Join(dir, name), name: name, modTime: info.ModTime()})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].modTime.Equal(backups[j].modTime) {
			return backups[i].name > backups[j].name
		}
		return backups[i].modTime.After(backups[j].modTime)
	})
	return backups, nil
}

func postgresProcessDatabaseURL(raw string) (string, []string, []string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		if strings.Contains(strings.ToLower(raw), "password=") {
			return "", nil, nil, fmt.Errorf("database backup/restore requires URL-style DATABASE_URL or PGPASSWORD; keyword DSNs with password= are not supported")
		}
		return raw, nil, []string{raw}, nil
	}
	password, hasPassword := parsed.User.Password()
	if !hasPassword {
		return raw, nil, []string{raw}, nil
	}
	username := parsed.User.Username()
	if username != "" {
		parsed.User = url.User(username)
	} else {
		parsed.User = nil
	}
	return parsed.String(), []string{"PGPASSWORD=" + password}, []string{raw, password}, nil
}

func runExternalDBTool(ctx context.Context, env, secrets []string, name string, args ...string) error {
	output, err := runExternalDBToolOutput(ctx, env, secrets, name, args...)
	if err != nil {
		return err
	}
	if output != "" {
		fmt.Print(output)
	}
	return nil
}

func runExternalDBToolOutput(ctx context.Context, env, secrets []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	output, err := cmd.CombinedOutput()
	sanitized := sanitizeCommandOutput(string(output), secrets)
	if err != nil {
		if len(output) > 0 {
			return "", fmt.Errorf("%s failed: %s", name, sanitized)
		}
		return "", fmt.Errorf("%s failed: %w", name, err)
	}
	return sanitized, nil
}

func sanitizeCommandOutput(output string, secrets []string) string {
	sanitized := output
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		sanitized = strings.ReplaceAll(sanitized, secret, "<redacted>")
	}
	return sanitized
}

func readContextBrief(root string) error {
	path, err := firstContextFile(root, "ASSOPS_CONTEXT.md")
	if err != nil {
		return err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Print(string(bytes))
	return nil
}

func readContextKey(root, key string) error {
	path, err := firstContextFile(root, "assops-context.json")
	if err != nil {
		return err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var data map[string]any
	if err := json.Unmarshal(bytes, &data); err != nil {
		return err
	}
	return printJSON(data[key])
}

func firstContextFile(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return err
		}
		if !d.IsDir() && d.Name() == name {
			found = path
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("%s not found under %s", name, root)
	}
	return found, nil
}

type readinessRow struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Evidence any    `json:"evidence"`
	Next     string `json:"next"`
}

func getProjectReadiness(base, token string) error {
	assets, err := getAPIJSON(base, token, "/api/assets")
	if err != nil {
		return err
	}
	operations, err := getAPIJSON(base, token, "/api/operations")
	if err != nil {
		return err
	}
	warnings := []string{}
	approvals, err := getAPIJSON(base, token, "/api/operation-approvals/summary")
	if err != nil {
		warnings = append(warnings, "approval summary unavailable: "+err.Error())
		approvals = map[string]any{}
	}
	graph, err := getAPIJSON(base, token, "/api/assets/graph")
	if err != nil {
		warnings = append(warnings, "asset graph unavailable: "+err.Error())
		graph = map[string]any{}
	} else if !assetGraphPayloadAvailable(graph) {
		warnings = append(warnings, "asset graph response missing nodes or edges")
	}
	report := firstVersionReadinessReportWithGraph(apiItems(assets), apiItems(operations), approvals, graph)
	if len(warnings) > 0 {
		report["warnings"] = warnings
	}
	return printJSON(report)
}

func firstVersionReadinessReport(assets, operations []map[string]any, approvals map[string]any) map[string]any {
	return firstVersionReadinessReportWithGraph(assets, operations, approvals, nil)
}

func firstVersionReadinessReportWithGraph(assets, operations []map[string]any, approvals, graph map[string]any) map[string]any {
	assetCounts := countAPIField(assets, "asset_type")
	operationCounts := countAPIField(operations, "operation_type")
	syncTriggered := operationCounts["repo.sync"] + operationCounts["repo.sync_remote"]
	webhookReady := assetCounts["webhook_connection"] > 0
	sshRuns := operationCounts["ssh.exec"] + operationCounts["ssh.command"]
	argoEvidence := assetCounts["argo_connection"] + assetCounts["deployment_target"] + operationCounts["argo.apps.sync"]
	approvalEvidence := intFromAPI(approvals["total"])
	pendingApprovalOps := countAPIStatus(operations, "pending_approval")
	activeApprovalRules := countAPITypeStatus(assets, "operation_approval_rule", "active")
	operationRuns := max(assetCounts["operation_run"], len(operations))
	operationLogs := countOperationRowsWithLogs(operations)
	contextEvidence := assetCounts["agent_task"] + assetCounts["ai_runtime"]
	graphNodes := len(apiItemsByKey(graph, "nodes"))
	graphEdges := len(apiItemsByKey(graph, "edges"))
	graphEvidence := graphNodes + graphEdges

	rows := []readinessRow{
		readinessItem("project", "Create/import project asset", "Create a project or run the demo seed.", assetCounts["project"] > 0, assetCounts["project"], false),
		readinessItem("repositories", "Attach source and mirror repositories", "Add repository metadata and at least two Git remotes.", assetCounts["repository"] > 0 && assetCounts["git_remote"] >= 2, fmt.Sprintf("%d repos / %d remotes", assetCounts["repository"], assetCounts["git_remote"]), assetCounts["repository"] > 0 || assetCounts["git_remote"] > 0),
		readinessItem("repo_sync", "Define RepoSyncAsset", "Create a RepoSyncAsset between source and mirror remotes.", assetCounts["repo_sync"] > 0, assetCounts["repo_sync"], false),
		readinessItem("sync_trigger", "Trigger sync manually and from webhook", "Run a manual sync and configure a Gitea webhook connection.", syncTriggered > 0 && webhookReady, fmt.Sprintf("%d sync ops / %d webhooks", syncTriggered, assetCounts["webhook_connection"]), syncTriggered > 0 || webhookReady),
		readinessItem("github_actions", "See GitHub Actions state", "Sync GitHub Actions for the mirror remote or receive workflow_run webhooks.", assetCounts["pipeline_run"] > 0, assetCounts["pipeline_run"], false),
		readinessItem("ssh", "Register SSH machines and audited commands", "Register an SSH machine and run an approval-gated command.", assetCounts["host"] > 0 && sshRuns > 0, fmt.Sprintf("%d hosts / %d commands", assetCounts["host"], sshRuns), assetCounts["host"] > 0 || sshRuns > 0),
		readinessItem("argo", "Sync Argo apps to deployment targets", "Create an Argo connection, sync apps, and inspect deployment targets.", assetCounts["argo_connection"] > 0 && assetCounts["deployment_target"] > 0 && operationCounts["argo.apps.sync"] > 0, fmt.Sprintf("%d targets / %d Argo connections / %d sync ops", assetCounts["deployment_target"], assetCounts["argo_connection"], operationCounts["argo.apps.sync"]), argoEvidence > 0),
		readinessItem("operations", "View operation history and logs", "Run any controlled operation and inspect its logs.", operationRuns > 0 && operationLogs > 0, fmt.Sprintf("%d runs / %d with logs", operationRuns, operationLogs), operationRuns > 0),
		readinessItem("approval", "Enforce approval for high-risk operations", "Queue a high-risk action that creates an approval request.", (approvalEvidence > 0 || pendingApprovalOps > 0) && activeApprovalRules > 0, fmt.Sprintf("%d approvals / %d pending ops / %d active rules", approvalEvidence, pendingApprovalOps, activeApprovalRules), approvalEvidence > 0 || pendingApprovalOps > 0 || activeApprovalRules > 0),
		readinessItem("context", "Generate AI-readable context from graph", "Create an agent task or AI runtime after syncing the canonical asset ledger.", contextEvidence > 0 && graphEvidence > 0, fmt.Sprintf("%d context assets / %d graph nodes / %d graph edges", contextEvidence, graphNodes, graphEdges), contextEvidence > 0 || graphEvidence > 0),
	}

	counts := map[string]int{"ready": 0, "partial": 0, "missing": 0}
	for _, row := range rows {
		counts[row.Status]++
	}
	return map[string]any{
		"ready":   counts["ready"],
		"partial": counts["partial"],
		"missing": counts["missing"],
		"total":   len(rows),
		"items":   rows,
	}
}

func countOperationRowsWithLogs(rows []map[string]any) int {
	count := 0
	for _, row := range rows {
		if intFromAPI(row["log_count"]) > 0 {
			count++
		}
	}
	return count
}

func readinessItem(key, label, next string, done bool, evidence any, partial bool) readinessRow {
	status := "missing"
	if done {
		status = "ready"
	} else if partial {
		status = "partial"
	}
	return readinessRow{Key: key, Label: label, Status: status, Evidence: evidence, Next: next}
}

func apiItems(payload map[string]any) []map[string]any {
	return apiItemsByKey(payload, "items")
}

func apiItemsByKey(payload map[string]any, key string) []map[string]any {
	rawItems, ok := payload[key].([]any)
	if !ok {
		return nil
	}
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		item := mapFromAPI(raw)
		if item != nil {
			items = append(items, item)
		}
	}
	return items
}

func assetGraphPayloadAvailable(graph map[string]any) bool {
	if graph == nil {
		return false
	}
	_, hasNodes := graph["nodes"]
	_, hasEdges := graph["edges"]
	return hasNodes || hasEdges
}

func mapFromAPI(value any) map[string]any {
	item, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return item
}

func countAPIField(rows []map[string]any, field string) map[string]int {
	counts := map[string]int{}
	for _, row := range rows {
		key := strings.TrimSpace(fmt.Sprint(row[field]))
		if key != "" && key != "<nil>" {
			counts[key]++
		}
	}
	return counts
}

func countAPIStatus(rows []map[string]any, status string) int {
	count := 0
	for _, row := range rows {
		if fmt.Sprint(row["status"]) == status {
			count++
		}
	}
	return count
}

func countAPITypeStatus(rows []map[string]any, typ, status string) int {
	count := 0
	for _, row := range rows {
		if fmt.Sprint(row["asset_type"]) == typ && fmt.Sprint(row["status"]) == status {
			count++
		}
	}
	return count
}

func intFromAPI(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		i, _ := typed.Int64()
		return int(i)
	default:
		return 0
	}
}

func getAPI(base, token, path string) error {
	payload, err := getAPIBytes(base, token, path)
	if err != nil {
		return err
	}
	fmt.Println(string(payload))
	return nil
}

func getAPIJSON(base, token, path string) (map[string]any, error) {
	body, err := getAPIBytes(base, token, path)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decoding %s response: %w", path, err)
	}
	return out, nil
}

func getAPIBytes(base, token, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("gateway returned %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func printJSON(value any) error {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(bytes))
	return nil
}
