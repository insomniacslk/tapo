// SPDX-License-Identifier: MIT

package tapo

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

const baseURL = "https://wap.tplinkcloud.com"

// Client is a tp-link cloud client for cloud-based operations.
type Client struct {
	log          *log.Logger
	terminalUUID uuid.UUID
	timeout      time.Duration
	token        string
}

func NewClient(logger *log.Logger) *Client {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Client{
		log:          logger,
		terminalUUID: uuid.New(),
		timeout:      defaultTimeout,
	}
}

func (c *Client) buildLoginRequest(username, password string) ([]byte, error) {
	type loginRequest struct {
		Method string `json:"method"`
		URL    string `json:"url"`
		Params struct {
			AppType       string `json:"appType"`
			CloudUserName string `json:"cloudUserName"`
			CloudPassword string `json:"cloudPassword"`
			TerminalUUID  string `json:"terminalUUID"`
		} `json:"params"`
	}
	r := loginRequest{
		Method: "login",
		URL:    baseURL,
	}
	r.Params.AppType = "Kasa_Android"
	r.Params.CloudUserName = username
	r.Params.CloudPassword = password
	r.Params.TerminalUUID = c.terminalUUID.String()
	b, err := json.Marshal(&r)
	if err != nil {
		return nil, fmt.Errorf("JSON marshal failed: %w", err)
	}
	return b, nil
}

func (c *Client) buildDeviceListRequest() ([]byte, error) {
	type deviceListRequest struct {
		Method string `json:"method"`
		Token  string `json:"token"`
	}
	r := deviceListRequest{
		Method: "getDeviceList",
		Token:  c.token,
	}
	b, err := json.Marshal(&r)
	if err != nil {
		return nil, fmt.Errorf("JSON marshal failed: %w", err)
	}
	return b, nil
}

func (c *Client) post(cloudURL string, data []byte) ([]byte, error) {
	u, err := url.Parse(cloudURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}
	params := url.Values{}
	params.Add("appName", "Kasa_Android")
	params.Add("termID", c.terminalUUID.String())
	params.Add("appVer", "1.4.4.607")
	params.Add("ospf", "Android+6.0.1")
	params.Add("netType", "wifi")
	params.Add("locale", "en_US")
	if c.token != "" {
		params.Add("token", c.token)
	}
	u.RawQuery = params.Encode()

	// TODO set headers:
	//      User-Agent: Dalvik/2.1.0 (Linux; U; Android 6.0.1; A0001 Build/M4B30X)
	resp, err := http.Post(u.String(), "application/json", bytes.NewBuffer(data))
	if err != nil {
		return nil, fmt.Errorf("POST failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("expected HTTP 200 OK, got %s", resp.Status)
	}
	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	return respData, nil
}

func (c *Client) CloudLogin(username, password string) error {
	lr, err := c.buildLoginRequest(username, password)
	if err != nil {
		return fmt.Errorf("failed to build login request: %w", err)
	}
	resp, err := c.post(baseURL, lr)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}

	loginResp := struct {
		ErrorCode int `json:"error_code"`
		Result    struct {
			AccountID    string `json:"accountId"`
			RegTime      string `json:"regTime"`
			CountryCode  string `json:"countryCode"`
			RiskDetected int    `json:"riskDetected"`
			Nickname     string `json:"nickname"`
			Email        string `json:"email"`
			Token        string `json:"token"`
		}
	}{}
	if err := json.Unmarshal(resp, &loginResp); err != nil {
		return fmt.Errorf("decode failed: %w", err)
	}
	c.token = loginResp.Result.Token
	return nil
}

func (c *Client) CloudList() ([]Device, error) {
	lr, err := c.buildDeviceListRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to build device list request: %w", err)
	}
	resp, err := c.post(baseURL, lr)
	if err != nil {
		return nil, fmt.Errorf("device list request failed: %w", err)
	}
	deviceListResp := struct {
		ErrorCode int `json:"error_code"`
		Result    struct {
			DeviceList []Device `json:"deviceList"`
		}
	}{}
	if err := json.Unmarshal(resp, &deviceListResp); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}
	devices := deviceListResp.Result.DeviceList
	for idx, d := range devices {
		decodedAlias, err := base64.StdEncoding.DecodeString(d.Alias)
		if err != nil {
			return nil, fmt.Errorf("failed to decode alias: %w", err)
		}
		d.DecodedAlias = string(decodedAlias)
		devices[idx] = d
	}
	return deviceListResp.Result.DeviceList, nil
}

