package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The session cookie is stateless: the value carries the signed-in identity,
// the display fields and the expiry, all covered by an HMAC-SHA256 tag. No
// session store is touched, so a restart (or another instance with the same
// SESSION_SECRET) verifies a cookie without coordinating.
//
// Value layout: v1.<base64url(claims JSON)>.<base64url(HMAC-SHA256)>.
// The tag is computed over "session.v1." + the payload segment, so a signature
// made for a session cookie can never be replayed as a signature for another
// purpose the application may sign later. Base64url never emits ".", so the
// three segments stay unambiguous without escaping.
const (
	sessionCookieName    = "asterisk_session"
	sessionCookiePath    = "/api"
	sessionVersion       = "v1"
	sessionAudience      = "asterisk"
	sessionTTL           = 24 * time.Hour
	minSessionSecretSize = 32
	maxSessionTokenSize  = 4096
	sessionMACPrefix     = "session.v1."
)

// sessionClaims is the signed cookie payload. The subject is the identity every
// authorization decision is made on; name and email are presentation only.
type sessionClaims struct {
	Subject  string `json:"sub"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
	Audience string `json:"aud"`
	Expires  int64  `json:"exp"`
}

// sessionCodec mints and verifies session cookies. The audience keeps a cookie
// minted by one application from being accepted by another even when both are
// configured with the same SESSION_SECRET.
type sessionCodec struct {
	key      []byte
	audience string
	now      func() time.Time
}

// newSessionCodec builds the codec. A secret shorter than 32 bytes is rejected
// rather than stretched: HMAC-SHA256 adds nothing to a weak key, and a startup
// error is easier to diagnose than silently weak sessions.
func newSessionCodec(secret, audience string) (*sessionCodec, error) {
	if len(secret) < minSessionSecretSize {
		return nil, fmt.Errorf("session secret must be at least %d bytes", minSessionSecretSize)
	}
	if audience == "" {
		return nil, errors.New("session audience must not be empty")
	}
	return &sessionCodec{key: []byte(secret), audience: audience, now: time.Now}, nil
}

// Issue mints a session token for subject and returns it with the expiry the
// cookie must carry. The caller sets both the cookie value and its Expires from
// the same instant, so the browser and the tag cannot disagree.
func (c *sessionCodec) Issue(subject, name, email string) (string, time.Time, error) {
	expires := c.now().Add(sessionTTL)
	payload, err := json.Marshal(sessionClaims{
		Subject:  subject,
		Name:     name,
		Email:    email,
		Audience: c.audience,
		Expires:  expires.Unix(),
	})
	if err != nil {
		return "", time.Time{}, err
	}

	segment := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte(sessionMACPrefix + segment))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return sessionVersion + "." + segment + "." + signature, expires, nil
}

// Decode verifies a cookie value and returns the claims it carries. It rejects
// anything malformed, tampered with, minted for another audience, or expired.
// The tag comparison is constant time, so a forged cookie cannot be discovered
// tag byte by tag byte.
func (c *sessionCodec) Decode(token string) (sessionClaims, bool) {
	var empty sessionClaims
	if c == nil || token == "" || len(token) > maxSessionTokenSize {
		return empty, false
	}

	version, rest, ok := strings.Cut(token, ".")
	if !ok || version != sessionVersion {
		return empty, false
	}
	segment, signature, ok := strings.Cut(rest, ".")
	if !ok || segment == "" || signature == "" {
		return empty, false
	}

	gotSignature, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return empty, false
	}
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte(sessionMACPrefix + segment))
	if !hmac.Equal(mac.Sum(nil), gotSignature) {
		return empty, false
	}

	payload, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return empty, false
	}
	var claims sessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return empty, false
	}
	if claims.Subject == "" || claims.Audience != c.audience {
		return empty, false
	}
	if c.now().Unix() > claims.Expires {
		return empty, false
	}
	return claims, true
}

// newSessionCookie builds the Set-Cookie value. Secure follows the request
// scheme, so plain-HTTP development works without a switch and a production
// HTTPS deployment always gets the flag. Lax keeps the cookie off cross-site
// POST/DELETE while still surviving a top-level navigation such as an OIDC
// redirect.
func newSessionCookie(name, path, token string, expires time.Time, secure bool) *http.Cookie {
	if expires.IsZero() {
		expires = time.Now().Add(sessionTTL)
	}
	return &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     path,
		Expires:  expires,
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// clearSessionCookie builds the deletion cookie. It carries the same attributes
// as newSessionCookie, because a browser matches the deletion on name, path and
// domain.
func clearSessionCookie(name, path string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}
