package tapo

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
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

type P100DeviceInfo struct {
	DeviceID           string `json:"device_id"`
	FWVersion          string `json:"fw_ver"`
	HWVersion          string `json:"hw_ver"`
	Type               string `json:"type"`
	Model              string `json:"model"`
	MAC                string `json:"mac"`
	HWID               string `json:"hw_id"`
	FWID               string `json:"fw_id"`
	OEMID              string `json:"oem_id"`
	Specs              string `json:"specs"`
	DeviceON           bool   `json:"device_on"`
	OnTime             int    `json:"on_time"`
	OverHeated         bool   `json:"overheated"`
	Nickname           string `json:"nickname"`
	Location           string `json:"location"`
	Avatar             string `json:"avatar"`
	Longitude          int    `json:"longitude"`
	Latitude           int    `json:"latitude"`
	HasSetLocationInfo bool   `json:"has_set_location_info"`
	IP                 string `json:"ip"`
	SSID               string `json:"ssid"`
	SignalLevel        int    `json:"signal_level"`
	RSSI               int    `json:"rssi"`
	Region             string `json:"region"`
	TimeDiff           int    `json:"time_diff"`
	Lang               string `json:"lang"`
	// DecodedSSID is the decoded version of the base64-encoded SSID field. This
	// is a computed field.
	DecodedSSID []byte
}

type GetDeviceInfoResponse struct {
	ErrorCode ErrorCode      `json:"error_code"`
	Result    P100DeviceInfo `json:"result"`
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
