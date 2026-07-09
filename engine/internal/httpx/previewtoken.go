package httpx

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Preview tokens are the capability that guards a served preview (audit C2 extended
// to serving). A preview runs UNTRUSTED repo JS; if the URL carried a session/service
// token, that JS could read its own location and replay the token against the API
// from the same origin. So /previews is authorized by a DIFFERENT credential: a
// short-lived HMAC token scoped to EXACTLY one /previews/{project}/{run}/ path and
// useless anywhere else. The console mints one (session-authenticated) to open a
// preview; httpx.Auth accepts only this type on /previews — never a session/service
// token by query.

// previewClaims is a preview token's payload: the one preview it authorizes + expiry.
type previewClaims struct {
	Project string `json:"p"`
	Run     string `json:"r"`
	Exp     int64  `json:"e"` // unix seconds
}

// ErrPreviewToken is returned when a preview token is malformed, forged, or expired.
var ErrPreviewToken = errors.New("invalid preview token")

// MintPreviewToken signs a capability for /previews/{project}/{run}/ valid for ttl
// from now. The returned token authorizes ONLY that preview path — it is not a
// session token and carries no user identity or broader access.
func MintPreviewToken(secret []byte, project, run string, ttl time.Duration, now time.Time) string {
	body, _ := json.Marshal(previewClaims{Project: project, Run: run, Exp: now.Add(ttl).Unix()})
	b := base64.RawURLEncoding.EncodeToString(body)
	return b + "." + previewSign(secret, b)
}

func previewSign(secret []byte, msg string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// VerifyPreviewToken checks the HMAC + expiry of token and returns its claims. now
// is passed in for testability. A tampered signature, bad encoding, or a past expiry
// all return ErrPreviewToken.
func VerifyPreviewToken(secret []byte, token string, now time.Time) (project, run string, err error) {
	i := strings.LastIndexByte(token, '.')
	if i <= 0 {
		return "", "", ErrPreviewToken
	}
	body, sig := token[:i], token[i+1:]
	if subtle.ConstantTimeCompare([]byte(sig), []byte(previewSign(secret, body))) != 1 {
		return "", "", ErrPreviewToken
	}
	raw, decErr := base64.RawURLEncoding.DecodeString(body)
	if decErr != nil {
		return "", "", ErrPreviewToken
	}
	var c previewClaims
	if json.Unmarshal(raw, &c) != nil {
		return "", "", ErrPreviewToken
	}
	if now.Unix() > c.Exp {
		return "", "", ErrPreviewToken // expired
	}
	return c.Project, c.Run, nil
}
