package tapo

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

func (c *Client) Login(username, password string) error {
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

func (c *Client) List() ([]Device, error) {
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

type Device struct {
	DeviceType   string `json:"deviceType"`
	Role         int    `json:"role"`
	FwVer        string `json:"fwVer"`
	AppServerURL string `json:"appServerUrl"`
	DeviceRegion string `json:"deviceRegion"`
	DeviceID     string `json:"deviceId"`
	DeviceName   string `json:"deviceName"`
	DeviceHwVer  string `json:"deviceHwVer"`
	Alias        string `json:"alias"`
	DeviceMAC    string `json:"deviceMac"`
	OemID        string `json:"oemId"`
	DeviceModel  string `json:"deviceModel"`
	HwID         string `json:"hwId"`
	FwID         string `json:"fwId"`
	IsSameRegion bool   `json:"isSameRegion"`
	Status       int    `json:"status"`
	// Computed values
	DecodedAlias string
}
