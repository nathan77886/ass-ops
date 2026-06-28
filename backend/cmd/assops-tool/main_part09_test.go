package main

import (
	"testing"
)

func TestFirstVersionReadinessReportApprovalReadinessMatrix(t *testing.T) {
	withoutSummary := firstVersionReadinessReport(nil, []map[string]any{
		{"operation_type": "approval.notify", "status": "completed"},
	}, nil)
	if got := readinessByKey(t, withoutSummary, "approval"); got.Status != "missing" {
		t.Fatalf("approval status from operation_type alone = %q, want missing", got.Status)
	}

	withSummary := firstVersionReadinessReport(nil, nil, map[string]any{"total": float64(1)})
	if got := readinessByKey(t, withSummary, "approval"); got.Status != "partial" || got.Evidence != "1 approvals / 0 approval assets / 0 pending ops / 0 active rules / 0 governed approvals / 0 gated ops / 0 complete approval chains / 0 approval asset chains" {
		t.Fatalf("approval status from summary without rule = %#v, want partial with rule evidence", got)
	}

	withRule := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_approval_rule", "source_id": "10", "status": "active"},
	}, nil, map[string]any{"total": float64(1)})
	if got := readinessByKey(t, withRule, "approval"); got.Status != "partial" || got.Evidence != "1 approvals / 0 approval assets / 0 pending ops / 1 active rules / 0 governed approvals / 0 gated ops / 0 complete approval chains / 0 approval asset chains" {
		t.Fatalf("approval status from summary and rule without graph = %#v, want partial", got)
	}

	withGovernedApproval := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "operation_approval", "source_id": "20", "status": "pending"},
		{"asset_type": "operation_approval_rule", "source_id": "10", "status": "active"},
	}, nil, map[string]any{"total": float64(1)}, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_approval_rule:10", "to_asset_id": "operation_approval:20", "relation_type": "governs"},
		},
	})
	if got := readinessByKey(t, withGovernedApproval, "approval"); got.Status != "partial" || got.Evidence != "1 approvals / 1 approval assets / 0 pending ops / 1 active rules / 1 governed approvals / 0 gated ops / 0 complete approval chains / 0 approval asset chains" {
		t.Fatalf("approval status from governed approval asset and active rule = %#v, want partial without pending operation chain", got)
	}

	withPendingOperationChain := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "operation_approval", "source_id": "20", "status": "pending"},
		{"asset_type": "operation_approval_rule", "source_id": "10", "status": "active"},
		{"asset_type": "operation_run", "source_id": "30", "status": "pending_approval"},
	}, []map[string]any{
		{"id": "30", "operation_type": "ssh.exec", "status": "pending_approval"},
	}, map[string]any{"total": float64(1)}, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_approval_rule:10", "to_asset_id": "operation_approval:20", "relation_type": "governs"},
			map[string]any{"from_asset_id": "operation_approval:20", "to_asset_id": "operation_run:30", "relation_type": "gates_operation"},
		},
	})
	if got := readinessByKey(t, withPendingOperationChain, "approval"); got.Status != "ready" || got.Evidence != "1 approvals / 1 approval assets / 1 pending ops / 1 active rules / 1 governed approvals / 1 gated ops / 1 complete approval chains / 1 approval asset chains" {
		t.Fatalf("approval status from pending operation approval chain = %#v, want ready", got)
	}

	withUnmatchedGovernedApproval := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "operation_approval", "source_id": "21", "status": "pending"},
		{"asset_type": "operation_approval_rule", "source_id": "10", "status": "active"},
	}, nil, map[string]any{"total": float64(1)}, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_approval_rule:10", "to_asset_id": "operation_approval:20", "relation_type": "governs"},
		},
	})
	if got := readinessByKey(t, withUnmatchedGovernedApproval, "approval"); got.Status != "partial" || got.Evidence != "1 approvals / 1 approval assets / 0 pending ops / 1 active rules / 0 governed approvals / 0 gated ops / 0 complete approval chains / 0 approval asset chains" {
		t.Fatalf("approval status from unmatched governed approval edge = %#v, want partial without canonical approval asset evidence", got)
	}

	withDisabledRuleGovernedApproval := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "operation_approval", "source_id": "20", "status": "pending"},
		{"asset_type": "operation_approval_rule", "source_id": "10", "status": "active"},
		{"asset_type": "operation_approval_rule", "source_id": "11", "status": "disabled"},
	}, nil, map[string]any{"total": float64(1)}, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_approval_rule:11", "to_asset_id": "operation_approval:20", "relation_type": "governs"},
		},
	})
	if got := readinessByKey(t, withDisabledRuleGovernedApproval, "approval"); got.Status != "partial" || got.Evidence != "1 approvals / 1 approval assets / 0 pending ops / 1 active rules / 0 governed approvals / 0 gated ops / 0 complete approval chains / 0 approval asset chains" {
		t.Fatalf("approval status from disabled governed rule and separate active rule = %#v, want partial", got)
	}

	ruleOnly := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_approval_rule", "source_id": "10", "status": "active"},
	}, nil, nil)
	if got := readinessByKey(t, ruleOnly, "approval"); got.Status != "partial" || got.Evidence != "0 approvals / 0 approval assets / 0 pending ops / 1 active rules / 0 governed approvals / 0 gated ops / 0 complete approval chains / 0 approval asset chains" {
		t.Fatalf("approval status from rule without request evidence = %#v, want partial", got)
	}
}

