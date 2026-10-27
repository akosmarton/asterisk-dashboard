package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// testSessionSecret is 32 bytes: the minimum the codec accepts.
const testSessionSecret = "0123456789abcdef0123456789abcdef"

var testSessionNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newTestSessionCodec(t *testing.T) *sessionCodec {
	t.Helper()
	codec, err := newSessionCodec(testSessionSecret, sessionAudience)
	if err != nil {
		t.Fatalf("newSessionCodec: %v", err)
	}
	codec.now = func() time.Time { return testSessionNow }
	return codec
}

func mustIssueSession(t *testing.T, codec *sessionCodec, subject, name, email string) string {
	t.Helper()
	token, _, err := codec.Issue(subject, name, email)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	return token
}

func mustDecodeSession(t *testing.T, codec *sessionCodec, token string) sessionClaims {
	t.Helper()
	claims, ok := codec.Decode(token)
	if !ok {
		t.Fatalf("expected a valid session token")
	}
	return claims
}

func TestSessionCodecRoundTrip(t *testing.T) {
	codec := newTestSessionCodec(t)
	token := mustIssueSession(t, codec, "user@example.com", "Ada A. Admin", "user@example.com")

	claims := mustDecodeSession(t, codec, token)
	if claims.Subject != "user@example.com" || claims.Name != "Ada A. Admin" || claims.Email != "user@example.com" {
		t.Errorf("unexpected claims: %+v", claims)
	}
	if claims.Audience != sessionAudience {
		t.Errorf("expected audience %q, got %q", sessionAudience, claims.Audience)
	}
	if want := testSessionNow.Add(sessionTTL).Unix(); claims.Expires != want {
		t.Errorf("expected expiry %d, got %d", want, claims.Expires)
	}
}

func TestSessionCodecRejectsTampering(t *testing.T) {
	codec := newTestSessionCodec(t)
	token := mustIssueSession(t, codec, "admin", "Admin", "")

	version, rest, _ := strings.Cut(token, ".")
	segment, signature, _ := strings.Cut(rest, ".")

	// A rewritten payload keeps the old tag and must be rejected.
	payload, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims sessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	claims.Subject = "attacker"
	edited, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	forgedPayload := version + "." + base64.RawURLEncoding.EncodeToString(edited) + "." + signature
	if _, ok := codec.Decode(forgedPayload); ok {
		t.Errorf("expected a rewritten payload to be rejected")
	}

	// A modified tag must be rejected even when the payload is untouched.
	badSignature := "A" + signature[1:]
	if signature[0] == 'A' {
		badSignature = "B" + signature[1:]
	}
	if _, ok := codec.Decode(version + "." + segment + "." + badSignature); ok {
		t.Errorf("expected a modified signature to be rejected")
	}
}

func TestSessionCodecRejectsExpiredToken(t *testing.T) {
	codec := newTestSessionCodec(t)
	token := mustIssueSession(t, codec, "admin", "Admin", "")

	codec.now = func() time.Time { return testSessionNow.Add(sessionTTL + time.Second) }
	if _, ok := codec.Decode(token); ok {
		t.Errorf("expected an expired token to be rejected")
	}
}

func TestSessionCodecRejectsAnotherAudience(t *testing.T) {
	issuer := newTestSessionCodec(t)
	token := mustIssueSession(t, issuer, "admin", "Admin", "")

	other, err := newSessionCodec(testSessionSecret, "other-app")
	if err != nil {
		t.Fatalf("newSessionCodec: %v", err)
	}
	other.now = func() time.Time { return testSessionNow }
	if _, ok := other.Decode(token); ok {
		t.Errorf("expected a token minted for %q to be rejected by another audience", sessionAudience)
	}
}

func TestSessionCodecRejectsMalformedTokens(t *testing.T) {
	codec := newTestSessionCodec(t)
	valid := mustIssueSession(t, codec, "admin", "Admin", "")
	_, rest, _ := strings.Cut(valid, ".")

	for _, token := range []string{
		"",
		"v1",
		"v1.",
		"v1..",
		"v2." + rest,
		"v1." + rest + ".not base64!",
		strings.Repeat("a", maxSessionTokenSize+1),
	} {
		if _, ok := codec.Decode(token); ok {
			t.Errorf("expected %q to be rejected", token)
		}
	}

	var nilCodec *sessionCodec
	if _, ok := nilCodec.Decode(valid); ok {
		t.Errorf("expected a nil codec to reject every token")
	}
	if _, ok := codec.Decode(valid); !ok {
		t.Errorf("expected the untampered token to stay valid")
	}
}

func TestNewSessionCodecRejectsWeakConfiguration(t *testing.T) {
	if _, err := newSessionCodec(strings.Repeat("a", minSessionSecretSize-1), sessionAudience); err == nil {
		t.Errorf("expected a short secret to be rejected")
	}
	if _, err := newSessionCodec(testSessionSecret, ""); err == nil {
		t.Errorf("expected an empty audience to be rejected")
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	expires := testSessionNow.Add(sessionTTL)
	cookie := newSessionCookie("test_session", "/api/test", "token", expires, true)
	if cookie.Name != "test_session" || cookie.Value != "token" {
		t.Errorf("unexpected name/value: %+v", cookie)
	}
	if cookie.Path != "/api/test" {
		t.Errorf("expected Path=/api/test, got %q", cookie.Path)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("unexpected flags: HttpOnly=%v Secure=%v SameSite=%v", cookie.HttpOnly, cookie.Secure, cookie.SameSite)
	}
	if cookie.MaxAge != int(sessionTTL.Seconds()) || !cookie.Expires.Equal(expires) {
		t.Errorf("unexpected expiry: MaxAge=%d Expires=%v", cookie.MaxAge, cookie.Expires)
	}

	cleared := clearSessionCookie("x_session", "/api/test", false)
	if cleared.MaxAge != -1 || !cleared.Expires.Equal(time.Unix(0, 0)) {
		t.Errorf("expected a deletion cookie, got MaxAge=%d Expires=%v", cleared.MaxAge, cleared.Expires)
	}
	if cleared.Path != "/api/test" || !cleared.HttpOnly || cleared.Secure || cleared.SameSite != http.SameSiteLaxMode {
		t.Errorf("unexpected deletion cookie attributes: %+v", cleared)
	}
}
