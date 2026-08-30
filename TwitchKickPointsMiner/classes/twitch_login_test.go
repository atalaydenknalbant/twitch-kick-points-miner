package classes

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginRecoversIdentityFromStoredToken(t *testing.T) {
	login, err := NewTwitchLogin("client-id", "device-id", "", "test-agent", "")
	if err != nil {
		t.Fatalf("NewTwitchLogin returned error: %v", err)
	}

	login.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://id.twitch.tv/oauth2/validate" {
			t.Fatalf("unexpected validation URL: %s", req.URL)
		}
		if got := req.Header.Get("Authorization"); got != "OAuth stored-token" {
			t.Fatalf("unexpected Authorization header: %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"login":"tester","user_id":"12345"}`)),
			Header:     make(http.Header),
		}, nil
	})

	cookiesPath := filepath.Join(t.TempDir(), "twitch.json")
	if err := os.WriteFile(cookiesPath, []byte(`{"auth-token":{"value":"stored-token"}}`), 0o600); err != nil {
		t.Fatalf("write cookie fixture: %v", err)
	}

	if err := login.Login(cookiesPath); err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if got := login.UserID(); got != "12345" {
		t.Fatalf("UserID = %q, want %q", got, "12345")
	}
	if login.Username != "tester" {
		t.Fatalf("Username = %q, want %q", login.Username, "tester")
	}

	data, err := os.ReadFile(cookiesPath)
	if err != nil {
		t.Fatalf("read saved cookies: %v", err)
	}
	store, err := decodeCookieStore(data)
	if err != nil {
		t.Fatalf("decode saved cookies: %v", err)
	}
	if got := store["persistent"].Value; got != "12345" {
		t.Fatalf("persistent cookie = %q, want %q", got, "12345")
	}
}
