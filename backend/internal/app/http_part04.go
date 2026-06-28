package app

import (
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
	"time"
)

func projectTemplatePreview(template map[string]any, projectName, projectSlug, description string, parameters map[string]any) map[string]any {
	if parameters == nil {
		parameters = map[string]any{}
	}
	defaults := mapFromAny(template["defaults"])
	repoDefaults := mapFromAny(defaults["repository"])
	repoParams := mapFromAny(parameters["repository"])
	syncDefaults := mapFromAny(defaults["repo_sync"])
	syncParams := mapFromAny(parameters["repo_sync"])
	remotes := previewTemplateRemotes(defaults, parameters)
	repository := map[string]any{
		"name":           firstNonEmptyString(stringFromMap(repoParams, "name"), templateNameWithSuffix(projectSlug, stringFromMap(repoDefaults, "name_suffix"), "service")),
		"repo_key":       firstNonEmptyString(stringFromMap(repoParams, "repo_key"), templateNameWithSuffix(projectSlug, stringFromMap(repoDefaults, "repo_key_suffix"), "service")),
		"display_name":   firstNonEmptyString(stringFromMap(repoParams, "display_name"), templateDisplayName(projectName, stringFromMap(repoDefaults, "display_name_suffix"), "Service")),
		"repo_role":      firstNonEmptyString(stringFromMap(repoParams, "repo_role"), stringFromMap(defaults, "repo_role"), "code"),
		"default_branch": firstNonEmptyString(stringFromMap(repoParams, "default_branch"), stringFromMap(defaults, "default_branch"), "main"),
		"status":         "planned",
	}
	sourceRemoteKey := firstNonEmptyString(stringFromMap(syncParams, "source_remote_key"), stringFromMap(syncDefaults, "source_remote_key"))
	targetRemoteKey := firstNonEmptyString(stringFromMap(syncParams, "target_remote_key"), stringFromMap(syncDefaults, "target_remote_key"))
	sourceRemoteID := firstNonEmptyString(stringFromMap(syncParams, "source_remote_id"), previewRemoteIDByKey(remotes, sourceRemoteKey))
	targetRemoteID := firstNonEmptyString(stringFromMap(syncParams, "target_remote_id"), previewRemoteIDByKey(remotes, targetRemoteKey))
	repoSyncStatus := "planned"
	repoSyncReason := "source_remote_id and target_remote_id are required after remotes are attached"
	if sourceRemoteID != "" && targetRemoteID != "" {
		if sourceRemoteID == targetRemoteID {
			repoSyncReason = "source_remote_id and target_remote_id must be different"
		} else {
			repoSyncStatus = "ready_for_remote_validation"
			repoSyncReason = "remote ownership will be validated when the template operation completes"
		}
	}
	repoSync := map[string]any{
		"name":              firstNonEmptyString(stringFromMap(syncParams, "name"), stringFromMap(syncDefaults, "name"), "default mirror"),
		"trigger_mode":      firstNonEmptyString(stringFromMap(syncParams, "trigger_mode"), stringFromMap(syncDefaults, "trigger_mode"), "manual"),
		"sync_mode":         firstNonEmptyString(stringFromMap(syncParams, "sync_mode"), stringFromMap(syncDefaults, "sync_mode"), "selected_refs"),
		"transport":         firstNonEmptyString(stringFromMap(syncParams, "transport"), stringFromMap(syncDefaults, "transport"), "ssh"),
		"driver":            firstNonEmptyString(stringFromMap(syncParams, "driver"), stringFromMap(syncDefaults, "driver"), "projectops_worker_git_ssh"),
		"enabled":           syncParams["enabled"],
		"source_remote_id":  sourceRemoteID,
		"target_remote_id":  targetRemoteID,
		"source_remote_key": sourceRemoteKey,
		"target_remote_key": targetRemoteKey,
		"status":            repoSyncStatus,
		"reason":            repoSyncReason,
	}
	if repoSync["enabled"] == nil {
		if enabled, ok := syncDefaults["enabled"].(bool); ok {
			repoSync["enabled"] = enabled
		} else {
			repoSync["enabled"] = false
		}
	}
	return map[string]any{
		"template": map[string]any{
			"id":      template["id"],
			"slug":    template["slug"],
			"name":    template["name"],
			"version": template["version"],
			"status":  template["status"],
		},
		"project": map[string]any{
			"name":        projectName,
			"slug":        projectSlug,
			"description": description,
		},
		"repository": repository,
		"remotes":    remotes,
		"repo_sync":  repoSync,
		"files":      previewTemplateFiles(template, projectName, projectSlug, repository, defaults, parameters),
		"steps":      templateRunSteps(template["steps"]),
		"defaults":   defaults,
		"parameters": parameters,
	}
}

func previewTemplateFiles(template map[string]any, projectName, projectSlug string, repository, defaults, parameters map[string]any) []map[string]any {
	items := templateFileItems(defaults, parameters)
	run := map[string]any{"template_slug": template["slug"]}
	project := map[string]any{"name": projectName, "slug": projectSlug}
	files := make([]map[string]any, 0, len(items))
	for _, item := range items {
		path := safeTemplateFilePath(stringFromMap(item, "path"))
		if path == "" {
			continue
		}
		files = append(files, map[string]any{
			"path":    path,
			"kind":    firstNonEmptyString(stringFromMap(item, "kind"), "text"),
			"content": renderTemplateFileContent(stringFromMap(item, "content"), run, project, repository),
			"status":  "planned",
		})
	}
	return files
}

