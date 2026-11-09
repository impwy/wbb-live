// Package livesessions owns the durable preparation and cancellation of a
// broadcast session. 04-A cannot create media; LIVE requires a later media
// adapter and is never inferred from a successful prepare request.
package livesessions

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/impwy/wbb-live/wbb-backend/internal/auth"
	"github.com/impwy/wbb-live/wbb-backend/internal/platform/db"
)

var (
	ErrNotFound         = errors.New("broadcast or session not found")
	ErrForbidden        = errors.New("not the owner or original login family")
	ErrConflict         = errors.New("active session or idempotency conflict")
	ErrGrantUnavailable = errors.New("grant cannot be renewed")
	ErrInvalidInput     = errors.New("invalid session input")
)

var validUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var validKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)

type Actor struct{ UserID, FamilyID, AuthSessionID string }

type Session struct {
	ID          string     `json:"id"`
	BroadcastID string     `json:"broadcastId"`
	State       string     `json:"state"`
	Version     int64      `json:"version"`
	StartedAt   time.Time  `json:"startedAt"`
	EndedAt     *time.Time `json:"endedAt"`
}

type Grant struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	WhipURL   string    `json:"whipUrl"`
}

type StartResult struct {
	Session           Session `json:"session"`
	PublishGrant      *Grant  `json:"publishGrant"`
	PublishGrantState string  `json:"publishGrantState,omitempty"`
}

type Service struct {
	db     *pgxpool.Pool
	tokens *auth.TokenManager
	nowMu  sync.RWMutex
	now    func() time.Time
	// This is true only while 04-A has no WHIP/worker media adapter. A future
	// adapter must explicitly disable this shortcut before accepting media.
	mediaCreationDisabled bool
}

func NewService(pool *pgxpool.Pool, tokens *auth.TokenManager) *Service {
	return &Service{db: pool, tokens: tokens, now: time.Now, mediaCreationDisabled: true}
}

func (s *Service) SetClock(now func() time.Time) {
	s.nowMu.Lock()
	s.now = now
	s.nowMu.Unlock()
}
func (s *Service) clock() time.Time {
	s.nowMu.RLock()
	now := s.now
	s.nowMu.RUnlock()
	return now().UTC().Truncate(time.Second)
}

func (s *Service) Start(ctx context.Context, actor Actor, broadcastID, source, key string) (StartResult, error) {
	if !validUUID.MatchString(broadcastID) {
		return StartResult{}, ErrNotFound
	}
	if source != "screen" && source != "camera" {
		return StartResult{}, ErrInvalidInput
	}
	if !validKey.MatchString(key) {
		return StartResult{}, ErrInvalidInput
	}
	now := s.clock()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return StartResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockFamily(ctx, tx, actor, now); err != nil {
		return StartResult{}, err
	}
	if err := lockBroadcast(ctx, tx, broadcastID, actor.UserID); err != nil {
		return StartResult{}, err
	}
	_, err = tx.Exec(ctx, `DELETE FROM session_start_requests WHERE user_id=$1 AND broadcast_id=$2 AND operation='start' AND idempotency_key=$3 AND expires_at <= $4`, actor.UserID, broadcastID, key, now)
	if err != nil {
		return StartResult{}, err
	}
	var oldSource, oldFamily, oldSessionID, oldJTI string
	err = tx.QueryRow(ctx, `SELECT source,auth_family_id::text,broadcast_session_id::text,original_grant_jti::text FROM session_start_requests WHERE user_id=$1 AND broadcast_id=$2 AND operation='start' AND idempotency_key=$3`, actor.UserID, broadcastID, key).Scan(&oldSource, &oldFamily, &oldSessionID, &oldJTI)
	if err == nil {
		if oldFamily != actor.FamilyID {
			return StartResult{}, ErrForbidden
		}
		if oldSource != source {
			return StartResult{}, ErrConflict
		}
		sess, err := getSession(ctx, tx, broadcastID, oldSessionID, false)
		if err != nil {
			return StartResult{}, err
		}
		grant, state, err := s.grantResponse(ctx, tx, actor, sess, oldJTI, now)
		if err != nil {
			return StartResult{}, err
		}
		return StartResult{Session: sess, PublishGrant: grant, PublishGrantState: state}, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return StartResult{}, err
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM broadcast_sessions WHERE broadcast_id=$1 AND state IN ('starting','live','stopping'))`, broadcastID).Scan(&active); err != nil {
		return StartResult{}, err
	}
	if active {
		return StartResult{}, ErrConflict
	}
	var sess Session
	sess.BroadcastID = broadcastID
	sess.State = "starting"
	sess.Version = 1
	sess.StartedAt = now
	err = tx.QueryRow(ctx, `INSERT INTO broadcast_sessions (broadcast_id,publisher_auth_family_id,initial_auth_session_id,source,state,started_at,prepare_deadline) VALUES ($1,$2,$3,$4,'starting',$5,$6) RETURNING id::text`, broadcastID, actor.FamilyID, actor.AuthSessionID, source, now, now.Add(60*time.Second)).Scan(&sess.ID)
	if err != nil {
		return StartResult{}, err
	}
	grant, jti, err := s.issueGrant(ctx, tx, actor, sess, now, now.Add(30*time.Second))
	if err != nil {
		return StartResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO session_start_requests (user_id,broadcast_id,operation,idempotency_key,source,auth_family_id,broadcast_session_id,original_grant_jti,expires_at) VALUES ($1,$2,'start',$3,$4,$5,$6,$7,$8)`, actor.UserID, broadcastID, key, source, actor.FamilyID, sess.ID, jti, now.Add(24*time.Hour))
	if err != nil {
		return StartResult{}, err
	}
	if err := db.RecordSessionEvent(ctx, tx, broadcastID, sess.ID, "starting", 1); err != nil {
		return StartResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StartResult{}, err
	}
	return StartResult{Session: sess, PublishGrant: grant}, nil
}