func TestCountAPITypeStatus(t *testing.T) {
	rows := []map[string]any{
		{"asset_type": "operation_approval_rule", "status": "active"},
		{"asset_type": "operation_approval_rule", "status": "disabled"},
		{"asset_type": "operation_approval", "status": "active"},
	}
	if got := countAPITypeStatus(rows, "operation_approval_rule", "active"); got != 1 {
		t.Fatalf("countAPITypeStatus = %d, want 1", got)
	}
}

func TestFirstVersionReadinessReportTreatsPendingApprovalOperationAsPartialEvidence(t *testing.T) {
	report := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_approval_rule", "source_id": "10", "status": "active"},
	}, []map[string]any{
		{"operation_type": "ssh.exec", "status": "pending_approval"},
	}, nil)
	if got := readinessByKey(t, report, "approval"); got.Status != "partial" || got.Evidence != "0 approvals / 0 approval assets / 1 pending ops / 1 active rules / 0 governed approvals / 0 gated ops / 0 complete approval chains / 0 approval asset chains" {
		t.Fatalf("approval status from pending operation and rule without graph = %#v, want partial", got)
	}

	withoutRule := firstVersionReadinessReport(nil, []map[string]any{
		{"operation_type": "ssh.exec", "status": "pending_approval"},
	}, nil)
	if got := readinessByKey(t, withoutRule, "approval"); got.Status != "partial" || got.Evidence != "0 approvals / 0 approval assets / 1 pending ops / 0 active rules / 0 governed approvals / 0 gated ops / 0 complete approval chains / 0 approval asset chains" {
		t.Fatalf("approval status from pending operation without rule = %#v, want partial", got)
	}
}

func TestFirstVersionReadinessReportIgnoresDisabledApprovalRules(t *testing.T) {
	report := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_approval_rule", "status": "disabled"},
	}, nil, map[string]any{"total": float64(1)})
	if got := readinessByKey(t, report, "approval"); got.Status != "partial" || got.Evidence != "1 approvals / 0 approval assets / 0 pending ops / 0 active rules / 0 governed approvals / 0 gated ops / 0 complete approval chains / 0 approval asset chains" {
		t.Fatalf("approval status with disabled rule = %#v, want partial without active rule evidence", got)
	}
}

