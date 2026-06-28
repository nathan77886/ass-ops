package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"unicode/utf8"
)

func defaultTargetRemoteIDsGorm(ctx context.Context, db *gorm.DB, repoID, sourceRemoteID string) ([]string, error) {
	var remotes []GormGitRemote
	if err := db.WithContext(ctx).
		Where(&GormGitRemote{ProjectGitRepositoryID: repoID, SyncEnabled: true}).
		Order("is_primary DESC").
		Order("name ASC").
		Find(&remotes).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(remotes))
	for _, remote := range remotes {
		if remote.ID == sourceRemoteID {
			continue
		}
		ids = append(ids, remote.ID)
	}
	return ids, nil
}

func defaultGitHubRemoteIDsGorm(ctx context.Context, db *gorm.DB, repoID string) ([]string, error) {
	var remotes []GormGitRemote
	if err := db.WithContext(ctx).
		Where(&GormGitRemote{ProjectGitRepositoryID: repoID}).
		Order("is_primary DESC").
		Order("name ASC").
		Find(&remotes).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(remotes))
	for _, remote := range remotes {
		if remote.ProviderType != "github" && remote.Kind != "github" {
			continue
		}
		ids = append(ids, remote.ID)
	}
	return ids, nil
}

func refsSummary(refs map[string]any) string {
	if len(refs) == 0 {
		return "default"
	}
	data, err := json.Marshal(refs)
	if err != nil {
		return "custom"
	}
	return string(data)
}

func refsFromRunRef(ref string, fallback map[string]any) map[string]any {
	if strings.TrimSpace(ref) == "" || ref == "default" || ref == "custom" {
		return fallback
	}
	var refs map[string]any
	if err := json.Unmarshal([]byte(ref), &refs); err != nil || len(refs) == 0 {
		return fallback
	}
	return refs
}

func repoSyncAssetArchived(asset map[string]any) bool {
	value := strings.TrimSpace(fmt.Sprint(asset["archived_at"]))
	return value != "" && value != "<nil>"
}

func refsSummaryFromInput(input map[string]any) string {
	if input == nil {
		return "default"
	}
	refs, ok := input["refs"].(map[string]any)
	if !ok {
		return stringFromMap(input, "ref", "branch", "tag")
	}
	return refsSummary(refs)
}

func stringFromMap(input map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := input[key]
		if !ok || value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			return text
		}
		if text, ok := value.([]byte); ok {
			if len(text) > 1<<20 || !utf8.Valid(text) {
				return ""
			}
			return string(text)
		}
		return fmt.Sprint(value)
	}
	return ""
}

func userCanReadAllProjects(user *User) bool {
	return user != nil && (user.Role == "admin" || user.Role == "owner")
}

func userIDOrNil(user *User) any {
	if user == nil || strings.TrimSpace(user.ID) == "" {
		return nil
	}
	return user.ID
}

func (s *Server) createRemoteOperation(tool string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&input)
		}
		remoteID := chi.URLParam(r, "id")
		remoteModel, projectID, err := s.gitRemoteWithProjectGorm(r.Context(), remoteID)
		if err != nil {
			writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
			return
		}
		payload := map[string]any{"kind": "remote_operation", "tool": tool, "remote_id": remoteID, "input": input}
		if !s.requireProjectPolicyOrApproval(w, r, PolicyResource{Type: "git_remote", ID: remoteID, ProjectID: projectID}, tool, tool+" "+remoteModel.Name, payload) {
			return
		}
		var op map[string]any
		if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
			var err error
			op, err = s.enqueueRemoteOperationRunGorm(r.Context(), tx, remoteID, tool, input, currentUser(r).ID)
			if err != nil {
				return err
			}
			_, err = syncCanonicalAssetsGorm(r.Context(), tx)
			return err
		}); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeCreatedOne(w, op, nil)
	}
}

