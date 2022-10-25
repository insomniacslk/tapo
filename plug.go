package tapo

// see https://k4czp3r.xyz/reverse-engineering/tp-link/tapo/2020/10/15/reverse-engineering-tp-link-tapo.html

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

	"github.com/google/uuid"
	"github.com/mergermarket/go-pkcs7"
)

var defaultTimeout = 10 * time.Second

type ErrorCode int

func (e ErrorCode) String() string {
	switch e {
	case 0:
		return "Success"
	case -1010:
		return "Invalid Public Key Length"
	case -1012:
		return "Invalid terminalUUID"
	case -1501:
		return "Invalid Request or Credentials"
	case -1002:
		return "Incorrect Request"
	case -1003:
		return "JSON formatting error"
	default:
		return fmt.Sprintf("Unknown error: %d", e)
	}
}

type P100 struct {
	log          *log.Logger
	addr         netip.Addr
	terminalUUID uuid.UUID
	privateKey   *rsa.PrivateKey
	publicKey    *rsa.PublicKey
	session      *Session
	timeout      time.Duration
	token        string
}

func NewP100(addr netip.Addr, email, password string, logger *log.Logger) *P100 {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &P100{
		log:          logger,
		addr:         addr,
		terminalUUID: uuid.New(),
		timeout:      defaultTimeout,
	}
}

type Session struct {
	Key []byte
	IV  []byte
	ID  string
}

func (p *P100) Handshake() (*Session, error) {
	// generate an RSA key pair
	bits := 1024
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}
	privkey, pubkey := key, key.Public().(*rsa.PublicKey)
	p.privateKey = privkey
	p.publicKey = pubkey
	pkix, err := x509.MarshalPKIXPublicKey(pubkey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal public key to PKIX: %w", err)
	}
	pkixBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pkix,
	})

	// make a new handshake request
	request := NewHandshakeRequest(string(pkixBytes))
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal handshake payload: %w", err)
	}
	p.log.Printf("Handshake request: %s", requestBytes)
	u := fmt.Sprintf("http://%s/app", p.addr.String())
	httpresp, err := http.Post(u, "application/json", bytes.NewBuffer(requestBytes))
	if err != nil {
		return nil, fmt.Errorf("HTTP POST failed: %w", err)
	}
	defer httpresp.Body.Close()

	httprespBytes, err := io.ReadAll(httpresp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP body: %w", err)
	}
	p.log.Printf("Handshake response: %s", httprespBytes)
	var resp HandshakeResponse
	if err := json.Unmarshal(httprespBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}
	if resp.ErrorCode != 0 {
		return nil, fmt.Errorf("request failed: %s", resp.ErrorCode)
	}

	// now decrypt the Tapo device encryption key with our public key
	encryptedKey, err := base64.StdEncoding.DecodeString(resp.Result.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to base64-decode device encryption key: %w", err)
	}
	sessionKey, err := rsa.DecryptPKCS1v15(rand.Reader, privkey, encryptedKey)
	if err != nil {
		return nil, fmt.Errorf("rsa.DecryptPKCS1v15 failed: %w", err)
	}
	if len(sessionKey) != 32 {
		return nil, fmt.Errorf("session key length is not 32 bytes, got %d", len(sessionKey))
	}
	var sessionID string
	for _, cookie := range httpresp.Cookies() {
		if cookie.Name == "TP_SESSIONID" {
			sessionID = "TP_SESSIONID=" + cookie.Value
			break
		}
	}
	if sessionID == "" {
		return nil, fmt.Errorf("no TP_SESSIONID cookie found in HTTP response")
	}
	return &Session{
		Key: sessionKey[:16],
		IV:  sessionKey[16:],
		ID:  sessionID,
	}, nil
}

