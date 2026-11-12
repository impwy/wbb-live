package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/impwy/wbb-live/wbb-backend/internal/auth"
	"github.com/redis/go-redis/v9"
)

const (
	googleAuthURL    = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL   = "https://oauth2.googleapis.com/token"
	googleJWKSURL    = "https://www.googleapis.com/oauth2/v3/certs"
	googleBindCookie = "wbb_google_bind"
)

func (s *Server) googleEnabled() bool {
	return s.cfg.GoogleClientID != "" && s.cfg.GoogleClientSecret != "" && s.cfg.GoogleCallbackURI != ""
}
func (s *Server) googleHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next(w, r)
	}
}
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]bool{"googleEnabled": s.googleEnabled(), "evaluationEnabled": s.cfg.EvaluationLoginEnabled})
}
func googleRandom() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func digest(v string) string {
	h := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func googleStateKey(state string) string { return "wbb:google:state:" + digest(state) }
func (s *Server) googleRedirect(w http.ResponseWriter, r *http.Request, code string) {
	target := "/auth/google/complete"
	if code != "" {
		target += "?error=" + url.QueryEscape(code)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
func (s *Server) handleGoogleStart(w http.ResponseWriter, r *http.Request) {
	if !s.googleEnabled() {
		writeError(w, r, 503, "unavailable", "Google login is unavailable.")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, r, 400, "invalid_request", "Unsupported login parameters.")
		return
	}
	state, e := googleRandom()
	if e != nil {
		writeDomainError(w, r, e)
		return
	}
	nonce, e := googleRandom()
	if e != nil {
		writeDomainError(w, r, e)
		return
	}
	verifier, e := googleRandom()
	if e != nil {
		writeDomainError(w, r, e)
		return
	}
	binding, e := googleRandom()
	if e != nil {
		writeDomainError(w, r, e)
		return
	}
	key := googleStateKey(state)
	_, e = s.redis.TxPipelined(r.Context(), func(p redis.Pipeliner) error {
		p.HSet(r.Context(), key, "binding", digest(binding), "nonce", nonce, "verifier", verifier)
		p.Expire(r.Context(), key, 5*time.Minute)
		return nil
	})
	if e != nil {
		writeDomainError(w, r, auth.ErrStoreUnavailable)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: googleBindCookie, Value: binding, Path: "/api/v1/auth/google", HttpOnly: true, Secure: s.cfg.Env == "prod", SameSite: http.SameSiteLaxMode, MaxAge: 300})
	q := url.Values{"client_id": {s.cfg.GoogleClientID}, "redirect_uri": {s.cfg.GoogleCallbackURI}, "response_type": {"code"}, "scope": {"openid email profile"}, "state": {state}, "nonce": {nonce}, "code_challenge": {digest(verifier)}, "code_challenge_method": {"S256"}}
	http.Redirect(w, r, googleAuthURL+"?"+q.Encode(), http.StatusFound)
}

var consumeGoogleState = redis.NewScript(`
if redis.call('HGET',KEYS[1],'binding') ~= ARGV[1] then return nil end
local v=redis.call('HMGET',KEYS[1],'nonce','verifier')
redis.call('DEL',KEYS[1])
return v
`)

func (s *Server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if !s.googleEnabled() {
		writeError(w, r, 503, "unavailable", "Google login is unavailable.")
		return
	}
	// Parse errors must surface (url.Values.Query silently drops malformed
	// pairs); every parameter must appear exactly once.
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		s.googleRedirect(w, r, "invalid_request")
		return
	}
	for _, vals := range q {
		if len(vals) != 1 {
			s.googleRedirect(w, r, "invalid_request")
			return
		}
	}
	// Key EXISTENCE is checked explicitly — a missing key and an empty value
	// are different failures.
	has := func(k string) bool { _, ok := q[k]; return ok }
	if !has("state") || q.Get("state") == "" {
		s.googleRedirect(w, r, "invalid_request")
		return
	}
	codePresent, errPresent := has("code"), has("error")
	if codePresent == errPresent { // exactly one of code / error
		s.googleRedirect(w, r, "invalid_request")
		return
	}
	if codePresent && q.Get("code") == "" {
		s.googleRedirect(w, r, "invalid_request")
		return
	}
	if errPresent && q.Get("error") == "" {
		s.googleRedirect(w, r, "invalid_request")
		return
	}
	state, code, providerErr := q.Get("state"), q.Get("code"), q.Get("error")
	// Real Google callbacks carry extra parameters (scope, authuser, prompt,
	// hd, session_state, …). Per Google's code-model guidance, unrecognized
	// response parameters — and error_description — are IGNORED: never used
	// for auth/redirect/linking decisions and never logged.
	cookie, e := r.Cookie(googleBindCookie)
	if e != nil || cookie.Value == "" {
		s.googleRedirect(w, r, "invalid_request")
		return
	}
	result, e := consumeGoogleState.Run(r.Context(), s.redis, []string{googleStateKey(state)}, digest(cookie.Value)).Result()
	if e != nil && !errors.Is(e, redis.Nil) {
		writeDomainError(w, r, auth.ErrStoreUnavailable)
		return
	}
	vals, ok := result.([]interface{})
	if !ok || len(vals) != 2 {
		s.googleRedirect(w, r, "invalid_request")
		return
	}
	nonce, ok1 := vals[0].(string)
	verifier, ok2 := vals[1].(string)
	if !ok1 || !ok2 {
		s.googleRedirect(w, r, "invalid_request")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: googleBindCookie, Path: "/api/v1/auth/google", MaxAge: -1, HttpOnly: true, Secure: s.cfg.Env == "prod", SameSite: http.SameSiteLaxMode})
	if providerErr != "" {
		if providerErr == "access_denied" {
			s.googleRedirect(w, r, "cancelled")
		} else {
			s.googleRedirect(w, r, "unavailable")
		}
		return
	}
	claims, e := s.googleExchange(r.Context(), code, verifier, nonce)
	if e != nil {
		s.googleRedirect(w, r, "unavailable")
		return
	}
	_, session, e := s.auth.LoginGoogle(r.Context(), claims.Subject, claims.Email, claims.Name)
	if errors.Is(e, auth.ErrAccountExists) {
		s.googleRedirect(w, r, "account_exists")
		return
	}
	if errors.Is(e, auth.ErrStoreUnavailable) {
		writeDomainError(w, r, e)
		return
	}
	if e != nil {
		s.googleRedirect(w, r, "unavailable")
		return
	}
	setRefreshCookie(w, session.RefreshToken, session.ExpiresAt, s.cfg.Env == "prod")
	s.googleRedirect(w, r, "")
}

