// Package auth implements the WBB authentication module: email/password
// registration, login with Argon2id, short-lived access JWTs and rotating
// refresh sessions persisted in PostgreSQL with a Redis active-session cache.
//
// Security invariants (docs/design.md §4):
//   - The PostgreSQL ledger is the final authority: both the
//     auth_session_families row (revoked_at) and the auth_sessions row
//     (revoked_at, expires_at) must be alive. The Redis active-sid cache
//     alone never grants access and its loss never revokes stored sessions.
//   - Redis errors fail closed (ErrStoreUnavailable → 503) for protected
//     flows; anonymous reads never touch Redis.
//   - Rotation, logout and reuse detection serialize on the family row lock,
//     so a revocation can never miss a concurrently rotated session even if
//     the cache delete fails.
//   - Refresh token reuse revokes the whole family.
//   - Only refresh token hashes are stored, never plaintext tokens.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var (
	// ErrEmailTaken reports a duplicate normalized email on registration.
	ErrEmailTaken = errors.New("email already registered")
	// ErrInvalidCredentials is returned for unknown email or wrong password.
	ErrInvalidCredentials = errors.New("invalid email or password")
	// ErrUnauthorized covers missing/expired/invalid tokens and sessions.
	ErrUnauthorized = errors.New("authentication required")
	// ErrSessionRevoked is returned when a revoked (rotated or destroyed)
	// refresh token is presented again.
	ErrSessionRevoked = errors.New("session was revoked")
	// ErrStoreUnavailable is the fail-closed response when Redis cannot be
	// consulted for protected operations.
	ErrStoreUnavailable = errors.New("session store unavailable")
	ErrAccountExists    = errors.New("account already exists")
)

// InvalidInputError reports a request validation failure for one field.
type InvalidInputError struct {
	Field  string
	Reason string
}

func (e *InvalidInputError) Error() string { return e.Field + ": " + e.Reason }

// User is the public shape of an account. Password hashes never leave this
// package.
type User struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

// Session is the result of login or refresh: a short-lived access JWT plus a
// rotating refresh token delivered as an HttpOnly cookie by the HTTP layer.
type Session struct {
	AccessToken  string
	RefreshToken string
	SessionID    string
	FamilyID     string
	ExpiresAt    time.Time
}

// Service is the auth module interface. All HTTP handlers authenticate
// through it; internals (hashing, token format, storage) stay hidden.
type Service struct {
	db         *pgxpool.Pool
	rdb        redis.UniversalClient
	tokens     *TokenManager
	refreshTTL time.Duration
	now        func() time.Time
}

func NewService(db *pgxpool.Pool, rdb redis.UniversalClient, tokens *TokenManager, refreshTTL time.Duration) *Service {
	return &Service{db: db, rdb: rdb, tokens: tokens, refreshTTL: refreshTTL, now: time.Now}
}

// SetClock is used by tests to control time.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

const (
	minPasswordLen = 12
	maxPasswordLen = 128
	maxEmailLen    = 254
	maxDisplayName = 40
)

func validateEmail(email string) error {
	if email == "" {
		return &InvalidInputError{Field: "email", Reason: "required"}
	}
	if len(email) > maxEmailLen {
		return &InvalidInputError{Field: "email", Reason: "must be at most 254 characters"}
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return &InvalidInputError{Field: "email", Reason: "must be a valid email address"}
	}
	return nil
}

func validateRegistration(email, password, displayName string) error {
	if err := validateEmail(email); err != nil {
		return err
	}
	// The password limit counts characters (runes), not bytes: a 12-byte
	// 4-character Korean password is 4 characters, not 12.
	pwLen := utf8.RuneCountInString(password)
	if pwLen < minPasswordLen || pwLen > maxPasswordLen {
		return &InvalidInputError{Field: "password", Reason: fmt.Sprintf("must be %d to %d characters", minPasswordLen, maxPasswordLen)}
	}
	if displayName == "" {
		return &InvalidInputError{Field: "displayName", Reason: "required"}
	}
	if len([]rune(displayName)) > maxDisplayName {
		return &InvalidInputError{Field: "displayName", Reason: "must be at most 40 characters"}
	}
	return nil
}

