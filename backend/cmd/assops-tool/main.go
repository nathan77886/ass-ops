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
		if (len(args) == 5 || len(args) == 6) && args[1] == "branch-protection-plan" {
			plan, err := releaseBranchProtectionPlan(args[2], args[3], args[4])
			if err != nil {
				return err
			}
			if len(args) == 6 {
				return writeTextFile(args[5], plan)
			}
			fmt.Print(plan)
			return nil
		}
	}
	return usage()
}

func usage() error {
	fmt.Fprintln(os.Stderr, "usage: assops-tool [--api URL] [--token TOKEN] <db migrate|db migrations|db seed-demo|db sync-assets|db record-demo-readiness-snapshot|db record-version-validation-snapshot|db pin-config-commit|db backup FILE|db backup-retain DIR KEEP|db inspect-backup FILE|db restore FILE|db rehearse-restore FILE TARGET_DATABASE_URL [REPORT_FILE]|project brief|project readiness|repo remotes|remote actions|operations recent|plan validate|release validate-bundle ARTIFACT_DIR REHEARSAL_REPORT|release helm-values GHCR_OWNER VERSION [OUTPUT_FILE]|release promotion-plan OWNER/REPO GHCR_OWNER VERSION ARTIFACT_DIR REHEARSAL_REPORT HELM_VALUES [OUTPUT_FILE]|release backup-schedule-plan OWNER/REPO ENV RUNNER CRON BACKUP_SOURCE RETENTION_DAYS [OUTPUT_FILE]|release branch-protection-plan OWNER/REPO RULESET_JSON CODEOWNERS [OUTPUT_FILE]>")
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
	case "record-demo-readiness-snapshot":
		fs := flag.NewFlagSet("db record-demo-readiness-snapshot", flag.ContinueOnError)
		projectSlug := fs.String("project-slug", "", "project slug to snapshot")
		projectID := fs.String("project-id", "", "project ID to snapshot")
		dryRun := fs.Bool("dry-run", false, "print the sanitized snapshot without writing")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return usage()
		}
		store, err := app.OpenStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.ApplyMigrations(ctx, "backend/migrations"); err != nil {
			return err
		}
		result, err := app.RecordDemoReadinessSnapshot(ctx, store, app.DemoReadinessSnapshotOptions{
			ProjectSlug: *projectSlug,
			ProjectID:   *projectID,
			DryRun:      *dryRun,
		})
		if err != nil {
			return err
		}
		return printJSON(result)
	case "record-version-validation-snapshot":
		fs := flag.NewFlagSet("db record-version-validation-snapshot", flag.ContinueOnError)
		projectVersionID := fs.String("project-version-id", "", "project version ID to snapshot")
		dryRun := fs.Bool("dry-run", false, "print the sanitized snapshot without writing")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return usage()
		}
		store, err := app.OpenStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.ApplyMigrations(ctx, "backend/migrations"); err != nil {
			return err
		}
		result, err := app.RecordProjectVersionValidationSnapshot(ctx, store, app.ProjectVersionValidationSnapshotOptions{
			ProjectVersionID: *projectVersionID,
			DryRun:           *dryRun,
		})
		if err != nil {
			return err
		}
		return printJSON(result)
	case "pin-config-commit":
		fs := flag.NewFlagSet("db pin-config-commit", flag.ContinueOnError)
		projectVersionID := fs.String("project-version-id", "", "project version ID to update")
		repositoryID := fs.String("repository-id", "", "config repository ID to pin")
		remoteID := fs.String("remote-id", "", "config remote ID to use when multiple remotes have latest_sha")
		dryRun := fs.Bool("dry-run", false, "print the sanitized result without writing")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return usage()
		}
		store, err := app.OpenStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.ApplyMigrations(ctx, "backend/migrations"); err != nil {
			return err
		}
		result, err := app.PinConfigCommit(ctx, store, app.ConfigCommitPinOptions{
			ProjectVersionID: *projectVersionID,
			RepositoryID:     *repositoryID,
			RemoteID:         *remoteID,
			DryRun:           *dryRun,
		})
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

func releaseBranchProtectionPlan(repo, rulesetPath, codeownersPath string) (string, error) {
	repo = strings.TrimSpace(repo)
	rulesetPath = strings.TrimSpace(rulesetPath)
	codeownersPath = strings.TrimSpace(codeownersPath)
	if !isOwnerRepo(repo) {
		return "", fmt.Errorf("repository must be owner/repo")
	}
	if rulesetPath == "" {
		return "", fmt.Errorf("ruleset JSON path is required")
	}
	if codeownersPath == "" {
		return "", fmt.Errorf("CODEOWNERS path is required")
	}
	ruleset, err := readBranchProtectionRuleset(rulesetPath)
	if err != nil {
		return "", err
	}
	codeowners, err := readCodeownersSummary(codeownersPath)
	if err != nil {
		return "", err
	}
	statusChecks := stringSliceFromAny(ruleset["required_status_checks"])
	if len(statusChecks) == 0 {
		return "", fmt.Errorf("ruleset required status checks are empty")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Branch Protection Plan\n\n")
	fmt.Fprintf(&b, "Repository: `%s`\n\n", repo)
	fmt.Fprintf(&b, "Ruleset template: `%s`\n\n", rulesetPath)
	fmt.Fprintf(&b, "CODEOWNERS: `%s`\n\n", codeownersPath)
	fmt.Fprintf(&b, "Ruleset name: `%s`\n\n", ruleset["name"])
	fmt.Fprintf(&b, "Target: `%s`\n\n", ruleset["target"])
	fmt.Fprintf(&b, "Enforcement: `%s`\n\n", ruleset["enforcement"])
	fmt.Fprintf(&b, "## Local Validation\n\n")
	fmt.Fprintf(&b, "- Ruleset targets `~DEFAULT_BRANCH` and keeps enforcement active.\n")
	fmt.Fprintf(&b, "- Branch deletion and non-fast-forward pushes are blocked.\n")
	fmt.Fprintf(&b, "- Pull requests require one approval, code owner review, last-pusher separation, stale-review dismissal, and resolved review threads.\n")
	fmt.Fprintf(&b, "- Required status checks are strict/fresh and match the first-version CI contract.\n")
	fmt.Fprintf(&b, "- CODEOWNERS has a default owner and path-specific owners for backend, web, GitHub governance, deploy, Dockerfile, and Makefile.\n\n")
	fmt.Fprintf(&b, "## Required Checks\n\n")
	for _, check := range statusChecks {
		fmt.Fprintf(&b, "- `%s`\n", check)
	}
	fmt.Fprintf(&b, "\n## CODEOWNERS Coverage\n\n")
	for _, pattern := range stringSliceFromAny(codeowners["required_patterns"]) {
		fmt.Fprintf(&b, "- `%s`\n", pattern)
	}
	fmt.Fprintf(&b, "\n## No-Call Boundary\n\n")
	fmt.Fprintf(&b, "- This plan is local validation only; it does not call GitHub, create rulesets, mutate branch protection, or read secrets.\n")
	fmt.Fprintf(&b, "- Applying the ruleset requires a repository administrator with `Administration: write` permission.\n")
	fmt.Fprintf(&b, "- Review the target repository and default branch in GitHub before running the commands below.\n\n")
	fmt.Fprintf(&b, "## Apply Commands\n\n```bash\n")
	fmt.Fprintf(&b, "jq . %q\n", rulesetPath)
	fmt.Fprintf(&b, "gh api \\\n")
	fmt.Fprintf(&b, "  --method POST \\\n")
	fmt.Fprintf(&b, "  -H \"Accept: application/vnd.github+json\" \\\n")
	fmt.Fprintf(&b, "  -H \"X-GitHub-Api-Version: 2026-03-10\" \\\n")
	fmt.Fprintf(&b, "  /repos/%s/rulesets \\\n", repo)
	fmt.Fprintf(&b, "  --input %q\n", rulesetPath)
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "## Verify Commands\n\n```bash\n")
	fmt.Fprintf(&b, "gh api \\\n")
	fmt.Fprintf(&b, "  -H \"Accept: application/vnd.github+json\" \\\n")
	fmt.Fprintf(&b, "  -H \"X-GitHub-Api-Version: 2026-03-10\" \\\n")
	fmt.Fprintf(&b, "  /repos/%s/rulesets\n", repo)
	fmt.Fprintf(&b, "gh api \\\n")
	fmt.Fprintf(&b, "  -H \"Accept: application/vnd.github+json\" \\\n")
	fmt.Fprintf(&b, "  -H \"X-GitHub-Api-Version: 2026-03-10\" \\\n")
	fmt.Fprintf(&b, "  /repos/%s/rules/branches/main\n", repo)
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "## Update Existing Ruleset\n\n")
	fmt.Fprintf(&b, "If the repository already has this ruleset, replace it only after comparing the active ruleset ID from the verification output:\n\n```bash\n")
	fmt.Fprintf(&b, "gh api \\\n")
	fmt.Fprintf(&b, "  --method PUT \\\n")
	fmt.Fprintf(&b, "  -H \"Accept: application/vnd.github+json\" \\\n")
	fmt.Fprintf(&b, "  -H \"X-GitHub-Api-Version: 2026-03-10\" \\\n")
	fmt.Fprintf(&b, "  /repos/%s/rulesets/<ruleset-id> \\\n", repo)
	fmt.Fprintf(&b, "  --input %q\n", rulesetPath)
	fmt.Fprintf(&b, "```\n")
	return b.String(), nil
}

