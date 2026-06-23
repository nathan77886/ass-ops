package app

import "net/http"

type PolicyEffect string

const (
	PolicyAllow          PolicyEffect = "allow"
	PolicyRequireConfirm PolicyEffect = "require_confirm"
	PolicyDeny           PolicyEffect = "deny"
)

type PolicyResource struct {
	Type      string
	ID        string
	ProjectID string
}

type PolicyDecision struct {
	Effect PolicyEffect `json:"effect"`
	Reason string       `json:"reason"`
}

type PolicyChecker struct{}

func NewPolicyChecker() PolicyChecker {
	return PolicyChecker{}
}

func (p PolicyChecker) Check(user *User, resource PolicyResource, action string) PolicyDecision {
	if user == nil {
		return PolicyDecision{Effect: PolicyDeny, Reason: "missing user"}
	}
	role := user.Role
	if role == "" {
		role = "viewer"
	}
	if resource.Type == "ssh_command_run" && isReadAction(action) && role != "admin" && role != "owner" {
		return PolicyDecision{Effect: PolicyDeny, Reason: "SSH command output requires an operator role"}
	}
	if resource.Type == "project_template" && isReadAction(action) {
		return PolicyDecision{Effect: PolicyAllow, Reason: "project templates are globally readable"}
	}
	if resource.Type == "operation_approval_rule" && !isReadAction(action) && role != "admin" && role != "owner" {
		return PolicyDecision{Effect: PolicyDeny, Reason: "approval rule mutations require an admin or owner role"}
	}
	switch role {
	case "admin", "owner":
		return PolicyDecision{Effect: PolicyAllow, Reason: "role can operate resource"}
	case "developer":
		return developerPolicy(action)
	case "viewer":
		if isReadAction(action) {
			return PolicyDecision{Effect: PolicyAllow, Reason: "viewer can read resource"}
		}
		return PolicyDecision{Effect: PolicyDeny, Reason: "viewer cannot mutate resource"}
	case "agent":
		if action == "context.generate" || isReadAction(action) {
			return PolicyDecision{Effect: PolicyAllow, Reason: "agent can read controlled context"}
		}
		return PolicyDecision{Effect: PolicyDeny, Reason: "agent mutations require explicit delegated operation"}
	default:
		return PolicyDecision{Effect: PolicyDeny, Reason: "unknown role"}
	}
}

func developerPolicy(action string) PolicyDecision {
	switch action {
	case "read", "create", "update", "context.generate", "repo.sync", "github.actions.sync", "argo.apps.sync", "ssh.verify", "node.echo", "agent.generate_plan", "agent.approve_plan":
		return PolicyDecision{Effect: PolicyAllow, Reason: "developer can perform standard first-version operation"}
	case "repo.tag", "ssh.exec", "operation.cancel", "agent.execute":
		return PolicyDecision{Effect: PolicyRequireConfirm, Reason: "operation requires explicit confirmation"}
	default:
		return PolicyDecision{Effect: PolicyDeny, Reason: "developer role cannot perform this operation"}
	}
}

func isReadAction(action string) bool {
	return action == "read" || action == "list"
}

func (s *Server) requirePolicy(w http.ResponseWriter, r *http.Request, resource PolicyResource, action string) bool {
	decision := NewPolicyChecker().Check(currentUser(r), resource, action)
	switch decision.Effect {
	case PolicyAllow:
		return true
	case PolicyRequireConfirm:
		writeJSON(w, http.StatusPreconditionRequired, decision)
		return false
	default:
		writeJSON(w, http.StatusForbidden, decision)
		return false
	}
}
