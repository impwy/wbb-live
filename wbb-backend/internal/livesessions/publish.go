package livesessions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/impwy/wbb-live/wbb-backend/internal/auth"
)

type PublishOperation struct {
	ID, SessionID string
	Generation    int64
}

// AllowMediaCreate is the database half of MediaMTX's private auth callback.
// The HMAC is verified by the HTTP adapter before this lookup.
func (s *Service) AllowMediaCreate(ctx context.Context, opID, path string) (bool, error) {
	if !validUUID.MatchString(opID) {
		return false, nil
	}
	var sid string
	var generation int64
	var state, opState string
	var familyActive bool
	err := s.db.QueryRow(ctx, `SELECT o.broadcast_session_id::text,o.media_generation,s.state,o.state,
		f.revoked_at IS NULL AND EXISTS(SELECT 1 FROM auth_sessions a WHERE a.refresh_family_id=f.id AND a.revoked_at IS NULL AND a.expires_at>now())
		FROM media_create_operations o JOIN broadcast_sessions s ON s.id=o.broadcast_session_id
		JOIN auth_session_families f ON f.id=s.publisher_auth_family_id WHERE o.id=$1 AND o.media_generation=s.media_generation`, opID).Scan(&sid, &generation, &state, &opState, &familyActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	want := fmt.Sprintf("wbb/%s/%d/source", sid, generation)
	return state == "starting" && (opState == "pending" || opState == "attached") && familyActive && strings.TrimPrefix(path, "/") == want, nil
}

func (s *Service) AllowWorkerMedia(ctx context.Context, runID string, fence int64, action, protocol, path string) (bool, error) {
	if !validUUID.MatchString(runID) || fence < 1 {
		return false, nil
	}
	var sid string
	var gen, currentFence int64
	var state, runState string
	var lease time.Time
	err := s.db.QueryRow(ctx, `SELECT s.id::text,s.media_generation,s.media_fencing_token,s.state,r.state,r.lease_until
		FROM media_worker_runs r JOIN broadcast_sessions s ON s.id=r.broadcast_session_id
		WHERE r.run_id=$1 AND r.fencing_token=$2 AND r.media_generation=s.media_generation`, runID, fence).Scan(&sid, &gen, &currentFence, &state, &runState, &lease)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if currentFence != fence || !s.clock().Before(lease) || (state != "starting" && state != "live") || (runState != "starting" && runState != "running") {
		return false, nil
	}
	source := fmt.Sprintf("wbb/%s/%d/source", sid, gen)
	output := fmt.Sprintf("wbb/%s/%d/720p", sid, gen)
	path = strings.TrimPrefix(path, "/")
	return (protocol == "rtsp" && action == "read" && path == source) ||
		(protocol == "rtsp" && action == "publish" && path == output) ||
		(protocol == "hls" && action == "read" && path == output), nil
}

type Resource struct {
	BroadcastID, SessionID, OperationID, UpstreamPath, ETag, State string
}

// LookupResource uses the current access identity, never the expired publish
// grant. A different login family of the same owner may use Stop instead.
func (s *Service) LookupResource(ctx context.Context, actor Actor, sessionID, operationID string, cleanup bool) (Resource, error) {
	if !validUUID.MatchString(sessionID) || !validUUID.MatchString(operationID) {
		return Resource{}, ErrNotFound
	}
	var res Resource
	var owner, family, sessionState, operationState string
	err := s.db.QueryRow(ctx, `SELECT b.id::text,b.owner_id::text,s.publisher_auth_family_id::text,s.state,o.state,o.upstream_resource_path,o.upstream_etag
		FROM media_create_operations o JOIN broadcast_sessions s ON s.id=o.broadcast_session_id JOIN broadcasts b ON b.id=s.broadcast_id
		WHERE o.id=$1 AND s.id=$2`, operationID, sessionID).Scan(&res.BroadcastID, &owner, &family, &sessionState, &operationState, &res.UpstreamPath, &res.ETag)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resource{}, ErrNotFound
	}
	if err != nil {
		return Resource{}, err
	}
	if owner != actor.UserID || family != actor.FamilyID {
		return Resource{}, ErrForbidden
	}
	if !cleanup && sessionState != "starting" && sessionState != "live" {
		return Resource{}, ErrConflict
	}
	if operationState != "attached" && !(cleanup && operationState == "cleanup_pending") {
		return Resource{}, ErrConflict
	}
	res.SessionID, res.OperationID, res.State = sessionID, operationID, sessionState
	return res, nil
}

func (s *Service) CloseResource(ctx context.Context, operationID string) error {
	_, err := s.db.Exec(ctx, `UPDATE media_create_operations SET state='closed',resolved_at=now() WHERE id=$1 AND state IN ('attached','cleanup_pending')`, operationID)
	return err
}

