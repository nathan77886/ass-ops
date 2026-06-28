package main

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

func isSafeBackupPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "..") {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '/', '.', '_', '@', '=', ':', '+', '-':
			continue
		default:
			return false
		}
	}
	return true
}

func isSafeCronExpression(value string) bool {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) != 5 {
		return false
	}
	for _, field := range fields {
		if field == "" {
			return false
		}
		for _, r := range field {
			if (r >= '0' && r <= '9') || r == '*' || r == ',' || r == '-' || r == '/' {
				continue
			}
			return false
		}
	}
	return true
}

func validateReleaseHelmValuesFile(path, owner, version string) (string, error) {
	expected, err := releaseHelmValues(owner, version)
	if err != nil {
		return "", err
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("checking Helm values path: %w", err)
	}
	for _, snippet := range releaseHelmValuesRequiredSnippets(expected) {
		if !strings.Contains(string(actual), snippet) {
			return "", fmt.Errorf("Helm values overlay does not match GHCR owner/version; regenerate it with release helm-values")
		}
	}
	if !strings.Contains(string(actual), "\n  commit: ") || !strings.Contains(string(actual), "\n  buildTime: ") {
		return "", fmt.Errorf("Helm values overlay does not include release health metadata; regenerate it with release helm-values")
	}
	sum := sha256.Sum256(actual)
	return fmt.Sprintf("%x", sum), nil
}

func releaseHelmValuesRequiredSnippets(values string) []string {
	var snippets []string
	for _, line := range strings.Split(values, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "commit:") || strings.HasPrefix(trimmed, "buildTime:") {
			continue
		}
		snippets = append(snippets, line)
	}
	return snippets
}

func normalizePublicCallbackOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("public origin is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parsing public origin: %w", err)
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("public origin must use https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("public origin must not include userinfo")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("public origin host is required")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("public origin must not include a path")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("public origin must not include query or fragment")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return "", fmt.Errorf("public origin host is required")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return "", fmt.Errorf("public origin must use a public staging hostname")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return "", fmt.Errorf("public origin must not use localhost, private, link-local, or unspecified IPs")
		}
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func isPublicIP(ip net.IP) bool {
	return !ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified() &&
		!ip.IsMulticast()
}

type simpleYAMLLevel struct {
	indent int
	key    string
}

func readSimpleHelmValues(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading Helm values: %w", err)
	}
	stack := []simpleYAMLLevel{}
	values := map[string]string{}
	for lineNo, rawLine := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(rawLine) == "" || strings.HasPrefix(strings.TrimSpace(rawLine), "#") {
			continue
		}
		if strings.Contains(rawLine, "\t") {
			return nil, fmt.Errorf("Helm values line %d uses tabs; use spaces", lineNo+1)
		}
		indent := leadingSpaces(rawLine)
		line := strings.TrimSpace(stripYAMLComment(rawLine))
		if line == "" {
			continue
		}
		for len(stack) > 0 && indent <= stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}
		if strings.HasPrefix(line, "- ") {
			pathKey := strings.Join(yamlPath(stack), ".")
			if pathKey == "" {
				return nil, fmt.Errorf("Helm values line %d has list item without parent", lineNo+1)
			}
			item := trimYAMLScalar(strings.TrimSpace(strings.TrimPrefix(line, "- ")))
			if item == "" {
				return nil, fmt.Errorf("Helm values line %d has empty list item", lineNo+1)
			}
			if values[pathKey] == "" {
				values[pathKey] = item
			} else {
				values[pathKey] += "," + item
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("Helm values line %d must be key: value", lineNo+1)
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " .\r\n") {
			return nil, fmt.Errorf("Helm values line %d has invalid key", lineNo+1)
		}
		value = strings.TrimSpace(value)
		pathParts := append(yamlPath(stack), key)
		if value == "" {
			stack = append(stack, simpleYAMLLevel{indent: indent, key: key})
			continue
		}
		values[strings.Join(pathParts, ".")] = trimYAMLScalar(value)
	}
	return values, nil
}

func yamlPath(stack []simpleYAMLLevel) []string {
	out := make([]string, 0, len(stack))
	for _, item := range stack {
		out = append(out, item.key)
	}
	return out
}

func leadingSpaces(value string) int {
	count := 0
	for _, char := range value {
		if char != ' ' {
			return count
		}
		count++
	}
	return count
}

func stripYAMLComment(value string) string {
	inSingle := false
	inDouble := false
	for index, char := range value {
		switch char {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (index == 0 || value[index-1] == ' ') {
				return value[:index]
			}
		}
	}
	return value
}

func trimYAMLScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"') {
			return strings.TrimSpace(value[1 : len(value)-1])
		}
	}
	return value
}

func requiredExternalSecretKeys() []string {
	return []string{
		"DATABASE_URL",
		"ASSOPS_JWT_SECRET",
		"ASSOPS_WEBHOOK_SECRET_KEY",
		"ASSOPS_ADMIN_EMAIL",
		"ASSOPS_ADMIN_PASSWORD",
		"ASSOPS_APPROVAL_WEBHOOK_TOKEN",
		"ASSOPS_GITHUB_ACTIONS_READ_TOKEN",
		"ASSOPS_ARGO_READ_TOKEN",
	}
}

func isOwnerRepo(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	return isContainerPathSegment(strings.ToLower(parts[0])) && isContainerPathSegment(strings.ToLower(parts[1]))
}

func isSafeProjectSlug(value string) bool {
	if !isContainerPathSegment(value) {
		return false
	}
	if strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			return true
		}
	}
	return false
}
