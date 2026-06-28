package app

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

func summarizeSSHRehearsalEvidence(runs []map[string]any) map[string]any {
	evidence := map[string]any{
		"total_runs":                       len(runs),
		"verify_runs":                      0,
		"exec_runs":                        0,
		"unknown_runs":                     0,
		"completed_runs":                   0,
		"completed_without_exit_code_runs": 0,
		"failed_runs":                      0,
		"running_runs":                     0,
		"queued_runs":                      0,
		"canceled_runs":                    0,
		"terminal_runs":                    0,
		"active_runs":                      0,
		"completed_verify":                 false,
		"completed_exec":                   false,
		"has_live_evidence":                len(runs) > 0,
		"has_failures":                     false,
		"has_cancellations":                false,
		"evidence_state":                   "not_recorded",
		"sanitized_metadata_only":          true,
		"stdout_included":                  false,
		"stderr_included":                  false,
		"raw_error_included":               false,
		"private_key_included":             false,
		"known_hosts_included":             false,
		"secret_included":                  false,
		"suppressed_fields":                []string{"command", "stdout", "stderr", "raw_error", "private_key", "passphrase", "known_hosts_body", "runtime_secret"},
		"latest_verify":                    nil,
		"latest_exec":                      nil,
		"latest_unknown":                   nil,
	}
	for _, run := range runs {
		operationType := cleanPreviewString(run["operation_type"])
		if operationType == "" {
			operationType = "unknown"
		}
		status := cleanPreviewString(run["status"])
		exitCodeRecorded := cleanPreviewString(run["exit_code"]) != ""
		item := map[string]any{
			"id":                 run["id"],
			"status":             status,
			"exit_code":          run["exit_code"],
			"exit_code_recorded": exitCodeRecorded,
			"created_at":         run["created_at"],
			"finished_at":        run["finished_at"],
			"operation_type":     operationType,
		}
		switch status {
		case "completed":
			evidence["completed_runs"] = intFromAny(evidence["completed_runs"], 0) + 1
			evidence["terminal_runs"] = intFromAny(evidence["terminal_runs"], 0) + 1
			if !exitCodeRecorded {
				evidence["completed_without_exit_code_runs"] = intFromAny(evidence["completed_without_exit_code_runs"], 0) + 1
			}
		case "failed":
			evidence["failed_runs"] = intFromAny(evidence["failed_runs"], 0) + 1
			evidence["terminal_runs"] = intFromAny(evidence["terminal_runs"], 0) + 1
			evidence["has_failures"] = true
		case "canceled", "cancelled":
			evidence["canceled_runs"] = intFromAny(evidence["canceled_runs"], 0) + 1
			evidence["terminal_runs"] = intFromAny(evidence["terminal_runs"], 0) + 1
			evidence["has_cancellations"] = true
		case "running":
			evidence["running_runs"] = intFromAny(evidence["running_runs"], 0) + 1
			evidence["active_runs"] = intFromAny(evidence["active_runs"], 0) + 1
		case "queued", "pending":
			evidence["queued_runs"] = intFromAny(evidence["queued_runs"], 0) + 1
			evidence["active_runs"] = intFromAny(evidence["active_runs"], 0) + 1
		}
		switch operationType {
		case "ssh.verify":
			evidence["verify_runs"] = intFromAny(evidence["verify_runs"], 0) + 1
			if evidence["latest_verify"] == nil {
				evidence["latest_verify"] = item
			}
			if status == "completed" && exitCodeRecorded {
				evidence["completed_verify"] = true
			}
		case "ssh.exec":
			evidence["exec_runs"] = intFromAny(evidence["exec_runs"], 0) + 1
			if evidence["latest_exec"] == nil {
				evidence["latest_exec"] = item
			}
			if status == "completed" && exitCodeRecorded {
				evidence["completed_exec"] = true
			}
		default:
			evidence["unknown_runs"] = intFromAny(evidence["unknown_runs"], 0) + 1
			if evidence["latest_unknown"] == nil {
				evidence["latest_unknown"] = item
			}
		}
	}
	if len(runs) == 0 {
		return evidence
	}
	switch {
	case boolOnlyFromAny(evidence["has_failures"]):
		evidence["evidence_state"] = "failed"
	case intFromAny(evidence["active_runs"], 0) > 0:
		evidence["evidence_state"] = "waiting_for_workers"
	case boolOnlyFromAny(evidence["has_cancellations"]):
		evidence["evidence_state"] = "canceled"
	case boolOnlyFromAny(evidence["completed_verify"]) && boolOnlyFromAny(evidence["completed_exec"]):
		evidence["evidence_state"] = "recorded"
	default:
		evidence["evidence_state"] = "partial_recorded"
	}
	return evidence
}

