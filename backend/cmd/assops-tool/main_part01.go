package main

import (
	"assops/backend/internal/app"
	"flag"
	"fmt"
	"os"
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
		if (len(args) == 3 || len(args) == 4) && args[1] == "helm-readiness-plan" {
			plan, err := releaseHelmReadinessPlan(args[2])
			if err != nil {
				return err
			}
			if len(args) == 4 {
				return writeTextFile(args[3], plan)
			}
			fmt.Print(plan)
			return nil
		}
		if (len(args) == 3 || len(args) == 4) && args[1] == "helm-test-readiness-plan" {
			plan, err := releaseHelmTestReadinessPlan(args[2])
			if err != nil {
				return err
			}
			if len(args) == 4 {
				return writeTextFile(args[3], plan)
			}
			fmt.Print(plan)
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
		if (len(args) == 3 || len(args) == 4) && args[1] == "callback-rehearsal-plan" {
			plan, err := releaseCallbackRehearsalPlan(args[2])
			if err != nil {
				return err
			}
			if len(args) == 4 {
				return writeTextFile(args[3], plan)
			}
			fmt.Print(plan)
			return nil
		}
		if (len(args) == 4 || len(args) == 5) && args[1] == "demo-import-plan" {
			plan, err := releaseDemoImportPlan(args[2], args[3])
			if err != nil {
				return err
			}
			if len(args) == 5 {
				return writeTextFile(args[4], plan)
			}
			fmt.Print(plan)
			return nil
		}
		if (len(args) == 6 || len(args) == 7) && args[1] == "pod-log-rehearsal-plan" {
			plan, err := releasePodLogRehearsalPlan(args[2], args[3], args[4], args[5])
			if err != nil {
				return err
			}
			if len(args) == 7 {
				return writeTextFile(args[6], plan)
			}
			fmt.Print(plan)
			return nil
		}
		if (len(args) == 4 || len(args) == 5) && args[1] == "ssh-rehearsal-plan" {
			plan, err := releaseSSHRehearsalPlan(args[2], args[3])
			if err != nil {
				return err
			}
			if len(args) == 5 {
				return writeTextFile(args[4], plan)
			}
			fmt.Print(plan)
			return nil
		}
		if (len(args) == 4 || len(args) == 5) && args[1] == "tag-rehearsal-plan" {
			plan, err := releaseTagRehearsalPlan(args[2], args[3])
			if err != nil {
				return err
			}
			if len(args) == 5 {
				return writeTextFile(args[4], plan)
			}
			fmt.Print(plan)
			return nil
		}
		if (len(args) == 4 || len(args) == 5) && args[1] == "config-rehearsal-plan" {
			plan, err := releaseConfigRehearsalPlan(args[2], args[3])
			if err != nil {
				return err
			}
			if len(args) == 5 {
				return writeTextFile(args[4], plan)
			}
			fmt.Print(plan)
			return nil
		}
		if (len(args) == 4 || len(args) == 5) && args[1] == "agent-code-rehearsal-plan" {
			plan, err := releaseAgentCodeRehearsalPlan(args[2], args[3])
			if err != nil {
				return err
			}
			if len(args) == 5 {
				return writeTextFile(args[4], plan)
			}
			fmt.Print(plan)
			return nil
		}
		if (len(args) == 4 || len(args) == 5) && args[1] == "agent-tool-rehearsal-plan" {
			plan, err := releaseAgentToolRehearsalPlan(args[2], args[3])
			if err != nil {
				return err
			}
			if len(args) == 5 {
				return writeTextFile(args[4], plan)
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
	fmt.Fprintln(os.Stderr, "usage: assops-tool [--api URL] [--token TOKEN] <db automigrate|db seed-demo|db sync-assets|db record-demo-readiness-snapshot|db record-version-validation-snapshot|db pin-config-commit|db backup FILE|db backup-retain DIR KEEP|db inspect-backup FILE|db restore FILE|db rehearse-restore FILE TARGET_DATABASE_URL [REPORT_FILE]|project brief|project readiness|repo remotes|remote actions|operations recent|plan validate|release validate-bundle ARTIFACT_DIR REHEARSAL_REPORT|release helm-values GHCR_OWNER VERSION [OUTPUT_FILE]|release helm-readiness-plan VALUES_FILE [OUTPUT_FILE]|release helm-test-readiness-plan VALUES_FILE [OUTPUT_FILE]|release promotion-plan OWNER/REPO GHCR_OWNER VERSION ARTIFACT_DIR REHEARSAL_REPORT HELM_VALUES [OUTPUT_FILE]|release backup-schedule-plan OWNER/REPO ENV RUNNER CRON BACKUP_SOURCE RETENTION_DAYS [OUTPUT_FILE]|release callback-rehearsal-plan PUBLIC_ORIGIN [OUTPUT_FILE]|release demo-import-plan PROJECT_SLUG PUBLIC_ORIGIN [OUTPUT_FILE]|release pod-log-rehearsal-plan PROJECT_SLUG PUBLIC_ORIGIN ENVIRONMENT NAMESPACE [OUTPUT_FILE]|release ssh-rehearsal-plan PROJECT_SLUG ENVIRONMENT [OUTPUT_FILE]|release tag-rehearsal-plan PROJECT_SLUG REMOTE_KEY [OUTPUT_FILE]|release config-rehearsal-plan PROJECT_SLUG REMOTE_KEY [OUTPUT_FILE]|release agent-code-rehearsal-plan PROJECT_SLUG RUNTIME_KEY [OUTPUT_FILE]|release agent-tool-rehearsal-plan PROJECT_SLUG RUNTIME_KEY [OUTPUT_FILE]|release branch-protection-plan OWNER/REPO RULESET_JSON CODEOWNERS [OUTPUT_FILE]>")
	return fmt.Errorf("unknown command")
}
