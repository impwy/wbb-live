// Package comments implements per-broadcast comments. Anyone can read them;
// signed-in users write them, and only the author may edit or delete one.
// The author is always taken from the authenticated session.
package comments

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound    = errors.New("comment not found")
	ErrForbidden   = errors.New("not the comment author")
	ErrInvalidBody = errors.New("comment body must be 1 to 500 characters")
)

const (
	maxBody      = 500
	defaultLimit = 50
	maxLimit     = 100
)

type Author struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type Comment struct {
	ID          string    `json:"id"`
	BroadcastID string    `json:"broadcastId"`
	Author      Author    `json:"author"`
	Body        string    `json:"body"`
	Edited      bool      `json:"edited"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Service struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) *Service { return &Service{db: db} }

func normalize(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > maxBody {
		return "", ErrInvalidBody
	}
	return body, nil
}

const selectComment = `SELECT c.id, c.broadcast_id, u.id, u.display_name, c.body, c.updated_at > c.created_at, c.created_at, c.updated_at
	FROM broadcast_comments c JOIN users u ON u.id = c.author_id`

func scan(row pgx.Row) (Comment, error) {
	var c Comment
	err := row.Scan(&c.ID, &c.BroadcastID, &c.Author.ID, &c.Author.DisplayName, &c.Body, &c.Edited, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// List returns the newest comments of a visible broadcast, oldest first.
func (s *Service) List(ctx context.Context, broadcastID string, limit int) ([]Comment, error) {
	if limit < 1 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM broadcasts WHERE id::text=$1 AND deleted_at IS NULL)`, broadcastID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.db.Query(ctx, `SELECT * FROM (`+selectComment+`
		WHERE c.broadcast_id::text = $1 AND c.deleted_at IS NULL
		ORDER BY c.created_at DESC, c.id DESC LIMIT $2) newest ORDER BY 7 ASC, 1 ASC`, broadcastID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Create adds a comment by the signed-in author to a visible broadcast.
func (s *Service) Create(ctx context.Context, authorID, broadcastID, body string) (Comment, error) {
	body, err := normalize(body)
	if err != nil {
		return Comment{}, err
	}
	var id string
	err = s.db.QueryRow(ctx, `INSERT INTO broadcast_comments (broadcast_id, author_id, body)
		SELECT b.id, $2, $3 FROM broadcasts b WHERE b.id::text = $1 AND b.deleted_at IS NULL
		RETURNING id`, broadcastID, authorID, body).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Comment{}, ErrNotFound
	}
	if err != nil {
		return Comment{}, err
	}
	return s.get(ctx, id)
}

func (s *Service) get(ctx context.Context, id string) (Comment, error) {
	return scan(s.db.QueryRow(ctx, selectComment+` WHERE c.id::text = $1 AND c.deleted_at IS NULL`, id))
}

// authorOf locks the comment row and checks the author on the server.
func authorOf(ctx context.Context, tx pgx.Tx, actorID, id string) error {
	var author string
	err := tx.QueryRow(ctx, `SELECT c.author_id::text FROM broadcast_comments c
		JOIN broadcasts b ON b.id = c.broadcast_id
		WHERE c.id::text = $1 AND c.deleted_at IS NULL AND b.deleted_at IS NULL FOR UPDATE OF c`, id).Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if author != actorID {
		return ErrForbidden
	}
	return nil
}

// Update edits the body; only the author may do it.
func (s *Service) Update(ctx context.Context, actorID, id, body string) (Comment, error) {
	body, err := normalize(body)
	if err != nil {
		return Comment{}, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Comment{}, err
	}
	defer tx.Rollback(ctx)
	if err := authorOf(ctx, tx, actorID, id); err != nil {
		return Comment{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE broadcast_comments SET body=$2, updated_at=clock_timestamp() WHERE id::text=$1`, id, body); err != nil {
		return Comment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Comment{}, err
	}
	return s.get(ctx, id)
}

// Delete soft-deletes the comment; only the author may do it.
func (s *Service) Delete(ctx context.Context, actorID, id string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := authorOf(ctx, tx, actorID, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE broadcast_comments SET deleted_at=clock_timestamp() WHERE id::text=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
