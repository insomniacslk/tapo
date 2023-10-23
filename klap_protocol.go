package tapo

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type KlapSession struct {
	log           *log.Logger
	addr          netip.Addr
	SessionID     string
	Expiry        time.Time
	LocalSeed     []byte
	RemoteSeed    []byte
	LocalAuthHash []byte
}

func (s *KlapSession) Addr() netip.Addr {
	return s.addr
}

func (s *KlapSession) encrypt(data []byte) ([]byte, error) {
	// see https://github.com/petretiandrea/plugp100/blob/main/plugp100/protocol/klap_protocol.py#L293
	return nil, fmt.Errorf("KLAP encryption not implemented yet")
}

func (s *KlapSession) decrypt(data []byte) ([]byte, error) {
	// see https://github.com/petretiandrea/plugp100/blob/main/plugp100/protocol/klap_protocol.py#L318
	return nil, fmt.Errorf("KLAP decryption not implemented yet")
}

func (s *KlapSession) Request(payload []byte) ([]byte, error) {
	u := url.URL{
		Scheme: "http",
		Host:   s.addr.String(),
		Path:   "/app/request",
	}
	encrypted, err := s.encrypt(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt payload: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(encrypted))
	if err != nil {
		return nil, fmt.Errorf("http new request creation failed: %w", err)
	}
	c := http.Client{}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http POST failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("expected 200 OK, got %s. Error message: %s", resp.Status, body)
	}
	decrypted, err := s.decrypt(body)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt payload: %w", err)
	}
	return decrypted, nil
}

func (s *KlapSession) Handshake(addr netip.Addr, username, password string) error {
	s.addr = addr
	if err := s.handshake1(username, password, addr); err != nil {
		return fmt.Errorf("KLAP handshake1 failed: %w", err)
	}
	return s.handshake2(addr)
}

func (s *KlapSession) handshake2(target netip.Addr) error {
	u := url.URL{
		Scheme: "http",
		Host:   target.String(),
		Path:   "/app/handshake2",
	}
	bytesToHash := append(s.RemoteSeed, s.LocalSeed...)
	bytesToHash = append(bytesToHash, s.LocalAuthHash...)
	payload := sha256.Sum256(bytesToHash)
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("failed to create cookie jar: %w", err)
	}
	c := http.Client{
		Jar: jar,
	}
	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(payload[:]))
	if err != nil {
		return fmt.Errorf("http new request creation failed: %w", err)
	}
	c.Jar.SetCookies(req.URL, []*http.Cookie{&http.Cookie{Name: "TP_SESSIONID", Value: s.SessionID}})
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("http POST failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("expected 200 OK, got %s. Error message: %s", resp.Status, body)
	}
	return nil
}

func (s *KlapSession) handshake1(username, password string, target netip.Addr) error {
	u := url.URL{
		Scheme: "http",
		Host:   target.String(),
		Path:   "/app/handshake1",
	}
	var localSeed [16]byte
	if _, err := rand.Read(localSeed[:]); err != nil {
		return fmt.Errorf("failed to generate local seed: %w", err)
	}
	c := http.Client{}
	resp, err := c.Post(u.String(), "application/octet-stream", bytes.NewReader(localSeed[:]))
	if err != nil {
		return fmt.Errorf("http post failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	cookies, err := parseBrokenCookies(resp)
	if err != nil {
		return fmt.Errorf("failed to parse cookies: %w", err)
	}
	var (
		sessionID string
		expiry    time.Time
	)
	for _, c := range cookies {
		if c.Name == "TP_SESSIONID" {
			sessionID = c.Value
		} else if c.Name == "TIMEOUT" {
			timeout, err := strconv.ParseInt(c.Value, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid timeout string '%s': %w", c.Value, err)
			}
			expiry = time.Now().Add(time.Duration(timeout) * time.Second)
		}
	}
	remoteSeed := body[:16]
	serverHash := body[16:]
	var bytesToHash []byte
	calcSha1 := func(s string) []byte {
		h := sha1.Sum([]byte(s))
		return h[:]
	}
	bytesToHash = append(bytesToHash, calcSha1(username)...)
	bytesToHash = append(bytesToHash, calcSha1(password)...)
	localAuthHash := sha256.Sum256(bytesToHash)

	bytesToHash = append(localSeed[:], remoteSeed...)
	bytesToHash = append(bytesToHash, localAuthHash[:]...)
	localSeedAuthHash := sha256.Sum256(bytesToHash)

	if !bytes.Equal(localSeedAuthHash[:], serverHash) {
		return fmt.Errorf("authentication failed")
	}
	s.SessionID = sessionID
	s.Expiry = expiry
	s.LocalSeed = localSeed[:]
	s.RemoteSeed = remoteSeed
	s.LocalAuthHash = localAuthHash[:]
	return nil
}

func parseBrokenCookies(r *http.Response) ([]*http.Cookie, error) {
	// Tapo's HTTP cookies are malformed, so here we go with custom parsing...
	cookieCount := len(r.Header["Set-Cookie"])
	cookies := make([]*http.Cookie, 0, cookieCount)
	if cookieCount != 0 {
		for _, line := range r.Header["Set-Cookie"] {
			parts := strings.Split(textproto.TrimString(line), ";")
			for _, part := range parts {
				name, value, ok := strings.Cut(part, "=")
				if !ok {
					continue
				}
				name = textproto.TrimString(name)
				c := &http.Cookie{
					Name:  name,
					Value: value,
					Raw:   line,
				}
				cookies = append(cookies, c)
			}
		}
	}
	return cookies, nil
}
