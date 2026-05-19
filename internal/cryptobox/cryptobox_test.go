package cryptobox

import (
	"bytes"
	"testing"
)

func key32(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	key := key32(0x01)
	plain := []byte("hello, cryptobox")
	ct, err := Seal(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(key, ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round-trip mismatch: got %q, want %q", got, plain)
	}
}

func TestNonceUniqueness(t *testing.T) {
	key := key32(0x02)
	plain := []byte("same plaintext")
	ct1, err := Seal(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	ct2, err := Seal(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ct1, ct2) {
		t.Fatal("two Seal calls produced identical ciphertext (nonce reuse)")
	}
}

func TestTamperDetection(t *testing.T) {
	key := key32(0x03)
	ct, err := Seal(key, []byte("tamper me"))
	if err != nil {
		t.Fatal(err)
	}
	ct[len(ct)-1] ^= 0xFF // flip last byte
	if _, err := Open(key, ct); err == nil {
		t.Fatal("Open should fail on tampered ciphertext")
	}
}

func TestWrongKey(t *testing.T) {
	ct, err := Seal(key32(0x04), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(key32(0x05), ct); err == nil {
		t.Fatal("Open should fail with wrong key")
	}
}

func TestBadKeyLength(t *testing.T) {
	if _, err := Seal([]byte("short"), []byte("x")); err == nil {
		t.Fatal("Seal should error on non-32-byte key")
	}
	// need a valid ciphertext to test Open with bad key
	ct, _ := Seal(key32(0x06), []byte("x"))
	if _, err := Open([]byte("short"), ct); err == nil {
		t.Fatal("Open should error on non-32-byte key")
	}
}
