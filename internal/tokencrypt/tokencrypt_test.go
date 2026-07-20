package tokencrypt

import (
	"bytes"
	"testing"
)

func testKey() []byte { return bytes.Repeat([]byte{0x2a}, 32) }

func TestSealOpenRoundTrip(t *testing.T) {
	c, err := New(testKey())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	plaintext := []byte(`{"access":"gho_x","refresh":"ghr_y"}`)
	ct, err := c.Seal(plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(ct, plaintext) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := c.Open(ct)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip mismatch: got %q", got)
	}
}

func TestSealIsNondeterministic(t *testing.T) {
	c, _ := New(testKey())
	a, _ := c.Seal([]byte("same"))
	b, _ := c.Seal([]byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext were identical (nonce reuse)")
	}
}

func TestOpenRejectsTamper(t *testing.T) {
	c, _ := New(testKey())
	ct, _ := c.Seal([]byte("secret"))
	ct[len(ct)-1] ^= 0xff
	if _, err := c.Open(ct); err == nil {
		t.Fatal("expected error opening tampered ciphertext")
	}
}

func TestNewRejectsShortKey(t *testing.T) {
	if _, err := New([]byte("too-short")); err == nil {
		t.Fatal("expected error for non-32-byte key")
	}
}
