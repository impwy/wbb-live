// Package broadcasts implements the broadcast (방송) metadata module: CRUD
// with server-side ownership checks, optimistic versioning and soft delete.
//
// Invariants (docs/design.md §3, §5):
//   - The owner is derived from the authenticated session; requests never
//     carry an ownerId the server trusts.
//   - Updates and deletes carry the known version; stale versions get 409.
//   - Deletes are soft; deleted broadcasts disappear from public reads.
//   - Title 1..100 chars, description 0..2000 chars.
//
// The status field is the state of the latest broadcast session, or "ready"
// with a null currentSessionId when the broadcast never started.
package broadcasts

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound covers unknown and soft-deleted broadcasts.
	ErrNotFound = errors.New("broadcast not found")
	// ErrForbidden is returned when a non-owner attempts a protected action.
	ErrForbidden = errors.New("not the broadcast owner")
	// ErrVersionConflict reports a stale version on update/delete.
	ErrVersionConflict = errors.New("version conflict")
	ErrActiveSession   = errors.New("broadcast has an active session")
)

// InvalidInputError reports a request validation failure for one field.
type InvalidInputError struct {
	Field  string
	Reason string
}

func (e *InvalidInputError) Error() string { return e.Field + ": " + e.Reason }

// Status values of the public broadcast object.
const (
	StatusReady  = "ready"
	StatusEnded  = "ended"
	maxTitleLen  = 100
	maxDescLen   = 2000
	defaultLimit = 20
	maxLimit     = 50
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Owner is the public owner summary embedded in broadcast objects.
type Owner struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// Broadcast is the public broadcast object (docs/design.md §5). It never
// includes owner emails, sessions internals or media addresses.
type Broadcast struct {
	ID               string    `json:"id"`
	OwnerID          string    `json:"ownerId"`
	Owner            Owner     `json:"owner"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	Version          int64     `json:"version"`
	Status           string    `json:"status"`
	CurrentSessionID *string   `json:"currentSessionId"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// Service is the broadcasts module interface.
type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service { return &Service{db: db} }

func validateTitle(title string) error {
	if title == "" {
		return &InvalidInputError{Field: "title", Reason: "required"}
	}
	if len([]rune(title)) > maxTitleLen {
		return &InvalidInputError{Field: "title", Reason: "must be at most 100 characters"}
	}
	return nil
}

func validateDescription(description string) error {
	if len([]rune(description)) > maxDescLen {
		return &InvalidInputError{Field: "description", Reason: "must be at most 2000 characters"}
	}
	return nil
}

// Create registers a new broadcast owned by the authenticated user.
func (s *Service) Create(ctx context.Context, ownerID, title, description string) (Broadcast, error) {
	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)
	if err := validateTitle(title); err != nil {
		return Broadcast{}, err
	}
	if err := validateDescription(description); err != nil {
		return Broadcast{}, err
	}
	var id string
	if err := s.db.QueryRow(ctx,
		`INSERT INTO broadcasts (owner_id, title, description) VALUES ($1, $2, $3) RETURNING id`,
		ownerID, title, description,
	).Scan(&id); err != nil {
		return Broadcast{}, err
	}
	return s.Get(ctx, id)
}

// Get returns one non-deleted broadcast.
func (s *Service) Get(ctx context.Context, id string) (Broadcast, error) {
	return s.queryOne(ctx,
		`SELECT b.id, b.owner_id, b.title, b.description, b.version, b.created_at, b.updated_at,
		        u.id, u.display_name, COALESCE(latest.state, 'ready'), latest.id::text
		 FROM broadcasts b JOIN users u ON u.id = b.owner_id
		 LEFT JOIN LATERAL (SELECT id,state FROM broadcast_sessions WHERE broadcast_id=b.id ORDER BY created_at DESC,id DESC LIMIT 1) latest ON true
		 WHERE b.id = $1 AND b.deleted_at IS NULL`,
		id,
	)
}

// ListFilter narrows the public list. Status "live" keeps broadcasts whose
// latest session is publicly playable; OwnerID keeps one owner's broadcasts.
type ListFilter struct {
	Status  string
	OwnerID string
}

// List returns non-deleted broadcasts newest-first with an opaque cursor.
func (s *Service) List(ctx context.Context, filter ListFilter, cursor string, limit int) ([]Broadcast, string, error) {
	if filter.Status != "" && filter.Status != "all" && filter.Status != "live" {
		return nil, "", &InvalidInputError{Field: "status", Reason: "must be \"live\" or \"all\""}
	}
	if limit < 1 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	args := []any{}
	where := "b.deleted_at IS NULL"
	if filter.Status == "live" {
		where += " AND latest.state = 'live'"
	}
	if filter.OwnerID != "" {
		if !uuidPattern.MatchString(filter.OwnerID) {
			return nil, "", &InvalidInputError{Field: "ownerId", Reason: "invalid"}
		}
		args = append(args, filter.OwnerID)
		where += fmt.Sprintf(" AND b.owner_id = $%d", len(args))
	}
	if cursor != "" {
		createdAt, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", &InvalidInputError{Field: "cursor", Reason: "invalid"}
		}
		args = append(args, createdAt, id)
		where += fmt.Sprintf(" AND (b.created_at, b.id) < ($%d, $%d)", len(args)-1, len(args))
	}
	args = append(args, limit+1)

	rows, err := s.db.Query(ctx, `
		SELECT b.id, b.owner_id, b.title, b.description, b.version, b.created_at, b.updated_at,
		       u.id, u.display_name, COALESCE(latest.state, 'ready'), latest.id::text
		FROM broadcasts b JOIN users u ON u.id = b.owner_id
		LEFT JOIN LATERAL (SELECT id,state FROM broadcast_sessions WHERE broadcast_id=b.id ORDER BY created_at DESC,id DESC LIMIT 1) latest ON true
		WHERE `+where+`
		ORDER BY b.created_at DESC, b.id DESC
		LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	items := []Broadcast{}
	for rows.Next() {
		b, err := scanBroadcast(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, b)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	next := ""
	if len(items) > limit {
		last := items[limit-1]
		items = items[:limit]
		next = encodeCursor(last.CreatedAt, last.ID)
	}
	return items, next, nil
}

// Update applies a partial metadata update as the owner. version must match
// the stored version; otherwise ErrVersionConflict.
func (s *Service) Update(ctx context.Context, actorID, id string, version int64, title, description *string) (Broadcast, error) {
	if title == nil && description == nil {
		return Broadcast{}, &InvalidInputError{Field: "body", Reason: "title or description is required"}
	}
	// Normalize once, then validate and store the same trimmed value.
	if title != nil {
		t := strings.TrimSpace(*title)
		if err := validateTitle(t); err != nil {
			return Broadcast{}, err
		}
		title = &t
	}
	if description != nil {
		d := strings.TrimSpace(*description)
		if err := validateDescription(d); err != nil {
			return Broadcast{}, err
		}
		description = &d
	}

	if err := s.checkOwner(ctx, actorID, id, &version); err != nil {
		return Broadcast{}, err
	}

	ct, err := s.db.Exec(ctx,
		`UPDATE broadcasts
		 SET title = COALESCE($3, title),
		     description = COALESCE($4, description),
		     version = version + 1,
		     updated_at = now()
		 WHERE id = $1 AND version = $2 AND deleted_at IS NULL`,
		id, version, title, description,
	)
	if err != nil {
		return Broadcast{}, err
	}
	if ct.RowsAffected() != 1 {
		return Broadcast{}, ErrVersionConflict
	}
	return s.Get(ctx, id)
}

// Delete soft-deletes a broadcast as the owner. version must match. Once
// media sessions exist, an active session will block deletion with 409.
func (s *Service) Delete(ctx context.Context, actorID, id string, version int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owner string
	var storedVersion int64
	err = tx.QueryRow(ctx, `SELECT owner_id::text,version FROM broadcasts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&owner, &storedVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if owner != actorID {
		return ErrForbidden
	}
	if storedVersion != version {
		return ErrVersionConflict
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM broadcast_sessions WHERE broadcast_id=$1 AND state IN ('starting','live','stopping'))`, id).Scan(&active); err != nil {
		return err
	}
	if active {
		return ErrActiveSession
	}
	ct, err := tx.Exec(ctx,
		`UPDATE broadcasts SET deleted_at = now(), version = version + 1, updated_at = now()
		 WHERE id = $1 AND version = $2 AND deleted_at IS NULL`,
		id, version,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrVersionConflict
	}
	return tx.Commit(ctx)
}

func (s *Service) checkOwner(ctx context.Context, actorID, id string, version *int64) error {
	var (
		ownerID   string
		storedVer int64
	)
	err := s.db.QueryRow(ctx,
		`SELECT owner_id::text, version FROM broadcasts WHERE id = $1 AND deleted_at IS NULL`, id,
	).Scan(&ownerID, &storedVer)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if ownerID != actorID {
		return ErrForbidden
	}
	if version != nil && *version != storedVer {
		return ErrVersionConflict
	}
	return nil
}

type rowScanner interface{ Scan(dest ...any) error }

func (s *Service) queryOne(ctx context.Context, sql string, args ...any) (Broadcast, error) {
	row := s.db.QueryRow(ctx, sql, args...)
	b, err := scanBroadcast(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Broadcast{}, ErrNotFound
	}
	return b, err
}

func scanBroadcast(row rowScanner) (Broadcast, error) {
	var b Broadcast
	b.Status = StatusReady
	err := row.Scan(
		&b.ID, &b.OwnerID, &b.Title, &b.Description, &b.Version, &b.CreatedAt, &b.UpdatedAt,
		&b.Owner.ID, &b.Owner.DisplayName, &b.Status, &b.CurrentSessionID,
	)
	b.OwnerID = b.Owner.ID
	return b, err
}
