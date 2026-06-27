package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type ArgoSyncer struct {
	HTTPClient        *http.Client
	SecretKeyMaterial string
}

type ArgoSyncResult struct {
	ProjectID    string
	ConnectionID string
	ServerURL    string
	Apps         []ArgoAppInput
}

type ArgoAppInput struct {
	Name        string
	Namespace   string
	Status      string
	Environment string
	ClusterName string
	Metadata    map[string]any
}

func NewArgoSyncer() *ArgoSyncer {
	return &ArgoSyncer{SecretKeyMaterial: argoCredentialSecretKeyMaterial()}
}

func (s *ArgoSyncer) SyncApps(ctx context.Context, db *sqlx.DB, opID string) (*ArgoSyncResult, error) {
	op, err := queryOne(ctx, db, "SELECT * FROM operation_runs WHERE id=$1", opID)
	if err != nil {
		return nil, err
	}
	input := mapFromAny(op["input"])
	connectionID := strings.TrimSpace(fmt.Sprint(input["argo_connection_id"]))
	if connectionID == "" || connectionID == "<nil>" {
		return nil, fmt.Errorf("operation is missing argo_connection_id")
	}
	connection, err := rawArgoConnection(ctx, db, connectionID)
	if err != nil {
		return nil, err
	}
	result := &ArgoSyncResult{
		ProjectID:    connection.ProjectID,
		ConnectionID: connection.ID,
		ServerURL:    connection.ServerURL,
	}
	apps, err := s.fetchApps(ctx, connection, db)
	if err != nil {
		return result, err
	}
	result.Apps = apps
	return result, nil
}