func createRemoteOperationRunGorm(ctx context.Context, tx *gorm.DB, op, remote, input map[string]any, actorID, tool string) error {
	switch tool {
	case "repo.sync":
		sourceID := stringFromMap(input, "source_remote_id")
		if sourceID == "" {
			sourceID = fmt.Sprint(remote["id"])
		}
		repoID := fmt.Sprint(remote["project_git_repository_id"])
		targetID := stringFromMap(input, "target_remote_id")
		if targetID == "" {
			targetID = stringFromMap(input, "target_id")
		}
		if targetID == "" {
			targetIDs, err := defaultTargetRemoteIDsGorm(ctx, tx, repoID, sourceID)
			if err != nil {
				return err
			}
			if len(targetIDs) > 0 {
				targetID = targetIDs[0]
			}
		}
		if targetID == "" {
			return fmt.Errorf("target_remote_id is required")
		}
		if targetID == sourceID {
			return fmt.Errorf("target_remote_id must be different from source_remote_id")
		}
		if _, err := remoteForRepositoryGorm(ctx, tx, repoID, sourceID); err != nil {
			return fmt.Errorf("source remote not found in repository")
		}
		if _, err := remoteForRepositoryGorm(ctx, tx, repoID, targetID); err != nil {
			return fmt.Errorf("target remote not found in repository")
		}
		run := GormRepoSyncRun{
			OperationRunID:         cleanOptionalID(fmt.Sprint(op["id"])),
			GitRemoteID:            targetID,
			ProjectID:              validNullString(cleanOptionalID(fmt.Sprint(remote["project_id"]))),
			ProjectGitRepositoryID: validNullString(cleanOptionalID(fmt.Sprint(remote["project_git_repository_id"]))),
			SourceRemoteID:         validNullString(sourceID),
			TargetRemoteID:         validNullString(targetID),
			Ref:                    refsSummaryFromInput(input),
			ActorUserID:            validNullString(actorID),
			Status:                 "queued",
		}
		return tx.WithContext(ctx).Create(&run).Error
	case "repo.tag":
		run := GormRepoTagRun{
			OperationRunID:         cleanOptionalID(fmt.Sprint(op["id"])),
			GitRemoteID:            cleanOptionalID(fmt.Sprint(remote["id"])),
			ProjectID:              validNullString(cleanOptionalID(fmt.Sprint(remote["project_id"]))),
			ProjectGitRepositoryID: validNullString(cleanOptionalID(fmt.Sprint(remote["project_git_repository_id"]))),
			TargetRemoteID:         validNullString(cleanOptionalID(fmt.Sprint(remote["id"]))),
			TagName:                stringFromMap(input, "tag_name", "tag"),
			TargetSHA:              stringFromMap(input, "target_sha", "sha"),
			TagMessage:             stringFromMap(input, "tag_message", "message"),
			ActorUserID:            validNullString(actorID),
			Status:                 "queued",
		}
		return tx.WithContext(ctx).Create(&run).Error
	default:
		return nil
	}
}

type repositoryTagRequest struct {
	TargetRemoteIDs []string `json:"target_remote_ids"`
	TagName         string   `json:"tag_name"`
	TargetSHA       string   `json:"target_sha"`
	Branch          string   `json:"branch"`
	TagMessage      string   `json:"tag_message"`
}

func (s *Server) enqueueRepositoryTagRunsGorm(ctx context.Context, tx *gorm.DB, repoID string, req repositoryTagRequest, actorID string) ([]map[string]any, error) {
	if strings.TrimSpace(req.TagName) == "" {
		return nil, fmt.Errorf("tag_name is required")
	}
	var repo GormProjectGitRepository
	if err := tx.WithContext(ctx).First(&repo, &GormProjectGitRepository{GormBase: GormBase{ID: repoID}}).Error; err != nil {
		return nil, gormNotFoundAsErrNotFound(err)
	}
	targetIDs := req.TargetRemoteIDs
	if len(targetIDs) == 0 {
		var err error
		targetIDs, err = defaultGitHubRemoteIDsGorm(ctx, tx, repoID)
		if err != nil {
			return nil, fmt.Errorf("could not select GitHub remotes")
		}
	}
	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("target_remote_ids is required")
	}
	var runs []map[string]any
	for _, targetID := range targetIDs {
		target, err := remoteForRepositoryGorm(ctx, tx, repoID, targetID)
		if err != nil {
			return nil, fmt.Errorf("target remote not found in repository")
		}
		input := map[string]any{
			"project_git_repository_id": repoID,
			"target_remote_id":          targetID,
			"tag_name":                  req.TagName,
			"target_sha":                req.TargetSHA,
			"branch":                    req.Branch,
			"tag_message":               req.TagMessage,
		}
		op, err := enqueueOperationGorm(ctx, tx, repo.ProjectID, targetID, "repo.create_tag", "tag "+req.TagName+" on "+fmt.Sprint(target["name"]), input, []string{"git"}, "")
		if err != nil {
			return nil, fmt.Errorf("could not enqueue tag")
		}
		run := GormRepoTagRun{
			OperationRunID:         cleanOptionalID(fmt.Sprint(op["id"])),
			GitRemoteID:            targetID,
			ProjectID:              validNullString(repo.ProjectID),
			ProjectGitRepositoryID: validNullString(repoID),
			TargetRemoteID:         validNullString(targetID),
			TagName:                req.TagName,
			TargetSHA:              req.TargetSHA,
			TagMessage:             req.TagMessage,
			ActorUserID:            validNullString(actorID),
			Status:                 "queued",
		}
		if err := tx.WithContext(ctx).Create(&run).Error; err != nil {
			return nil, fmt.Errorf("could not create tag run")
		}
		runs = append(runs, repoTagRunMap(run))
	}
	return runs, nil
}
