package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadQuickLogins(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "accounts.txt")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	got, err := loadQuickLogins(write("# test accounts\n방송자1\tstreamer1@example.test\tplaceholder-pass-1\n\n시청자1\tviewer1@example.test\tplaceholder-pass-2\n"))
	if err != nil || len(got) != 2 || got[0].ID != "streamer1" || got[1].Label != "시청자1" {
		t.Fatalf("valid file: %v %v", got, err)
	}
	if got, err := loadQuickLogins(write("")); err != nil || len(got) != 0 {
		t.Fatalf("empty file must disable: %v %v", got, err)
	}
	for _, bad := range []string{
		"only-two\tfields@example.test\n",
		"라벨\tnot-an-email\tpw\n",
		"라벨\tA@example.test\tpw\n라벨2\ta@example.test\tpw\n",
		"라벨\tx@example.test\t\n",
	} {
		if _, err := loadQuickLogins(write(bad)); err == nil {
			t.Fatalf("accepted invalid file %q", bad)
		}
	}
	if _, err := loadQuickLogins(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
}