func readBranchProtectionRuleset(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading ruleset JSON: %w", err)
	}
	var ruleset map[string]any
	if err := json.Unmarshal(raw, &ruleset); err != nil {
		return nil, fmt.Errorf("parsing ruleset JSON: %w", err)
	}
	name, _ := ruleset["name"].(string)
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("ruleset name is required")
	}
	if ruleset["target"] != "branch" {
		return nil, fmt.Errorf("ruleset target must be branch")
	}
	if ruleset["enforcement"] != "active" {
		return nil, fmt.Errorf("ruleset enforcement must be active")
	}
	conditions := toolMapFromAny(ruleset["conditions"])
	refName := toolMapFromAny(conditions["ref_name"])
	if !toolContainsString(stringSliceFromAny(refName["include"]), "~DEFAULT_BRANCH") {
		return nil, fmt.Errorf("ruleset must include ~DEFAULT_BRANCH")
	}
	rules, ok := ruleset["rules"].([]any)
	if !ok || len(rules) == 0 {
		return nil, fmt.Errorf("ruleset rules must be a non-empty array")
	}
	ruleByType := map[string]map[string]any{}
	for _, rawRule := range rules {
		rule := toolMapFromAny(rawRule)
		ruleType := strings.TrimSpace(fmt.Sprint(rule["type"]))
		if ruleType != "" {
			ruleByType[ruleType] = rule
		}
	}
	for _, required := range []string{"deletion", "non_fast_forward", "pull_request", "required_status_checks"} {
		if _, ok := ruleByType[required]; !ok {
			return nil, fmt.Errorf("ruleset missing %s rule", required)
		}
	}
	prParams := toolMapFromAny(ruleByType["pull_request"]["parameters"])
	for _, field := range []string{"dismiss_stale_reviews_on_push", "require_code_owner_review", "require_last_push_approval", "required_review_thread_resolution"} {
		if prParams[field] != true {
			return nil, fmt.Errorf("pull request rule must set %s", field)
		}
	}
	if intFromAny(prParams["required_approving_review_count"]) < 1 {
		return nil, fmt.Errorf("pull request rule must require at least one approval")
	}
	statusParams := toolMapFromAny(ruleByType["required_status_checks"]["parameters"])
	if statusParams["strict_required_status_checks_policy"] != true {
		return nil, fmt.Errorf("required status checks must be strict")
	}
	checks := statusCheckContexts(statusParams["required_status_checks"])
	for _, want := range firstVersionRequiredStatusChecks() {
		if !toolContainsString(checks, want) {
			return nil, fmt.Errorf("ruleset missing required status check %q", want)
		}
	}
	ruleset["required_status_checks"] = checks
	return ruleset, nil
}

func readCodeownersSummary(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading CODEOWNERS: %w", err)
	}
	ownersByPattern := map[string][]string{}
	defaultOwnerLine := 0
	for lineNo, rawLine := range strings.Split(string(raw), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("CODEOWNERS line %d must include a pattern and owner", lineNo+1)
		}
		pattern := fields[0]
		if strings.Contains(pattern, "..") {
			return nil, fmt.Errorf("CODEOWNERS line %d contains unsafe pattern", lineNo+1)
		}
		for _, owner := range fields[1:] {
			if !strings.HasPrefix(owner, "@") || len(owner) < 2 || strings.ContainsAny(owner, " \t\r\n") {
				return nil, fmt.Errorf("CODEOWNERS line %d contains invalid owner", lineNo+1)
			}
			ownersByPattern[pattern] = append(ownersByPattern[pattern], owner)
		}
		if pattern == "*" {
			defaultOwnerLine = lineNo + 1
		}
	}
	for _, pattern := range requiredCodeownerPatterns() {
		if len(ownersByPattern[pattern]) == 0 {
			return nil, fmt.Errorf("CODEOWNERS missing required pattern %s", pattern)
		}
	}
	return map[string]any{
		"required_patterns":  requiredCodeownerPatterns(),
		"pattern_count":      len(ownersByPattern),
		"default_owner_line": defaultOwnerLine,
	}, nil
}

func statusCheckContexts(value any) []string {
	rawItems, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item := toolMapFromAny(rawItem)
		context, ok := item["context"].(string)
		context = strings.TrimSpace(context)
		if ok && context != "" {
			out = append(out, context)
		}
	}
	sort.Strings(out)
	return out
}

// Keep this list synchronized with `.github/workflows/ci.yml` job names and
// `.github/rulesets/main-required-checks.json`.
func firstVersionRequiredStatusChecks() []string {
	return []string{
		"Workflow Lint",
		"Secret Scan",
		"Go",
		"Web",
		"Compose Config",
		"DB Rehearsal",
		"Helm Chart",
		"Helm Smoke",
		"Docker Build (gateway)",
		"Docker Build (worker)",
		"Docker Build (node-worker)",
		"Docker Build (web)",
		"Go Vulnerability Check",
	}
}

func requiredCodeownerPatterns() []string {
	return []string{"*", "/backend/", "/web/", "/.github/", "/deploy/", "/docs/deploy-production.md", "/docs/deploy-helm.md", "/docs/github-branch-protection.md", "/Dockerfile", "/Makefile"}
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

func toolMapFromAny(value any) map[string]any {
	if item, ok := value.(map[string]any); ok {
		return item
	}
	return nil
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		n, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
		return n
	}
}

func toolContainsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
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
	Key                   string `json:"key"`
	Label                 string `json:"label"`
	Status                string `json:"status"`
	Evidence              any    `json:"evidence"`
	Next                  string `json:"next"`
	DemoDataRehearsalPlan any    `json:"demo_data_rehearsal_plan,omitempty"`
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
	giteaWebhooks := countAPITypeMetadata(assets, "webhook_connection", "provider", "gitea")
	giteaWebhookEvents := countAPITypeMetadata(assets, "webhook_event", "provider", "gitea")
	sshVerifyRuns := operationCounts["ssh.verify"]
	sshCommandRuns := operationCounts["ssh.exec"] + operationCounts["ssh.command"]
	approvalEvidence := intFromAPI(approvals["total"])
	pendingApprovalOps := countAPIStatus(operations, "pending_approval")
	approvalAssets := assetCounts["operation_approval"]
	activeApprovalRules := countAPITypeStatus(assets, "operation_approval_rule", "active")
	operationAssets := assetCounts["operation_run"]
	listedOperationRuns := len(operations)
	operationLogs := countOperationRowsWithLogs(operations, assetIDsByType(assets, "operation_run"))
	contextEvidence := assetCounts["agent_task"] + assetCounts["ai_runtime"]
	contextGenerations := countContextGenerationEvidence(assets)
	graphNodes := len(apiItemsByKey(graph, "nodes"))
	graphEdges := len(apiItemsByKey(graph, "edges"))
	graphEvidence := graphNodes + graphEdges
	projectGraphNodes := countGraphNodesByPrefix(graph, "project:")
	projectAssetGraphNodes := countGraphNodesByKnownIDs(graph, assetIDsByType(assets, "project"))
	repositoryGraphLinks := countRepositoryGraphLinks(graph, assetIDsByType(assets, "project"), assetIDsByType(assets, "repository"), assetIDsByType(assets, "git_remote"))
	repoSyncGraphLinks := countRepoSyncGraphLinks(graph, assetIDsByType(assets, "repository"), assetIDsByType(assets, "repo_sync"), assetIDsByType(assets, "git_remote"))
	syncOperationIDs := mergeBoolMaps(operationIDsByType(operations, "repo.sync"), operationIDsByType(operations, "repo.sync_remote"))
	webhookSyncGraphLinks := countWebhookSyncGraphLinks(
		graph,
		assetIDsByTypeMetadata(assets, "webhook_connection", "provider", "gitea"),
		assetIDsByTypeMetadata(assets, "webhook_event", "provider", "gitea"),
		assetIDsByType(assets, "repo_sync"),
		syncOperationIDs,
	)
	tagOperationIDs := mergeBoolMaps(operationIDsByType(operations, "repo.tag"), operationIDsByType(operations, "repo.create_tag"))
	githubActionLinks := countGitHubActionGraphLinks(
		graph,
		assetIDsByType(assets, "project"),
		assetIDsByType(assets, "repository"),
		assetIDsByType(assets, "git_remote"),
		assetIDsByGraphType(assets, "pipeline_run", "github_action_run"),
		assetIDsByType(assets, "repo_tag_run"),
		tagRunAssetIDsByOperation(assets),
		tagOperationIDs,
	)
	repoTagRuns := operationCounts["repo.tag"] + operationCounts["repo.create_tag"]
	sshVerifyOperationIDs := operationIDsByType(operations, "ssh.verify")
	sshRunOperationIDs := mergeBoolMaps(operationIDsByType(operations, "ssh.exec"), operationIDsByType(operations, "ssh.command"))
	sshOperationIDs := mergeBoolMaps(sshVerifyOperationIDs, sshRunOperationIDs)
	sshMachineAssetIDs := mergeBoolMaps(assetIDsByGraphType(assets, "host", "ssh_machine"), assetIDsByType(assets, "ssh_machine"))
	sshMachineAssets := assetCounts["host"] + assetCounts["ssh_machine"]
	sshGraphLinks := countSSHGraphLinks(graph, assetIDsByType(assets, "ssh_command_run"), sshMachineAssetIDs, sshOperationIDs, sshVerifyOperationIDs, sshRunOperationIDs)
	argoGraphLinks := countArgoGraphLinks(
		graph,
		assetIDsByType(assets, "argo_connection"),
		assetIDsByType(assets, "argo_app"),
		assetIDsByType(assets, "deployment_target"),
		operationIDsByType(operations, "argo.apps.sync"),
	)
	activeApprovalRuleIDs := activeAssetIDsByTypeStatus(assets, "operation_approval_rule", "active")
	approvalGraphLinks := countApprovalGraphLinks(
		graph,
		activeApprovalRuleIDs,
		assetIDsByType(assets, "operation_approval"),
		assetIDsByType(assets, "operation_run"),
		operationIDsByStatus(operations, "pending_approval"),
	)
	contextGraphLinks := countContextGraphLinks(assets, graph)
	argoEvidence := assetCounts["argo_connection"] + assetCounts["argo_app"] + assetCounts["deployment_target"] + operationCounts["argo.apps.sync"] + argoGraphLinks.ConnectionApps + argoGraphLinks.AppTargets + argoGraphLinks.CompleteAppAssets

	projectRow := readinessItem("project", "Create/import project asset", "Create a project or run the demo seed.", assetCounts["project"] > 0 && projectAssetGraphNodes > 0, fmt.Sprintf("%d project assets / %d project graph nodes / %d project asset nodes", assetCounts["project"], projectGraphNodes, projectAssetGraphNodes), assetCounts["project"] > 0 || projectGraphNodes > 0 || projectAssetGraphNodes > 0)
	projectRow.DemoDataRehearsalPlan = projectDemoDataRehearsalPlan(projectRow.Status, map[string]int{
		"project_assets":      assetCounts["project"],
		"project_graph_nodes": projectGraphNodes,
		"project_asset_nodes": projectAssetGraphNodes,
	}, []string{"project_asset", "project_asset_node"})
	repositoriesRow := readinessItem("repositories", "Attach source and mirror repositories", "Add repository metadata and at least two Git remotes.", assetCounts["repository"] > 0 && assetCounts["git_remote"] >= 2 && repositoryGraphLinks.CompleteRepoAssets > 0, fmt.Sprintf("%d repos / %d remotes / %d complete repos / %d repo asset paths / %d project links / %d remote links", assetCounts["repository"], assetCounts["git_remote"], repositoryGraphLinks.CompleteRepos, repositoryGraphLinks.CompleteRepoAssets, repositoryGraphLinks.ProjectRepository, repositoryGraphLinks.RepositoryRemotes), assetCounts["repository"] > 0 || assetCounts["git_remote"] > 0 || repositoryGraphLinks.ProjectRepository > 0 || repositoryGraphLinks.RepositoryRemotes > 0 || repositoryGraphLinks.CompleteRepoAssets > 0)
	repositoriesRow.DemoDataRehearsalPlan = projectDemoDataRehearsalPlan(repositoriesRow.Status, map[string]int{
		"repository_assets":         assetCounts["repository"],
		"git_remote_assets":         assetCounts["git_remote"],
		"complete_repository_paths": repositoryGraphLinks.CompleteRepoAssets,
		"project_repository_links":  repositoryGraphLinks.ProjectRepository,
		"repository_remote_links":   repositoryGraphLinks.RepositoryRemotes,
	}, []string{"repository_asset", "two_git_remote_assets", "project_to_repository_graph_link", "repository_to_two_remotes_graph_path"})
	argoEvidenceText := fmt.Sprintf("%d targets / %d Argo connections / %d apps / %d sync ops / %d complete app links / %d app asset chains", assetCounts["deployment_target"], assetCounts["argo_connection"], assetCounts["argo_app"], operationCounts["argo.apps.sync"], argoGraphLinks.CompleteApps, argoGraphLinks.CompleteAppAssets)
	if argoGraphLinks.CompleteApps > 0 && argoGraphLinks.CompleteAppAssets == 0 {
		argoEvidenceText += " / canonical evidence missing"
	}
	syncTriggerEvidenceText := fmt.Sprintf("%d sync ops / %d Gitea webhooks / %d Gitea events / %d any-provider complete webhook chains / %d webhook asset chains", syncTriggered, giteaWebhooks, giteaWebhookEvents, webhookSyncGraphLinks.CompleteChains, webhookSyncGraphLinks.CompleteChainAssets)
	if webhookSyncGraphLinks.CompleteChains > 0 && webhookSyncGraphLinks.CompleteChainAssets == 0 {
		syncTriggerEvidenceText += " / canonical evidence missing"
	}
	rows := []readinessRow{
		projectRow,
		repositoriesRow,
		readinessItem("repo_sync", "Define RepoSyncAsset", "Create a RepoSyncAsset between source and mirror remotes.", assetCounts["repo_sync"] > 0 && repoSyncGraphLinks.CompleteSyncAssets > 0, fmt.Sprintf("%d repo syncs / %d graph-complete syncs / %d sync asset paths / %d repository links / %d source links / %d target links", assetCounts["repo_sync"], repoSyncGraphLinks.CompleteSyncs, repoSyncGraphLinks.CompleteSyncAssets, repoSyncGraphLinks.RepositorySync, repoSyncGraphLinks.SourceRemotes, repoSyncGraphLinks.TargetRemotes), assetCounts["repo_sync"] > 0 || repoSyncGraphLinks.RepositorySync > 0 || repoSyncGraphLinks.SourceRemotes > 0 || repoSyncGraphLinks.TargetRemotes > 0 || repoSyncGraphLinks.CompleteSyncAssets > 0),
		readinessItem("sync_trigger", "Trigger sync manually and from webhook", "Run a manual sync and receive or replay a Gitea webhook event.", syncTriggered > 0 && giteaWebhooks > 0 && giteaWebhookEvents > 0 && webhookSyncGraphLinks.CompleteChainAssets > 0, syncTriggerEvidenceText, syncTriggered > 0 || giteaWebhooks > 0 || giteaWebhookEvents > 0 || webhookSyncGraphLinks.ConnectionEvents > 0 || webhookSyncGraphLinks.EventRepoSyncs > 0 || webhookSyncGraphLinks.EventOperations > 0 || webhookSyncGraphLinks.CompleteChainAssets > 0),
		readinessItem("github_actions", "See GitHub tags and Actions state", "Create a repository tag and sync GitHub Actions for the mirror remote or receive workflow_run webhooks.", assetCounts["pipeline_run"] > 0 && githubActionLinks.CompleteActionAssets > 0 && repoTagRuns > 0 && githubActionLinks.CompleteTaggedRemoteAssets > 0 && githubActionLinks.LinkedTagRunAssets > 0, fmt.Sprintf("%d pipeline runs / %d complete action chains / %d action asset chains / %d tag ops / %d complete tag links / %d tag asset links / %d linked tag runs / %d linked tag assets / %d project links / %d remote links / %d action links / %d tag links / %d tag-action links", assetCounts["pipeline_run"], githubActionLinks.CompleteActionRuns, githubActionLinks.CompleteActionAssets, repoTagRuns, githubActionLinks.CompleteTaggedRemotes, githubActionLinks.CompleteTaggedRemoteAssets, githubActionLinks.LinkedTagRuns, githubActionLinks.LinkedTagRunAssets, githubActionLinks.ProjectRepositories, githubActionLinks.RepositoryRemotes, githubActionLinks.RemoteActionRuns, githubActionLinks.TaggedRemotes, githubActionLinks.TagActionRunLinks), assetCounts["pipeline_run"] > 0 || repoTagRuns > 0 || githubActionLinks.ProjectRepositories > 0 || githubActionLinks.RepositoryRemotes > 0 || githubActionLinks.RemoteActionRuns > 0 || githubActionLinks.TaggedRemotes > 0 || githubActionLinks.TagActionRunLinks > 0 || githubActionLinks.CompleteActionAssets > 0 || githubActionLinks.CompleteTaggedRemoteAssets > 0 || githubActionLinks.LinkedTagRunAssets > 0),
		readinessItem("ssh", "Register SSH machines and audited commands", "Verify an SSH machine, then run an approval-gated command.", sshMachineAssets > 0 && sshVerifyRuns > 0 && sshCommandRuns > 0 && sshGraphLinks.CompleteVerifyCommandAssets > 0 && sshGraphLinks.CompleteRunCommandAssets > 0, fmt.Sprintf("%d machines / %d verify ops / %d command ops / %d command assets / %d complete audit chains / %d command asset chains / %d verify chains / %d run chains", sshMachineAssets, sshVerifyRuns, sshCommandRuns, assetCounts["ssh_command_run"], sshGraphLinks.CompleteCommands, sshGraphLinks.CompleteCommandAssets, sshGraphLinks.CompleteVerifyCommandAssets, sshGraphLinks.CompleteRunCommandAssets), sshMachineAssets > 0 || sshVerifyRuns > 0 || sshCommandRuns > 0 || assetCounts["ssh_command_run"] > 0 || sshGraphLinks.OperationCommands > 0 || sshGraphLinks.CommandMachines > 0 || sshGraphLinks.CompleteCommandAssets > 0),
		readinessItem("argo", "Sync Argo apps to deployment targets", "Create an Argo connection, sync apps, and inspect deployment targets.", assetCounts["argo_connection"] > 0 && assetCounts["argo_app"] > 0 && assetCounts["deployment_target"] > 0 && operationCounts["argo.apps.sync"] > 0 && argoGraphLinks.CompleteAppAssets > 0, argoEvidenceText, argoEvidence > 0),
		readinessItem("operations", "View operation history and logs", "Run any controlled operation and inspect its logs.", operationAssets > 0 && operationLogs > 0, fmt.Sprintf("%d operation assets / %d listed runs / %d with logs", operationAssets, listedOperationRuns, operationLogs), operationAssets > 0 || listedOperationRuns > 0 || operationLogs > 0),
		readinessItem("approval", "Enforce approval for high-risk operations", "Queue a high-risk action that creates an approval request.", approvalAssets > 0 && pendingApprovalOps > 0 && activeApprovalRules > 0 && approvalGraphLinks.CompleteApprovalAssetChains > 0, fmt.Sprintf("%d approvals / %d approval assets / %d pending ops / %d active rules / %d governed approvals / %d gated ops / %d complete approval chains / %d approval asset chains", approvalEvidence, approvalAssets, pendingApprovalOps, activeApprovalRules, approvalGraphLinks.RuleApprovals, approvalGraphLinks.ApprovalOperations, approvalGraphLinks.CompleteApprovalChains, approvalGraphLinks.CompleteApprovalAssetChains), approvalEvidence > 0 || approvalAssets > 0 || pendingApprovalOps > 0 || activeApprovalRules > 0 || approvalGraphLinks.RuleApprovals > 0 || approvalGraphLinks.ApprovalOperations > 0 || approvalGraphLinks.CompleteApprovalAssetChains > 0),
		readinessItem("context", "Generate AI-readable context from graph", "Create an agent task or AI runtime after syncing the canonical asset ledger.", contextEvidence > 0 && contextGenerations > 0 && graphEvidence > 0 && contextGraphLinks.CompleteContextTaskAssets > 0, fmt.Sprintf("%d context assets / %d context generations / %d complete context tasks / %d context asset tasks / %d runtime links / %d context tool links / %d graph nodes / %d graph edges", contextEvidence, contextGenerations, contextGraphLinks.CompleteContextTasks, contextGraphLinks.CompleteContextTaskAssets, contextGraphLinks.TaskRuntimes, contextGraphLinks.TaskContextToolCalls, graphNodes, graphEdges), contextEvidence > 0 || contextGenerations > 0 || graphEvidence > 0 || contextGraphLinks.TaskRuntimes > 0 || contextGraphLinks.TaskContextToolCalls > 0 || contextGraphLinks.CompleteContextTaskAssets > 0),
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

func countOperationRowsWithLogs(rows []map[string]any, operationAssetIDs map[string]bool) int {
	count := 0
	for _, row := range rows {
		if intFromAPI(row["log_count"]) > 0 && operationAssetIDs[operationRowAssetID(row)] {
			count++
		}
	}
	return count
}

func assetIDsByType(rows []map[string]any, typ string) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["asset_type"]) != typ {
			continue
		}
		if assetID := canonicalAssetGraphID(row, typ); assetID != "" {
			ids[assetID] = true
		}
	}
	return ids
}

