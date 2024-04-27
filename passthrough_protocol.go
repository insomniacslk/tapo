// SPDX-License-Identifier: MIT

package tapo

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/mergermarket/go-pkcs7"
)

func NewPassthroughSession(l *log.Logger) *PassthroughSession {
	return &PassthroughSession{
		log: l,
	}
}

type PassthroughSession struct {
	log        *log.Logger
	Key        []byte
	IV         []byte
	ID         string
	addr       netip.Addr
	username   string
	password   string
	token      string
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	timeout    time.Duration
}

func (p *PassthroughSession) Addr() netip.Addr {
	return p.addr
}

func (p *PassthroughSession) Handshake(addr netip.Addr, username, password string) error {
	p.addr = addr
	p.username = username
	p.password = password
	// generate an RSA key pair
	bits := 1024
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return fmt.Errorf("failed to generate RSA key: %w", err)
	}
	privkey, pubkey := key, key.Public().(*rsa.PublicKey)
	p.privateKey = privkey
	p.publicKey = pubkey
	pkix, err := x509.MarshalPKIXPublicKey(pubkey)
	if err != nil {
		return fmt.Errorf("failed to marshal public key to PKIX: %w", err)
	}
	pkixBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pkix,
	})

	// make a new handshake request
	request := NewHandshakeRequest(string(pkixBytes))
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal handshake payload: %w", err)
	}
	p.log.Printf("Handshake request: %s", requestBytes)
	u := fmt.Sprintf("http://%s/app", p.addr.String())
	httpresp, err := http.Post(u, "application/json", bytes.NewBuffer(requestBytes))
	if err != nil {
		return fmt.Errorf("HTTP POST failed: %w", err)
	}
	defer httpresp.Body.Close()
	body, err := io.ReadAll(httpresp.Body)
	if err != nil {
		return fmt.Errorf("failed to read HTTP body: %w", err)
	}
	if httpresp.StatusCode != 200 {
		return fmt.Errorf("expected 200 OK, got %s. Error message: %s", httpresp.Status, body)
	}
	p.log.Printf("Handshake response: %s", body)
	var resp HandshakeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}
	if resp.ErrorCode != 0 {
		return fmt.Errorf("request failed: %w (%d)", resp.ErrorCode, resp.ErrorCode)
	}

	// now decrypt the Tapo device encryption key with our public key
	encryptedKey, err := base64.StdEncoding.DecodeString(resp.Result.Key)
	if err != nil {
		return fmt.Errorf("failed to base64-decode device encryption key: %w", err)
	}
	sessionKey, err := rsa.DecryptPKCS1v15(rand.Reader, privkey, encryptedKey)
	if err != nil {
		return fmt.Errorf("rsa.DecryptPKCS1v15 failed: %w", err)
	}
	if len(sessionKey) != 32 {
		return fmt.Errorf("session key length is not 32 bytes, got %d", len(sessionKey))
	}
	var sessionID string
	for _, cookie := range httpresp.Cookies() {
		if cookie.Name == "TP_SESSIONID" {
			sessionID = "TP_SESSIONID=" + cookie.Value
			break
		}
	}
	if sessionID == "" {
		return fmt.Errorf("no TP_SESSIONID cookie found in HTTP response")
	}
	p.Key = sessionKey[:16]
	p.ID = sessionID
	p.IV = sessionKey[16:]
	return nil
}

func (s *PassthroughSession) Request(requestBytes []byte) ([]byte, error) {
	// encrypt the request
	encodedRequest, err := s.encryptRequest(requestBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt request")
	}

	// wrap it in a secure_passthrough request
	passthroughRequest := NewSecurePassthroughRequest(encodedRequest)
	passthroughRequestBytes, err := json.Marshal(&passthroughRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal securePassthrough payload: %w", err)
	}
	s.log.Printf("Passthrough request: %s", passthroughRequestBytes)

	// send it via http
	u := fmt.Sprintf("http://%s/app", s.addr.String())
	if s.token != "" {
		u += "?token=" + s.token
	}
	req, err := http.NewRequest("POST", u, bytes.NewBuffer(passthroughRequestBytes))
	if err != nil {
		return nil, fmt.Errorf("http.NewRequest failed: %w", err)
	}
	req.Header.Set("Cookie", s.ID)
	req.Close = true
	client := http.Client{Timeout: s.timeout}
	httpresp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP POST failed: %w", err)
	}
	defer httpresp.Body.Close()
	// handle JSON response
	body, err := io.ReadAll(httpresp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP body: %w", err)
	}
	if httpresp.StatusCode != 200 {
		return nil, fmt.Errorf("expected 200 OK, got %s. Error message: %s", httpresp.Status, body)
	}
	s.log.Printf("Passthrough response: %s", body)
	var resp SecurePassthroughResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}
	if resp.ErrorCode != 0 {
		return nil, fmt.Errorf("request failed: %s", resp.ErrorCode)
	}
	// decrypt response
	response, err := s.decryptResponse(resp.Result.Response)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt response: %w", err)
	}

	return response, nil
}

func (s *PassthroughSession) encryptRequest(req []byte) (string, error) {
	block, err := aes.NewCipher(s.Key)
	if err != nil {
		return "", fmt.Errorf("aes.NewCipher failed: %w", err)
	}
	encrypter := cipher.NewCBCEncrypter(block, s.IV)
	paddedRequestBytes, err := pkcs7.Pad(req, aes.BlockSize)
	if err != nil {
		return "", fmt.Errorf("pkcs7.Pad failed: %w", err)
	}
	encryptedRequest := make([]byte, len(paddedRequestBytes))
	encrypter.CryptBlocks(encryptedRequest, paddedRequestBytes)

	// now base64-encode the request
	encodedRequest := base64.StdEncoding.EncodeToString(encryptedRequest)
	encodedRequest = strings.Replace(encodedRequest, "\r\n", "", -1)
	return encodedRequest, nil
}

func (s *PassthroughSession) decryptResponse(resp string) ([]byte, error) {
	encryptedResponse, err := base64.StdEncoding.DecodeString(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to base64-decode response: %w", err)
	}

	block, err := aes.NewCipher(s.Key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher failed: %w", err)
	}
	encrypter := cipher.NewCBCDecrypter(block, s.IV)

	paddedResponse := make([]byte, len(encryptedResponse))
	encrypter.CryptBlocks(paddedResponse, encryptedResponse)

	response, err := pkcs7.Unpad(paddedResponse, aes.BlockSize)
	if err != nil {
		return nil, fmt.Errorf("pkcs7.Pad failed: %w", err)
	}
	return response, err
}