func (c *Client) Discover() (map[string]DiscoverResponse, []DiscoverResponse, error) {
	// TODO make broadcast addresses and timeout configurable.
	// TODO make it possible to only use one discovery method.
	reqv2, err := hex.DecodeString("020000010000000000000000463cb5d3")
	if err != nil {
		return nil, nil, fmt.Errorf("invalid request v2 hex string. Bug? %w", err)
	}

	// discovery protocol v1: send a broadcast UDP message to port 9999
	// containing a XOR'ed JSON request.
	req := NewDiscoverV1Request()
	reqb, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal discovery request to JSON: %w", err)
	}
	encReq := make([]byte, len(reqb))
	key := byte(DiscoverV1InitializationVector)
	for idx := range reqb {
		key ^= reqb[idx]
		encReq[idx] = key
	}
	// send broadcast packet
	pc, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to listen on packet connection: %w", err)
	}
	defer pc.Close()
	addr, err := net.ResolveUDPAddr("udp4", "255.255.255.255:9999")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve broadcast address: %w", err)
	}
	addrv2, err := net.ResolveUDPAddr("udp4", "255.255.255.255:20002")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve broadcast address: %w", err)
	}
	// listen for responses in a different goroutine
	if err := pc.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, nil, fmt.Errorf("failed to set read deadline: %w", err)
	}
	go func() {
		for i := 0; i < 6; i++ {
			// send req v1
			_, err = pc.WriteTo(encReq, addr)
			if err != nil {
				c.log.Printf("Failed to send broadcast discover v1 packet: %v", err)
				break
			}
			// send req v2
			_, err = pc.WriteTo(reqv2, addrv2)
			if err != nil {
				c.log.Printf("Failed to send broadcast discover v2 packet: %v", err)
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}()
	ret := make(map[string]DiscoverResponse, 0)
	errs := make([]DiscoverResponse, 0)
	for {
		msg := make([]byte, 2048)
		n, _, err := pc.ReadFrom(msg)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				break
			}
			return nil, nil, fmt.Errorf("read failed: %w", err)
		}
		var resp DiscoverResponse
		if err := json.Unmarshal(msg[16:n], &resp); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal discover response to JSON: %w", err)
		}
		// override earlier responses with later responses
		if resp.Result.ErrorCode != 0 {
			errs = append(errs, resp)
		} else {
			ret[resp.Result.DeviceID] = resp
		}
	}

	return ret, errs, nil
}

// Tapo uses a non-standard MAC representation, a 12-char hex string with no
// separators. Custom unmarshalling here it goes.
type tapoMAC net.HardwareAddr

func (tm tapoMAC) String() string {
	h := net.HardwareAddr(tm)
	return h.String()
}

func stripQuotes(s string) (string, error) {
	if len(s) < 2 || (s[0] != '"' && s[len(s)-1] != '"') {
		return s, errors.New("not a properly double-quoted string")
	}
	return s[1 : len(s)-1], nil
}

func (tm *tapoMAC) UnmarshalJSON(b []byte) error {
	s, err := stripQuotes(string(b))
	if err != nil {
		return err
	}
	decoded, err := hex.DecodeString(s)
	if err != nil {
		return err
	}
	if len(decoded) != 6 {
		return fmt.Errorf("invalid MAC length, want 6 bytes, got %d", len(decoded))
	}
	*tm = tapoMAC(decoded)
	return nil
}

func (tm tapoMAC) MarshalJSON() ([]byte, error) {
	return []byte("\"" + tm.String() + "\""), nil
}

type Device struct {
	DeviceType   string  `json:"deviceType"`
	Role         int     `json:"role"`
	FwVer        string  `json:"fwVer"`
	AppServerURL string  `json:"appServerUrl"`
	DeviceRegion string  `json:"deviceRegion"`
	DeviceID     string  `json:"deviceId"`
	DeviceName   string  `json:"deviceName"`
	DeviceHwVer  string  `json:"deviceHwVer"`
	Alias        string  `json:"alias"`
	DeviceMAC    tapoMAC `json:"deviceMac"`
	OemID        string  `json:"oemId"`
	DeviceModel  string  `json:"deviceModel"`
	HwID         string  `json:"hwId"`
	FwID         string  `json:"fwId"`
	IsSameRegion bool    `json:"isSameRegion"`
	Status       int     `json:"status"`
	// Computed values
	DecodedAlias string
}