func lockFamily(ctx context.Context, tx pgx.Tx, actor Actor, now time.Time) error {
	var revoked *time.Time
	err := tx.QueryRow(ctx, `SELECT revoked_at FROM auth_session_families WHERE id=$1 AND user_id=$2 FOR UPDATE`, actor.FamilyID, actor.UserID).Scan(&revoked)
	if errors.Is(err, pgx.ErrNoRows) || revoked != nil {
		return auth.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM auth_sessions WHERE id=$1 AND refresh_family_id=$2 AND user_id=$3 AND revoked_at IS NULL AND expires_at>$4)`, actor.AuthSessionID, actor.FamilyID, actor.UserID, now).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return auth.ErrUnauthorized
	}
	return nil
}

func lockBroadcast(ctx context.Context, tx pgx.Tx, id, owner string) error {
	var actual string
	err := tx.QueryRow(ctx, `SELECT owner_id::text FROM broadcasts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&actual)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if owner != "" && actual != owner {
		return ErrForbidden
	}
	return nil
}

func lockSessionFamily(ctx context.Context, tx pgx.Tx, broadcastID, sessionID string) error {
	var familyID string
	err := tx.QueryRow(ctx, `SELECT publisher_auth_family_id::text FROM broadcast_sessions WHERE id=$1 AND broadcast_id=$2`, sessionID, broadcastID).Scan(&familyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var locked string
	return tx.QueryRow(ctx, `SELECT id::text FROM auth_session_families WHERE id=$1 FOR UPDATE`, familyID).Scan(&locked)
}

func getSession(ctx context.Context, tx pgx.Tx, broadcastID, sessionID string, lock bool) (Session, error) {
	var sess Session
	query := `SELECT id::text,broadcast_id::text,state,version,started_at,ended_at FROM broadcast_sessions WHERE id=$1 AND broadcast_id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, query, sessionID, broadcastID).Scan(&sess.ID, &sess.BroadcastID, &sess.State, &sess.Version, &sess.StartedAt, &sess.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return sess, err
}

func (s *Service) issueGrant(ctx context.Context, tx pgx.Tx, actor Actor, sess Session, issuedAt, expiresAt time.Time) (*Grant, string, error) {
	var jti string
	err := tx.QueryRow(ctx, `INSERT INTO publish_grants (broadcast_session_id,auth_family_id,issued_at,expires_at) VALUES ($1,$2,$3,$4) RETURNING jti::text`, sess.ID, actor.FamilyID, issuedAt, expiresAt).Scan(&jti)
	if err != nil {
		return nil, "", err
	}
	token, err := s.tokens.IssuePublish(actor.UserID, sess.BroadcastID, sess.ID, actor.FamilyID, jti, 1, issuedAt, expiresAt)
	if err != nil {
		return nil, "", err
	}
	return &Grant{Token: token, ExpiresAt: expiresAt, WhipURL: "/api/v1/live-sessions/" + sess.ID + "/whip"}, jti, nil
}

func (s *Service) grantResponse(ctx context.Context, tx pgx.Tx, actor Actor, sess Session, jti string, now time.Time) (*Grant, string, error) {
	if sess.State != "starting" {
		return nil, "unavailable", nil
	}
	var family string
	var issuedAt, expiresAt time.Time
	var revokedAt, consumedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT auth_family_id::text,issued_at,expires_at,revoked_at,consumed_at FROM publish_grants WHERE jti=$1 AND broadcast_session_id=$2`, jti, sess.ID).Scan(&family, &issuedAt, &expiresAt, &revokedAt, &consumedAt)
	if err != nil {
		return nil, "", err
	}
	if family != actor.FamilyID || revokedAt != nil {
		return nil, "unavailable", nil
	}
	if consumedAt != nil {
		return nil, "consumed", nil
	}
	if !now.Before(expiresAt) {
		return nil, "expired", nil
	}
	var pending bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_create_operations WHERE broadcast_session_id=$1 AND state<>'closed')`, sess.ID).Scan(&pending); err != nil {
		return nil, "", err
	}
	if pending {
		return nil, "pending", nil
	}
	token, err := s.tokens.IssuePublish(actor.UserID, sess.BroadcastID, sess.ID, actor.FamilyID, jti, 1, issuedAt, expiresAt)
	if err != nil {
		return nil, "", err
	}
	return &Grant{Token: token, ExpiresAt: expiresAt, WhipURL: "/api/v1/live-sessions/" + sess.ID + "/whip"}, "", nil
}

func (s *Service) Current(ctx context.Context, broadcastID string) (*Session, error) {
	if !validUUID.MatchString(broadcastID) {
		return nil, ErrNotFound
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM broadcasts WHERE id=$1 AND deleted_at IS NULL)`, broadcastID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	var sess Session
	err := s.db.QueryRow(ctx, `SELECT id::text,broadcast_id::text,state,version,started_at,ended_at FROM broadcast_sessions WHERE broadcast_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, broadcastID).Scan(&sess.ID, &sess.BroadcastID, &sess.State, &sess.Version, &sess.StartedAt, &sess.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Service) Stop(ctx context.Context, actor Actor, broadcastID, sessionID string) (Session, bool, error) {
	if !validUUID.MatchString(broadcastID) || !validUUID.MatchString(sessionID) {
		return Session{}, false, ErrNotFound
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Session{}, false, err
	}
	defer tx.Rollback(ctx)
	if err := lockSessionFamily(ctx, tx, broadcastID, sessionID); err != nil {
		return Session{}, false, err
	}
	if err := lockBroadcast(ctx, tx, broadcastID, actor.UserID); err != nil {
		return Session{}, false, err
	}
	sess, err := getSession(ctx, tx, broadcastID, sessionID, true)
	if err != nil {
		return Session{}, false, err
	}
	if sess.State == "ended" {
		return sess, true, tx.Commit(ctx)
	}
	if sess.State != "stopping" {
		sess, err = transition(ctx, tx, sess, "stopping", "owner_stop", s.clock())
		if err != nil {
			return Session{}, false, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE publish_grants SET revoked_at=COALESCE(revoked_at,$2) WHERE broadcast_session_id=$1`, sess.ID, s.clock()); err != nil {
		return Session{}, false, err
	}
	return sess, false, tx.Commit(ctx)
}

