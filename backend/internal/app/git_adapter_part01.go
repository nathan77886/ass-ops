package app

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"os"
	"os/exec"
)

type commandRunner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, string, error)
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=ASSOPS",
		"GIT_AUTHOR_EMAIL=assops@local",
		"GIT_COMMITTER_NAME=ASSOPS",
		"GIT_COMMITTER_EMAIL=assops@local",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

type GitExecutor struct {
	Runner            commandRunner
	HTTPClient        *http.Client
	WorkDir           string
	LocalBareBaseDirs []string
}

type gitExecutionResult struct {
	Stdout   string
	Stderr   string
	AfterSHA string
	Details  map[string]any
}

const (
	providerDiagnosticErrorLimit = 240
	providerRunErrorLimit        = 512
)

type gitRefs struct {
	Branches []string
	Tags     []string
}

func NewGitExecutor(workDir string) *GitExecutor {
	return &GitExecutor{Runner: execCommandRunner{}, WorkDir: workDir}
}

func operationRunByID(ctx context.Context, db *gorm.DB, id string) (GormOperationRun, error) {
	if db == nil {
		return GormOperationRun{}, fmt.Errorf("database is not configured")
	}
	var op GormOperationRun
	if err := db.WithContext(ctx).Where(map[string]any{"id": id}).First(&op).Error; err != nil {
		return GormOperationRun{}, err
	}
	return op, nil
}

func gitRemoteMapByID(ctx context.Context, db *gorm.DB, id string) (map[string]any, error) {
	var remote GormGitRemote
	if err := db.WithContext(ctx).Where(map[string]any{"id": id}).First(&remote).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return gitRemoteMap(remote, nil, ""), nil
}

func projectGitRepositoryMapByID(ctx context.Context, db *gorm.DB, id string) (map[string]any, error) {
	var repo GormProjectGitRepository
	if err := db.WithContext(ctx).Where(map[string]any{"id": id}).First(&repo).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return gitRepositoryMap(repo), nil
}

func remoteForRepositoryGorm(ctx context.Context, db *gorm.DB, repoID, remoteID string) (map[string]any, error) {
	var remote GormGitRemote
	if err := db.WithContext(ctx).
		Where(&GormGitRemote{ProjectGitRepositoryID: repoID}).
		Where(map[string]any{"id": remoteID}).
		First(&remote).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return gitRemoteMap(remote, nil, ""), nil
}

func repoSyncRunMapForOperation(ctx context.Context, db *gorm.DB, opID string) (map[string]any, error) {
	op, err := operationRunByID(ctx, db, opID)
	if err != nil {
		return nil, err
	}
	var run GormRepoSyncRun
	if err := db.WithContext(ctx).Where(&GormRepoSyncRun{OperationRunID: opID}).First(&run).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	item := map[string]any{
		"id":                        run.ID,
		"operation_run_id":          run.OperationRunID,
		"git_remote_id":             run.GitRemoteID,
		"project_id":                nullableStringValue(run.ProjectID),
		"project_git_repository_id": nullableStringValue(run.ProjectGitRepositoryID),
		"repo_sync_asset_id":        nullableStringValue(run.RepoSyncAssetID),
		"source_remote_id":          nullableStringValue(run.SourceRemoteID),
		"target_remote_id":          nullableStringValue(run.TargetRemoteID),
		"ref":                       run.Ref,
		"before_sha":                run.BeforeSHA,
		"after_sha":                 run.AfterSHA,
		"actor_user_id":             nullableStringValue(run.ActorUserID),
		"status":                    run.Status,
		"stdout":                    run.Stdout,
		"stderr":                    run.Stderr,
		"error_message":             run.ErrorMessage,
		"started_at":                nullableTimeAny(run.StartedAt),
		"finished_at":               nullableTimeAny(run.FinishedAt),
		"created_at":                run.CreatedAt,
		"input":                     mapFromAny(op.Input.Data),
	}
	return item, nil
}

func repoTagRunMapForOperation(ctx context.Context, db *gorm.DB, opID string) (map[string]any, error) {
	op, err := operationRunByID(ctx, db, opID)
	if err != nil {
		return nil, err
	}
	var run GormRepoTagRun
	if err := db.WithContext(ctx).Where(&GormRepoTagRun{OperationRunID: opID}).First(&run).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	item := repoTagRunMap(run)
	item["input"] = mapFromAny(op.Input.Data)
	if run.TargetRemoteID.Valid {
		if remote, err := gitRemoteMapByID(ctx, db, run.TargetRemoteID.String); err == nil {
			item["default_branch"] = remote["default_branch"]
		}
	}
	return item, nil
}

func repoTagRunMapByID(ctx context.Context, db *gorm.DB, id string) (map[string]any, error) {
	var run GormRepoTagRun
	if err := db.WithContext(ctx).Where(map[string]any{"id": id}).First(&run).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return repoTagRunMap(run), nil
}

func repoTagRunMap(run GormRepoTagRun) map[string]any {
	return map[string]any{
		"id":                        run.ID,
		"operation_run_id":          run.OperationRunID,
		"git_remote_id":             run.GitRemoteID,
		"project_id":                nullableStringValue(run.ProjectID),
		"project_git_repository_id": nullableStringValue(run.ProjectGitRepositoryID),
		"target_remote_id":          nullableStringValue(run.TargetRemoteID),
		"tag_name":                  run.TagName,
		"target_sha":                run.TargetSHA,
		"tag_message":               run.TagMessage,
		"actor_user_id":             nullableStringValue(run.ActorUserID),
		"status":                    run.Status,
		"stdout":                    run.Stdout,
		"stderr":                    run.Stderr,
		"error_message":             run.ErrorMessage,
		"started_at":                nullableTimeAny(run.StartedAt),
		"finished_at":               nullableTimeAny(run.FinishedAt),
		"created_at":                run.CreatedAt,
	}
}

func nullableTimeAny(value sql.NullTime) any {
	if value.Valid {
		return value.Time
	}
	return nil
}
