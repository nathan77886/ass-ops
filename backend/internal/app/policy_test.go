package app

import "testing"

func TestPolicyChecker_Check(t *testing.T) {
	tests := []struct {
		name   string
		user   *User
		action string
		want   PolicyEffect
	}{
		{
			name:   "admin can tag remote",
			user:   &User{Role: "admin"},
			action: "repo.tag",
			want:   PolicyAllow,
		},
		{
			name:   "developer can sync remote",
			user:   &User{Role: "developer"},
			action: "repo.sync",
			want:   PolicyAllow,
		},
		{
			name:   "developer cannot update approval rules",
			user:   &User{Role: "developer"},
			action: "update",
			want:   PolicyDeny,
		},
		{
			name:   "developer requires confirmation for tag",
			user:   &User{Role: "developer"},
			action: "repo.tag",
			want:   PolicyRequireConfirm,
		},
		{
			name:   "developer requires confirmation for ssh exec",
			user:   &User{Role: "developer"},
			action: "ssh.exec",
			want:   PolicyRequireConfirm,
		},
		{
			name:   "developer can verify ssh machine",
			user:   &User{Role: "developer"},
			action: "ssh.verify",
			want:   PolicyAllow,
		},
		{
			name:   "viewer can read",
			user:   &User{Role: "viewer"},
			action: "read",
			want:   PolicyAllow,
		},
		{
			name:   "viewer can read global project templates",
			user:   &User{Role: "viewer"},
			action: "read",
			want:   PolicyAllow,
		},
		{
			name:   "agent can read global project templates",
			user:   &User{Role: "agent"},
			action: "read",
			want:   PolicyAllow,
		},
		{
			name:   "viewer cannot read ssh command output",
			user:   &User{Role: "viewer"},
			action: "read",
			want:   PolicyDeny,
		},
		{
			name:   "viewer cannot generate context",
			user:   &User{Role: "viewer"},
			action: "context.generate",
			want:   PolicyDeny,
		},
		{
			name:   "missing user denied",
			user:   nil,
			action: "read",
			want:   PolicyDeny,
		},
		{
			name:   "unknown role denied",
			user:   &User{Role: "auditor"},
			action: "read",
			want:   PolicyDeny,
		},
	}

	checker := NewPolicyChecker()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceType := "project"
			if tt.name == "viewer cannot read ssh command output" {
				resourceType = "ssh_command_run"
			}
			if tt.name == "viewer can read global project templates" || tt.name == "agent can read global project templates" {
				resourceType = "project_template"
			}
			if tt.name == "developer cannot update approval rules" {
				resourceType = "operation_approval_rule"
			}
			got := checker.Check(tt.user, PolicyResource{Type: resourceType, ID: "project-1"}, tt.action)
			if got.Effect != tt.want {
				t.Fatalf("effect = %q, want %q; reason=%s", got.Effect, tt.want, got.Reason)
			}
		})
	}
}

func TestApprovalRolesFromAny(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  []string
	}{
		{name: "postgres array text", input: "{admin,owner}", want: []string{"admin", "owner"}},
		{name: "quoted postgres array text", input: `{"admin,ops","Owner"}`, want: []string{"admin,ops", "owner"}},
		{name: "json array text", input: `["owner","admin"]`, want: []string{"owner", "admin"}},
		{name: "deduplicates and trims", input: []any{" owner ", "owner", "admin"}, want: []string{"owner", "admin"}},
		{name: "empty", input: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := approvalRolesFromAny(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("roles = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("roles = %#v, want %#v", got, tt.want)
				}
			}
		})
	}
}

func TestCanDecideOperationApprovalUsesRuleSnapshot(t *testing.T) {
	approval := map[string]any{"required_approver_roles": "{owner}"}
	if canDecideOperationApproval(&User{Role: "admin"}, approval) {
		t.Fatal("admin should not decide owner-only approval")
	}
	if !canDecideOperationApproval(&User{Role: "Owner"}, approval) {
		t.Fatal("owner should decide owner-only approval")
	}
	if canDecideOperationApproval(&User{Role: "developer"}, map[string]any{}) {
		t.Fatal("developer should not decide default approval")
	}
}
