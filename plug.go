// SPDX-License-Identifier: MIT

package tapo

// see https://k4czp3r.xyz/reverse-engineering/tp-link/tapo/2020/10/15/reverse-engineering-tp-link-tapo.html
// and
// https://github.com/petretiandrea/plugp100/blob/main/plugp100/protocol/klap_protocol.py

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

var defaultTimeout = 10 * time.Second

type TapoError int

func (te TapoError) Error() string {
	switch te {
	case 0:
		return "Success"
	case -1010:
		return "Invalid Public Key Length"
	case -1012:
		return "Invalid terminalUUID"
	case -1501:
		return "Invalid Request or Credentials"
	case 1002:
		return "Incorrect Request"
	case -1003:
		return "JSON formatting error"
	case 1003:
		return "Communication error"
	case 9999:
		return "Session timeout"
	default:
		return fmt.Sprintf("Unknown error: %d", te)
	}
}

type Plug struct {
	log          *log.Logger
	Addr         netip.Addr
	terminalUUID uuid.UUID
	session      Session
}

func NewPlug(addr netip.Addr, logger *log.Logger) *Plug {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Plug{
		log:          logger,
		Addr:         addr,
		terminalUUID: uuid.New(),
	}
}

func (p *Plug) Handshake(username, password string) error {
	if p.session == nil {
		// try the newer KLAP protocol first
		ks := NewKlapSession(p.log)
		if err := ks.Handshake(p.Addr, username, password); err != nil {
			p.log.Printf("KLAP handshake failed, trying passthrough handshake")
			// then try the older passthrough protocol
			ps := NewPassthroughSession(p.log)
			if err := ps.Handshake(p.Addr, username, password); err != nil {
				return fmt.Errorf("passthrough handshake failed: %w", err)
			}
			request := NewLoginDeviceRequest(username, password)
			requestBytes, err := json.Marshal(request)
			if err != nil {
				return fmt.Errorf("failed to marshal login_device payload: %w", err)
			}
			p.log.Printf("Login request: %s", requestBytes)

			response, err := ps.Request(requestBytes)
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
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
			ps.token = loginResp.Result.Token
			p.session = ps
		} else {
			p.session = ks
		}
		p.log.Printf("Session: %+v", p.session)
	}

	return nil
}

func (p *Plug) GetDeviceInfo() (*DeviceInfo, error) {
	if p.session == nil {
		return nil, fmt.Errorf("not logged in")
	}
	request := NewGetDeviceInfoRequest()
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal get_device_info payload: %w", err)
	}
	p.log.Printf("GetDeviceInfo request: %s", requestBytes)

	response, err := p.session.Request(requestBytes)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
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

func (p *Plug) SetDeviceInfo(deviceOn bool) error {
	if p.session == nil {
		return fmt.Errorf("not logged in")
	}
	request := NewSetDeviceInfoRequest(deviceOn)
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal set_device_info payload: %w", err)
	}
	p.log.Printf("SetDeviceInfo request: %s", requestBytes)

	response, err := p.session.Request(requestBytes)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
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

func (p *Plug) GetDeviceUsage() (*DeviceUsage, error) {
	if p.session == nil {
		return nil, fmt.Errorf("not logged in")
	}
	request := NewGetDeviceUsageRequest()
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal get_device_usage payload: %w", err)
	}
	p.log.Printf("GetDeviceUsage request: %s", requestBytes)

	response, err := p.session.Request(requestBytes)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
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

func (p *Plug) GetEnergyUsage() (*EnergyUsage, error) {
	if p.session == nil {
		return nil, fmt.Errorf("not logged in")
	}
	request := NewGetEnergyUsageRequest()
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal get_energy_usage payload: %w", err)
	}
	p.log.Printf("GetEnergyUsage request: %s", requestBytes)

	response, err := p.session.Request(requestBytes)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
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

func (p *Plug) On() error {
	return p.SetDeviceInfo(true)
}

func (p *Plug) Off() error {
	return p.SetDeviceInfo(false)
}

func (p *Plug) IsOn() (bool, error) {
	info, err := p.GetDeviceInfo()
	if err != nil {
		return false, err
	}
	return info.DeviceON, nil
}