func previewTemplateRemotes(defaults, parameters map[string]any) []map[string]any {
	items := templateRemoteItems(defaults, parameters)
	remotes := make([]map[string]any, 0, len(items))
	for _, item := range items {
		remoteKey := firstNonEmptyString(stringFromMap(item, "remote_key"), stringFromMap(item, "name"))
		name := firstNonEmptyString(stringFromMap(item, "name"), remoteKey)
		kind := firstNonEmptyString(stringFromMap(item, "kind"), stringFromMap(item, "provider_type"), "git")
		urls := stringSliceFromAny(item["urls"])
		remoteURL := stringFromMap(item, "remote_url")
		if remoteURL == "" && len(urls) > 0 {
			remoteURL = urls[0]
		}
		remotes = append(remotes, map[string]any{
			"id":                    "remote_key:" + remoteKey,
			"name":                  name,
			"remote_key":            remoteKey,
			"kind":                  kind,
			"provider_type":         firstNonEmptyString(stringFromMap(item, "provider_type"), kind),
			"provider_account_id":   stringFromMap(item, "provider_account_id"),
			"provider_account_name": stringFromMap(item, "provider_account_name"),
			"remote_url":            remoteURL,
			"web_url":               stringFromMap(item, "web_url"),
			"remote_role":           firstNonEmptyString(stringFromMap(item, "remote_role"), stringFromMap(item, "role"), "mirror"),
			"default_branch":        firstNonEmptyString(stringFromMap(item, "default_branch"), "main"),
			"sync_enabled":          boolDefaultFromMap(item, "sync_enabled", true),
			"is_primary":            boolFromMap(item, "is_primary"),
			"protected":             boolFromMap(item, "protected"),
			"status":                "planned",
		})
	}
	return remotes
}

func previewRemoteIDByKey(remotes []map[string]any, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	for _, remote := range remotes {
		if stringFromMap(remote, "remote_key") == key || stringFromMap(remote, "name") == key {
			return stringFromMap(remote, "id")
		}
	}
	return ""
}

func (s *Server) listProjectTemplateRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template_run"}, "read") {
		return
	}
	user := currentUser(r)
	var runs []GormProjectTemplateRun
	if err := s.store.Gorm.WithContext(r.Context()).Order(gormOrderDesc("created_at")).Limit(100).Find(&runs).Error; err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	templateNames, err := projectTemplateNamesByID(r.Context(), s.store.Gorm, runs)
	if err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	operationStatuses, err := operationStatusesByID(r.Context(), s.store.Gorm, runs)
	if err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	memberProjects := map[string]bool{}
	if !userCanReadAllProjects(user) && user != nil {
		var memberships []GormProjectMember
		if err := s.store.Gorm.WithContext(r.Context()).Where(&GormProjectMember{UserID: user.ID}).Find(&memberships).Error; err != nil {
			writeQueryResult(w, nil, err)
			return
		}
		for _, membership := range memberships {
			memberProjects[membership.ProjectID] = true
		}
	}
	items := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		if !userCanReadAllProjects(user) {
			userID := ""
			if user != nil {
				userID = user.ID
			}
			if cleanOptionalID(run.RequestedBy.String) != userID && !memberProjects[cleanOptionalID(run.ProjectID.String)] {
				continue
			}
		}
		item := projectTemplateRunMap(run)
		item["template_name"] = templateNames[cleanOptionalID(run.ProjectTemplateID.String)]
		item["operation_status"] = operationStatuses[cleanOptionalID(run.OperationRunID.String)]
		items = append(items, item)
	}
	writeQueryResult(w, items, nil)
}

func (s *Server) retryProjectTemplateProvision(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template"}, "create") {
		return
	}
	user := currentUser(r)
	var op map[string]any
	var run map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var runModel GormProjectTemplateRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&runModel, &GormProjectTemplateRun{GormBase: GormBase{ID: chi.URLParam(r, "id")}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		allowed, err := userCanAccessProjectTemplateRun(r.Context(), tx, user, runModel)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrNotFound
		}
		run = projectTemplateRunMap(runModel)
		if runModel.ProjectTemplateID.Valid {
			var template GormProjectTemplate
			if err := tx.First(&template, &GormProjectTemplate{GormBase: GormBase{ID: runModel.ProjectTemplateID.String}}).Error; err == nil {
				run["template_name"] = template.Name
			} else if !errorsIsRecordNotFound(err) {
				return err
			}
		}
		if !canRetryTemplateProvision(run) {
			return errProjectTemplateRunNotRetryable
		}
		input := map[string]any{
			"project_template_run_id":   run["id"],
			"previous_operation_run_id": cleanOptionalID(fmt.Sprint(run["operation_run_id"])),
		}
		var opErr error
		op, opErr = enqueueOperationGorm(
			r.Context(),
			tx,
			cleanOptionalID(fmt.Sprint(run["project_id"])),
			"",
			"project.template_provision_retry",
			"retry template provisioning "+fmt.Sprint(run["project_name"]),
			input,
			[]string{"template"},
			"control-worker",
		)
		if opErr != nil {
			return opErr
		}
		result := mapFromAny(runModel.Result.Data)
		result["retry_requested"] = map[string]any{
			"operation_run_id": op["id"],
			"requested_by":     userIDOrNil(user),
			"requested_at":     time.Now().UTC().Format(time.RFC3339),
		}
		if err := tx.Model(&GormProjectTemplateRun{}).
			Where(&GormProjectTemplateRun{GormBase: GormBase{ID: runModel.ID}}).
			Updates(map[string]any{"status": "queued", "result": JSONValue{Data: result}}).Error; err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		if errors.Is(err, errProjectTemplateRunNotRetryable) {
			writeError(w, http.StatusConflict, "template run is not eligible for repository provisioning retry")
			return
		}
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"operation": op, "run": run})
}
