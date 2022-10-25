package tapo

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type HandshakeRequest struct {
	Method          string `json:"method"`
	RequestTimeMils int    `json:"requestTimeMils"`
	Params          struct {
		Key string `json:"key"`
	} `json:"params"`
}

type HandshakeResponse struct {
	ErrorCode ErrorCode `json:"error_code"`
	Result    struct {
		Key string `json:"key"`
	}
}

func NewHandshakeRequest(key string) *HandshakeRequest {
	r := HandshakeRequest{
		Method: "handshake",
	}
	r.Params.Key = key
	now := time.Now()
	r.RequestTimeMils = int(now.UnixMilli())
	return &r
}

type LoginDeviceRequest struct {
	Method          string `json:"method"`
	RequestTimeMils int    `json:"requestTimeMils"`
	Params          struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"params"`
}

type LoginDeviceResponse struct {
	ErrorCode ErrorCode `json:"error_code"`
	Result    struct {
		Token string `json:"token"`
	} `json:"result"`
}

func NewLoginDeviceRequest(username, password string) *LoginDeviceRequest {
	if len(password) > 8 {
		fmt.Fprintf(os.Stderr, "Warning: passwords longer than 8 characters will not work due to a Tapo firmware bug, see https://github.com/fishbigger/TapoP100/issues/4")
	}
	r := LoginDeviceRequest{
		Method: "login_device",
	}
	tmp := sha1.Sum([]byte(username))
	hexsha := make([]byte, hex.EncodedLen(len(tmp)))
	hex.Encode(hexsha, tmp[:])
	r.Params.Username = base64.StdEncoding.EncodeToString(hexsha)
	r.Params.Password = base64.StdEncoding.EncodeToString([]byte(password))
	r.RequestTimeMils = int(time.Now().UnixMilli())
	return &r
}

type GetDeviceInfoRequest struct {
	Method          string `json:"method"`
	RequestTimeMils int    `json:"requestTimeMils"`
}

// TODO differentiate fields between P100 and P110
type DeviceInfo struct {
	DeviceID           string `json:"device_id"`
	FWVersion          string `json:"fw_ver"`
	HWVersion          string `json:"hw_ver"`
	Type               string `json:"type"`
	Model              string `json:"model"`
	MAC                string `json:"mac"`
	HWID               string `json:"hw_id"`
	FWID               string `json:"fw_id"`
	OEMID              string `json:"oem_id"`
	IP                 string `json:"ip"`
	TimeDiff           int    `json:"time_diff"`
	SSID               string `json:"ssid"`
	RSSI               int    `json:"rssi"`
	SignalLevel        int    `json:"signal_level"`
	Latitude           int    `json:"latitude"`
	Longitude          int    `json:"longitude"`
	Lang               string `json:"lang"`
	Avatar             string `json:"avatar"`
	Region             string `json:"region"`
	Specs              string `json:"specs"`
	Nickname           string `json:"nickname"`
	HasSetLocationInfo bool   `json:"has_set_location_info"`
	DeviceON           bool   `json:"device_on"`
	OnTime             int    `json:"on_time"`
	DefaultStates      struct {
		Type string `json:"type"`
		// TODO add the structure for State
		State *json.RawMessage `json:"state"`
	} `json:"default_states"`
	OverHeated            bool   `json:"overheated"`
	PowerProtectionStatus string `json:"power_protection_status,omitempty"`
	Location              string `json:"location,omitempty"`

	// Computed values below.
	// DecodedSSID is the decoded version of the base64-encoded SSID field.
	DecodedSSID string
	// DecodedNickname is the decoded version of the base64-encoded Nickname field.
	DecodedNickname string
}

type GetDeviceInfoResponse struct {
	ErrorCode ErrorCode  `json:"error_code"`
	Result    DeviceInfo `json:"result"`
}

func NewGetDeviceInfoRequest() *GetDeviceInfoRequest {
	return &GetDeviceInfoRequest{
		Method:          "get_device_info",
		RequestTimeMils: int(time.Now().UnixMilli()),
	}
}

type SetDeviceInfoRequest struct {
	Method string `json:"method"`
	Params struct {
		DeviceOn bool `json:"device_on"`
	} `json:"params"`
}

type SetDeviceInfoResponse struct {
	ErrorCode ErrorCode `json:"error_code"`
	Result    struct {
		Response string `json:"response"`
	}
}

func NewSetDeviceInfoRequest(deviceOn bool) *SetDeviceInfoRequest {
	r := SetDeviceInfoRequest{
		Method: "set_device_info",
	}
	r.Params.DeviceOn = deviceOn
	return &r
}

type GetDeviceUsageRequest struct {
	Method          string `json:"method"`
	RequestTimeMils int    `json:"requestTimeMils"`
}

type DeviceUsage struct {
	TimeUsage struct {
		Today  int `json:"today"`
		Past7  int `json:"past7"`
		Past30 int `json:"past30"`
	} `json:"time_usage"`
	PowerUsage struct {
		Today  int `json:"today"`
		Past7  int `json:"past7"`
		Past30 int `json:"past30"`
	} `json:"power_usage"`
	SavedPower struct {
		Today  int `json:"today"`
		Past7  int `json:"past7"`
		Past30 int `json:"past30"`
	} `json:"saved_power"`
}

type GetDeviceUsageResponse struct {
	ErrorCode ErrorCode   `json:"error_code"`
	Result    DeviceUsage `json:"result"`
}

func NewGetDeviceUsageRequest() *GetDeviceUsageRequest {
	return &GetDeviceUsageRequest{
		Method:          "get_device_usage",
		RequestTimeMils: int(time.Now().UnixMilli()),
	}
}

type GetEnergyUsageRequest struct {
	Method          string `json:"method"`
	RequestTimeMils int    `json:"requestTimeMils"`
}

type GetEnergyUsageResponse struct {
	ErrorCode ErrorCode   `json:"error_code"`
	Result    DeviceUsage `json:"result"`
}

func NewGetEnergyUsageRequest() *GetEnergyUsageRequest {
	return &GetEnergyUsageRequest{
		Method:          "get_device_usage",
		RequestTimeMils: int(time.Now().UnixMilli()),
	}
}

type SecurePassthroughRequest struct {
	Method string `json:"method"`
	Params struct {
		Request string `json:"request"`
	} `json:"params"`
}

type SecurePassthroughResponse struct {
	ErrorCode ErrorCode `json:"error_code"`
	Result    struct {
		Response string `json:"response"`
	}
}

func NewSecurePassthroughRequest(innerRequest string) *SecurePassthroughRequest {
	r := SecurePassthroughRequest{
		Method: "securePassthrough",
	}
	r.Params.Request = innerRequest
	return &r
}