// Register creates a local email/password account. It never logs the caller
// in; the client follows up with Login.
func (s *Service) Register(ctx context.Context, email, password, displayName string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	if err := validateRegistration(email, password, displayName); err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	var u User
	err = s.db.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, display_name) VALUES ($1, $2, $3)
		 RETURNING id, email, display_name`,
		email, hash, displayName,
	).Scan(&u.ID, &u.Email, &u.DisplayName)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return User{}, ErrEmailTaken
		}
		return User{}, err
	}
	return u, nil
}

// Login verifies credentials and opens a new refresh session family.
func (s *Service) Login(ctx context.Context, email, password string) (User, Session, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if err := validateEmail(email); err != nil || password == "" {
		return User{}, Session{}, ErrInvalidCredentials
	}
	var id, display string
	var hash *string
	err := s.db.QueryRow(ctx,
		`SELECT id, password_hash, display_name FROM users WHERE email = $1`, email,
	).Scan(&id, &hash, &display)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, Session{}, err
	}
	if hash == nil || *hash == "" { // OIDC-only account
		return User{}, Session{}, ErrInvalidCredentials
	}
	ok, err := VerifyPassword(*hash, password)
	if err != nil {
		return User{}, Session{}, err
	}
	if !ok {
		return User{}, Session{}, ErrInvalidCredentials
	}
	sess, err := s.openSession(ctx, id)
	if err != nil {
		return User{}, Session{}, err
	}
	return User{ID: id, Email: email, DisplayName: display}, sess, nil
}

// LoginGoogle resolves the verified Google subject before considering email.
// The transaction makes a new account and identity inseparable; concurrent
// first logins converge on the winning identity without linking by email.
func (s *Service) LoginGoogle(ctx context.Context, subject, email, displayName string) (User, Session, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if subject == "" || validateEmail(email) != nil {
		return User{}, Session{}, ErrInvalidCredentials
	}
	find := func() (User, error) {
		var u User
		err := s.db.QueryRow(ctx, `SELECT u.id::text,u.email,u.display_name FROM auth_identities i JOIN users u ON u.id=i.user_id WHERE i.provider='google' AND i.issuer='https://accounts.google.com' AND i.subject=$1`, subject).Scan(&u.ID, &u.Email, &u.DisplayName)
		return u, err
	}
	if u, err := find(); err == nil {
		sess, e := s.openSession(ctx, u.ID)
		return u, sess, e
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return User{}, Session{}, err
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = "Google 사용자"
	}
	rr := []rune(displayName)
	if len(rr) > 40 {
		displayName = string(rr[:40])
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return User{}, Session{}, err
	}
	defer tx.Rollback(ctx)
	var u User
	err = tx.QueryRow(ctx, `INSERT INTO users(email,password_hash,display_name) VALUES ($1,NULL,$2) ON CONFLICT(email) DO NOTHING RETURNING id::text,email,display_name`, email, displayName).Scan(&u.ID, &u.Email, &u.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(ctx)
		if winner, e := find(); e == nil {
			sess, e := s.openSession(ctx, winner.ID)
			return winner, sess, e
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return User{}, Session{}, e
		}
		return User{}, Session{}, ErrAccountExists
	}
	if err != nil {
		return User{}, Session{}, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO auth_identities(user_id,provider,issuer,subject) VALUES ($1,'google','https://accounts.google.com',$2) ON CONFLICT DO NOTHING`, u.ID, subject)
	if err != nil {
		return User{}, Session{}, err
	}
	if tag.RowsAffected() == 0 {
		_ = tx.Rollback(ctx)
		winner, e := find()
		if e != nil {
			return User{}, Session{}, e
		}
		sess, e := s.openSession(ctx, winner.ID)
		return winner, sess, e
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, Session{}, err
	}
	sess, err := s.openSession(ctx, u.ID)
	return u, sess, err
}