func (s *Service) RenewGrant(ctx context.Context, actor Actor, broadcastID, sessionID string) (*Grant, error) {
	if !validUUID.MatchString(broadcastID) || !validUUID.MatchString(sessionID) {
		return nil, ErrNotFound
	}
	now := s.clock()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := lockFamily(ctx, tx, actor, now); err != nil {
		return nil, err
	}
	// The publisher family is immutable. Reject another login family before
	// taking the broadcast row lock: logout of the original family may hold
	// the session row while its outbox insert needs a broadcast KEY SHARE lock.
	// Ownership and existence are checked read-only here, then rechecked under
	// the broadcast/session locks below for permitted requests.
	var ownerID string
	err = tx.QueryRow(ctx, `SELECT owner_id::text FROM broadcasts WHERE id=$1 AND deleted_at IS NULL`, broadcastID).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if ownerID != actor.UserID {
		return nil, ErrForbidden
	}
	var publisherFamily string
	err = tx.QueryRow(ctx, `SELECT publisher_auth_family_id::text FROM broadcast_sessions WHERE id=$1 AND broadcast_id=$2`, sessionID, broadcastID).Scan(&publisherFamily)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if publisherFamily != actor.FamilyID {
		return nil, ErrForbidden
	}
	if err := lockBroadcast(ctx, tx, broadcastID, actor.UserID); err != nil {
		return nil, err
	}
	sess, err := getSession(ctx, tx, broadcastID, sessionID, true)
	if err != nil {
		return nil, err
	}
	var family string
	var deadline time.Time
	err = tx.QueryRow(ctx, `SELECT publisher_auth_family_id::text,prepare_deadline FROM broadcast_sessions WHERE id=$1`, sess.ID).Scan(&family, &deadline)
	if err != nil {
		return nil, err
	}
	if family != actor.FamilyID {
		return nil, ErrForbidden
	}
	if sess.State != "starting" || !now.Before(deadline) {
		return nil, ErrGrantUnavailable
	}
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT expires_at FROM publish_grants WHERE broadcast_session_id=$1 ORDER BY issued_at DESC,jti DESC LIMIT 1`, sess.ID).Scan(&expires)
	if err != nil {
		return nil, err
	}
	if now.Before(expires) {
		return nil, ErrGrantUnavailable
	}
	var pending bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_create_operations WHERE broadcast_session_id=$1 AND state<>'closed')`, sess.ID).Scan(&pending); err != nil {
		return nil, err
	}
	if pending {
		return nil, ErrGrantUnavailable
	}
	if _, err = tx.Exec(ctx, `UPDATE publish_grants SET revoked_at=COALESCE(revoked_at,$2) WHERE broadcast_session_id=$1`, sess.ID, now); err != nil {
		return nil, err
	}
	grant, _, err := s.issueGrant(ctx, tx, actor, sess, now, minTime(now.Add(30*time.Second), deadline))
	if err != nil {
		return nil, err
	}
	return grant, tx.Commit(ctx)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func transition(ctx context.Context, tx pgx.Tx, sess Session, state, reason string, now time.Time) (Session, error) {
	var ended *time.Time
	if state == "ended" {
		ended = &now
	}
	err := tx.QueryRow(ctx, `UPDATE broadcast_sessions SET state=$2,version=version+1,end_reason=COALESCE($3,end_reason),ended_at=COALESCE($4,ended_at) WHERE id=$1 AND version=$5 RETURNING version,ended_at`, sess.ID, state, reason, ended, sess.Version).Scan(&sess.Version, &sess.EndedAt)
	if err != nil {
		return Session{}, err
	}
	sess.State = state
	if err := db.RecordSessionEvent(ctx, tx, sess.BroadcastID, sess.ID, state, sess.Version); err != nil {
		return Session{}, err
	}
	return sess, nil
}

