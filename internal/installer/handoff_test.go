package installer

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandoffServerHealthStatusAndAnnouncement(t *testing.T) {
	server := newTestHandoffServer(t)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/v1/status")
	if err != nil {
		t.Fatalf("GET /v1/status error = %v", err)
	}
	defer resp.Body.Close()
	var status HandoffStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.State != HandoffWaiting || status.ManifestAccepted {
		t.Fatalf("status = %#v", status)
	}

	announcement := server.Announcement("http://192.0.2.10:8080/")
	if !strings.Contains(announcement, "http://192.0.2.10:8080/v1/install") || !strings.Contains(announcement, "token=test-token") {
		t.Fatalf("announcement = %q", announcement)
	}
}

func TestHandoffServerRequiresTokenAndAcceptsOneManifest(t *testing.T) {
	server := newTestHandoffServer(t)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	manifest := []byte(`{"apiVersion":"katl.install/v1alpha1","kind":"InstallManifest"}`)
	resp := postManifest(t, ts.URL, "", manifest)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST without token status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	resp = postManifest(t, ts.URL, "test-token", manifest)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST valid manifest status = %d, want 200", resp.StatusCode)
	}
	if got := string(server.Manifest()); got != string(manifest) {
		t.Fatalf("stored manifest = %s", got)
	}

	resp = postManifest(t, ts.URL, "test-token", manifest)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second POST status = %d, want 409", resp.StatusCode)
	}
}

func TestHandoffServerValidatesBeforeAccepting(t *testing.T) {
	server := newTestHandoffServer(t)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp := postManifest(t, ts.URL, "test-token", []byte(`{"apiVersion":"wrong","kind":"InstallManifest"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid manifest status = %d, want 400", resp.StatusCode)
	}
	if server.Status().State != HandoffWaiting {
		t.Fatalf("state = %s, want waiting after invalid manifest", server.Status().State)
	}
}

func newTestHandoffServer(t *testing.T) *HandoffServer {
	t.Helper()
	server, err := NewHandoffServer("test-token", nil)
	if err != nil {
		t.Fatalf("NewHandoffServer() error = %v", err)
	}
	return server
}

func postManifest(t *testing.T, baseURL, token string, manifest []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/install", bytes.NewReader(manifest))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/install error = %v", err)
	}
	return resp
}
