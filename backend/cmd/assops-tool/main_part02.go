package main

import (
	"assops/backend/internal/app"
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

func runDBCommand(cfg app.Config, args []string) error {
	if len(args) == 0 {
		return usage()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	switch args[0] {
	case "automigrate":
		store, err := app.OpenStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.AutoMigrate(ctx); err != nil {
			return err
		}
		if err := store.SeedAdmin(ctx, cfg); err != nil {
			return err
		}
		fmt.Println("schema automigrated")
		return nil
	case "seed-demo":
		store, err := app.OpenStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.AutoMigrate(ctx); err != nil {
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
		if err := store.AutoMigrate(ctx); err != nil {
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
		if err := store.AutoMigrate(ctx); err != nil {
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
		if err := store.AutoMigrate(ctx); err != nil {
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
		if err := store.AutoMigrate(ctx); err != nil {
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
	if err := store.AutoMigrate(ctx); err != nil {
		return err
	}
	report := map[string]any{
		"backup":               backupPath,
		"target_database":      redactedDatabaseURL(targetDatabaseURL),
		"inspect_line_count":   countNonEmptyLines(inspectOutput),
		"backup_object_counts": pgRestoreListObjectCounts(inspectOutput),
		"restore_output_lines": countNonEmptyLines(restoreOutput),
		"schema_update":        "gorm_auto_migrate",
		"rehearsed_at":         time.Now().UTC().Format(time.RFC3339),
	}
	if reportPath != "" {
		if err := writeJSONReport(reportPath, report); err != nil {
			return err
		}
	}
	return printJSON(report)
}