// Reconcile is safe to run on startup and every five seconds. It never reads
// Redis and never infers absence when an operation, ingress or worker marker
// exists. 04-B must replace the no-media proof with an adapter observation.
func (s *Service) Reconcile(ctx context.Context) error {
	// Retain request keys for the full 24-hour replay window, then remove them
	// independently of public GET requests.
	if _, err := s.db.Exec(ctx, `DELETE FROM session_start_requests WHERE expires_at <= $1`, s.clock()); err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, `SELECT id::text,broadcast_id::text FROM broadcast_sessions WHERE state IN ('starting','stopping') ORDER BY created_at`)
	if err != nil {
		return err
	}
	type pair struct{ id, bid string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err = rows.Scan(&p.id, &p.bid); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range pairs {
		if err := s.reconcileOne(ctx, p.bid, p.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileOne(ctx context.Context, broadcastID, sessionID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Family -> broadcast -> session matches start, stop and revocation. In
	// particular, outbox's broadcast FK must not deadlock with revocation.
	if err := lockSessionFamily(ctx, tx, broadcastID, sessionID); err != nil {
		return err
	}
	if err := lockBroadcast(ctx, tx, broadcastID, ""); err != nil {
		return err
	}
	sess, err := getSession(ctx, tx, broadcastID, sessionID, true)
	if err != nil {
		return err
	}
	var deadline time.Time
	var ingest, lease *string
	// A non-null worker lease is unknown until a media adapter reconciles it.
	err = tx.QueryRow(ctx, `SELECT prepare_deadline,ingest_connection_id,worker_lease_until::text FROM broadcast_sessions WHERE id=$1`, sessionID).Scan(&deadline, &ingest, &lease)
	if err != nil {
		return err
	}
	now := s.clock()
	if sess.State == "starting" && !now.Before(deadline) {
		sess, err = transition(ctx, tx, sess, "stopping", "prepare_timeout", now)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE publish_grants SET revoked_at=COALESCE(revoked_at,$2) WHERE broadcast_session_id=$1`, sessionID, now); err != nil {
			return err
		}
	}
	if sess.State == "stopping" && s.mediaCreationDisabled && ingest == nil && lease == nil {
		var unresolved bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_create_operations WHERE broadcast_session_id=$1 AND state<>'closed')`, sessionID).Scan(&unresolved); err != nil {
			return err
		}
		if !unresolved {
			if _, err = transition(ctx, tx, sess, "ended", "", now); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) RunMaintenance(ctx context.Context, report func(error)) {
	if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
		report(err)
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
				report(err)
			}
		}
	}
}
