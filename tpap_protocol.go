// SPDX-License-Identifier: MIT

package tapo

// Tapo TPAP protocol, the SPAKE2+ login of newer firmware. It replicates
// https://github.com/python-kasa/python-kasa/pull/1592 for plain-HTTP devices
// that log in with the account password (pake 2) and cipher suite 1.

import (
	"bytes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
)

// A rejected pake_share counts toward the device's lockout, so the login is
// never retried. The iteration cap bounds work done on unauthenticated input.
const (
	maxPakeIterations = 100_000
	maxBody           = 1 << 20
)

var (
	// ErrNotTPAP means the device does not answer the TPAP discovery.
	ErrNotTPAP = errors.New("not a TPAP device")
	// ErrAuthRejected means the device rejected the credentials. Do not retry.
	ErrAuthRejected = errors.New("TPAP login rejected")

	tpapClient = &http.Client{Timeout: defaultTimeout}
)

func NewTpapSession(l *log.Logger) *TpapSession {
	return &TpapSession{log: l}
}

type TpapSession struct {
	log   *log.Logger
	host  string
	base  string
	ds    string
	block cipher.Block
	nonce []byte
	mu    sync.Mutex
	seq   uint32
}

func (s *TpapSession) Host() string {
	return s.host
}

// login posts one login step and decodes its result.
func (s *TpapSession) login(params map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"method": "login", "params": params})
	if err != nil {
		return err
	}
	resp, err := tpapClient.Post(s.base+"/", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("http POST failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var r UntypedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&r); err != nil {
		return fmt.Errorf("failed to decode response (HTTP %s): %w", resp.Status, err)
	}
	if r.ErrorCode != StatusSuccess {
		return fmt.Errorf("%v: %w", params["sub_method"], r.ErrorCode)
	}
	if r.Result == nil {
		return fmt.Errorf("%v: empty result", params["sub_method"])
	}
	return json.Unmarshal(*r.Result, out)
}

// passcode encodes the password as the device's extra_crypt asks.
func passcode(password, typ string, id int) (string, error) {
	switch {
	case typ == "password_shadow" && id == 2:
		h := sha1.Sum([]byte(password))
		return hex.EncodeToString(h[:]), nil
	}
	return "", fmt.Errorf("unsupported extra_crypt %q, passwd_id %d", typ, id)
}

