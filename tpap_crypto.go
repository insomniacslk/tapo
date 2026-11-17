// SPDX-License-Identifier: MIT

package tapo

// SPAKE2+ (P-256, SHA-256, HMAC) and the AES-CCM session cipher of the TPAP
// login, following https://github.com/python-kasa/python-kasa/pull/1592.
// Only cipher suite 1 is implemented.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"filippo.io/nistec"
)

const (
	spakeM = "02886e2f97ace46e55ba9dd7242579f2993b64e16ef3dcab95afd497333d8fa12f"
	spakeN = "03d8bbd6c639c62937b04d997f38c3770719c629d7014d49a24b4f98baa1292b49"
)

func mul(p *nistec.P256Point, k *big.Int) (*nistec.P256Point, error) {
	return nistec.NewP256Point().ScalarMult(p, k.FillBytes(make([]byte, 32)))
}

func constPoint(h string) (*nistec.P256Point, error) {
	b, err := hex.DecodeString(h)
	if err != nil {
		return nil, err
	}
	return nistec.NewP256Point().SetBytes(b)
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

type spakeResult struct {
	share, confirm, wantConfirm, key []byte
}

// spake2p computes the client side of the pake_share step. wantConfirm is
// what the device has to answer with.
func spake2p(cred string, salt []byte, iters int, userRandom, devRandom, devShare []byte) (*spakeResult, error) {
	m, err := constPoint(spakeM)
	if err != nil {
		return nil, err
	}
	n, err := constPoint(spakeN)
	if err != nil {
		return nil, err
	}
	r, err := nistec.NewP256Point().SetBytes(devShare)
	if err != nil {
		return nil, fmt.Errorf("invalid device share: %w", err)
	}
	if r.IsInfinity() == 1 {
		return nil, errors.New("device share is the point at infinity")
	}
	d, err := pbkdf2.Key(sha256.New, cred, salt, iters, 80)
	if err != nil {
		return nil, err
	}
	order := elliptic.P256().Params().N
	w0 := new(big.Int).Mod(new(big.Int).SetBytes(d[:40]), order)
	w1 := new(big.Int).Mod(new(big.Int).SetBytes(d[40:]), order)
	x, err := rand.Int(rand.Reader, new(big.Int).Sub(order, big.NewInt(1)))
	if err != nil {
		return nil, err
	}
	x.Add(x, big.NewInt(1))

	xg, err := nistec.NewP256Point().ScalarBaseMult(x.FillBytes(make([]byte, 32)))
	if err != nil {
		return nil, err
	}
	w0m, err := mul(m, w0)
	if err != nil {
		return nil, err
	}
	w0n, err := mul(n, w0)
	if err != nil {
		return nil, err
	}
	rp := nistec.NewP256Point().Add(r, nistec.NewP256Point().Negate(w0n))
	zp, err := mul(rp, x)
	if err != nil {
		return nil, err
	}
	vp, err := mul(rp, w1)
	if err != nil {
		return nil, err
	}
	l, z, v := nistec.NewP256Point().Add(xg, w0m).Bytes(), zp.Bytes(), vp.Bytes()

	ctx := sha256.Sum256(append(append([]byte("PAKE V1"), userRandom...), devRandom...))
	var t []byte
	for _, f := range [][]byte{ctx[:], nil, nil, m.Bytes(), n.Bytes(), l, r.Bytes(), z, v, w0.FillBytes(make([]byte, 32))} {
		t = binary.LittleEndian.AppendUint64(t, uint64(len(f)))
		t = append(t, f...)
	}
	th := sha256.Sum256(t)
	zero := make([]byte, 32)
	ck, err := hkdf.Key(sha256.New, th[:], zero, "ConfirmationKeys", 64)
	if err != nil {
		return nil, err
	}
	key, err := hkdf.Key(sha256.New, th[:], zero, "SharedKey", 32)
	if err != nil {
		return nil, err
	}
	return &spakeResult{l, hmacSHA256(ck[:32], r.Bytes()), hmacSHA256(ck[32:], l), key}, nil
}

// sessionCipher derives the aes_128_ccm session key and base nonce.
func sessionCipher(shared []byte) (cipher.Block, []byte, error) {
	key, err := hkdf.Key(sha256.New, shared, []byte("tp-kdf-salt-aes128-key"), "tp-kdf-info-aes128-key", 16)
	if err != nil {
		return nil, nil, err
	}
	nonce, err := hkdf.Key(sha256.New, shared, []byte("tp-kdf-salt-aes128-iv"), "tp-kdf-info-aes128-iv", 12)
	if err != nil {
		return nil, nil, err
	}
	b, err := aes.NewCipher(key)
	return b, nonce, err
}

// AES-CCM with a 12-byte nonce, a 16-byte tag and no additional data (RFC 3610).

func ccmKeystream(b cipher.Block, nonce []byte, ctr int) []byte {
	a := make([]byte, 16)
	a[0] = 2
	copy(a[1:], nonce)
	a[13], a[14], a[15] = byte(ctr>>16), byte(ctr>>8), byte(ctr)
	b.Encrypt(a, a)
	return a
}

func ccmXOR(b cipher.Block, nonce, in []byte) []byte {
	out := make([]byte, len(in))
	var ks []byte
	for i := range in {
		if i%16 == 0 {
			ks = ccmKeystream(b, nonce, i/16+1)
		}
		out[i] = in[i] ^ ks[i%16]
	}
	return out
}

func ccmTag(b cipher.Block, nonce, pt []byte) []byte {
	x := make([]byte, 16)
	x[0] = 0x3a
	copy(x[1:], nonce)
	x[13], x[14], x[15] = byte(len(pt)>>16), byte(len(pt)>>8), byte(len(pt))
	b.Encrypt(x, x)
	for i := 0; i < len(pt); i += 16 {
		for j, c := range pt[i:min(i+16, len(pt))] {
			x[j] ^= c
		}
		b.Encrypt(x, x)
	}
	s0 := ccmKeystream(b, nonce, 0)
	for i := range x {
		x[i] ^= s0[i]
	}
	return x
}

func ccmSeal(b cipher.Block, nonce, pt []byte) []byte {
	return append(ccmXOR(b, nonce, pt), ccmTag(b, nonce, pt)...)
}

func ccmOpen(b cipher.Block, nonce, ct []byte) ([]byte, error) {
	if len(ct) < 16 {
		return nil, errors.New("ciphertext too short")
	}
	pt := ccmXOR(b, nonce, ct[:len(ct)-16])
	if subtle.ConstantTimeCompare(ccmTag(b, nonce, pt), ct[len(ct)-16:]) != 1 {
		return nil, errors.New("authentication tag mismatch")
	}
	return pt, nil
}
