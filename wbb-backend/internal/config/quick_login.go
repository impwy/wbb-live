package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"regexp"
	"strings"
)

// QuickLogin is a server-held test account offered as a one-click login.
// Only ID and Label are ever sent to browsers.
type QuickLogin struct {
	ID, Label, Email, Password string
}

var quickLoginID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// loadQuickLogins reads "label<TAB>email<TAB>password" lines. The id is the
// email's local part. Blank lines and lines starting with # are ignored; an
// empty file disables quick logins.
func loadQuickLogins(path string) ([]QuickLogin, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("WBB_QUICK_LOGIN_FILE is not readable")
	}
	defer f.Close()
	var out []QuickLogin
	seen := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return nil, fmt.Errorf("WBB_QUICK_LOGIN_FILE line %d: want label, email and password separated by tabs", n)
		}
		label, email, password := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), parts[2]
		parsed, err := mail.ParseAddress(email)
		if err != nil || parsed.Address != email || label == "" || len([]rune(label)) > 40 || password == "" {
			return nil, fmt.Errorf("WBB_QUICK_LOGIN_FILE line %d: invalid label, email or password", n)
		}
		id := strings.ToLower(email[:strings.Index(email, "@")])
		if !quickLoginID.MatchString(id) || seen[id] {
			return nil, fmt.Errorf("WBB_QUICK_LOGIN_FILE line %d: email local part must be a unique lowercase id", n)
		}
		seen[id] = true
		out = append(out, QuickLogin{ID: id, Label: label, Email: strings.ToLower(email), Password: password})
		if len(out) > 20 {
			return nil, errors.New("WBB_QUICK_LOGIN_FILE: at most 20 accounts")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("WBB_QUICK_LOGIN_FILE is not readable")
	}
	return out, nil
}