func assetIDsByTypeMetadata(rows []map[string]any, typ, key, value string) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		metadata := mapFromAPI(row["metadata"])
		if fmt.Sprint(row["asset_type"]) != typ || !metadataValueEqual(metadata[key], value) {
			continue
		}
		if assetID := canonicalAssetGraphID(row, typ); assetID != "" {
			ids[assetID] = true
		}
	}
	return ids
}

func tagRunAssetIDsByOperation(rows []map[string]any) map[string]map[string]bool {
	ids := map[string]map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["asset_type"]) != "repo_tag_run" {
			continue
		}
		assetID := canonicalAssetGraphID(row, "repo_tag_run")
		if assetID == "" {
			continue
		}
		metadata := mapFromAPI(row["metadata"])
		operationID := cleanOperationAssetID(fmt.Sprint(metadata["operation_run_id"]))
		if operationID == "" {
			continue
		}
		if ids[operationID] == nil {
			ids[operationID] = map[string]bool{}
		}
		ids[operationID][assetID] = true
	}
	return ids
}

func cleanOperationAssetID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "<nil>" {
		return ""
	}
	if strings.HasPrefix(value, "operation_run:") {
		return value
	}
	return "operation_run:" + value
}

func assetIDsByGraphType(rows []map[string]any, assetType, graphType string) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["asset_type"]) != assetType {
			continue
		}
		if assetID := canonicalAssetGraphID(row, graphType); assetID != "" {
			ids[assetID] = true
		}
	}
	return ids
}

func canonicalAssetGraphID(row map[string]any, typ string) string {
	sourceID := strings.TrimSpace(fmt.Sprint(row["source_id"]))
	if sourceID != "" && sourceID != "<nil>" {
		return typ + ":" + sourceID
	}
	for _, key := range []string{"asset_id", "id"} {
		raw, ok := row[key].(string)
		if !ok {
			continue
		}
		value := strings.TrimSpace(raw)
		if value == "" || value == "<nil>" {
			continue
		}
		if strings.HasPrefix(value, typ+":") {
			return value
		}
		if !strings.Contains(value, ":") {
			return typ + ":" + value
		}
	}
	return ""
}

func operationRowAssetID(row map[string]any) string {
	for _, key := range []string{"id", "asset_id"} {
		value := strings.TrimSpace(fmt.Sprint(row[key]))
		if value == "" || value == "<nil>" {
			continue
		}
		if strings.HasPrefix(value, "operation_run:") {
			return value
		}
		return "operation_run:" + value
	}
	return ""
}

func operationIDsByType(rows []map[string]any, typ string) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["operation_type"]) != typ {
			continue
		}
		if assetID := operationRowAssetID(row); assetID != "" {
			ids[assetID] = true
		}
	}
	return ids
}

func operationIDsByStatus(rows []map[string]any, status string) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["status"]) != status {
			continue
		}
		if assetID := operationRowAssetID(row); assetID != "" {
			ids[assetID] = true
		}
	}
	return ids
}

func mergeBoolMaps(maps ...map[string]bool) map[string]bool {
	merged := map[string]bool{}
	for _, values := range maps {
		for key, value := range values {
			if value {
				merged[key] = true
			}
		}
	}
	return merged
}

func countContextGenerationEvidence(assets []map[string]any) int {
	count := 0
	for _, row := range assets {
		metadata := mapFromAPI(row["metadata"])
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(row["status"])))
		if fmt.Sprint(row["asset_type"]) == "agent_tool_call" &&
			fmt.Sprint(metadata["tool_name"]) == "context.generate" &&
			status == "completed" {
			count++
		}
	}
	return count
}

func apiAssetGraphID(row map[string]any) string {
	for _, key := range []string{"asset_id", "id"} {
		raw, ok := row[key].(string)
		if !ok {
			continue
		}
		value := strings.TrimSpace(raw)
		if value != "" && value != "<nil>" {
			return value
		}
	}
	typ := strings.TrimSpace(fmt.Sprint(row["asset_type"]))
	sourceID := strings.TrimSpace(fmt.Sprint(row["source_id"]))
	if typ != "" && typ != "<nil>" && sourceID != "" && sourceID != "<nil>" {
		return typ + ":" + sourceID
	}
	return ""
}

type contextGraphLinkCounts struct {
	TaskRuntimes              int
	TaskContextToolCalls      int
	CompleteContextTasks      int
	CompleteContextTaskAssets int
}

