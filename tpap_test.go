// SPDX-License-Identifier: MIT

package tapo

import (
	"crypto/aes"
	"encoding/hex"
	"testing"
)

// Vectors from Python's cryptography AESCCM, key 00..0f, nonce 00..0b.
func TestCCM(t *testing.T) {
	b, err := aes.NewCipher([]byte("\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f"))
	if err != nil {
		t.Fatal(err)
	}
	nonce := []byte("\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b")
	for _, tc := range []struct{ pt, ct string }{
		{"", "a9452a1d712c00a7b1c3fab799f1bc51"},
		{"68656c6c6f2074706170", "5b709f0bb3a0b0c1106301586db4f848e8c82f48a754cc05ef9b"},
		{
			"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f2021222324252627",
			"3314f164d885c2b6791ac3eb0ee78b8f7c470b21df11a12f567e5686ec3db5aed2646b3e30bb282a866ac1eca8f10416fdb535b32338bcdc",
		},
	} {
		pt, _ := hex.DecodeString(tc.pt)
		ct := hex.EncodeToString(ccmSeal(b, nonce, pt))
		if ct != tc.ct {
			t.Errorf("seal(%s) = %s, want %s", tc.pt, ct, tc.ct)
		}
		raw, _ := hex.DecodeString(tc.ct)
		got, err := ccmOpen(b, nonce, raw)
		if err != nil || hex.EncodeToString(got) != tc.pt {
			t.Errorf("open(%s) = %x, %v", tc.ct, got, err)
		}
		raw[len(raw)-1] ^= 1
		if _, err := ccmOpen(b, nonce, raw); err == nil {
			t.Errorf("open accepted a corrupted tag for %s", tc.pt)
		}
	}
}

func TestPasscode(t *testing.T) {
	got, err := passcode("password", "password_shadow", 2)
	if err != nil || got != "5baa61e4c9b93f3f0682250b6cf8331b7ee68fd8" {
		t.Errorf("passcode = %q, %v", got, err)
	}
	if _, err := passcode("password", "", 0); err == nil {
		t.Error("missing extra_crypt accepted")
	}
	if _, err := passcode("password", "password_shadow", 9); err == nil {
		t.Error("unsupported passwd_id accepted")
	}
}
