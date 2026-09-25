package secretbox

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func newKeyB64(t *testing.T) string {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k)
}

const plain = "whsec_fake_tenant_secret"

func TestSecretboxRoundTrip(t *testing.T) {
	t.Setenv(EnvKey, newKeyB64(t))
	enc, err := Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, Prefix) || strings.Contains(enc, plain) {
		t.Fatal("ciphertext must carry enc:v1: and not contain the plaintext")
	}
	enc2, _ := Encrypt(plain)
	if enc == enc2 {
		t.Fatal("random nonce: two encryptions of the same value must differ")
	}
	got, err := Decrypt(enc)
	if err != nil || got != plain {
		t.Fatalf("round trip failed: err=%v", err)
	}
}

func TestSecretboxTamperRejected(t *testing.T) {
	t.Setenv(EnvKey, newKeyB64(t))
	enc, _ := Encrypt(plain)
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, Prefix))
	raw[len(raw)-1] ^= 0x01
	tampered := Prefix + base64.StdEncoding.EncodeToString(raw)
	if _, err := Decrypt(tampered); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("want ErrDecrypt, got %v", err)
	}
	if _, err := Decrypt(Prefix + "!!notbase64"); !errors.Is(err, ErrMalformed) {
		t.Fatalf("want ErrMalformed, got %v", err)
	}
	if _, err := Decrypt(Prefix + base64.StdEncoding.EncodeToString([]byte("short"))); !errors.Is(err, ErrMalformed) {
		t.Fatalf("want ErrMalformed for short, got %v", err)
	}
}

func TestSecretboxWrongKeyRejected(t *testing.T) {
	t.Setenv(EnvKey, newKeyB64(t))
	enc, _ := Encrypt(plain)
	t.Setenv(EnvKey, newKeyB64(t))
	if _, err := Decrypt(enc); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("want ErrDecrypt, got %v", err)
	}
}

func TestSecretboxMissingOrInvalidKeyFailsClosed(t *testing.T) {
	t.Setenv(EnvKey, "")
	if _, err := Encrypt(plain); !errors.Is(err, ErrMissingKey) {
		t.Fatalf("encrypt: want ErrMissingKey, got %v", err)
	}
	if _, err := Decrypt(Prefix + "AAAA"); !errors.Is(err, ErrMissingKey) {
		t.Fatalf("decrypt: want ErrMissingKey, got %v", err)
	}
	t.Setenv(EnvKey, base64.StdEncoding.EncodeToString([]byte("too-short")))
	if _, err := Encrypt(plain); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("want ErrInvalidKey, got %v", err)
	}
	t.Setenv(EnvKey, "%%%not-base64%%%")
	if _, err := Encrypt(plain); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("want ErrInvalidKey, got %v", err)
	}
	if strings.Contains(ErrMissingKey.Error(), plain) {
		t.Fatal("error leaks value")
	}
}

func TestSecretboxLegacyPlaintextRejectedAndReEncryptIdempotent(t *testing.T) {
	t.Setenv(EnvKey, newKeyB64(t))
	if _, err := Decrypt(plain); !errors.Is(err, ErrLegacyPlaintext) {
		t.Fatalf("legacy plaintext must be rejected, got %v", err)
	}
	if strings.Contains(ErrLegacyPlaintext.Error(), plain) {
		t.Fatal("error leaks value")
	}
	out, changed, err := EnsureEncrypted(plain)
	if err != nil || !changed || !IsEncrypted(out) {
		t.Fatalf("legacy must be re-encrypted: changed=%v err=%v", changed, err)
	}
	again, changed2, err := EnsureEncrypted(out)
	if err != nil || changed2 || again != out {
		t.Fatalf("second pass must be a no-op: changed=%v err=%v", changed2, err)
	}
	got, err := Decrypt(out)
	if err != nil || got != plain {
		t.Fatalf("re-encrypted value must open: %v", err)
	}
	// A sealed value under another key is not silently re-sealed.
	t.Setenv(EnvKey, newKeyB64(t))
	if _, _, err := EnsureEncrypted(out); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong-key value must error, got %v", err)
	}
}

func TestSecretboxEmptyRejected(t *testing.T) {
	t.Setenv(EnvKey, newKeyB64(t))
	if _, err := Encrypt(""); !errors.Is(err, ErrEmpty) {
		t.Fatalf("want ErrEmpty, got %v", err)
	}
}