func (s *TpapSession) Handshake(host, username, password string) error {
	s.host = host
	s.base = "http://" + host
	var disc struct {
		Tpap struct {
			TLS  int   `json:"tls"`
			Port int   `json:"port"`
			Pake []int `json:"pake"`
		} `json:"tpap"`
	}
	if err := s.login(map[string]any{"sub_method": "discover"}, &disc); err != nil {
		return fmt.Errorf("%w: %w", ErrNotTPAP, err)
	}
	if disc.Tpap.TLS != 0 || !slices.Contains(disc.Tpap.Pake, 2) {
		return fmt.Errorf("unsupported TPAP login: tls %d, pake %v", disc.Tpap.TLS, disc.Tpap.Pake)
	}
	if disc.Tpap.Port > 0 {
		s.base = "http://" + net.JoinHostPort(host, strconv.Itoa(disc.Tpap.Port))
	}

	var userRandom [32]byte
	if _, err := rand.Read(userRandom[:]); err != nil {
		return err
	}
	user := md5.Sum([]byte("admin"))
	var reg struct {
		DevRandom    string `json:"dev_random"`
		DevSalt      string `json:"dev_salt"`
		DevShare     string `json:"dev_share"`
		CipherSuites int    `json:"cipher_suites"`
		Encryption   string `json:"encryption"`
		Iterations   int    `json:"iterations"`
		ExtraCrypt   struct {
			Type   string `json:"type"`
			Params struct {
				PasswdID int `json:"passwd_id"`
			} `json:"params"`
		} `json:"extra_crypt"`
	}
	err := s.login(map[string]any{
		"sub_method":    "pake_register",
		"username":      hex.EncodeToString(user[:]),
		"user_random":   base64.StdEncoding.EncodeToString(userRandom[:]),
		"cipher_suites": []int{1},
		"encryption":    []string{"aes_128_ccm"},
		"passcode_type": "userpw",
	}, &reg)
	if err != nil {
		return fmt.Errorf("TPAP pake_register failed: %w", err)
	}
	if reg.CipherSuites != 1 || reg.Encryption != "aes_128_ccm" {
		return fmt.Errorf("unsupported TPAP suite %d, cipher %q", reg.CipherSuites, reg.Encryption)
	}
	if reg.Iterations < 1 || reg.Iterations > maxPakeIterations {
		return fmt.Errorf("invalid TPAP iterations: %d", reg.Iterations)
	}
	pc, err := passcode(password, reg.ExtraCrypt.Type, reg.ExtraCrypt.Params.PasswdID)
	if err != nil {
		return err
	}
	var salt, devRandom, devShare []byte
	for _, f := range []struct {
		dst *[]byte
		s   string
	}{{&salt, reg.DevSalt}, {&devRandom, reg.DevRandom}, {&devShare, reg.DevShare}} {
		if *f.dst, err = base64.StdEncoding.DecodeString(f.s); err != nil {
			return fmt.Errorf("TPAP pake_register: bad base64: %w", err)
		}
	}
	sp, err := spake2p(pc, salt, reg.Iterations, userRandom[:], devRandom, devShare)
	if err != nil {
		return fmt.Errorf("SPAKE2+ failed: %w", err)
	}

	var share struct {
		DevConfirm string `json:"dev_confirm"`
		SessionID  string `json:"sessionId"`
		Stok       string `json:"stok"`
		StartSeq   uint32 `json:"start_seq"`
	}
	err = s.login(map[string]any{
		"sub_method":   "pake_share",
		"user_share":   base64.StdEncoding.EncodeToString(sp.share),
		"user_confirm": base64.StdEncoding.EncodeToString(sp.confirm),
	}, &share)
	if err != nil {
		var st TapoStatus
		if errors.As(err, &st) {
			err = fmt.Errorf("%w (wrong password?): %w", ErrAuthRejected, err)
		}
		return fmt.Errorf("TPAP pake_share failed: %w", err)
	}
	if dc, err := base64.StdEncoding.DecodeString(share.DevConfirm); err != nil || !hmac.Equal(dc, sp.wantConfirm) {
		return fmt.Errorf("TPAP confirmation mismatch")
	}
	id := share.SessionID
	if id == "" {
		id = share.Stok
	}
	if id == "" {
		return fmt.Errorf("TPAP share response has no session id")
	}
	if s.block, s.nonce, err = sessionCipher(sp.key); err != nil {
		return err
	}
	s.seq = share.StartSeq
	s.ds = s.base + "/stok=" + url.PathEscape(id) + "/ds"
	return nil
}

func (s *TpapSession) Request(payload []byte) (*UntypedResponse, error) {
	if s.block == nil {
		return nil, fmt.Errorf("not logged in")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := s.seq
	s.seq++
	body := binary.BigEndian.AppendUint32(nil, seq)
	body = append(body, ccmSeal(s.block, s.seqNonce(seq), payload)...)
	resp, err := tpapClient.Post(s.ds, "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http POST failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("expected 200 OK, got %s. Error message: %s", resp.Status, data)
	}
	if len(data) < 4 || json.Valid(data) {
		// A plain reply is the device reporting an error, never an answer.
		return nil, fmt.Errorf("response not encrypted: %s", data)
	}
	if rs := binary.BigEndian.Uint32(data); rs != seq {
		return nil, fmt.Errorf("reply sequence %d, want %d", rs, seq)
	}
	plain, err := ccmOpen(s.block, s.seqNonce(seq), data[4:])
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt payload: %w", err)
	}
	var r UntypedResponse
	if err := json.Unmarshal(plain, &r); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	var probe struct {
		ErrorCode *int `json:"error_code"`
	}
	if json.Unmarshal(plain, &probe) != nil || probe.ErrorCode == nil {
		return nil, errors.New("reply has no error_code")
	}
	return &r, nil
}

func (s *TpapSession) seqNonce(seq uint32) []byte {
	n := slices.Clone(s.nonce)
	binary.BigEndian.PutUint32(n[8:], seq)
	return n
}
