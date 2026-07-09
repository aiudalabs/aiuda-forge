package httpx

import (
	"errors"
	"testing"
	"time"
)

func TestPreviewTokenRoundTrip(t *testing.T) {
	secret := []byte("a-32-byte-ish-hmac-secret-000000")
	now := time.Unix(1_700_000_000, 0)
	tok := MintPreviewToken(secret, "proj-1", "run_abc", 10*time.Minute, now)

	proj, run, err := VerifyPreviewToken(secret, tok, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if proj != "proj-1" || run != "run_abc" {
		t.Fatalf("claims = %q/%q, want proj-1/run_abc", proj, run)
	}
}

func TestPreviewTokenRejectsTampering(t *testing.T) {
	secret := []byte("a-32-byte-ish-hmac-secret-000000")
	now := time.Unix(1_700_000_000, 0)
	tok := MintPreviewToken(secret, "proj-1", "run_abc", 10*time.Minute, now)

	// Flip the last character of the signature.
	bad := tok[:len(tok)-1]
	if tok[len(tok)-1] == 'A' {
		bad += "B"
	} else {
		bad += "A"
	}
	if _, _, err := VerifyPreviewToken(secret, bad, now); !errors.Is(err, ErrPreviewToken) {
		t.Fatalf("tampered token: err = %v, want ErrPreviewToken", err)
	}

	// A different secret must not verify.
	if _, _, err := VerifyPreviewToken([]byte("different-secret-different-key-0"), tok, now); !errors.Is(err, ErrPreviewToken) {
		t.Fatalf("wrong secret: err = %v, want ErrPreviewToken", err)
	}

	// Malformed (no signature separator).
	if _, _, err := VerifyPreviewToken(secret, "not-a-token", now); !errors.Is(err, ErrPreviewToken) {
		t.Fatalf("malformed: err = %v, want ErrPreviewToken", err)
	}
}

func TestPreviewTokenExpires(t *testing.T) {
	secret := []byte("a-32-byte-ish-hmac-secret-000000")
	now := time.Unix(1_700_000_000, 0)
	tok := MintPreviewToken(secret, "p", "r", 5*time.Minute, now)

	// One second past expiry → rejected.
	if _, _, err := VerifyPreviewToken(secret, tok, now.Add(5*time.Minute+time.Second)); !errors.Is(err, ErrPreviewToken) {
		t.Fatalf("expired token: err = %v, want ErrPreviewToken", err)
	}
	// Just before expiry → still valid.
	if _, _, err := VerifyPreviewToken(secret, tok, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("pre-expiry token should verify: %v", err)
	}
}
