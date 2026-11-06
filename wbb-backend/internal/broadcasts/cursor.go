package broadcasts

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// Cursors are opaque base64 values of "createdAt|id" so pagination stays
// stable under the (created_at DESC, id DESC) sort key.
func encodeCursor(createdAt time.Time, id string) string {
	raw := fmt.Sprintf("%d|%s", createdAt.UnixMicro(), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(cursor))
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("malformed cursor")
	}
	var micros int64
	if _, err := fmt.Sscanf(parts[0], "%d", &micros); err != nil {
		return time.Time{}, "", err
	}
	if parts[1] == "" {
		return time.Time{}, "", fmt.Errorf("malformed cursor")
	}
	return time.UnixMicro(micros).UTC(), parts[1], nil
}
