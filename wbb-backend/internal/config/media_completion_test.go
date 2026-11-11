package config

import (
	"strings"
	"testing"
)

func TestMediaRequiresSeparatePinnedCompletionConfiguration(t *testing.T) {
	t.Setenv("WBB_ENV", "dev")
	t.Setenv("WBB_DATABASE_URL", "postgres://example.invalid/db")
	t.Setenv("WBB_MEDIA_WHIP_URL", "http://mediamtx:8889")
	t.Setenv("WBB_MEDIA_SECRET", strings.Repeat("a", 64))
	t.Setenv("WBB_COMPLETION_URL", "")
	t.Setenv("WBB_COMPLETION_KEY_HEX", "")
	if _, err := Load(); err == nil {
		t.Fatal("media accepted without completion contract")
	}
	t.Setenv("WBB_COMPLETION_URL", "http://mediamtx:9998")
	t.Setenv("WBB_COMPLETION_ROOT", "root-a")
	t.Setenv("WBB_COMPLETION_SLOT", "slot-a")
	t.Setenv("WBB_COMPLETION_KEY_HEX", strings.Repeat("b", 64))
	cfg, err := Load()
	if err != nil || len(cfg.MediaCompletionKey) != 32 || cfg.MediaCompletionRoot != "root-a" {
		t.Fatalf("pinned media: %v", err)
	}
	t.Setenv("WBB_COMPLETION_URL", "http://mediamtx:9998/")
	if _, err := Load(); err != nil {
		t.Fatalf("trailing slash origin rejected by config: %v", err)
	}
	for _, invalid := range []string{"http://other.test/path", "https://mediamtx:9998", "http://user@mediamtx:9998"} {
		t.Setenv("WBB_COMPLETION_URL", invalid)
		if _, err := Load(); err == nil {
			t.Fatalf("unsafe completion URL accepted: %s", invalid)
		}
	}
}
