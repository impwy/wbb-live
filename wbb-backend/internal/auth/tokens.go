package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWT audiences. The login/session audience and the (future) media publish
// audience are deliberately separate: a wbb-api token must never be accepted
// as a publish grant and vice versa.
const (
	AudienceAPI     = "wbb-api"
	AudiencePublish = "wbb-publish"
	issuer          = "wbb"
)

var (
	ErrTokenInvalid = errors.New("token is invalid or expired")
)

// AccessClaims is the login access JWT payload. sid/fid reference the active
// auth_sessions row and its refresh family; sub is the user id.
type AccessClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
	FamilyID  string `json:"fid"`
}

// PublishClaims are deliberately distinct from login claims. 04-A issues
// grants for a prepared session; no media endpoint consumes them yet.
type PublishClaims struct {
	jwt.RegisteredClaims
	BroadcastID string `json:"bid"`
	SessionID   string `json:"bsid"`
	FamilyID    string `json:"fid"`
	Generation  int64  `json:"generation"`
}

// TokenManager signs and verifies access JWTs with a fixed algorithm
// (HS256), issuer and audience. Only HS256 is accepted on verification.
type TokenManager struct {
	secret []byte
	ttl    time.Duration
}

func NewTokenManager(secret []byte, accessTTL time.Duration) *TokenManager {
	return &TokenManager{secret: secret, ttl: accessTTL}
}

// AccessTTL returns the configured access token lifetime.
func (m *TokenManager) AccessTTL() time.Duration { return m.ttl }

// IssueAccess signs a login access JWT for the given user and session.
func (m *TokenManager) IssueAccess(userID, sessionID, familyID string, now time.Time) (string, error) {
	claims := AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{AudienceAPI},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
			ID:        sessionID,
		},
		SessionID: sessionID,
		FamilyID:  familyID,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

// VerifyAccess validates signature, algorithm, issuer, audience, expiry and
// required claims (sub, sid, fid) of a login access JWT.
func (m *TokenManager) VerifyAccess(token string) (*AccessClaims, error) {
	parsed, err := jwt.ParseWithClaims(
		token,
		&AccessClaims{},
		func(t *jwt.Token) (any, error) {
			if t.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
			}
			return m.secret, nil
		},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(AudienceAPI),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil || !parsed.Valid {
		return nil, ErrTokenInvalid
	}
	claims, ok := parsed.Claims.(*AccessClaims)
	if !ok || claims.Subject == "" || claims.SessionID == "" || claims.FamilyID == "" {
		return nil, ErrTokenInvalid
	}
	return claims, nil
}

// IssuePublish signs fixed database claims. Reissuing the same jti and times
// during an idempotent replay cannot extend the grant's lifetime.
func (m *TokenManager) IssuePublish(userID, broadcastID, sessionID, familyID, jti string, generation int64, issuedAt, expiresAt time.Time) (string, error) {
	claims := PublishClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID, Issuer: issuer,
			Audience:  jwt.ClaimStrings{AudiencePublish},
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			ExpiresAt: jwt.NewNumericDate(expiresAt), ID: jti,
		},
		BroadcastID: broadcastID, SessionID: sessionID,
		FamilyID: familyID, Generation: generation,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}
