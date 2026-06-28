package main

import (
	"fmt"
	"strings"
)

func releaseDemoImportPlan(projectSlug, publicOrigin string) (string, error) {
	projectSlug = strings.ToLower(strings.TrimSpace(projectSlug))
	if !isSafeProjectSlug(projectSlug) {
		return "", fmt.Errorf("project slug must contain letters or numbers and may include internal dot, underscore, or hyphen")
	}
	origin, err := normalizePublicCallbackOrigin(publicOrigin)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Live Demo Import Plan\n\n")
	fmt.Fprintf(&b, "Project slug: `%s`\n\n", projectSlug)
	fmt.Fprintf(&b, "Public origin: `%s`\n\n", origin)
	fmt.Fprintf(&b, "## Local Validation\n\n")
	fmt.Fprintf(&b, "- Project slug is a safe local identifier for matching the ASSOPS project row.\n")
	fmt.Fprintf(&b, "- Public origin is a clean HTTPS origin suitable for provider callback registration.\n")
	fmt.Fprintf(&b, "- This plan does not call providers, run Git, create repositories, write ASSOPS rows, or read credentials.\n\n")
	fmt.Fprintf(&b, "## Live Import Sequence\n\n")
	for index, step := range []string{
		"Create or select the real Gitea source repository and GitHub mirror repository in the provider UIs.",
		"Create or import the ASSOPS project and attach exactly one source repository plus one mirror repository as project-owned assets.",
		"Add Gitea and GitHub remotes in ASSOPS with provider kind, owner/name metadata, and redacted/canonical remote identifiers only.",
		"Define the RepoSyncAsset from the Gitea remote to the GitHub remote, then run a manual low-risk sync through ASSOPS.",
		"Configure provider callbacks using the public origin and record one sanitized callback rehearsal snapshot after observed events exist.",
		"Run `assops-tool db sync-assets` after import/migration changes, then run `assops-tool db record-demo-readiness-snapshot --project-slug " + projectSlug + " --dry-run`.",
		"Record the non-dry-run readiness snapshot only after the graph-backed project/repository/remote evidence is complete.",
	} {
		fmt.Fprintf(&b, "%d. %s\n", index+1, step)
	}
	fmt.Fprintf(&b, "\n## Required Evidence\n\n")
	for _, item := range []string{
		"project asset and project graph node",
		"one project-owned repository asset",
		"at least two project-owned Git remote assets",
		"Gitea source remote and GitHub mirror remote provider labels",
		"RepoSyncAsset relation from repository to source and target remotes",
		"manual sync operation linked to the RepoSyncAsset",
		"provider callback event linked to RepoSyncAsset or sync operation",
		"sanitized first-version readiness snapshot",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## Suppressed Material\n\n")
	for _, item := range []string{
		"remote clone URLs",
		"provider tokens or webhook secrets",
		"provider request/response bodies",
		"raw headers or payloads",
		"Git stdout/stderr",
		"commit SHAs, branch names, tag names, and workflow URLs",
		"operator notes containing provider details",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## No-Call Boundary\n\n")
	fmt.Fprintf(&b, "- This plan is local documentation only; it does not create/import rows, call Gitea/GitHub, run Git, replay webhooks, or record snapshots.\n")
	fmt.Fprintf(&b, "- Provider repository creation, remote credential binding, webhook configuration, and live sync execution remain operator-owned staging tasks.\n\n")
	fmt.Fprintf(&b, "## Verification Commands\n\n```bash\n")
	fmt.Fprintf(&b, "assops-tool project readiness\n")
	fmt.Fprintf(&b, "assops-tool db record-demo-readiness-snapshot --project-slug %q --dry-run\n", projectSlug)
	fmt.Fprintf(&b, "assops-tool db record-demo-readiness-snapshot --project-slug %q\n", projectSlug)
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "Run the non-dry-run snapshot only after the dry-run proof shows complete graph-backed project, repository, and remote evidence.\n")
	return b.String(), nil
}

func releasePodLogRehearsalPlan(projectSlug, publicOrigin, environment, namespace string) (string, error) {
	projectSlug = strings.ToLower(strings.TrimSpace(projectSlug))
	if !isSafeProjectSlug(projectSlug) {
		return "", fmt.Errorf("project slug must contain letters or numbers and may include only internal dot, underscore, or hyphen")
	}
	origin, err := normalizePublicCallbackOrigin(publicOrigin)
	if err != nil {
		return "", err
	}
	environment = strings.ToLower(strings.TrimSpace(environment))
	if !isSafeProjectSlug(environment) {
		return "", fmt.Errorf("environment must contain letters or numbers and may include only internal dot, underscore, or hyphen")
	}
	namespace = strings.TrimSpace(namespace)
	if hasUppercaseASCII(namespace) {
		return "", fmt.Errorf("namespace must be a lowercase Kubernetes DNS label")
	}
	if !isSafeKubernetesNamespace(namespace) {
		return "", fmt.Errorf("namespace must be a Kubernetes DNS label")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Argo Pod Log Rehearsal Plan\n\n")
	fmt.Fprintf(&b, "Project slug: `%s`\n\n", projectSlug)
	fmt.Fprintf(&b, "Public origin: `%s`\n\n", origin)
	fmt.Fprintf(&b, "Environment: `%s`\n\n", environment)
	fmt.Fprintf(&b, "Namespace: `%s`\n\n", namespace)
	fmt.Fprintf(&b, "## Local Validation\n\n")
	fmt.Fprintf(&b, "- Project slug and environment are safe local identifiers for release evidence matching.\n")
	fmt.Fprintf(&b, "- Namespace is a Kubernetes DNS label and is recorded only as scoped metadata.\n")
	fmt.Fprintf(&b, "- Public origin is a clean HTTPS origin for ASSOPS operator access.\n")
	fmt.Fprintf(&b, "- This plan does not read kubeconfig, create Kubernetes clients, call Argo/Kubernetes, open log streams, or write ASSOPS rows.\n\n")
	fmt.Fprintf(&b, "## Live Rehearsal Sequence\n\n")
	for index, step := range []string{
		"Confirm the deployment target in ASSOPS belongs to the selected project, environment, and namespace.",
		"Bind a namespace-scoped kubeconfig secret out of band and confirm it can only read pods/logs in the selected namespace.",
		"Review token subject and RBAC permissions before any live log retrieval backend is enabled.",
		"Create the ASSOPS `argo.pod_logs` approval request with pod, container, tail, and since controls.",
		"After approval, retrieve a short low-risk pod log sample through the live backend once it exists.",
		"Record only sanitized result metadata: operation id, approval id, target id, pod/container identifiers, status, line count, truncation flag, and review statuses.",
		"Record the local pod-log audit snapshot after terminal sanitized evidence is visible in ASSOPS.",
	} {
		fmt.Fprintf(&b, "%d. %s\n", index+1, step)
	}
	fmt.Fprintf(&b, "\n## Required Evidence\n\n")
	for _, item := range []string{
		"deployment target linked to project and Argo app",
		"namespace-scoped kubeconfig binding reviewed",
		"token subject review completed",
		"RBAC read pods/logs review completed",
		"operation approval recorded for argo.pod_logs",
		"pod and container scope confirmation",
		"log redaction review completed",
		"sanitized pod-log audit snapshot status",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## Suppressed Material\n\n")
	for _, item := range []string{
		"kubeconfig bodies",
		"cluster tokens or authorization headers",
		"client certificates or private keys",
		"raw Kubernetes or Argo responses",
		"pod environment variables and secret mounts",
		"raw log bodies and redacted log bodies",
		"provider URLs, operator notes, and incident details",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## No-Call Boundary\n\n")
	fmt.Fprintf(&b, "- This plan is local documentation only; it does not read kubeconfig, call Kubernetes or Argo, open a log stream, create approvals, enqueue workers, record snapshots, or store log bodies.\n")
	fmt.Fprintf(&b, "- Namespace-scoped credential binding, RBAC review, live log retrieval, result redaction, and audit snapshot recording remain operator-owned staging tasks.\n\n")
	fmt.Fprintf(&b, "## Verification Commands\n\n```bash\n")
	fmt.Fprintf(&b, "assops-tool project readiness\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: Project -> Argo -> deployment target -> request pod log audit\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: Project -> Argo -> Record pod-log audit snapshot after terminal sanitized evidence\n")
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "Run the live retrieval rehearsal only after the target environment, namespace, kubeconfig scope, RBAC review, approval, and redaction review are confirmed out of band.\n")
	return b.String(), nil
}

func releaseSSHRehearsalPlan(projectSlug, environment string) (string, error) {
	projectSlug = strings.ToLower(strings.TrimSpace(projectSlug))
	if !isSafeProjectSlug(projectSlug) {
		return "", fmt.Errorf("project slug must contain letters or numbers and may include only internal dot, underscore, or hyphen")
	}
	environment = strings.ToLower(strings.TrimSpace(environment))
	if !isSafeProjectSlug(environment) {
		return "", fmt.Errorf("environment must contain letters or numbers and may include only internal dot, underscore, or hyphen")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS SSH Target Rehearsal Plan\n\n")
	fmt.Fprintf(&b, "Project slug: `%s`\n\n", projectSlug)
	fmt.Fprintf(&b, "Environment: `%s`\n\n", environment)
	fmt.Fprintf(&b, "## Local Validation\n\n")
	fmt.Fprintf(&b, "- Project slug and environment are safe local identifiers for matching ASSOPS release evidence.\n")
	fmt.Fprintf(&b, "- This plan intentionally does not accept hostnames, usernames, SSH key paths, runbook URLs, fixture IDs, operator names, or command text as inputs.\n")
	fmt.Fprintf(&b, "- This plan does not read SSH keys, read known_hosts, open sockets, start SSH processes, enqueue workers, create approvals, write operation logs, or record snapshots.\n\n")
	fmt.Fprintf(&b, "## Live Rehearsal Sequence\n\n")
	for index, step := range []string{
		"Confirm the ASSOPS project and environment map to the intended authorized target machine outside this document.",
		"Register or review the SSH machine row in ASSOPS with a configured secret reference and constrained known_hosts material.",
		"Request the approval-gated `ssh.verify` rehearsal and wait for terminal sanitized exit-code evidence.",
		"Request one low-risk `ssh.exec` rehearsal command through ASSOPS after approval and wait for terminal sanitized exit-code evidence.",
		"Verify that operation runs, SSH command-run assets, and operation-to-command-to-machine graph chains exist for both verify and exec.",
		"Register the target-environment proof only after verify and exec evidence, live controls, and operator attestation are ready.",
		"Record the broader SSH rehearsal attestation snapshot after the canonical asset graph has been synced.",
	} {
		fmt.Fprintf(&b, "%d. %s\n", index+1, step)
	}
	fmt.Fprintf(&b, "\n## Required Evidence\n\n")
	for _, item := range []string{
		"project-owned ssh_machine or host asset",
		"approval request for ssh.verify or ssh.exec",
		"completed ssh.verify command-run evidence with exit-code metadata",
		"completed ssh.exec command-run evidence with exit-code metadata",
		"operation_run to ssh_command_run graph edge for verify",
		"operation_run to ssh_command_run graph edge for exec",
		"ssh_command_run to ssh_machine graph edge for both runs",
		"target-environment proof snapshot status",
		"ssh rehearsal attestation snapshot status",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## Suppressed Material\n\n")
	for _, item := range []string{
		"hostnames, IP addresses, usernames, and ports",
		"SSH private keys, public keys, known_hosts bodies, and key paths",
		"commands, arguments, stdout, stderr, exit errors, and raw adapter output",
		"runbook URLs, fixture identifiers, environment identifiers beyond the safe label, and operator identity",
		"approval notes, incident details, tokens, passwords, cookies, and session material",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## No-Call Boundary\n\n")
	fmt.Fprintf(&b, "- This plan is local documentation only; it does not probe environments, read key material, start SSH, create approvals, enqueue workers, write operation logs, sync assets, or record snapshots.\n")
	fmt.Fprintf(&b, "- Machine registration, credential binding, approval, verify/exec execution, environment proof, and snapshot recording remain operator-owned staging tasks.\n\n")
	fmt.Fprintf(&b, "## Verification Commands\n\n```bash\n")
	fmt.Fprintf(&b, "assops-tool project readiness\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: SSH -> machine -> rehearsal preview\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: SSH -> machine -> Register target-environment proof after completed verify and exec evidence\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: SSH -> machine -> Record rehearsal snapshot after canonical graph evidence is synced\n")
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "Run real verify/exec only after the target environment, authorized machine, secret reference, known_hosts scope, approval policy, and output-redaction review are confirmed out of band.\n")
	return b.String(), nil
}
