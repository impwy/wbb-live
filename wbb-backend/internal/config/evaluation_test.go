package config

import (
	"strings"
	"testing"
)

func TestEvaluationLoginConfiguration(t *testing.T) {
	t.Setenv("WBB_ENV", "dev")
	t.Setenv("WBB_DATABASE_URL", "postgres://example.invalid/db")
	t.Setenv("WBB_EVAL_LOGIN_ENABLED", "")
	t.Setenv("WBB_EVAL_LOGIN_EMAIL", "")
	t.Setenv("WBB_EVAL_LOGIN_PASSWORD", "")
	if c, err := Load(); err != nil || c.EvaluationLoginEnabled {
		t.Fatalf("default must be disabled: enabled=%t err=%v", c.EvaluationLoginEnabled, err)
	}
	for _, values := range [][3]string{
		{"true", "", ""}, {"", "e@example.test", ""},
		{"false", "e@example.test", "placeholder-password"},
		{"true", "e@example.test", ""}, {"yes", "e@example.test", "placeholder-password"},
	} {
		t.Setenv("WBB_EVAL_LOGIN_ENABLED", values[0])
		t.Setenv("WBB_EVAL_LOGIN_EMAIL", values[1])
		t.Setenv("WBB_EVAL_LOGIN_PASSWORD", values[2])
		if _, err := Load(); err == nil {
			t.Fatalf("partial or contradictory evaluation settings accepted: enabled=%q emailPresent=%t passwordPresent=%t", values[0], values[1] != "", values[2] != "")
		}
	}
	t.Setenv("WBB_EVAL_LOGIN_ENABLED", "true")
	t.Setenv("WBB_EVAL_LOGIN_EMAIL", strings.Repeat("a", 245)+"@example.test")
	t.Setenv("WBB_EVAL_LOGIN_PASSWORD", "placeholder-password")
	if _, err := Load(); err == nil {
		t.Fatal("oversized evaluation email accepted")
	}
	t.Setenv("WBB_EVAL_LOGIN_ENABLED", "true")
	t.Setenv("WBB_EVAL_LOGIN_EMAIL", "e@example.test")
	t.Setenv("WBB_EVAL_LOGIN_PASSWORD", "placeholder-password")
	if c, err := Load(); err != nil || !c.EvaluationLoginEnabled || c.EvaluationLoginEmail != "e@example.test" || c.EvaluationLoginIPLimit < 1 || c.EvaluationLoginAccountLimit < 1 {
		t.Fatalf("complete settings rejected: enabled=%t err=%v", c.EvaluationLoginEnabled, err)
	}
	t.Setenv("WBB_RATE_LIMIT_WINDOW", "500ms")
	if _, err := Load(); err == nil {
		t.Fatal("subsecond Redis rate window accepted")
	}
}