func countContextGraphLinks(assets []map[string]any, graph map[string]any) contextGraphLinkCounts {
	counts := contextGraphLinkCounts{}
	contextToolCalls := map[string]bool{}
	taskAssetIDs := assetIDsByType(assets, "agent_task")
	runtimeAssetIDs := assetIDsByType(assets, "ai_runtime")
	for _, row := range assets {
		metadata := mapFromAPI(row["metadata"])
		if metadata == nil {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(row["status"])))
		if fmt.Sprint(row["asset_type"]) == "agent_tool_call" &&
			fmt.Sprint(metadata["tool_name"]) == "context.generate" &&
			status == "completed" {
			if assetID := apiAssetGraphID(row); strings.HasPrefix(assetID, "agent_tool_call:") {
				contextToolCalls[assetID] = true
			}
		}
	}

	type taskLinks struct {
		runtimes     map[string]bool
		contextTools map[string]bool
	}
	byTask := map[string]*taskLinks{}
	taskEntry := func(assetID string) *taskLinks {
		entry := byTask[assetID]
		if entry == nil {
			entry = &taskLinks{runtimes: map[string]bool{}, contextTools: map[string]bool{}}
			byTask[assetID] = entry
		}
		return entry
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "uses_runtime":
			if strings.HasPrefix(from, "agent_task:") && strings.HasPrefix(to, "ai_runtime:") {
				counts.TaskRuntimes++
				taskEntry(from).runtimes[to] = true
			}
		case "records_tool_call":
			if strings.HasPrefix(from, "agent_task:") && contextToolCalls[to] {
				counts.TaskContextToolCalls++
				taskEntry(from).contextTools[to] = true
			}
		}
	}
	for taskID, entry := range byTask {
		if len(entry.runtimes) > 0 && len(entry.contextTools) > 0 {
			counts.CompleteContextTasks++
			if taskAssetIDs[taskID] && hasAnyKnownID(entry.runtimes, runtimeAssetIDs) && hasAnyKnownID(entry.contextTools, contextToolCalls) {
				counts.CompleteContextTaskAssets++
			}
		}
	}
	return counts
}

func hasAnyKnownID(ids, knownIDs map[string]bool) bool {
	for id := range ids {
		if knownIDs[id] {
			return true
		}
	}
	return false
}

func hasAnyIDInBoth(ids, firstKnownIDs, secondKnownIDs map[string]bool) bool {
	for id := range ids {
		if firstKnownIDs[id] && secondKnownIDs[id] {
			return true
		}
	}
	return false
}

func countGraphNodesByPrefix(graph map[string]any, prefix string) int {
	count := 0
	for _, node := range apiItemsByKey(graph, "nodes") {
		id := fmt.Sprint(node["id"])
		if strings.HasPrefix(id, prefix) {
			count++
		}
	}
	return count
}

func countGraphNodesByKnownIDs(graph map[string]any, knownIDs map[string]bool) int {
	count := 0
	for _, node := range apiItemsByKey(graph, "nodes") {
		id := fmt.Sprint(node["id"])
		if knownIDs[id] {
			count++
		}
	}
	return count
}

func projectDemoDataRehearsalPlan(status string, evidence map[string]int, requiredEvidence []string) map[string]any {
	planState := "planned"
	if status == "ready" {
		planState = "observed"
	} else if status == "missing" {
		planState = "blocked"
	}
	blockedReasons := []string{}
	if status != "ready" {
		blockedReasons = append(blockedReasons, "live_demo_graph_evidence_incomplete")
	}
	environmentPlan := demoDataEnvironmentEvidencePlan(status, evidence, requiredEvidence)
	graphPlan := demoDataGraphProofPlan(status, evidence, requiredEvidence)
	environmentProof := demoDataEnvironmentProof(status, evidence, requiredEvidence)
	resultPlan := demoDataResultRecordingPlan(status, evidence, requiredEvidence)
	return map[string]any{
		"mode":                      "first_version_demo_data_rehearsal_plan",
		"plan_state":                planState,
		"readiness_status":          status,
		"execution_enabled":         false,
		"external_call_made":        false,
		"demo_seed_written":         false,
		"project_created":           false,
		"repository_created":        false,
		"git_remote_created":        false,
		"asset_graph_written":       false,
		"contains_remote_url":       false,
		"contains_credentials":      false,
		"required_evidence":         requiredEvidence,
		"evidence_counts":           evidence,
		"environment_evidence_plan": environmentPlan,
		"environment_demo_proof":    environmentProof,
		"graph_proof_plan":          graphPlan,
		"result_recording_plan":     resultPlan,
		"disabled_backends": []string{
			"project_create",
			"repository_create",
			"git_remote_create",
			"demo_seed_write",
			"asset_graph_write",
		},
		"suppressed_fields": []string{
			"remote_url",
			"git_credentials",
			"provider_token",
			"repository_secret",
			"webhook_secret",
		},
		"blocked_reasons": blockedReasons,
		"message":         "Demo data rehearsal is audit-only; create project/repository/remote evidence in the live environment, then sync the canonical asset graph.",
	}
}

// Keep this proof contract in sync with web/src/main.tsx demoDataEnvironmentProof.
func demoDataEnvironmentProof(status string, evidence map[string]int, requiredEvidence []string) map[string]any {
	if evidence == nil {
		evidence = map[string]int{}
	}
	checks := demoDataEvidenceChecks(evidence)
	missing := missingDemoDataEvidence(checks, requiredEvidence)
	proofState := "observed"
	if len(missing) > 0 {
		proofState = "partial"
	}
	if status == "missing" {
		proofState = "blocked"
	}
	liveEnvironmentDataObserved := len(missing) == 0
	if status == "missing" {
		liveEnvironmentDataObserved = false
	}
	multiRemoteObserved := evidence["repository_assets"] > 0 &&
		evidence["git_remote_assets"] >= 2 &&
		evidence["project_repository_links"] > 0 &&
		evidence["complete_repository_paths"] > 0 &&
		evidence["repository_remote_links"] >= 2 &&
		proofState != "blocked"
	return map[string]any{
		"mode":                           "first_version_demo_environment_proof",
		"proof_state":                    proofState,
		"proof_ready":                    len(missing) == 0 && status == "ready",
		"proof_source":                   "canonical_asset_graph_counts",
		"live_environment_data_observed": liveEnvironmentDataObserved,
		"complete_repository_multi_remote_path_observed": multiRemoteObserved,
		"required_evidence":    requiredEvidence,
		"missing_evidence":     missing,
		"evidence_counts":      evidence,
		"external_call_made":   false,
		"demo_seed_written":    false,
		"project_created":      false,
		"repository_created":   false,
		"git_remote_created":   false,
		"asset_graph_written":  false,
		"contains_remote_url":  false,
		"contains_credentials": false,
		"suppressed_fields":    []string{"project_asset_id", "repository_asset_id", "source_remote_asset_id", "mirror_remote_asset_id", "remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"},
	}
}

func demoDataEvidenceChecks(evidence map[string]int) map[string]bool {
	if evidence == nil {
		evidence = map[string]int{}
	}
	return map[string]bool{
		"project_asset":                        evidence["project_assets"] > 0,
		"project_graph_node":                   evidence["project_graph_nodes"] > 0,
		"project_asset_node":                   evidence["project_asset_nodes"] > 0,
		"repository_asset":                     evidence["repository_assets"] > 0,
		"two_git_remote_assets":                evidence["git_remote_assets"] >= 2,
		"project_to_repository_graph_link":     evidence["project_repository_links"] > 0,
		"repository_to_two_remotes_graph_path": evidence["complete_repository_paths"] > 0,
	}
}

func missingDemoDataEvidence(checks map[string]bool, requiredEvidence []string) []string {
	missing := make([]string, 0)
	for _, key := range requiredEvidence {
		if !checks[key] {
			missing = append(missing, key)
		}
	}
	return missing
}

func demoDataEnvironmentEvidencePlan(status string, evidence map[string]int, requiredEvidence []string) map[string]any {
	metadataReady := status == "ready"
	blockedReasons := []string{"demo_seed_execution_disabled", "live_environment_not_recorded"}
	if status != "ready" {
		blockedReasons = append(blockedReasons, "required_graph_evidence_missing")
	}
	return map[string]any{
		"mode":                        "first_version_demo_environment_evidence_plan",
		"evidence_state":              mapStatusToPlanState(status),
		"evidence_ready":              false,
		"evidence_ready_reason":       "demo_environment_execution_disabled",
		"metadata_ready":              metadataReady,
		"execution_enabled":           false,
		"demo_seed_written":           false,
		"project_created":             false,
		"repository_created":          false,
		"git_remote_created":          false,
		"external_call_made":          false,
		"contains_remote_url":         false,
		"contains_credentials":        false,
		"required_evidence":           requiredEvidence,
		"evidence_counts":             evidence,
		"required_environment_fields": []string{"project_asset", "project_graph_node", "project_asset_node", "repository_asset", "two_git_remote_assets", "project_repository_graph_link", "repository_to_two_remotes_graph_path"},
		"suppressed_fields":           []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"},
		"blocked_reasons":             blockedReasons,
		"message":                     "Demo environment evidence is observed only; this plan does not create demo project, repository, or remote rows.",
	}
}

