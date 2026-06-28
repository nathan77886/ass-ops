package main

import (
	"testing"
)

func TestFirstVersionReadinessReportRequiresSSHCommandGraphLinks(t *testing.T) {
	withoutGraphLinks := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host"},
		{"asset_type": "ssh_command_run", "source_id": "10"},
	}, []map[string]any{
		{"operation_type": "ssh.verify"},
		{"operation_type": "ssh.exec"},
	}, nil, map[string]any{"edges": []any{}})
	if got := readinessByKey(t, withoutGraphLinks, "ssh"); got.Status != "partial" || got.Evidence != "1 machines / 1 verify ops / 1 command ops / 1 command assets / 0 complete audit chains / 0 command asset chains / 0 verify chains / 0 run chains" {
		t.Fatalf("ssh readiness without graph links = %#v, want partial with graph evidence", got)
	}

	withoutVerify := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host", "source_id": "30"},
		{"asset_type": "ssh_command_run", "source_id": "20"},
	}, []map[string]any{
		{"id": "10", "operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, withoutVerify, "ssh"); got.Status != "partial" || got.Evidence != "1 machines / 0 verify ops / 1 command ops / 1 command assets / 1 complete audit chains / 1 command asset chains / 0 verify chains / 1 run chains" {
		t.Fatalf("ssh readiness without verify op = %#v, want partial with verify gap", got)
	}

	singleCompleteChain := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host", "source_id": "30"},
		{"asset_type": "ssh_command_run", "source_id": "20"},
	}, []map[string]any{
		{"id": "9", "operation_type": "ssh.verify"},
		{"id": "10", "operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, singleCompleteChain, "ssh"); got.Status != "partial" || got.Evidence != "1 machines / 1 verify ops / 1 command ops / 1 command assets / 1 complete audit chains / 1 command asset chains / 0 verify chains / 1 run chains" {
		t.Fatalf("ssh readiness with only one complete command graph = %#v, want partial until verify and command audits are both represented", got)
	}

	withoutMatchingCommandAssets := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host"},
		{"asset_type": "ssh_command_run", "source_id": "90"},
		{"asset_type": "ssh_command_run", "source_id": "91"},
	}, []map[string]any{
		{"operation_type": "ssh.verify"},
		{"operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "operation_run:11", "to_asset_id": "ssh_command_run:21", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:21", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, withoutMatchingCommandAssets, "ssh"); got.Status != "partial" || got.Evidence != "1 machines / 1 verify ops / 1 command ops / 2 command assets / 2 complete audit chains / 0 command asset chains / 0 verify chains / 0 run chains" {
		t.Fatalf("ssh readiness with unmatched command assets = %#v, want partial without canonical command asset chains", got)
	}

	twoVerifyChainsOnly := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host", "source_id": "30"},
		{"asset_type": "ssh_command_run", "source_id": "20"},
		{"asset_type": "ssh_command_run", "source_id": "21"},
	}, []map[string]any{
		{"id": "10", "operation_type": "ssh.verify"},
		{"id": "11", "operation_type": "ssh.verify"},
		{"id": "12", "operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "operation_run:11", "to_asset_id": "ssh_command_run:21", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:21", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, twoVerifyChainsOnly, "ssh"); got.Status != "partial" || got.Evidence != "1 machines / 2 verify ops / 1 command ops / 2 command assets / 2 complete audit chains / 2 command asset chains / 2 verify chains / 0 run chains" {
		t.Fatalf("ssh readiness with only verify command chains = %#v, want partial until a run command chain is represented", got)
	}

	ready := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host", "source_id": "30"},
		{"asset_type": "ssh_command_run", "source_id": "20"},
		{"asset_type": "ssh_command_run", "source_id": "21"},
	}, []map[string]any{
		{"id": "10", "operation_type": "ssh.verify"},
		{"id": "11", "operation_type": "ssh.command"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "operation_run:11", "to_asset_id": "ssh_command_run:21", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:21", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, ready, "ssh"); got.Status != "ready" || got.Evidence != "1 machines / 1 verify ops / 1 command ops / 2 command assets / 2 complete audit chains / 2 command asset chains / 1 verify chains / 1 run chains" {
		t.Fatalf("ssh readiness with complete verify and command graphs = %#v, want ready", got)
	}

	nativeSSHMachineAsset := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "ssh_machine", "source_id": "30"},
		{"asset_type": "ssh_command_run", "source_id": "20"},
		{"asset_type": "ssh_command_run", "source_id": "21"},
	}, []map[string]any{
		{"id": "10", "operation_type": "ssh.verify"},
		{"id": "11", "operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "operation_run:11", "to_asset_id": "ssh_command_run:21", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:21", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, nativeSSHMachineAsset, "ssh"); got.Status != "ready" || got.Evidence != "1 machines / 1 verify ops / 1 command ops / 2 command assets / 2 complete audit chains / 2 command asset chains / 1 verify chains / 1 run chains" {
		t.Fatalf("ssh readiness with native ssh_machine asset = %#v, want ready", got)
	}

	mixedHostAndSSHMachineAssets := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host", "source_id": "30"},
		{"asset_type": "ssh_machine", "source_id": "40"},
		{"asset_type": "ssh_command_run", "source_id": "20"},
		{"asset_type": "ssh_command_run", "source_id": "21"},
	}, []map[string]any{
		{"id": "10", "operation_type": "ssh.verify"},
		{"id": "11", "operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:20", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
			map[string]any{"from_asset_id": "operation_run:11", "to_asset_id": "ssh_command_run:21", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:21", "to_asset_id": "ssh_machine:40", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, mixedHostAndSSHMachineAssets, "ssh"); got.Status != "ready" || got.Evidence != "2 machines / 1 verify ops / 1 command ops / 2 command assets / 2 complete audit chains / 2 command asset chains / 1 verify chains / 1 run chains" {
		t.Fatalf("ssh readiness with mixed host and ssh_machine assets = %#v, want ready", got)
	}

	crossCommandAggregation := firstVersionReadinessReportWithGraph([]map[string]any{
		{"asset_type": "host"},
		{"asset_type": "ssh_command_run", "source_id": "20"},
		{"asset_type": "ssh_command_run", "source_id": "21"},
	}, []map[string]any{
		{"operation_type": "ssh.verify"},
		{"operation_type": "ssh.exec"},
	}, nil, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "operation_run:10", "to_asset_id": "ssh_command_run:20", "relation_type": "ran_ssh_command"},
			map[string]any{"from_asset_id": "ssh_command_run:21", "to_asset_id": "ssh_machine:30", "relation_type": "executed_on"},
		},
	})
	if got := readinessByKey(t, crossCommandAggregation, "ssh"); got.Status != "partial" || got.Evidence != "1 machines / 1 verify ops / 1 command ops / 2 command assets / 0 complete audit chains / 0 command asset chains / 0 verify chains / 0 run chains" {
		t.Fatalf("ssh readiness with cross-command aggregate links = %#v, want partial without a complete command", got)
	}
}
