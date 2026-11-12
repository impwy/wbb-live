package livesessions

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *Service) Playback(ctx context.Context, broadcastID string) (string, int64, error) {
	if !validUUID.MatchString(broadcastID) {
		return "", 0, ErrNotFound
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM broadcasts WHERE id=$1 AND deleted_at IS NULL)`, broadcastID).Scan(&exists); err != nil {
		return "", 0, err
	}
	if !exists {
		return "", 0, ErrNotFound
	}
	var sid string
	var gen int64
	err := s.db.QueryRow(ctx, `SELECT id::text,media_generation FROM broadcast_sessions WHERE broadcast_id=$1 AND state='live'`, broadcastID).Scan(&sid, &gen)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, ErrConflict
	}
	return sid, gen, err
}

func (s *Service) AllowHLS(ctx context.Context, sessionID string, generation int64) (bool, error) {
	if !validUUID.MatchString(sessionID) || generation < 1 {
		return false, nil
	}
	var allowed bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM broadcast_sessions WHERE id=$1 AND media_generation=$2 AND state='live')`, sessionID, generation).Scan(&allowed)
	return allowed, err
}

func HLSMasterURL(sessionID string, generation int64) string {
	return fmt.Sprintf("/media/wbb/%s/%d/720p/index.m3u8", sessionID, generation)
}