// ReservePublish consumes a single-purpose grant and commits a durable create
// operation before any network call. Lock order remains family, broadcast,
// session so revocation and stop can race without reversing row locks.
func (s *Service) ReservePublish(ctx context.Context, c *auth.PublishClaims) (PublishOperation, error) {
	now := s.clock()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return PublishOperation{}, err
	}
	defer tx.Rollback(ctx)
	var revoked *time.Time
	err = tx.QueryRow(ctx, `SELECT revoked_at FROM auth_session_families WHERE id=$1 AND user_id=$2 FOR UPDATE`, c.FamilyID, c.Subject).Scan(&revoked)
	if errors.Is(err, pgx.ErrNoRows) || revoked != nil {
		return PublishOperation{}, auth.ErrUnauthorized
	}
	if err != nil {
		return PublishOperation{}, err
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM auth_sessions WHERE refresh_family_id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>$3)`, c.FamilyID, c.Subject, now).Scan(&active)
	if err != nil {
		return PublishOperation{}, err
	}
	if !active {
		return PublishOperation{}, auth.ErrUnauthorized
	}
	if err = lockBroadcast(ctx, tx, c.BroadcastID, c.Subject); err != nil {
		return PublishOperation{}, err
	}
	sess, err := getSession(ctx, tx, c.BroadcastID, c.SessionID, true)
	if err != nil {
		return PublishOperation{}, err
	}
	var family string
	var generation int64
	var deadline time.Time
	err = tx.QueryRow(ctx, `SELECT publisher_auth_family_id::text,media_generation,prepare_deadline FROM broadcast_sessions WHERE id=$1`, c.SessionID).Scan(&family, &generation, &deadline)
	if err != nil {
		return PublishOperation{}, err
	}
	if family != c.FamilyID || generation != c.Generation {
		return PublishOperation{}, ErrForbidden
	}
	if sess.State != "starting" || !now.Before(deadline) {
		return PublishOperation{}, ErrConflict
	}
	var grantFamily string
	var grantSession string
	var grantIssued, grantExpiry time.Time
	var revokedAt, consumedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT auth_family_id::text,broadcast_session_id::text,issued_at,expires_at,revoked_at,consumed_at FROM publish_grants WHERE jti=$1 FOR UPDATE`, c.ID).Scan(&grantFamily, &grantSession, &grantIssued, &grantExpiry, &revokedAt, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublishOperation{}, auth.ErrTokenInvalid
	}
	if err != nil {
		return PublishOperation{}, err
	}
	if grantFamily != c.FamilyID || grantSession != c.SessionID || !grantIssued.Equal(c.IssuedAt.Time) || !grantExpiry.Equal(c.ExpiresAt.Time) {
		return PublishOperation{}, auth.ErrTokenInvalid
	}
	if revokedAt != nil || consumedAt != nil || !now.Before(grantExpiry) {
		return PublishOperation{}, ErrConflict
	}
	var op PublishOperation
	op.SessionID, op.Generation = c.SessionID, c.Generation
	err = tx.QueryRow(ctx, `INSERT INTO media_create_operations(broadcast_session_id,media_generation,grant_jti,state) VALUES ($1,$2,$3,'pending') RETURNING id::text`, c.SessionID, c.Generation, c.ID).Scan(&op.ID)
	if err != nil {
		return PublishOperation{}, ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE publish_grants SET consumed_at=$2 WHERE jti=$1`, c.ID, now); err != nil {
		return PublishOperation{}, err
	}
	return op, tx.Commit(ctx)
}

// AttachPublish records only a response for the still active generation.
// Late success after STOPPING remains a cleanup obligation, never a 201.
func (s *Service) AttachPublish(ctx context.Context, op PublishOperation, upstreamPath, etag string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var bid, fid string
	err = tx.QueryRow(ctx, `SELECT broadcast_id::text,publisher_auth_family_id::text FROM broadcast_sessions WHERE id=$1`, op.SessionID).Scan(&bid, &fid)
	if err != nil {
		return err
	}
	var locked string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM auth_session_families WHERE id=$1 FOR UPDATE`, fid).Scan(&locked); err != nil {
		return err
	}
	if err = lockBroadcast(ctx, tx, bid, ""); err != nil {
		return err
	}
	sess, err := getSession(ctx, tx, bid, op.SessionID, true)
	if err != nil {
		return err
	}
	state := "attached"
	if sess.State != "starting" {
		state = "cleanup_pending"
	}
	var updatedState string
	err = tx.QueryRow(ctx, `UPDATE media_create_operations SET state=CASE WHEN state='cleanup_pending' THEN 'cleanup_pending' ELSE $2 END,
		upstream_resource_path=$3,upstream_etag=$4 WHERE id=$1 AND broadcast_session_id=$5 AND media_generation=$6
		AND state IN ('pending','cleanup_pending') RETURNING state`, op.ID, state, upstreamPath, etag, op.SessionID, op.Generation).Scan(&updatedState)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if updatedState != "attached" {
		return ErrConflict
	}
	return nil
}

func (s *Service) CloseFailedPublish(ctx context.Context, op PublishOperation) error {
	_, err := s.db.Exec(ctx, `UPDATE media_create_operations SET state='closed',resolved_at=now() WHERE id=$1 AND broadcast_session_id=$2 AND state='pending'`, op.ID, op.SessionID)
	return err
}

// MarkDispatched must commit before the first upstream byte is sent. A
// STOPPING race returns conflict, so a late POST cannot begin after stop.
func (s *Service) MarkDispatched(ctx context.Context, op PublishOperation) error {
	result, err := s.db.Exec(ctx, `UPDATE media_create_operations o SET request_started_at=now() FROM broadcast_sessions s
		WHERE o.id=$1 AND o.broadcast_session_id=s.id AND o.state='pending' AND o.request_started_at IS NULL
		AND s.state='starting' AND s.media_generation=o.media_generation`, op.ID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Service) AbandonPublish(ctx context.Context, op PublishOperation) error {
	_, err := s.db.Exec(ctx, `UPDATE media_create_operations SET state='cleanup_pending',abandoned_at=COALESCE(abandoned_at,now()) WHERE id=$1 AND state='pending'`, op.ID)
	return err
}