func rawArgoConnection(ctx context.Context, db *sqlx.DB, id string) (*ArgoConnection, error) {
	var connection ArgoConnection
	err := db.GetContext(ctx, &connection, "SELECT * FROM argo_connections WHERE id=$1", id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &connection, nil
}

func (s *ArgoSyncer) fetchApps(ctx context.Context, connection *ArgoConnection, dbOpt ...*sqlx.DB) ([]ArgoAppInput, error) {
	var db *sqlx.DB
	if len(dbOpt) > 0 {
		db = dbOpt[0]
	}
	baseURL := strings.TrimRight(connection.ServerURL, "/")
	if !validPublicHTTPURL(ctx, baseURL) {
		return nil, fmt.Errorf("Argo server_url must be a public http or https URL")
	}
	endpoint, err := url.Parse(baseURL + "/api/v1/applications")
	if err != nil {
		return nil, err
	}
	token, err := s.argoToken(ctx, db, connection)
	if err != nil {
		return nil, err
	}
	client := s.HTTPClient
	if client == nil {
		client = argoHTTPClient(connection)
	}

	apps := make([]ArgoAppInput, 0, 128)
	continueToken := ""
	for page := 0; page < 100; page++ {
		pageURL := *endpoint
		query := pageURL.Query()
		query.Set("limit", "100")
		if continueToken != "" {
			query.Set("continue", continueToken)
		}
		pageURL.RawQuery = query.Encode()

		payload, err := s.fetchAppsPage(ctx, client, pageURL.String(), token)
		if err != nil {
			return nil, err
		}
		apps = append(apps, argoAppsFromPayload(payload)...)
		continueToken = strings.TrimSpace(payload.Metadata.Continue)
		if continueToken == "" {
			return apps, nil
		}
	}
	return nil, fmt.Errorf("Argo applications API returned too many pages")
}

type argoAppsPayload struct {
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []struct {
		Metadata struct {
			Name      string         `json:"name"`
			Namespace string         `json:"namespace"`
			Labels    map[string]any `json:"labels"`
		} `json:"metadata"`
		Status struct {
			Sync struct {
				Status   string `json:"status"`
				Revision string `json:"revision"`
			} `json:"sync"`
			Health struct {
				Status string `json:"status"`
			} `json:"health"`
			Summary struct {
				Images []string `json:"images"`
			} `json:"summary"`
		} `json:"status"`
	} `json:"items"`
}

func (s *ArgoSyncer) fetchAppsPage(ctx context.Context, client *http.Client, pageURL, token string) (argoAppsPayload, error) {
	var payload argoAppsPayload
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return payload, err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		return payload, fmt.Errorf("querying Argo applications: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return payload, fmt.Errorf("Argo applications API returned %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return payload, fmt.Errorf("decoding Argo applications response: %w", err)
	}
	return payload, nil
}

func argoAppsFromPayload(payload argoAppsPayload) []ArgoAppInput {
	apps := make([]ArgoAppInput, 0, len(payload.Items))
	for _, item := range payload.Items {
		if item.Metadata.Name == "" {
			continue
		}
		status := item.Status.Sync.Status
		if status == "" {
			status = "unknown"
		}
		apps = append(apps, ArgoAppInput{
			Name:        item.Metadata.Name,
			Namespace:   item.Metadata.Namespace,
			Status:      status,
			Environment: argoAppEnvironment(item.Metadata.Labels, item.Metadata.Namespace),
			ClusterName: argoAppClusterName(item.Metadata.Labels),
			Metadata: map[string]any{
				"health_status": item.Status.Health.Status,
				"sync_status":   item.Status.Sync.Status,
				"revision":      item.Status.Sync.Revision,
				"images":        item.Status.Summary.Images,
				"labels":        item.Metadata.Labels,
			},
		})
	}
	return apps
}

func argoAppEnvironment(labels map[string]any, namespace string) string {
	for _, key := range []string{"environment", "env", "app.kubernetes.io/environment", "assops/environment"} {
		if value := strings.TrimSpace(fmt.Sprint(labels[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	if namespace = strings.TrimSpace(namespace); namespace != "" {
		return namespace
	}
	return "default"
}

func argoAppClusterName(labels map[string]any) string {
	for _, key := range []string{"cluster", "cluster_name", "argocd.argoproj.io/cluster-name", "assops/cluster"} {
		if value := strings.TrimSpace(fmt.Sprint(labels[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func argoHTTPClient(connection *ArgoConnection) *http.Client {
	config := mapFromAny(connection.Config.Data)
	insecure := false
	if value, ok := config["insecure_skip_verify"].(bool); ok {
		insecure = value
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialPublicOnly
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: transport}
}

func dialPublicOnly(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", strings.Trim(host, "[]"))
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return nil, fmt.Errorf("refusing to dial non-public Argo address %s", ip)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("Argo host resolved to no addresses")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

func (s *ArgoSyncer) argoToken(ctx context.Context, db *sqlx.DB, connection *ArgoConnection) (string, error) {
	config := mapFromAny(connection.Config.Data)
	for _, key := range []string{"token", "access_token", "ARGO_TOKEN"} {
		if value := strings.TrimSpace(fmt.Sprint(config[key])); value != "" && value != "<nil>" {
			return value, nil
		}
	}
	if connection.CredentialID.Valid && strings.TrimSpace(connection.CredentialID.String) != "" {
		if db == nil {
			return "", fmt.Errorf("Argo credential lookup requires database")
		}
		ciphertext, err := s.argoCredentialCiphertext(ctx, db, connection)
		if err != nil {
			return "", err
		}
		return decryptArgoCredentialSecret(ciphertext, s.SecretKeyMaterial)
	}
	if value, ok := config["use_env_token"].(bool); !ok || !value {
		return "", nil
	}
	return strings.TrimSpace(os.Getenv("ASSOPS_ARGO_READ_TOKEN")), nil
}

func argoToken(connection *ArgoConnection) string {
	token, err := NewArgoSyncer().argoToken(context.Background(), nil, connection)
	if err != nil {
		return ""
	}
	return token
}

func (s *ArgoSyncer) argoCredentialCiphertext(ctx context.Context, db *sqlx.DB, connection *ArgoConnection) (string, error) {
	var ciphertext string
	err := db.GetContext(ctx, &ciphertext, `
		SELECT secret_ciphertext
		FROM connection_credentials
		WHERE id=$1 AND project_id=$2 AND kind='argo_token'`, connection.CredentialID.String, connection.ProjectID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNotFound
		}
		return "", err
	}
	if strings.TrimSpace(ciphertext) == "" {
		return "", fmt.Errorf("Argo credential has no token configured")
	}
	return ciphertext, nil
}

func decryptArgoCredentialSecret(ciphertext, material string) (string, error) {
	parts := strings.Split(strings.TrimSpace(ciphertext), ":")
	if len(parts) == 3 && parts[0] == "v1" {
		parts = parts[1:]
	} else if len(parts) != 2 {
		return "", fmt.Errorf("invalid credential ciphertext")
	}
	nonce, err := hex.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decoding credential nonce: %w", err)
	}
	sealed, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decoding credential ciphertext: %w", err)
	}
	sum := sha256.Sum256([]byte("assops:webhook-secret-encryption:" + material))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting credential: %w", err)
	}
	return string(plain), nil
}

func argoCredentialSecretKeyMaterial() string {
	if value := strings.TrimSpace(os.Getenv("ASSOPS_WEBHOOK_SECRET_KEY")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("ASSOPS_JWT_SECRET")); value != "" {
		return value
	}
	return "dev-assops-webhook-change-me"
}