func demoDataGraphProofPlan(status string, evidence map[string]int, requiredEvidence []string) map[string]any {
	metadataReady := status == "ready"
	blockedReasons := []string{"asset_graph_write_disabled"}
	if status != "ready" {
		blockedReasons = append(blockedReasons, "graph_proof_incomplete")
	}
	return map[string]any{
		"mode":                  "first_version_demo_graph_proof_plan",
		"proof_state":           mapStatusToPlanState(status),
		"proof_ready":           false,
		"proof_ready_reason":    "demo_graph_proof_execution_disabled",
		"metadata_ready":        metadataReady,
		"asset_graph_written":   false,
		"asset_sync_triggered":  false,
		"graph_query_performed": false,
		"external_call_made":    false,
		"required_evidence":     requiredEvidence,
		"evidence_counts":       evidence,
		"required_graph_paths":  []string{"project:*", "project:* -> repository:*", "repository:* -> git_remote:*", "repository:* -> second git_remote:*"},
		"suppressed_fields":     []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"},
		"blocked_reasons":       blockedReasons,
		"message":               "Demo graph proof is read-only; future execution must sync canonical assets and prove one repository has at least two remotes.",
	}
}

func demoDataResultRecordingPlan(status string, evidence map[string]int, requiredEvidence []string) map[string]any {
	// Result recording stays blocked even when graph evidence is observed; it only
	// becomes meaningful after a future live demo-data execution writes a result.
	checks := demoDataEvidenceChecks(evidence)
	missing := missingDemoDataEvidence(checks, requiredEvidence)
	readinessSnapshotReady := status == "ready" && len(missing) == 0
	graphSnapshotReady := readinessSnapshotReady
	blockedReasons := []string{"demo_result_write_disabled", "readiness_snapshot_write_disabled", "asset_graph_snapshot_write_disabled"}
	if !readinessSnapshotReady {
		blockedReasons = append(blockedReasons, "required_demo_evidence_missing")
	}
	preflight := map[string]any{
		"mode":                                  "first_version_demo_data_result_recording_preflight",
		"readiness_status":                      status,
		"readiness_snapshot_ready_for_review":   readinessSnapshotReady,
		"asset_graph_snapshot_ready_for_review": graphSnapshotReady,
		"snapshot_contract_ready":               readinessSnapshotReady && graphSnapshotReady,
		"snapshot_write_enabled":                false,
		"asset_graph_write_enabled":             false,
		"operation_log_write_enabled":           false,
		"external_call_made":                    false,
		"contains_remote_url":                   false,
		"contains_credentials":                  false,
		"required_evidence":                     requiredEvidence,
		"missing_required_evidence":             missing,
		"evidence_counts":                       evidence,
		"required_snapshot_fields":              []string{"project_asset_id", "repository_asset_id", "source_remote_asset_id", "mirror_remote_asset_id", "graph_proof_status", "readiness_status", "evidence_counts", "missing_required_evidence"},
		"suppressed_fields":                     []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret", "raw_graph_payload", "operation_log_body"},
		"disabled_backends":                     []string{"demo_result_write", "readiness_snapshot_write", "asset_graph_snapshot_write", "operation_log_write"},
		"blocked_reasons":                       blockedReasons,
		"message":                               "Demo result recording preflight is review metadata only; snapshot and operation-log writes remain disabled.",
	}
	return map[string]any{
		"mode":                          "first_version_demo_data_result_recording_plan",
		"result_recording_state":        "blocked",
		"result_recording_ready":        false,
		"result_recording_ready_reason": "demo_data_execution_not_performed",
		"recording_enabled":             false,
		"result_written":                false,
		"operation_log_written":         false,
		"readiness_snapshot_written":    false,
		"asset_graph_snapshot_written":  false,
		"raw_remote_url_recorded":       false,
		"raw_credentials_recorded":      false,
		"required_result_fields":        []string{"project_asset_id", "repository_asset_id", "source_remote_asset_id", "mirror_remote_asset_id", "graph_proof_status", "readiness_status"},
		"result_recording_preflight":    preflight,
		"suppressed_fields":             []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret", "raw_graph_payload", "operation_log_body"},
		"blocked_reasons":               []string{"demo_data_execution_not_performed", "readiness_snapshot_not_recorded", "asset_graph_snapshot_not_recorded"},
		"message":                       "Demo data result recording is disabled until a live environment run creates and proves the graph-backed demo evidence.",
	}
}

func mapStatusToPlanState(status string) string {
	if status == "ready" {
		return "observed"
	}
	if status == "missing" {
		return "blocked"
	}
	return "planned"
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

func activeAssetIDsByTypeStatus(rows []map[string]any, typ, status string) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["asset_type"]) != typ || fmt.Sprint(row["status"]) != status {
			continue
		}
		if assetID := apiAssetGraphID(row); assetID != "" {
			ids[assetID] = true
		}
	}
	return ids
}

func countAPITypeMetadata(rows []map[string]any, typ, key, value string) int {
	count := 0
	for _, row := range rows {
		metadata := mapFromAPI(row["metadata"])
		if fmt.Sprint(row["asset_type"]) == typ && metadataValueEqual(metadata[key], value) {
			count++
		}
	}
	return count
}

func metadataValueEqual(raw any, value string) bool {
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(raw)), strings.TrimSpace(value))
}

type repositoryGraphLinkCounts struct {
	ProjectRepository  int
	RepositoryRemotes  int
	CompleteRepos      int
	CompleteRepoAssets int
}

func countRepositoryGraphLinks(graph map[string]any, projectAssetIDs, repositoryAssetIDs, remoteAssetIDs map[string]bool) repositoryGraphLinkCounts {
	counts := repositoryGraphLinkCounts{}
	type repositoryLinks struct {
		projects map[string]bool
		remotes  map[string]bool
	}
	byRepository := map[string]*repositoryLinks{}
	repositoryEntry := func(assetID string) *repositoryLinks {
		entry := byRepository[assetID]
		if entry == nil {
			entry = &repositoryLinks{projects: map[string]bool{}, remotes: map[string]bool{}}
			byRepository[assetID] = entry
		}
		return entry
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "owns":
			if strings.HasPrefix(from, "project:") && strings.HasPrefix(to, "repository:") {
				counts.ProjectRepository++
				repositoryEntry(to).projects[from] = true
			}
		case "has_remote":
			if strings.HasPrefix(from, "repository:") && strings.HasPrefix(to, "git_remote:") {
				counts.RepositoryRemotes++
				repositoryEntry(from).remotes[to] = true
			}
		}
	}
	for repositoryID, entry := range byRepository {
		if len(entry.projects) > 0 && len(entry.remotes) >= 2 {
			counts.CompleteRepos++
			if hasAnyKnownID(entry.projects, projectAssetIDs) && repositoryAssetIDs[repositoryID] && countMatchingAssets(entry.remotes, remoteAssetIDs) >= 2 {
				counts.CompleteRepoAssets++
			}
		}
	}
	return counts
}

func countMatchingAssets(ids, knownIDs map[string]bool) int {
	count := 0
	for id := range ids {
		if knownIDs[id] {
			count++
		}
	}
	return count
}

type repoSyncGraphLinkCounts struct {
	RepositorySync     int
	SourceRemotes      int
	TargetRemotes      int
	CompleteSyncs      int
	CompleteSyncAssets int
}

func countRepoSyncGraphLinks(graph map[string]any, repositoryAssetIDs, repoSyncAssetIDs, remoteAssetIDs map[string]bool) repoSyncGraphLinkCounts {
	counts := repoSyncGraphLinkCounts{}
	type syncLinks struct {
		repositories map[string]bool
		sources      map[string]bool
		targets      map[string]bool
	}
	bySync := map[string]*syncLinks{}
	syncEntry := func(assetID string) *syncLinks {
		entry := bySync[assetID]
		if entry == nil {
			entry = &syncLinks{repositories: map[string]bool{}, sources: map[string]bool{}, targets: map[string]bool{}}
			bySync[assetID] = entry
		}
		return entry
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "has_sync":
			if strings.HasPrefix(from, "repository:") && strings.HasPrefix(to, "repo_sync:") {
				counts.RepositorySync++
				syncEntry(to).repositories[from] = true
			}
		case "synced_from":
			if strings.HasPrefix(from, "repo_sync:") && strings.HasPrefix(to, "git_remote:") {
				counts.SourceRemotes++
				syncEntry(from).sources[to] = true
			}
		case "mirrors_to":
			if strings.HasPrefix(from, "repo_sync:") && strings.HasPrefix(to, "git_remote:") {
				counts.TargetRemotes++
				syncEntry(from).targets[to] = true
			}
		}
	}
	for syncID, entry := range bySync {
		if len(entry.repositories) > 0 && hasDistinctSourceTarget(entry.sources, entry.targets) {
			counts.CompleteSyncs++
			if repoSyncAssetIDs[syncID] && hasAnyKnownID(entry.repositories, repositoryAssetIDs) && hasDistinctKnownSourceTarget(entry.sources, entry.targets, remoteAssetIDs) {
				counts.CompleteSyncAssets++
			}
		}
	}
	return counts
}