func (p *P100) Login(username, password string) error {
	if p.session == nil {
		sk, err := p.Handshake()
		if err != nil {
			return fmt.Errorf("handshake failed: %w", err)
		}
		p.session = sk
	}
	p.log.Printf("Session: %+v", p.session)

	request := NewLoginDeviceRequest(username, password)
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal login_device payload: %w", err)
	}
	p.log.Printf("Login request: %s", requestBytes)

	response, err := p.securePassthrough(requestBytes)
	if err != nil {
		return fmt.Errorf("Passthrough request failed: %w", err)
	}
	p.log.Printf("Login response: %s", response)
	var loginResp LoginDeviceResponse
	if err := json.Unmarshal(response, &loginResp); err != nil {
		return fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}
	if loginResp.ErrorCode != 0 {
		return fmt.Errorf("request failed: %s", loginResp.ErrorCode)
	}
	if loginResp.Result.Token == "" {
		return fmt.Errorf("empty token returned by device")
	}
	p.token = loginResp.Result.Token

	return nil
}

func (p *P100) GetDeviceInfo() (*DeviceInfo, error) {
	if p.token == "" {
		return nil, fmt.Errorf("not logged in")
	}
	request := NewGetDeviceInfoRequest()
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal get_device_info payload: %w", err)
	}
	p.log.Printf("GetDeviceInfo request: %s", requestBytes)

	response, err := p.securePassthrough(requestBytes)
	if err != nil {
		return nil, fmt.Errorf("Passthrough request failed: %w", err)
	}
	p.log.Printf("GetDeviceInfo response: %s", response)
	var infoResp GetDeviceInfoResponse
	if err := json.Unmarshal(response, &infoResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}
	if infoResp.ErrorCode != 0 {
		return nil, fmt.Errorf("request failed: %s", infoResp.ErrorCode)
	}
	// decode base64-encoded fields
	decodedSSID, err := base64.StdEncoding.DecodeString(infoResp.Result.SSID)
	if err != nil {
		return nil, fmt.Errorf("failed to base64-decode SSID: %w", err)
	}
	infoResp.Result.DecodedSSID = string(decodedSSID)

	decodedNickname, err := base64.StdEncoding.DecodeString(infoResp.Result.Nickname)
	if err != nil {
		return nil, fmt.Errorf("failed to base64-decode Nickname: %w", err)
	}
	infoResp.Result.DecodedNickname = string(decodedNickname)

	return &infoResp.Result, nil
}

func (p *P100) SetDeviceInfo(deviceOn bool) error {
	if p.token == "" {
		return fmt.Errorf("not logged in")
	}
	request := NewSetDeviceInfoRequest(deviceOn)
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal set_device_info payload: %w", err)
	}
	p.log.Printf("SetDeviceInfo request: %s", requestBytes)

	response, err := p.securePassthrough(requestBytes)
	if err != nil {
		return fmt.Errorf("Passthrough request failed: %w", err)
	}
	p.log.Printf("SetDeviceInfo response: %s", response)
	var infoResp SetDeviceInfoResponse
	if err := json.Unmarshal(response, &infoResp); err != nil {
		return fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}
	if infoResp.ErrorCode != 0 {
		return fmt.Errorf("request failed: %s", infoResp.ErrorCode)
	}
	return nil
}

func (p *P100) GetDeviceUsage() (*DeviceUsage, error) {
	if p.token == "" {
		return nil, fmt.Errorf("not logged in")
	}
	request := NewGetDeviceUsageRequest()
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal get_device_usage payload: %w", err)
	}
	p.log.Printf("GetDeviceUsage request: %s", requestBytes)

	response, err := p.securePassthrough(requestBytes)
	if err != nil {
		return nil, fmt.Errorf("Passthrough request failed: %w", err)
	}
	p.log.Printf("GetDeviceUsage response: %s", response)
	var usageResp GetDeviceUsageResponse
	if err := json.Unmarshal(response, &usageResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}
	if usageResp.ErrorCode != 0 {
		return nil, fmt.Errorf("request failed: %s", usageResp.ErrorCode)
	}
	return &usageResp.Result, nil
}