func TestFirstVersionReadinessReportRequiresOperationLogs(t *testing.T) {
	allZero := firstVersionReadinessReport(nil, nil, nil)
	if got := readinessByKey(t, allZero, "operations"); got.Status != "missing" || got.Evidence != "0 operation assets / 0 listed runs / 0 with logs" {
		t.Fatalf("operations readiness without evidence = %#v, want missing", got)
	}

	withoutLogs := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_run", "source_id": "op-1"},
	}, []map[string]any{
		{"id": "op-1", "operation_type": "repo.sync", "log_count": 0},
	}, nil)
	if got := readinessByKey(t, withoutLogs, "operations"); got.Status != "partial" || got.Evidence != "1 operation assets / 1 listed runs / 0 with logs" {
		t.Fatalf("operations readiness without logs = %#v, want partial with log evidence", got)
	}

	withoutAsset := firstVersionReadinessReport(nil, []map[string]any{
		{"id": "op-1", "operation_type": "repo.sync", "log_count": 2},
	}, nil)
	if got := readinessByKey(t, withoutAsset, "operations"); got.Status != "partial" || got.Evidence != "0 operation assets / 1 listed runs / 0 with logs" {
		t.Fatalf("operations readiness without operation asset = %#v, want partial with asset evidence", got)
	}

	withMismatchedAssetAndLogs := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_run", "source_id": "op-1"},
	}, []map[string]any{
		{"id": "op-2", "operation_type": "repo.sync", "log_count": 2},
	}, nil)
	if got := readinessByKey(t, withMismatchedAssetAndLogs, "operations"); got.Status != "partial" || got.Evidence != "1 operation assets / 1 listed runs / 0 with logs" {
		t.Fatalf("operations readiness with mismatched asset and logged run = %#v, want partial without canonical log evidence", got)
	}

	withAssetAndLogs := firstVersionReadinessReport([]map[string]any{
		{"asset_type": "operation_run", "source_id": "op-1"},
	}, []map[string]any{
		{"id": "op-1", "operation_type": "repo.sync", "log_count": 2},
	}, nil)
	if got := readinessByKey(t, withAssetAndLogs, "operations"); got.Status != "ready" || got.Evidence != "1 operation assets / 1 listed runs / 1 with logs" {
		t.Fatalf("operations readiness with operation asset and logs = %#v, want ready", got)
	}
}

