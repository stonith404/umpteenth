package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

const (
	SessionCookieName = "umpteenth_session"
	loginCookieName   = "umpteenth_login"
	sessionTTL        = 7 * 24 * time.Hour
	loginStateTTL     = 10 * time.Minute
)

// Cookie kinds are part of every signature, so a cookie of one kind never verifies as another
// Without them, the login-state cookie that anyone can get from /api/auth/login would pass as a session
const (
	kindSession    = "session"
	kindLoginState = "login"
)

// sessionClaims is the signed content of the session cookie
// Sessions are stateless so any replica can serve any request (PLAN.md §3.4)
type sessionClaims struct {
	UserID      string `json:"uid"`
	WorkspaceID string `json:"wid"`
	// Provider is the ID of the sign-in provider the user signed in with, which the login page remembers as the last one used
	Provider  string `json:"pid,omitempty"`
	ExpiresAt int64  `json:"exp"`
}

// loginState is the signed, short-lived login state kept in a cookie instead of process memory
type loginState struct {
	// Provider is the ID of the sign-in provider the login started with, the only one whose callback may finish it
	Provider  string `json:"provider"`
	State     string `json:"state"`
	Nonce     string `json:"nonce"`
	Verifier  string `json:"verifier"`
	Redirect  string `json:"redirect"`
	ExpiresAt int64  `json:"exp"`
}

// cookieCodec signs and verifies JSON cookie payloads
type cookieCodec struct {
	key    []byte
	secure bool
}

func (c *cookieCodec) encode(kind string, v any) (string, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	data := base64.RawURLEncoding.EncodeToString(payload)
	return data + "." + crypto.Sign(c.key, []byte(kind+"."+data)), nil
}

func (c *cookieCodec) decode(kind, value string, into any) error {
	data, sig, ok := strings.Cut(value, ".")
	if !ok || !crypto.Verify(c.key, []byte(kind+"."+data), sig) {
		return errors.New("invalid cookie signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, into)
}

func (c *cookieCodec) cookie(name, value, path string, ttl time.Duration) http.Cookie {
	// #nosec G124 -- HttpOnly and SameSite are always set; Secure follows app.url so plain-HTTP development works
	return http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   c.secure,
		// Lax is required so the cookie survives the top-level redirect back from the sign-in provider
		SameSite: http.SameSiteLaxMode,
	}
}

func (c *cookieCodec) expired(name, path string) http.Cookie {
	// #nosec G124 -- an expiring cookie carries no value
	return http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1, HttpOnly: true, Secure: c.secure, SameSite: http.SameSiteLaxMode}
}

// sessionCookie creates a signed session cookie for the user in the workspace that ends at expiresAt
func (c *cookieCodec) sessionCookie(userID, workspaceID, providerID string, expiresAt time.Time) (http.Cookie, error) {
	value, err := c.encode(kindSession, sessionClaims{UserID: userID, WorkspaceID: workspaceID, Provider: providerID, ExpiresAt: expiresAt.Unix()})
	if err != nil {
		return http.Cookie{}, err
	}
	return c.cookie(SessionCookieName, value, "/", time.Until(expiresAt)), nil
}

// parseSession verifies a session cookie value and checks its expiry
func (c *cookieCodec) parseSession(value string) (sessionClaims, error) {
	var claims sessionClaims
	err := c.decode(kindSession, value, &claims)
	if err != nil {
		return sessionClaims{}, err
	}
	if time.Now().Unix() > claims.ExpiresAt {
		return sessionClaims{}, errors.New("session expired")
	}
	if claims.UserID == "" || claims.WorkspaceID == "" {
		return sessionClaims{}, errors.New("session names no user or workspace")
	}
	return claims, nil
}