func (p *P100) GetEnergyUsage() (*DeviceUsage, error) {
	if p.token == "" {
		return nil, fmt.Errorf("not logged in")
	}
	request := NewGetEnergyUsageRequest()
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal get_energy_usage payload: %w", err)
	}
	p.log.Printf("GetEnergyUsage request: %s", requestBytes)

	response, err := p.securePassthrough(requestBytes)
	if err != nil {
		return nil, fmt.Errorf("Passthrough request failed: %w", err)
	}
	p.log.Printf("GetEnergyUsage response: %s", response)
	var usageResp GetEnergyUsageResponse
	if err := json.Unmarshal(response, &usageResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}
	if usageResp.ErrorCode != 0 {
		return nil, fmt.Errorf("request failed: %s", usageResp.ErrorCode)
	}
	return &usageResp.Result, nil
}

func (p *P100) encryptRequest(req []byte) (string, error) {
	block, err := aes.NewCipher(p.session.Key)
	if err != nil {
		return "", fmt.Errorf("aes.NewCipher failed: %w", err)
	}
	encrypter := cipher.NewCBCEncrypter(block, p.session.IV)
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

func (p *P100) decryptResponse(resp string) ([]byte, error) {
	encryptedResponse, err := base64.StdEncoding.DecodeString(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to base64-decode response: %w", err)
	}

	block, err := aes.NewCipher(p.session.Key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher failed: %w", err)
	}
	encrypter := cipher.NewCBCDecrypter(block, p.session.IV)

	paddedResponse := make([]byte, len(encryptedResponse))
	encrypter.CryptBlocks(paddedResponse, encryptedResponse)

	response, err := pkcs7.Unpad(paddedResponse, aes.BlockSize)
	if err != nil {
		return nil, fmt.Errorf("pkcs7.Pad failed: %w", err)
	}
	return response, err
}

func (p *P100) securePassthrough(requestBytes []byte) ([]byte, error) {
	// encrypt the request
	encodedRequest, err := p.encryptRequest(requestBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt request")
	}

	// wrap it in a secure_passthrough request
	passthroughRequest := NewSecurePassthroughRequest(encodedRequest)
	passthroughRequestBytes, err := json.Marshal(&passthroughRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal securePassthrough payload: %w", err)
	}
	p.log.Printf("Passthrough request: %s", passthroughRequestBytes)

	// send it via http
	u := fmt.Sprintf("http://%s/app", p.addr.String())
	if p.token != "" {
		u += "?token=" + p.token
	}
	req, err := http.NewRequest("POST", u, bytes.NewBuffer(passthroughRequestBytes))
	if err != nil {
		return nil, fmt.Errorf("http.NewRequest failed: %w", err)
	}
	req.Header.Set("Cookie", p.session.ID)
	req.Close = true
	client := http.Client{Timeout: p.timeout}
	httpresp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP POST failed: %w", err)
	}
	defer httpresp.Body.Close()

	// handle JSON response
	httprespBytes, err := io.ReadAll(httpresp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP body: %w", err)
	}
	p.log.Printf("Passthrough response: %s", httprespBytes)
	var resp SecurePassthroughResponse
	if err := json.Unmarshal(httprespBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}
	if resp.ErrorCode != 0 {
		return nil, fmt.Errorf("request failed: %s", resp.ErrorCode)
	}
	// decrypt response
	response, err := p.decryptResponse(resp.Result.Response)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt response: %w", err)
	}

	return response, nil
}

func (p *P100) On() error {
	return p.SetDeviceInfo(true)
}

func (p *P100) Off() error {
	return p.SetDeviceInfo(false)
}

func (p *P100) IsOn() (bool, error) {
	info, err := p.GetDeviceInfo()
	if err != nil {
		return false, err
	}
	return info.DeviceON, nil
}
