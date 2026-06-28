package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFetchArgoApps(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.URL.Path; got != "/api/v1/applications" {
			t.Fatalf("path = %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "100" {
			t.Fatalf("limit = %q, want 100", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"items": [{
					"metadata": {
						"name": "assops-api",
						"namespace": "argocd",
						"labels": { "team": "platform" }
					},
					"status": {
						"sync": { "status": "Synced", "revision": "abc123" },
						"health": { "status": "Healthy" },
						"summary": { "images": ["registry.example.com/assops-api:abc123"] }
					}
				}]
			}`)),
		}, nil
	})}

	syncer := &ArgoSyncer{HTTPClient: client}
	apps, err := syncer.fetchApps(context.Background(), &GormArgoConnection{
		ServerURL: "https://93.184.216.34",
		Config:    JSONValue{Data: map[string]any{"token": "test-token"}},
	})
	if err != nil {
		t.Fatalf("fetchApps returned error: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("len(apps) = %d, want 1", len(apps))
	}
	if apps[0].Name != "assops-api" || apps[0].Namespace != "argocd" || apps[0].Status != "Synced" {
		t.Fatalf("unexpected app: %+v", apps[0])
	}
	if apps[0].Metadata["health_status"] != "Healthy" {
		t.Fatalf("health_status = %v, want Healthy", apps[0].Metadata["health_status"])
	}
	if apps[0].Metadata["revision"] != "abc123" {
		t.Fatalf("revision = %v, want abc123", apps[0].Metadata["revision"])
	}
	images := stringSliceFromAny(apps[0].Metadata["images"])
	if len(images) != 1 || images[0] != "registry.example.com/assops-api:abc123" {
		t.Fatalf("images = %#v", images)
	}
}

func TestFetchArgoAppsFollowsContinueToken(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			if got := r.URL.Query().Get("continue"); got != "" {
				t.Fatalf("first continue = %q, want empty", got)
			}
			return jsonResponse(`{
				"metadata": { "continue": "next-page" },
				"items": [{
					"metadata": { "name": "first", "namespace": "argocd" },
					"status": { "sync": { "status": "Synced" } }
				}]
			}`), nil
		}
		if got := r.URL.Query().Get("continue"); got != "next-page" {
			t.Fatalf("second continue = %q, want next-page", got)
		}
		return jsonResponse(`{
			"metadata": {},
			"items": [{
				"metadata": { "name": "second", "namespace": "argocd" },
				"status": { "sync": { "status": "OutOfSync" } }
			}]
		}`), nil
	})}

	syncer := &ArgoSyncer{HTTPClient: client}
	apps, err := syncer.fetchApps(context.Background(), &GormArgoConnection{ServerURL: "https://93.184.216.34"})
	if err != nil {
		t.Fatalf("fetchApps returned error: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	if len(apps) != 2 || apps[0].Name != "first" || apps[1].Name != "second" {
		t.Fatalf("unexpected apps: %+v", apps)
	}
}

func TestArgoTokenFallsBackToEnvWhenEnabled(t *testing.T) {
	t.Setenv("ASSOPS_ARGO_READ_TOKEN", "env-token")
	got := argoToken(&GormArgoConnection{Config: JSONValue{Data: map[string]any{"use_env_token": true}}})
	if got != "env-token" {
		t.Fatalf("argoToken = %q, want env-token", got)
	}
}

func TestArgoTokenDoesNotUseEnvByDefault(t *testing.T) {
	t.Setenv("ASSOPS_ARGO_READ_TOKEN", "env-token")
	got := argoToken(&GormArgoConnection{Config: JSONValue{Data: map[string]any{}}})
	if got != "" {
		t.Fatalf("argoToken = %q, want empty token", got)
	}
}

func TestFetchArgoAppsRejectsInvalidURL(t *testing.T) {
	syncer := NewArgoSyncer()
	_, err := syncer.fetchApps(context.Background(), &GormArgoConnection{ServerURL: "file:///tmp/argocd"})
	if err == nil {
		t.Fatal("expected invalid URL to fail")
	}
}

func TestFetchArgoAppsRejectsPrivateAddress(t *testing.T) {
	syncer := NewArgoSyncer()
	_, err := syncer.fetchApps(context.Background(), &GormArgoConnection{ServerURL: "http://169.254.169.254"})
	if err == nil {
		t.Fatal("expected private URL to fail")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
