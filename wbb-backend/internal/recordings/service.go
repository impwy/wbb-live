// Package recordings registers MediaMTX recordings of finished broadcast
// sessions and serves them as replays. Files live under a recordings root
// shared read-only with the API; rows keep each file's location, size, start
// time and duration so replays survive restarts.
package recordings

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("recording not found")

// <root>/wbb/<session>/<generation>/720p/<YYYY-MM-DD_HH-MM-SS-ffffff>.mp4
var recordingPath = regexp.MustCompile(`^wbb/([0-9a-f-]{36})/[0-9]+/720p/([0-9]{4}-[0-9]{2}-[0-9]{2}_[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9]{6})\.mp4$`)

const startLayout = "2006-01-02_15-04-05-000000"

type Recording struct {
	ID              string    `json:"id"`
	BroadcastID     string    `json:"broadcastId"`
	BroadcastTitle  string    `json:"broadcastTitle"`
	OwnerName       string    `json:"ownerName"`
	SessionID       string    `json:"sessionId"`
	StartedAt       time.Time `json:"startedAt"`
	DurationSeconds int       `json:"durationSeconds"`
	SizeBytes       int64     `json:"sizeBytes"`
}

type Service struct {
	db    *pgxpool.Pool
	root  string
	mu    sync.Mutex
	known map[string]bool
}

func NewService(db *pgxpool.Pool, root string) *Service {
	return &Service{db: db, root: root, known: map[string]bool{}}
}

func (s *Service) Enabled() bool { return s.root != "" }

// Run registers finished recordings periodically until ctx ends.
func (s *Service) Run(ctx context.Context, onError func(error)) {
	if !s.Enabled() {
		return
	}
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.Scan(ctx); err != nil && ctx.Err() == nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Scan registers recordings whose session has ended. Files of live
// sessions are picked up on a later scan.
func (s *Service) Scan(ctx context.Context) error {
	base := filepath.Join(s.root, "wbb")
	if _, err := os.Stat(base); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		s.mu.Lock()
		seen := s.known[rel]
		s.mu.Unlock()
		if seen {
			return nil
		}
		m := recordingPath.FindStringSubmatch(rel)
		if m == nil {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() == 0 {
			return nil
		}
		started, err := time.ParseInLocation(startLayout, m[2], time.UTC)
		if err != nil {
			return nil
		}
		duration := int(info.ModTime().Sub(started).Round(time.Second) / time.Second)
		if duration < 0 {
			duration = 0
		}
		tag, err := s.db.Exec(ctx, `INSERT INTO recordings (session_id, broadcast_id, file_path, size_bytes, started_at, duration_seconds)
			SELECT bs.id, bs.broadcast_id, $2, $3, $4, $5 FROM broadcast_sessions bs
			WHERE bs.id::text = $1 AND bs.state = 'ended'
			ON CONFLICT (file_path) DO NOTHING`, m[1], rel, info.Size(), started, duration)
		if err != nil {
			return err
		}
		var exists bool
		if tag.RowsAffected() == 0 {
			_ = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM recordings WHERE file_path=$1)`, rel).Scan(&exists)
		}
		if tag.RowsAffected() == 1 || exists {
			s.mu.Lock()
			s.known[rel] = true
			s.mu.Unlock()
		}
		return nil
	})
}

const selectRecording = `SELECT r.id, r.broadcast_id, b.title, u.display_name, r.session_id, r.started_at, r.duration_seconds, r.size_bytes
	FROM recordings r JOIN broadcasts b ON b.id = r.broadcast_id JOIN users u ON u.id = b.owner_id
	WHERE b.deleted_at IS NULL`

func scanRows(rows pgx.Rows) ([]Recording, error) {
	defer rows.Close()
	out := []Recording{}
	for rows.Next() {
		var r Recording
		if err := rows.Scan(&r.ID, &r.BroadcastID, &r.BroadcastTitle, &r.OwnerName, &r.SessionID, &r.StartedAt, &r.DurationSeconds, &r.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// List returns recent replays, optionally of one broadcast.
func (s *Service) List(ctx context.Context, broadcastID string, limit int) ([]Recording, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if broadcastID != "" {
		rows, err := s.db.Query(ctx, selectRecording+` AND r.broadcast_id::text = $1 ORDER BY r.started_at DESC LIMIT $2`, broadcastID, limit)
		if err != nil {
			return nil, err
		}
		return scanRows(rows)
	}
	rows, err := s.db.Query(ctx, selectRecording+` ORDER BY r.started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}

func (s *Service) Get(ctx context.Context, id string) (Recording, error) {
	rows, err := s.db.Query(ctx, selectRecording+` AND r.id::text = $1`, id)
	if err != nil {
		return Recording{}, err
	}
	list, err := scanRows(rows)
	if err != nil {
		return Recording{}, err
	}
	if len(list) == 0 {
		return Recording{}, ErrNotFound
	}
	return list[0], nil
}

// Open returns the stored file of a visible recording.
func (s *Service) Open(ctx context.Context, id string) (*os.File, os.FileInfo, error) {
	var rel string
	err := s.db.QueryRow(ctx, `SELECT r.file_path FROM recordings r JOIN broadcasts b ON b.id = r.broadcast_id
		WHERE r.id::text = $1 AND b.deleted_at IS NULL`, id).Scan(&rel)
	if errors.Is(err, pgx.ErrNoRows) || s.root == "" {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	full := filepath.Join(s.root, filepath.FromSlash(rel))
	if !strings.HasPrefix(full, filepath.Clean(s.root)+string(filepath.Separator)) {
		return nil, nil, ErrNotFound
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, ErrNotFound
	}
	return f, info, nil
}