func hasDistinctSourceTarget(sources, targets map[string]bool) bool {
	for source := range sources {
		for target := range targets {
			if source != target {
				return true
			}
		}
	}
	return false
}

func hasDistinctKnownSourceTarget(sources, targets, knownIDs map[string]bool) bool {
	for source := range sources {
		if !knownIDs[source] {
			continue
		}
		for target := range targets {
			if source != target && knownIDs[target] {
				return true
			}
		}
	}
	return false
}

type githubActionGraphLinkCounts struct {
	ProjectRepositories        int
	RepositoryRemotes          int
	RemoteActionRuns           int
	TaggedRemotes              int
	TagActionRunLinks          int
	CompleteActionRuns         int
	CompleteActionAssets       int
	CompleteTaggedRemotes      int
	CompleteTaggedRemoteAssets int
	LinkedTagRuns              int
	LinkedTagRunAssets         int
}

func countGitHubActionGraphLinks(graph map[string]any, projectAssetIDs, repositoryAssetIDs, remoteAssetIDs, actionAssetIDs, tagRunAssetIDs map[string]bool, tagRunAssetIDsByOperation map[string]map[string]bool, tagOperationIDs map[string]bool) githubActionGraphLinkCounts {
	counts := githubActionGraphLinkCounts{}
	repositoryProjects := map[string]map[string]bool{}
	remoteRepositories := map[string]map[string]bool{}
	remoteActionRuns := map[string]map[string]bool{}
	taggedRemoteOps := map[string]map[string]bool{}
	tagActionRuns := map[string]map[string]bool{}
	addRepositoryProject := func(repositoryID, projectID string) {
		if repositoryProjects[repositoryID] == nil {
			repositoryProjects[repositoryID] = map[string]bool{}
		}
		repositoryProjects[repositoryID][projectID] = true
	}
	addRemoteRepository := func(remoteID, repositoryID string) {
		if remoteRepositories[remoteID] == nil {
			remoteRepositories[remoteID] = map[string]bool{}
		}
		remoteRepositories[remoteID][repositoryID] = true
	}
	addRemoteActionRun := func(remoteID, actionID string) {
		if remoteActionRuns[remoteID] == nil {
			remoteActionRuns[remoteID] = map[string]bool{}
		}
		remoteActionRuns[remoteID][actionID] = true
	}
	addTaggedRemoteOp := func(remoteID, operationID string) {
		if taggedRemoteOps[remoteID] == nil {
			taggedRemoteOps[remoteID] = map[string]bool{}
		}
		taggedRemoteOps[remoteID][operationID] = true
	}
	addTagActionRun := func(tagRunID, actionID string) {
		if tagActionRuns[tagRunID] == nil {
			tagActionRuns[tagRunID] = map[string]bool{}
		}
		tagActionRuns[tagRunID][actionID] = true
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "owns":
			if strings.HasPrefix(from, "project:") && strings.HasPrefix(to, "repository:") {
				counts.ProjectRepositories++
				addRepositoryProject(to, from)
			}
		case "has_remote":
			if strings.HasPrefix(from, "repository:") && strings.HasPrefix(to, "git_remote:") {
				counts.RepositoryRemotes++
				addRemoteRepository(to, from)
			}
		case "triggered_by":
			if strings.HasPrefix(from, "git_remote:") && strings.HasPrefix(to, "github_action_run:") {
				counts.RemoteActionRuns++
				addRemoteActionRun(from, to)
			}
		case "tagged_remote":
			metadata := mapFromAPI(edge["metadata"])
			status := strings.ToLower(strings.TrimSpace(fmt.Sprint(metadata["status"])))
			if strings.HasPrefix(from, "operation_run:") && strings.HasPrefix(to, "git_remote:") && (status == "completed" || status == "succeeded" || status == "success") {
				counts.TaggedRemotes++
				addTaggedRemoteOp(to, from)
			}
		case "matched_action_run":
			if strings.HasPrefix(from, "repo_tag_run:") && strings.HasPrefix(to, "github_action_run:") {
				counts.TagActionRunLinks++
				addTagActionRun(from, to)
			}
		}
	}
	projectLinkedActionRuns := map[string]bool{}
	canonicalProjectLinkedActionRuns := map[string]bool{}
	canonicalTaggedTagRunAssets := map[string]bool{}
	for remoteID, actionRuns := range remoteActionRuns {
		hasProjectRepository := false
		for repositoryID := range remoteRepositories[remoteID] {
			if len(repositoryProjects[repositoryID]) > 0 {
				hasProjectRepository = true
				break
			}
		}
		if hasProjectRepository {
			counts.CompleteActionRuns += len(actionRuns)
			for actionID := range actionRuns {
				projectLinkedActionRuns[actionID] = true
				if hasCanonicalProjectRemote(remoteID, remoteRepositories, repositoryProjects, projectAssetIDs, repositoryAssetIDs) && remoteAssetIDs[remoteID] && actionAssetIDs[actionID] {
					counts.CompleteActionAssets++
					canonicalProjectLinkedActionRuns[actionID] = true
				}
			}
		}
	}
	for remoteID, operations := range taggedRemoteOps {
		hasProjectRepository := false
		for repositoryID := range remoteRepositories[remoteID] {
			if len(repositoryProjects[repositoryID]) > 0 {
				hasProjectRepository = true
				break
			}
		}
		if hasProjectRepository {
			counts.CompleteTaggedRemotes += len(operations)
			if hasCanonicalProjectRemote(remoteID, remoteRepositories, repositoryProjects, projectAssetIDs, repositoryAssetIDs) && remoteAssetIDs[remoteID] {
				for operationID := range operations {
					if tagOperationIDs[operationID] && hasAnyKnownID(tagRunAssetIDsByOperation[operationID], tagRunAssetIDs) {
						counts.CompleteTaggedRemoteAssets++
						for tagRunID := range tagRunAssetIDsByOperation[operationID] {
							if tagRunAssetIDs[tagRunID] {
								canonicalTaggedTagRunAssets[tagRunID] = true
							}
						}
					}
				}
			}
		}
	}
	for tagRunID, actionRuns := range tagActionRuns {
		linked := false
		linkedAsset := false
		for actionID := range actionRuns {
			if projectLinkedActionRuns[actionID] {
				linked = true
				if canonicalTaggedTagRunAssets[tagRunID] && actionAssetIDs[actionID] && canonicalProjectLinkedActionRuns[actionID] {
					linkedAsset = true
				}
			}
		}
		if linked {
			counts.LinkedTagRuns++
		}
		if linkedAsset {
			counts.LinkedTagRunAssets++
		}
	}
	return counts
}

func hasCanonicalProjectRemote(remoteID string, remoteRepositories, repositoryProjects map[string]map[string]bool, projectAssetIDs, repositoryAssetIDs map[string]bool) bool {
	for repositoryID := range remoteRepositories[remoteID] {
		if !repositoryAssetIDs[repositoryID] {
			continue
		}
		if hasAnyKnownID(repositoryProjects[repositoryID], projectAssetIDs) {
			return true
		}
	}
	return false
}

type webhookSyncGraphLinkCounts struct {
	ConnectionEvents    int
	EventRepoSyncs      int
	EventOperations     int
	CompleteChains      int
	CompleteChainAssets int
}

func countWebhookSyncGraphLinks(graph map[string]any, connectionAssetIDs, eventAssetIDs, repoSyncAssetIDs, syncOperationIDs map[string]bool) webhookSyncGraphLinkCounts {
	counts := webhookSyncGraphLinkCounts{}
	type eventLinks struct {
		connections map[string]bool
		repoSyncs   map[string]bool
		operations  map[string]bool
	}
	operationRepoSyncs := map[string]map[string]bool{}
	byEvent := map[string]*eventLinks{}
	eventEntry := func(assetID string) *eventLinks {
		entry := byEvent[assetID]
		if entry == nil {
			entry = &eventLinks{connections: map[string]bool{}, repoSyncs: map[string]bool{}, operations: map[string]bool{}}
			byEvent[assetID] = entry
		}
		return entry
	}
	addOperationRepoSync := func(operationID, repoSyncID string) {
		if operationRepoSyncs[operationID] == nil {
			operationRepoSyncs[operationID] = map[string]bool{}
		}
		operationRepoSyncs[operationID][repoSyncID] = true
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "received_webhook_event":
			if strings.HasPrefix(from, "webhook_connection:") && strings.HasPrefix(to, "webhook_event:") {
				counts.ConnectionEvents++
				eventEntry(to).connections[from] = true
			}
		case "matched_repo_sync":
			if strings.HasPrefix(from, "webhook_event:") && strings.HasPrefix(to, "repo_sync:") {
				counts.EventRepoSyncs++
				eventEntry(from).repoSyncs[to] = true
			}
		case "triggered_operation":
			// Ignore legacy webhook_connection -> operation_run compatibility edges.
			if strings.HasPrefix(from, "webhook_event:") && strings.HasPrefix(to, "operation_run:") {
				counts.EventOperations++
				eventEntry(from).operations[to] = true
			}
		case "ran_repo_sync":
			if strings.HasPrefix(from, "operation_run:") && strings.HasPrefix(to, "repo_sync:") {
				addOperationRepoSync(from, to)
			}
		}
	}
	for eventID, entry := range byEvent {
		if len(entry.connections) > 0 && hasOperationRepoSyncChain(entry.repoSyncs, entry.operations, operationRepoSyncs) {
			counts.CompleteChains++
			if eventAssetIDs[eventID] &&
				hasKnownWebhookConnection(entry.connections, connectionAssetIDs) &&
				hasCanonicalOperationRepoSyncChain(entry.repoSyncs, entry.operations, operationRepoSyncs, repoSyncAssetIDs, syncOperationIDs) {
				counts.CompleteChainAssets++
			}
		}
	}
	return counts
}