type googleClaims struct{ Subject, Email, Name string }

func (s *Server) googleExchange(ctx context.Context, code, verifier, nonce string) (googleClaims, error) {
	form := url.Values{"code": {code}, "client_id": {s.cfg.GoogleClientID}, "client_secret": {s.cfg.GoogleClientSecret}, "redirect_uri": {s.cfg.GoogleCallbackURI}, "grant_type": {"authorization_code"}, "code_verifier": {verifier}}
	req, e := http.NewRequestWithContext(ctx, "POST", googleTokenURL, strings.NewReader(form.Encode()))
	if e != nil {
		return googleClaims{}, e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := s.googleClient.Do(req)
	if e != nil {
		return googleClaims{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return googleClaims{}, errors.New("provider exchange failed")
	}
	var body struct {
		IDToken string `json:"id_token"`
	}
	if e := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); e != nil || body.IDToken == "" {
		return googleClaims{}, errors.New("missing id token")
	}
	return s.verifyGoogleToken(ctx, body.IDToken, nonce)
}
func (s *Server) verifyGoogleToken(ctx context.Context, raw, nonce string) (googleClaims, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", googleJWKSURL, nil)
	if e != nil {
		return googleClaims{}, e
	}
	resp, e := s.googleClient.Do(req)
	if e != nil {
		return googleClaims{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return googleClaims{}, errors.New("JWKS unavailable")
	}
	// Explicit map avoids accepting a differently named key as a signing key.
	var wire struct {
		Keys []struct {
			KID string `json:"kid"`
			KTY string `json:"kty"`
			ALG string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if e := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wire); e != nil {
		return googleClaims{}, e
	}
	claims := jwt.MapClaims{}
	parsed, e := jwt.NewParser(jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(s.cfg.GoogleClientID), jwt.WithExpirationRequired()).ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing kid")
		}
		for _, k := range wire.Keys {
			if k.KID != kid || k.KTY != "RSA" || (k.ALG != "" && k.ALG != "RS256") {
				continue
			}
			nb, e := base64.RawURLEncoding.DecodeString(k.N)
			if e != nil {
				return nil, e
			}
			eb, e := base64.RawURLEncoding.DecodeString(k.E)
			if e != nil {
				return nil, e
			}
			exp := new(big.Int).SetBytes(eb).Int64()
			if exp < 3 || exp > 1<<31-1 {
				return nil, errors.New("invalid exponent")
			}
			return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: int(exp)}, nil
		}
		return nil, errors.New("unknown signing key")
	})
	if e != nil || !parsed.Valid {
		return googleClaims{}, errors.New("invalid ID token")
	}
	str := func(k string) string { v, _ := claims[k].(string); return v }
	iss := str("iss")
	if iss != "https://accounts.google.com" && iss != "accounts.google.com" {
		return googleClaims{}, errors.New("invalid issuer")
	}
	// The audience must be the single configured client ID as a STRING —
	// array forms are not accepted loosely.
	aud, audIsString := claims["aud"].(string)
	if !audIsString || aud != s.cfg.GoogleClientID {
		return googleClaims{}, errors.New("invalid audience")
	}
	// azp is optional, but when PRESENT it must be a non-empty string equal
	// to the configured client ID. Wrong types (numbers/arrays) or an empty
	// value are rejected — they must never be treated as "absent".
	if azpVal, present := claims["azp"]; present {
		azp, ok := azpVal.(string)
		if !ok || azp == "" || azp != s.cfg.GoogleClientID {
			return googleClaims{}, errors.New("invalid azp")
		}
	}
	if str("nonce") != nonce || str("sub") == "" || claims["email_verified"] != true {
		return googleClaims{}, errors.New("invalid identity claims")
	}
	email := str("email")
	parsedEmail, emailErr := mail.ParseAddress(email)
	if emailErr != nil || parsedEmail.Address != email {
		return googleClaims{}, errors.New("invalid provider email")
	}
	return googleClaims{Subject: str("sub"), Email: email, Name: str("name")}, nil
}
