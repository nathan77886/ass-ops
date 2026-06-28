package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func providerErrorMessages(value any) []string {
	switch typed := value.(type) {
	case map[string]any:
		var out []string
		for _, key := range []string{"message", "error", "resource", "code"} {
			if message := strings.TrimSpace(fmt.Sprint(typed[key])); message != "" && message != "<nil>" {
				out = append(out, message)
			}
		}
		for _, key := range []string{"errors", "details"} {
			out = append(out, providerErrorMessages(typed[key])...)
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, providerErrorMessages(item)...)
		}
		return out
	default:
		return nil
	}
}

func providerErrorMessageMeansAlreadyExists(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return message == "already_exists" ||
		message == "already exists" ||
		message == "name already exists" ||
		message == "repository already exists"
}

type externalTemplateProviderConfig struct {
	Provider       string
	APIBase        string
	CreateURL      string
	Owner          string
	RepositoryName string
	Description    string
	TokenEnv       string
	Token          string
	Private        bool
}

func buildExternalTemplateProviderSpec(repo, remote map[string]any) (externalTemplateProviderConfig, bool) {
	provider := strings.ToLower(strings.TrimSpace(firstNonEmptyString(stringFromMap(remote, "provider_type"), stringFromMap(remote, "kind"))))
	if provider != "github" && provider != "gitea" {
		return externalTemplateProviderConfig{}, false
	}
	metadata := mapFromAny(remote["metadata"])
	repoName := firstNonEmptyString(stringFromMap(metadata, "repository_name"), stringFromMap(metadata, "name"), stringFromMap(repo, "repo_key"), stringFromMap(repo, "name"))
	if repoName == "" || !isSafeRepositoryName(repoName) {
		return externalTemplateProviderConfig{}, false
	}
	owner := firstNonEmptyString(stringFromMap(metadata, "owner"), stringFromMap(metadata, "org"), repositoryOwnerFromURL(remoteURLFromRow(remote)))
	tokenEnv := firstNonEmptyString(stringFromMap(metadata, "token_env"), defaultTemplateProviderTokenEnv(provider))
	if templateRemoteUsesProviderAccount(remote, metadata) {
		tokenEnv = firstNonEmptyString(stringFromMap(metadata, "token_env"), stringFromMap(metadata, "provider_account_env"))
	}
	if !safeTemplateProviderTokenEnv(provider, tokenEnv) {
		return externalTemplateProviderConfig{}, false
	}
	visibility := strings.ToLower(strings.TrimSpace(stringFromMap(metadata, "visibility")))
	spec := externalTemplateProviderConfig{
		Provider:       provider,
		APIBase:        firstNonEmptyString(stringFromMap(metadata, "api_base_url"), defaultTemplateProviderAPIBase(provider, remote)),
		Owner:          owner,
		RepositoryName: repoName,
		Description:    firstNonEmptyString(stringFromMap(metadata, "description"), stringFromMap(repo, "description")),
		TokenEnv:       tokenEnv,
		Token:          strings.TrimSpace(os.Getenv(tokenEnv)),
		Private:        templateProviderPrivate(metadata, visibility),
	}
	createURL, ok := templateProviderCreateURL(spec.Provider, spec.APIBase, spec.Owner)
	if !ok {
		return externalTemplateProviderConfig{}, false
	}
	spec.CreateURL = createURL
	return spec, true
}

func templateRemoteUsesProviderAccount(remote, metadata map[string]any) bool {
	return strings.TrimSpace(stringFromMap(remote, "source_account_id")) != "" ||
		strings.TrimSpace(stringFromMap(metadata, "provider_account_id")) != "" ||
		strings.TrimSpace(stringFromMap(metadata, "provider_account_name")) != ""
}

func templateProviderPrivate(metadata map[string]any, visibility string) bool {
	if _, ok := metadata["private"]; ok {
		return boolDefaultFromMap(metadata, "private", true)
	}
	switch visibility {
	case "public":
		return false
	case "internal", "private":
		return true
	default:
		return true
	}
}

func templateRemoteProtectsDefaultBranch(remote map[string]any) bool {
	if boolFromMap(remote, "protected") {
		return true
	}
	metadata := mapFromAny(remote["metadata"])
	return boolFromMap(metadata, "protected") || boolFromMap(metadata, "protected_branch")
}

func templateRemoteAllowsProtectedBranchPush(remote map[string]any) bool {
	metadata := mapFromAny(remote["metadata"])
	return boolFromMap(metadata, "allow_protected_branch_push")
}

func templateRemoteAllowsExistingRepositoryPush(remote map[string]any) bool {
	metadata := mapFromAny(remote["metadata"])
	return boolFromMap(metadata, "allow_existing_repository_push")
}

func safeTemplateProviderTokenEnv(provider, value string) bool {
	value = strings.TrimSpace(value)
	switch provider {
	case "github":
		return value == "ASSOPS_GITHUB_TEMPLATE_TOKEN" || safeTemplateProviderTokenEnvSuffix(value, "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_")
	case "gitea":
		return value == "ASSOPS_GITEA_TEMPLATE_TOKEN" || safeTemplateProviderTokenEnvSuffix(value, "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_")
	default:
		return false
	}
}

func safeTemplateProviderTokenEnvSuffix(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) > len(prefix)+64 {
		return false
	}
	suffix := strings.TrimPrefix(value, prefix)
	if suffix == "" {
		return false
	}
	for _, r := range suffix {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func providerErrorSuffix(body []byte) string {
	message := providerErrorMessage(body)
	if message == "" {
		return ""
	}
	return ": " + truncateProviderError(message, providerDiagnosticErrorLimit)
}

func providerErrorMessage(body []byte) string {
	payload := map[string]any{}
	if err := json.Unmarshal(body, &payload); err == nil {
		for _, key := range []string{"message", "error"} {
			message := strings.TrimSpace(fmt.Sprint(payload[key]))
			if message != "" && message != "<nil>" {
				return message
			}
		}
	}
	return ""
}

func truncateProviderError(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func newTemplateProviderHTTPClient() *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if err := validateTemplateProviderHost(ctx, host); err != nil {
				return nil, err
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(host, port))
		},
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return validateTemplateProviderURL(req.Context(), req.URL.String())
		},
	}
}

func validateTemplateProviderURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("scheme must be http or https")
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("host is required")
	}
	return validateTemplateProviderHost(ctx, parsed.Hostname())
}

func validateTemplateProviderHost(ctx context.Context, host string) error {
	if allowLocalTemplateProviderAPI() && isLoopbackHost(host) {
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return fmt.Errorf("host resolved to no addresses")
	}
	for _, item := range ips {
		if !isPublicTemplateProviderIP(item.IP) {
			return fmt.Errorf("host resolves to non-public address")
		}
	}
	return nil
}

func isPublicTemplateProviderIP(ip net.IP) bool {
	return ip != nil &&
		ip.IsGlobalUnicast() &&
		!ip.IsPrivate() &&
		!ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified()
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func allowLocalTemplateProviderAPI() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API")), "true")
}
