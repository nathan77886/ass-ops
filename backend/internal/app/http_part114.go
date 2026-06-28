package app

import (
	"context"
	"database/sql"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
)

func (s *Server) updateSSHMachine(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	var current GormSSHMachine
	if err := s.store.Gorm.WithContext(r.Context()).First(&current, &GormSSHMachine{GormBase: GormBase{ID: machineID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: current.ProjectID}, "update") {
		return
	}
	var req struct {
		Name         *string        `json:"name"`
		Host         *string        `json:"host"`
		Port         *int           `json:"port"`
		Username     *string        `json:"username"`
		AuthType     *string        `json:"auth_type"`
		CredentialID *string        `json:"credential_id"`
		Metadata     map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	next := current
	if req.Name != nil {
		next.Name = strings.TrimSpace(*req.Name)
	}
	if req.Host != nil {
		next.Host = strings.TrimSpace(*req.Host)
	}
	if req.Port != nil {
		next.Port = *req.Port
	}
	if next.Port == 0 {
		next.Port = 22
	}
	if next.Port < 1 || next.Port > 65535 {
		writeError(w, http.StatusBadRequest, "port must be between 1 and 65535")
		return
	}
	if req.Username != nil {
		next.Username = strings.TrimSpace(*req.Username)
	}
	if req.AuthType != nil {
		next.AuthType = strings.TrimSpace(*req.AuthType)
	}
	if next.AuthType == "" {
		next.AuthType = "key"
	}
	credentialKind := connectionCredentialKindForSSHAuth(next.AuthType)
	if credentialKind == "" {
		writeError(w, http.StatusBadRequest, "auth_type must be key or password")
		return
	}
	if req.Metadata != nil {
		metadata := mapFromAny(next.Metadata.Data)
		for key, value := range req.Metadata {
			metadata[key] = value
		}
		next.Metadata = JSONValue{Data: metadata}
	}
	credentialID := cleanOptionalID(next.CredentialID.String)
	if req.CredentialID != nil {
		credentialID = cleanOptionalID(*req.CredentialID)
		next.CredentialID = validNullString(credentialID)
	}
	credential, err := s.connectionCredentialForProjectOrGlobal(r.Context(), next.ProjectID, credentialID, credentialKind)
	if err != nil {
		writeError(w, http.StatusBadRequest, "credential_id must reference a matching SSH credential in this project")
		return
	}
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var locked GormSSHMachine
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, &GormSSHMachine{GormBase: GormBase{ID: machineID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		next.GormBase = locked.GormBase
		next.ProjectID = locked.ProjectID
		if err := tx.Save(&next).Error; err != nil {
			return err
		}
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, sshMachineMap(next, credential))
}

func (s *Server) deleteSSHMachine(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	var machine GormSSHMachine
	if err := s.store.Gorm.WithContext(r.Context()).First(&machine, &GormSSHMachine{GormBase: GormBase{ID: machineID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: machine.ProjectID}, "delete") {
		return
	}
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var locked GormSSHMachine
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, &GormSSHMachine{GormBase: GormBase{ID: machineID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		if err := tx.Model(&GormSSHCommandRun{}).Where(&GormSSHCommandRun{SSHMachineID: validNullString(machineID)}).Update("ssh_machine_id", sql.NullString{}).Error; err != nil {
			return err
		}
		if err := deleteCanonicalAssetForSourceGorm(r.Context(), tx, "ssh_machine", "ssh_machines", machineID); err != nil {
			return err
		}
		if err := tx.Delete(&locked).Error; err != nil {
			return err
		}
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": machineID})
}

func (s *Server) getSSHMachineRehearsal(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	var machineModel GormSSHMachine
	if err := s.store.Gorm.WithContext(r.Context()).First(&machineModel, &GormSSHMachine{GormBase: GormBase{ID: machineID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	machine := sshMachineMap(machineModel, nil)
	projectID := cleanPreviewString(machineModel.ProjectID)
	if projectID == "" {
		writeError(w, http.StatusInternalServerError, "SSH machine has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: projectID}, "read") {
		return
	}
	runs, err := sshMachineRehearsalRunMaps(r.Context(), s.store.Gorm, machineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load SSH rehearsal evidence")
		return
	}
	proofEvidence, err := sshMachineTargetEnvironmentProofEvidence(r.Context(), s.store.Gorm, machineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load SSH target environment proof evidence")
		return
	}
	writeJSON(w, http.StatusOK, buildSSHMachineRehearsalPreview(machine, runs, proofEvidence))
}

func sshMachineRehearsalRunMaps(ctx context.Context, db *gorm.DB, machineID string) ([]map[string]any, error) {
	var runs []GormSSHCommandRun
	if err := db.WithContext(ctx).
		Where(&GormSSHCommandRun{SSHMachineID: validNullString(machineID)}).
		Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "created_at"}, Desc: true}}}).
		Limit(50).
		Find(&runs).Error; err != nil {
		return nil, err
	}
	opTypes, err := operationTypesByID(ctx, db, sshRunOperationIDs(runs))
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		items = append(items, map[string]any{
			"id":             run.ID,
			"status":         run.Status,
			"exit_code":      nullableInt64Any(run.ExitCode),
			"created_at":     run.CreatedAt,
			"finished_at":    nullableTimeAny(run.FinishedAt),
			"operation_type": opTypes[cleanOptionalID(run.OperationRunID.String)],
		})
	}
	return items, nil
}

func sshRunOperationIDs(runs []GormSSHCommandRun) []string {
	ids := make([]string, 0, len(runs))
	seen := map[string]bool{}
	for _, run := range runs {
		id := cleanOptionalID(run.OperationRunID.String)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func operationTypesByID(ctx context.Context, db *gorm.DB, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	var runs []GormOperationRun
	if err := db.WithContext(ctx).Find(&runs, ids).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(runs))
	for _, run := range runs {
		out[run.ID] = run.OperationType
	}
	return out, nil
}

func (s *Server) recordSSHMachineTargetEnvironmentProof(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	machine, err := sshMachineForRehearsalSnapshot(r.Context(), s.store.Gorm, machineID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := cleanPreviewString(machine["project_id"])
	if projectID == "" {
		writeError(w, http.StatusInternalServerError, "SSH machine has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: projectID}, "update") {
		return
	}
	result, err := RecordSSHMachineTargetEnvironmentProof(r.Context(), s.store, SSHMachineTargetEnvironmentProofOptions{
		MachineID: machineID,
		DryRun:    req.DryRun,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("ssh target environment proof failed", "ssh_machine_id", machineID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record SSH target environment proof failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