// openSession creates a family plus its first session row in one transaction
// and mirrors the active sid into Redis. On Redis failure everything rolls
// back (fail closed).
func (s *Service) openSession(ctx context.Context, userID string) (Session, error) {
	now := s.now()
	refresh, err := newRefreshToken()
	if err != nil {
		return Session{}, err
	}
	expires := now.Add(s.refreshTTL)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback(ctx)

	var sess Session
	err = tx.QueryRow(ctx,
		`INSERT INTO auth_session_families (user_id) VALUES ($1) RETURNING id::text`,
		userID,
	).Scan(&sess.FamilyID)
	if err != nil {
		return Session{}, err
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO auth_sessions (refresh_family_id, user_id, refresh_token_hash, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id::text`,
		sess.FamilyID, userID, hashToken(refresh), expires,
	).Scan(&sess.SessionID)
	if err != nil {
		return Session{}, err
	}
	if err := s.cacheActiveSession(ctx, sess.FamilyID, sess.SessionID, expires.Sub(now)); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	access, err := s.tokens.IssueAccess(userID, sess.SessionID, sess.FamilyID, now)
	if err != nil {
		return Session{}, err
	}
	sess.AccessToken = access
	sess.RefreshToken = refresh
	sess.ExpiresAt = expires
	return sess, nil
}

// Refresh rotates a refresh token from the HttpOnly cookie. The presented
// token is consumed atomically; reuse revokes the entire family.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (User, Session, error) {
	now := s.now()
	if refreshToken == "" {
		return User{}, Session{}, ErrUnauthorized
	}
	var (
		sessID, familyID, userID string
		expires                  time.Time
		revokedAt                *time.Time
	)
	err := s.db.QueryRow(ctx,
		`SELECT id::text, refresh_family_id::text, user_id::text, expires_at, revoked_at
		 FROM auth_sessions WHERE refresh_token_hash = $1`,
		hashToken(refreshToken),
	).Scan(&sessID, &familyID, &userID, &expires, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, Session{}, ErrUnauthorized
	}
	if err != nil {
		return User{}, Session{}, err
	}
	if revokedAt != nil {
		// Token replay after rotation: destroy the whole family via the ledger.
		if rerr := s.revokeFamily(ctx, familyID, "reuse"); rerr != nil {
			return User{}, Session{}, rerr
		}
		return User{}, Session{}, ErrSessionRevoked
	}
	if now.After(expires) {
		_ = s.revokeFamily(ctx, familyID, "expired")
		return User{}, Session{}, ErrUnauthorized
	}

	// Fail closed on Redis outages. The cached value itself is advisory
	// only: the family-locked transaction below re-verifies the ledger and is
	// the sole revocation authority, so a stale or missing entry can never
	// revoke (or revive) a family by itself.
	if _, _, err := s.cachedSession(ctx, familyID); err != nil {
		return User{}, Session{}, err
	}

	newRefresh, err := newRefreshToken()
	if err != nil {
		return User{}, Session{}, err
	}
	newExpires := now.Add(s.refreshTTL)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return User{}, Session{}, err
	}
	defer tx.Rollback(ctx)

	// Serialize with logout/reuse revocation on the family row and re-check
	// the ledger under the lock.
	var familyRevokedAt *time.Time
	err = tx.QueryRow(ctx,
		`SELECT revoked_at FROM auth_session_families WHERE id = $1 FOR UPDATE`, familyID,
	).Scan(&familyRevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, Session{}, ErrSessionRevoked
	}
	if err != nil {
		return User{}, Session{}, err
	}
	if familyRevokedAt != nil {
		return User{}, Session{}, ErrSessionRevoked
	}

	ct, err := tx.Exec(ctx,
		`UPDATE auth_sessions SET revoked_at = $2, revoked_reason = 'rotated'
		 WHERE id = $1 AND revoked_at IS NULL`, sessID, now)
	if err != nil {
		return User{}, Session{}, err
	}
	if ct.RowsAffected() != 1 {
		// Lost a concurrent rotation: this token was already consumed. Treat
		// the replay exactly like reuse — revoke the family ledger row in this
		// transaction (we hold the family lock, so the revocation can never
		// miss the winner's freshly inserted session) and commit before failing.
		if _, err := tx.Exec(ctx,
			`UPDATE auth_session_families
			 SET revoked_at = COALESCE(revoked_at, $2), revoked_reason = COALESCE(revoked_reason, 'reuse')
			 WHERE id = $1`, familyID, now); err != nil {
			return User{}, Session{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return User{}, Session{}, err
		}
		// Best-effort cache delete; the ledger above is authoritative.
		s.rdb.Del(ctx, sessionKey(familyID))
		return User{}, Session{}, ErrSessionRevoked
	}
	var newSess Session
	err = tx.QueryRow(ctx,
		`INSERT INTO auth_sessions (refresh_family_id, user_id, refresh_token_hash, rotated_from_id, expires_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id::text`,
		familyID, userID, hashToken(newRefresh), sessID, newExpires,
	).Scan(&newSess.SessionID)
	if err != nil {
		return User{}, Session{}, err
	}
	if err := s.cacheActiveSession(ctx, familyID, newSess.SessionID, newExpires.Sub(now)); err != nil {
		return User{}, Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, Session{}, err
	}

	access, err := s.tokens.IssueAccess(userID, newSess.SessionID, familyID, now)
	if err != nil {
		return User{}, Session{}, err
	}
	var u User
	if err := s.db.QueryRow(ctx,
		`SELECT id::text, email, display_name FROM users WHERE id = $1`, userID,
	).Scan(&u.ID, &u.Email, &u.DisplayName); err != nil {
		return User{}, Session{}, err
	}
	newSess.AccessToken = access
	newSess.RefreshToken = newRefresh
	newSess.FamilyID = familyID
	newSess.ExpiresAt = newExpires
	return u, newSess, nil
}

// LogoutByRefreshToken revokes the family bound to the presented refresh
// cookie token. Unknown tokens are a no-op (logout is idempotent).
func (s *Service) LogoutByRefreshToken(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	var familyID string
	err := s.db.QueryRow(ctx,
		`SELECT s.refresh_family_id::text
		 FROM auth_sessions s
		 WHERE s.refresh_token_hash = $1`,
		hashToken(refreshToken),
	).Scan(&familyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.revokeFamily(ctx, familyID, "logout")
}

// LogoutByAccessToken revokes the family referenced by an access JWT, so
// logout also works when only the in-memory access token remains.
func (s *Service) LogoutByAccessToken(ctx context.Context, accessToken string) error {
	claims, err := s.tokens.VerifyAccess(accessToken)
	if err != nil {
		return nil // idempotent: nothing valid to revoke
	}
	return s.revokeFamily(ctx, claims.FamilyID, "logout")
}

// VerifyRequest authenticates an access JWT for protected APIs. The Redis
// active-sid cache is consulted first; a miss or a mismatching value on a
// healthy Redis falls through to the PostgreSQL ledger (family revoked_at,
// session revoked_at, expiry) and repairs the cache under the family lock
// without ever overwriting a newer sid. A real Redis outage fails closed
// with ErrStoreUnavailable. The ledger is always the final authority.
func (s *Service) VerifyRequest(ctx context.Context, accessToken string) (User, error) {
	claims, err := s.tokens.VerifyAccess(accessToken)
	if err != nil {
		return User{}, ErrUnauthorized
	}
	cached, cachedFound, err := s.cachedSession(ctx, claims.FamilyID)
	if err != nil {
		return User{}, err // ErrStoreUnavailable fails closed
	}
	var (
		userID, email, display string
		sessionExpires         time.Time
		sessionRevokedAt       *time.Time
		familyRevokedAt        *time.Time
	)
	err = s.db.QueryRow(ctx,
		`SELECT s.user_id::text, s.expires_at, s.revoked_at, f.revoked_at, u.email, u.display_name
		 FROM auth_sessions s
		 JOIN auth_session_families f ON f.id = s.refresh_family_id
		 JOIN users u ON u.id = s.user_id
		 WHERE s.id = $1`,
		claims.SessionID,
	).Scan(&userID, &sessionExpires, &sessionRevokedAt, &familyRevokedAt, &email, &display)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnauthorized
	}
	if err != nil {
		return User{}, err
	}
	if sessionRevokedAt != nil || familyRevokedAt != nil ||
		s.now().After(sessionExpires) || userID != claims.Subject {
		return User{}, ErrUnauthorized
	}
	if !cachedFound || cached != claims.SessionID {
		// Cache miss (eviction/restart) or a stale value: repair it under the
		// family lock so a concurrent rotation can never be overwritten by an
		// older sid. Ledger-invalid sessions are rejected without touching the
		// cache; a failed cache write fails closed (503).
		if err := s.recoverSessionCache(ctx, claims); err != nil {
			return User{}, err
		}
	}
	return User{ID: userID, Email: email, DisplayName: display}, nil
}

// recoverSessionCache repopulates the active-sid cache while holding the
// family row lock (the same lock rotation, logout and reuse revocation
// take). It re-verifies that claims.SessionID is still the family's live
// session before writing, so:
//   - a rotation that already committed leaves the newer sid untouched
//     (our sid is revoked → ErrUnauthorized, no write);
//   - a rotation waiting on the lock runs afterwards and overwrites us;
//   - cache write failures fail closed with ErrStoreUnavailable.
func (s *Service) recoverSessionCache(ctx context.Context, claims *AccessClaims) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var familyRevokedAt *time.Time
	err = tx.QueryRow(ctx,
		`SELECT revoked_at FROM auth_session_families WHERE id = $1 FOR UPDATE`, claims.FamilyID,
	).Scan(&familyRevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if familyRevokedAt != nil {
		return ErrUnauthorized
	}

	var liveID string
	var expires time.Time
	err = tx.QueryRow(ctx,
		`SELECT id::text, expires_at FROM auth_sessions
		 WHERE refresh_family_id = $1 AND revoked_at IS NULL
		 ORDER BY created_at DESC LIMIT 1`,
		claims.FamilyID,
	).Scan(&liveID, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if liveID != claims.SessionID || s.now().After(expires) {
		return ErrUnauthorized // rotated away or expired: never overwrite
	}
	if err := s.cacheActiveSession(ctx, claims.FamilyID, claims.SessionID, time.Until(expires)); err != nil {
		return err // ErrStoreUnavailable → 503
	}
	return tx.Commit(ctx)
}

// SessionOwner resolves the user owning the session family of a refresh
// token (by hash). Returns "" for unknown tokens. Used for per-account rate
// limiting so the budget accumulates across rotations and families.
func (s *Service) SessionOwner(ctx context.Context, refreshToken string) (string, error) {
	if refreshToken == "" {
		return "", nil
	}
	var userID string
	err := s.db.QueryRow(ctx,
		`SELECT user_id::text FROM auth_sessions WHERE refresh_token_hash = $1`,
		hashToken(refreshToken),
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return userID, nil
}

// UserByID loads the public user shape.
func (s *Service) UserByID(ctx context.Context, id string) (User, error) {
	var u User
	err := s.db.QueryRow(ctx,
		`SELECT id::text, email, display_name FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnauthorized
	}
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// cachedSession returns the cached active sid for a family.
//   - found=false, err=nil: healthy Redis, key missing (eviction/restart)
//   - err=ErrStoreUnavailable: real Redis outage, callers fail closed
func (s *Service) cachedSession(ctx context.Context, familyID string) (sid string, found bool, err error) {
	val, err := s.rdb.Get(ctx, sessionKey(familyID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, ErrStoreUnavailable
	}
	return val, true, nil
}

func (s *Service) cacheActiveSession(ctx context.Context, familyID, sessionID string, ttl time.Duration) error {
	if err := s.rdb.Set(ctx, sessionKey(familyID), sessionID, ttl).Err(); err != nil {
		return ErrStoreUnavailable
	}
	return nil
}

// revokeFamily marks the family ledger row revoked (authoritative, single
// row) and best-effort revokes session rows plus the cache. Even when the
// cache delete fails, VerifyRequest rejects via the ledger.
func (s *Service) revokeFamily(ctx context.Context, familyID, reason string) error {
	now := s.now()
	_, err := s.db.Exec(ctx,
		`UPDATE auth_session_families
		 SET revoked_at = COALESCE(revoked_at, $2), revoked_reason = COALESCE(revoked_reason, $3)
		 WHERE id = $1`,
		familyID, now, reason,
	)
	if err != nil {
		return err
	}
	// Audit bookkeeping; the ledger row above is what authorization checks.
	_, _ = s.db.Exec(ctx,
		`UPDATE auth_sessions
		 SET revoked_at = COALESCE(revoked_at, $2), revoked_reason = COALESCE(revoked_reason, $3)
		 WHERE refresh_family_id = $1`,
		familyID, now, reason,
	)
	s.rdb.Del(ctx, sessionKey(familyID))
	return nil
}

func sessionKey(familyID string) string { return "wbb:sess:" + familyID }

func newRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