func hasKnownWebhookConnection(connections, knownIDs map[string]bool) bool {
	for connectionID := range connections {
		if knownIDs[connectionID] {
			return true
		}
	}
	return false
}

func hasOperationRepoSyncChain(repoSyncs, operations map[string]bool, operationRepoSyncs map[string]map[string]bool) bool {
	for operationID := range operations {
		for repoSyncID := range repoSyncs {
			if operationRepoSyncs[operationID][repoSyncID] {
				return true
			}
		}
	}
	return false
}

func hasCanonicalOperationRepoSyncChain(repoSyncs, operations map[string]bool, operationRepoSyncs map[string]map[string]bool, repoSyncAssetIDs, syncOperationIDs map[string]bool) bool {
	for operationID := range operations {
		if !syncOperationIDs[operationID] {
			continue
		}
		for repoSyncID := range repoSyncs {
			if repoSyncAssetIDs[repoSyncID] && operationRepoSyncs[operationID][repoSyncID] {
				return true
			}
		}
	}
	return false
}

type sshGraphLinkCounts struct {
	OperationCommands           int
	CommandMachines             int
	CompleteCommands            int
	CompleteCommandAssets       int
	CompleteVerifyCommandAssets int
	CompleteRunCommandAssets    int
}

func countSSHGraphLinks(graph map[string]any, commandAssetIDs, machineAssetIDs, operationIDs, verifyOperationIDs, runOperationIDs map[string]bool) sshGraphLinkCounts {
	counts := sshGraphLinkCounts{}
	type commandLinks struct {
		operations map[string]bool
		machines   map[string]bool
	}
	byCommand := map[string]*commandLinks{}
	commandEntry := func(assetID string) *commandLinks {
		entry := byCommand[assetID]
		if entry == nil {
			entry = &commandLinks{operations: map[string]bool{}, machines: map[string]bool{}}
			byCommand[assetID] = entry
		}
		return entry
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "ran_ssh_command":
			if strings.HasPrefix(from, "operation_run:") && strings.HasPrefix(to, "ssh_command_run:") {
				counts.OperationCommands++
				commandEntry(to).operations[from] = true
			}
		case "executed_on":
			if strings.HasPrefix(from, "ssh_command_run:") && strings.HasPrefix(to, "ssh_machine:") {
				counts.CommandMachines++
				commandEntry(from).machines[to] = true
			}
		}
	}
	for commandID, entry := range byCommand {
		if len(entry.operations) > 0 && len(entry.machines) > 0 {
			counts.CompleteCommands++
			if commandAssetIDs[commandID] && hasAnyKnownID(entry.operations, operationIDs) && hasAnyKnownID(entry.machines, machineAssetIDs) {
				counts.CompleteCommandAssets++
				if hasAnyKnownID(entry.operations, verifyOperationIDs) {
					counts.CompleteVerifyCommandAssets++
				}
				if hasAnyKnownID(entry.operations, runOperationIDs) {
					counts.CompleteRunCommandAssets++
				}
			}
		}
	}
	return counts
}

type argoGraphLinkCounts struct {
	ConnectionApps    int
	AppTargets        int
	CompleteApps      int
	CompleteAppAssets int
}

func countArgoGraphLinks(graph map[string]any, connectionAssetIDs, appAssetIDs, targetAssetIDs, syncOperationIDs map[string]bool) argoGraphLinkCounts {
	counts := argoGraphLinkCounts{}
	type appLinks struct {
		connections map[string]bool
		targets     map[string]bool
	}
	syncedConnections := map[string]map[string]bool{}
	byApp := map[string]*appLinks{}
	appEntry := func(assetID string) *appLinks {
		entry := byApp[assetID]
		if entry == nil {
			entry = &appLinks{connections: map[string]bool{}, targets: map[string]bool{}}
			byApp[assetID] = entry
		}
		return entry
	}
	addSyncedConnection := func(connectionID, operationID string) {
		if syncedConnections[connectionID] == nil {
			syncedConnections[connectionID] = map[string]bool{}
		}
		syncedConnections[connectionID][operationID] = true
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "manages":
			if strings.HasPrefix(from, "argo_connection:") && strings.HasPrefix(to, "argo_app:") {
				counts.ConnectionApps++
				appEntry(to).connections[from] = true
			}
		case "deployed_to":
			if strings.HasPrefix(from, "argo_app:") && strings.HasPrefix(to, "deployment_target:") {
				counts.AppTargets++
				appEntry(from).targets[to] = true
			}
		case "synced_argo_connection":
			if strings.HasPrefix(from, "operation_run:") && strings.HasPrefix(to, "argo_connection:") {
				addSyncedConnection(to, from)
			}
		}
	}
	for appID, entry := range byApp {
		if len(entry.targets) > 0 && hasSyncedConnection(entry.connections, syncedConnections) {
			counts.CompleteApps++
			if hasCanonicalArgoAppChain(appID, entry.connections, entry.targets, syncedConnections, connectionAssetIDs, appAssetIDs, targetAssetIDs, syncOperationIDs) {
				counts.CompleteAppAssets++
			}
		}
	}
	return counts
}

func hasSyncedConnection(connections map[string]bool, syncedConnections map[string]map[string]bool) bool {
	for connectionID := range connections {
		if len(syncedConnections[connectionID]) > 0 {
			return true
		}
	}
	return false
}

func hasCanonicalArgoAppChain(appID string, connections, targets map[string]bool, syncedConnections map[string]map[string]bool, connectionAssetIDs, appAssetIDs, targetAssetIDs, syncOperationIDs map[string]bool) bool {
	if !appAssetIDs[appID] {
		return false
	}
	for connectionID := range connections {
		if !connectionAssetIDs[connectionID] {
			continue
		}
		if !hasCanonicalSyncedOperation(syncedConnections[connectionID], syncOperationIDs) {
			continue
		}
		for targetID := range targets {
			if targetAssetIDs[targetID] {
				return true
			}
		}
	}
	return false
}

func hasCanonicalSyncedOperation(operationIDs, syncOperationIDs map[string]bool) bool {
	for operationID := range operationIDs {
		if syncOperationIDs[operationID] {
			return true
		}
	}
	return false
}

type approvalGraphLinkCounts struct {
	RuleApprovals               int
	ApprovalOperations          int
	CompleteApprovalChains      int
	CompleteApprovalAssetChains int
}

func countApprovalGraphLinks(graph map[string]any, activeRuleIDs, approvalAssetIDs, operationAssetIDs, pendingOperationIDs map[string]bool) approvalGraphLinkCounts {
	counts := approvalGraphLinkCounts{}
	type approvalLinks struct {
		rules      map[string]bool
		operations map[string]bool
	}
	byApproval := map[string]*approvalLinks{}
	approvalEntry := func(assetID string) *approvalLinks {
		entry := byApproval[assetID]
		if entry == nil {
			entry = &approvalLinks{rules: map[string]bool{}, operations: map[string]bool{}}
			byApproval[assetID] = entry
		}
		return entry
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "governs":
			if activeRuleIDs[from] && approvalAssetIDs[to] {
				counts.RuleApprovals++
				approvalEntry(to).rules[from] = true
			}
		case "gates_operation":
			if strings.HasPrefix(from, "operation_approval:") && strings.HasPrefix(to, "operation_run:") {
				counts.ApprovalOperations++
				approvalEntry(from).operations[to] = true
			}
		}
	}
	for approvalID, entry := range byApproval {
		if len(entry.rules) > 0 && len(entry.operations) > 0 {
			counts.CompleteApprovalChains++
			// operation_run asset_inventory.source_id is emitted from operations.id,
			// matching the operation_run:<id> graph edges used for pending operations.
			if approvalAssetIDs[approvalID] && hasAnyIDInBoth(entry.operations, operationAssetIDs, pendingOperationIDs) {
				counts.CompleteApprovalAssetChains++
			}
		}
	}
	return counts
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