func TestFirstVersionReadinessReportRequiresContextGraphEvidence(t *testing.T) {
	missing := firstVersionReadinessReportWithGraph(nil, nil, nil, nil)
	if got := readinessByKey(t, missing, "context"); got.Status != "missing" || got.Evidence != "0 context assets / 0 context generations / 0 complete context tasks / 0 context asset tasks / 0 runtime links / 0 context tool links / 0 graph nodes / 0 graph edges" {
		t.Fatalf("context readiness without context or graph evidence = %#v, want missing", got)
	}

	withoutGraph := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
		{"asset_type": "agent_task"},
	}, nil, nil, nil)
	if got := readinessByKey(t, withoutGraph, "context"); got.Status != "partial" || got.Evidence != "2 context assets / 0 context generations / 0 complete context tasks / 0 context asset tasks / 0 runtime links / 0 context tool links / 0 graph nodes / 0 graph edges" {
		t.Fatalf("context readiness without graph evidence = %#v, want partial with graph evidence", got)
	}

	graphOnly := firstVersionReadinessReportWithGraph(nil, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
	})
	if got := readinessByKey(t, graphOnly, "context"); got.Status != "partial" || got.Evidence != "0 context assets / 0 context generations / 0 complete context tasks / 0 context asset tasks / 0 runtime links / 0 context tool links / 1 graph nodes / 0 graph edges" {
		t.Fatalf("context readiness without context assets = %#v, want partial with graph evidence", got)
	}

	withoutGeneration := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
	}, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
		"edges": []any{map[string]any{"from_asset_id": "project:1", "to_asset_id": "repo:1"}},
	})
	if got := readinessByKey(t, withoutGeneration, "context"); got.Status != "partial" || got.Evidence != "1 context assets / 0 context generations / 0 complete context tasks / 0 context asset tasks / 0 runtime links / 0 context tool links / 1 graph nodes / 1 graph edges" {
		t.Fatalf("context readiness without generation evidence = %#v, want partial", got)
	}

	withoutContextGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
		{"asset_type": "agent_task"},
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:20", "status": "completed", "metadata": map[string]any{"tool_name": "context.generate"}},
	}, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
		"edges": []any{map[string]any{"from_asset_id": "project:1", "to_asset_id": "repo:1"}},
	})
	if got := readinessByKey(t, withoutContextGraphLinks, "context"); got.Status != "partial" || got.Evidence != "2 context assets / 1 context generations / 0 complete context tasks / 0 context asset tasks / 0 runtime links / 0 context tool links / 1 graph nodes / 1 graph edges" {
		t.Fatalf("context readiness without task graph links = %#v, want partial", got)
	}

	queuedContextToolCall := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime", "source_id": "30"},
		{"asset_type": "agent_task", "source_id": "10"},
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:20", "status": "queued", "metadata": map[string]any{"tool_name": "context.generate"}},
	}, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
		"edges": []any{
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "ai_runtime:30", "relation_type": "uses_runtime"},
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "agent_tool_call:20", "relation_type": "records_tool_call"},
		},
	})
	if got := readinessByKey(t, queuedContextToolCall, "context"); got.Status != "partial" || got.Evidence != "2 context assets / 0 context generations / 0 complete context tasks / 0 context asset tasks / 1 runtime links / 0 context tool links / 1 graph nodes / 2 graph edges" {
		t.Fatalf("context readiness with queued context.generate = %#v, want partial without generated context evidence", got)
	}

	crossTaskAggregation := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
		{"asset_type": "agent_task"},
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:20", "status": "completed", "metadata": map[string]any{"tool_name": "context.generate"}},
	}, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
		"edges": []any{
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "ai_runtime:30", "relation_type": "uses_runtime"},
			map[string]any{"from_asset_id": "agent_task:11", "to_asset_id": "agent_tool_call:20", "relation_type": "records_tool_call"},
		},
	})
	if got := readinessByKey(t, crossTaskAggregation, "context"); got.Status != "partial" || got.Evidence != "2 context assets / 1 context generations / 0 complete context tasks / 0 context asset tasks / 1 runtime links / 1 context tool links / 1 graph nodes / 2 graph edges" {
		t.Fatalf("context readiness with cross-task links = %#v, want partial without complete context task", got)
	}

	graphOnlyCompleteContextTask := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime"},
		{"asset_type": "agent_task"},
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:20", "status": "completed", "metadata": map[string]any{"tool_name": "context.generate"}},
	}, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
		"edges": []any{
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "ai_runtime:30", "relation_type": "uses_runtime"},
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "agent_tool_call:20", "relation_type": "records_tool_call"},
		},
	})
	if got := readinessByKey(t, graphOnlyCompleteContextTask, "context"); got.Status != "partial" || got.Evidence != "2 context assets / 1 context generations / 1 complete context tasks / 0 context asset tasks / 1 runtime links / 1 context tool links / 1 graph nodes / 2 graph edges" {
		t.Fatalf("context readiness with graph-only complete task = %#v, want partial without canonical context task", got)
	}

	withGeneration := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ai_runtime", "source_id": "30"},
		{"asset_type": "agent_task", "source_id": "10"},
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:20", "status": "completed", "metadata": map[string]any{"tool_name": "context.generate"}},
	}, nil, nil, map[string]any{
		"nodes": []any{map[string]any{"id": "project:1"}},
		"edges": []any{
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "ai_runtime:30", "relation_type": "uses_runtime"},
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "agent_tool_call:20", "relation_type": "records_tool_call"},
		},
	})
	if got := readinessByKey(t, withGeneration, "context"); got.Status != "ready" || got.Evidence != "2 context assets / 1 context generations / 1 complete context tasks / 1 context asset tasks / 1 runtime links / 1 context tool links / 1 graph nodes / 2 graph edges" {
		t.Fatalf("context readiness with complete task graph = %#v, want ready", got)
	}
}