func cleanPreviewString(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func statusWhen(ok bool) string {
	if ok {
		return "planned"
	}
	return "blocked"
}

func reasonWhen(ok bool, ready, blocked string) string {
	if ok {
		return ready
	}
	return blocked
}

func (s *Server) createSSHCommand(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	var machineModel GormSSHMachine
	if err := s.store.Gorm.WithContext(r.Context()).First(&machineModel, &GormSSHMachine{GormBase: GormBase{ID: machineID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	machine := sshMachineMap(machineModel, nil)
	var req struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Command = strings.TrimSpace(req.Command)
	if req.Command == "" {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}
	if len(req.Command) > 4096 {
		writeError(w, http.StatusBadRequest, "command is too long")
		return
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 60
	}
	if req.TimeoutSeconds > 300 {
		writeError(w, http.StatusBadRequest, "timeout_seconds must be <= 300")
		return
	}
	input := map[string]any{
		"ssh_machine_id":  machineID,
		"command":         req.Command,
		"timeout_seconds": req.TimeoutSeconds,
	}
	payload := map[string]any{"kind": "ssh_command", "machine_id": machineID, "input": input}
	if !s.requireProjectPolicyOrApproval(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: fmt.Sprint(machine["project_id"])}, "ssh.exec", "ssh "+fmt.Sprint(machine["name"]), payload) {
		return
	}
	s.createSSHRun(w, r, machineID, input, "ssh.exec", "ssh "+fmt.Sprint(machine["name"]), "ssh_command.enqueue")
}

func (s *Server) verifySSHMachine(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	var machineModel GormSSHMachine
	if err := s.store.Gorm.WithContext(r.Context()).First(&machineModel, &GormSSHMachine{GormBase: GormBase{ID: machineID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	machine := sshMachineMap(machineModel, nil)
	input := map[string]any{
		"ssh_machine_id":  machineID,
		"command":         "true",
		"timeout_seconds": 15,
		"verify":          true,
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: fmt.Sprint(machine["project_id"])}, "ssh.verify") {
		return
	}
	s.createSSHRun(w, r, machineID, input, "ssh.verify", "verify ssh "+fmt.Sprint(machine["name"]), "ssh_verify.enqueue")
}

func (s *Server) createSSHRun(w http.ResponseWriter, r *http.Request, machineID string, input map[string]any, operationType, title, syncReason string) {
	var op map[string]any
	var run map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		op, run, err = s.enqueueSSHCommandRunGorm(r.Context(), tx, machineID, input, currentUser(r).ID, operationType, title)
		if err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"operation": op, "run": run})
}

func (s *Server) enqueueSSHCommandRunGorm(ctx context.Context, tx *gorm.DB, machineID string, input map[string]any, actorID, operationType, title string) (map[string]any, map[string]any, error) {
	var machine GormSSHMachine
	if err := tx.First(&machine, &GormSSHMachine{GormBase: GormBase{ID: machineID}}).Error; err != nil {
		return nil, nil, gormNotFoundAsErrNotFound(err)
	}
	command := strings.TrimSpace(stringFromMap(input, "command"))
	if command == "" {
		return nil, nil, fmt.Errorf("command is required")
	}
	if operationType == "" {
		operationType = "ssh.exec"
	}
	if title == "" {
		title = "ssh " + machine.Name
	}
	op, err := enqueueOperationGorm(ctx, tx, machine.ProjectID, "", operationType, title, input, []string{"ssh"}, "control-worker")
	if err != nil {
		return nil, nil, fmt.Errorf("could not enqueue SSH command")
	}
	run := GormSSHCommandRun{OperationRunID: validNullString(cleanOptionalID(fmt.Sprint(op["id"]))), SSHMachineID: validNullString(machineID), ProjectID: validNullString(machine.ProjectID), ActorUserID: validNullString(actorID), Command: command, Status: "queued"}
	if err := tx.Create(&run).Error; err != nil {
		return nil, nil, fmt.Errorf("could not create SSH command run")
	}
	return op, sshCommandRunMap(run, operationType), nil
}

func sshCommandRunMap(run GormSSHCommandRun, operationType string) map[string]any {
	return map[string]any{"id": run.ID, "operation_run_id": nullableStringValue(run.OperationRunID), "ssh_machine_id": nullableStringValue(run.SSHMachineID), "project_id": nullableStringValue(run.ProjectID), "actor_user_id": nullableStringValue(run.ActorUserID), "command": run.Command, "status": run.Status, "exit_code": nullableInt64Any(run.ExitCode), "stdout": run.Stdout, "stderr": run.Stderr, "error_message": run.ErrorMessage, "started_at": nullableTimeAny(run.StartedAt), "finished_at": nullableTimeAny(run.FinishedAt), "created_at": run.CreatedAt, "operation_type": operationType}
}

func nullableInt64Any(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}
	return nil
}

func (s *Server) listSSHCommandRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "ssh_command_run"}, "read") {
		return
	}
	projectID := r.URL.Query().Get("project_id")
	machineID := r.URL.Query().Get("machine_id")
	switch {
	case machineID != "":
		items, err := s.sshCommandRunMaps(r.Context(), GormSSHCommandRun{SSHMachineID: validNullString(machineID)})
		writeQueryResult(w, items, err)
	case projectID != "":
		items, err := s.sshCommandRunMaps(r.Context(), GormSSHCommandRun{ProjectID: validNullString(projectID)})
		writeQueryResult(w, items, err)
	default:
		writeError(w, http.StatusBadRequest, "project_id or machine_id is required")
	}
}
