package config

import "testing"

func TestGoogleConfiguration(t *testing.T) {
	t.Setenv("WBB_ENV", "dev")
	t.Setenv("WBB_DATABASE_URL", "postgres://example.invalid/db")
	t.Setenv("WBB_GOOGLE_CLIENT_ID", "")
	t.Setenv("WBB_GOOGLE_CLIENT_SECRET", "")
	t.Setenv("WBB_GOOGLE_CALLBACK_URI", "")
	if _, err := Load(); err != nil {
		t.Fatalf("disabled: %v", err)
	}
	t.Setenv("WBB_GOOGLE_CLIENT_ID", "test-client")
	if _, err := Load(); err == nil {
		t.Fatal("partial settings accepted")
	}
	t.Setenv("WBB_GOOGLE_CLIENT_SECRET", "test-secret-placeholder")
	for _, uri := range []string{"http://evil.example/api/v1/auth/google/callback", "http://127.0.0.1:8080/other", "http://127.0.0.1:8080/api/v1/auth/google/callback?next=evil"} {
		t.Setenv("WBB_GOOGLE_CALLBACK_URI", uri)
		if _, err := Load(); err == nil {
			t.Fatalf("bad URI accepted: %s", uri)
		}
	}
	t.Setenv("WBB_GOOGLE_CALLBACK_URI", "http://127.0.0.1:8080/api/v1/auth/google/callback")
	if _, err := Load(); err != nil {
		t.Fatalf("dev loopback: %v", err)
	}
	t.Setenv("WBB_ENV", "prod")
	t.Setenv("WBB_JWT_SECRET", "test-only-key-at-least-thirty-two-bytes-long")
	if _, err := Load(); err == nil {
		t.Fatal("prod HTTP accepted")
	}
	t.Setenv("WBB_GOOGLE_CALLBACK_URI", "https://live.wombatburrow.com/api/v1/auth/google/callback")
	if _, err := Load(); err != nil {
		t.Fatalf("prod HTTPS: %v", err)
	}
}
