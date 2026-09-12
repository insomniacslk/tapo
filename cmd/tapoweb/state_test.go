// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/insomniacslk/tapo"
)

// dev builds a Device with just enough of a DeviceInfo to be found by IP.
func dev(ip string) Device {
	return Device{info: &tapo.DeviceInfo{IP: ip}}
}

func newTestState(discover func(*Config) ([]Device, []netip.Addr, error)) *state {
	return &state{discover: discover}
}

func testConfig() *Config {
	cfg := defaultConfig()
	return &cfg
}

// TestRefreshKeepsThePreviousListOnFailure is the whole point of this file.
// The code this replaces called log.Fatalf on a discovery error, so one lost
// broadcast ended the process.
func TestRefreshKeepsThePreviousListOnFailure(t *testing.T) {
	boom := errors.New("broadcast went nowhere")
	var fail bool
	st := newTestState(func(*Config) ([]Device, []netip.Addr, error) {
		if fail {
			return nil, nil, boom
		}
		return []Device{dev("192.0.2.1")}, nil, nil
	})

	if err := st.refresh(testConfig()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	good := st.get()
	if len(good.devices) != 1 {
		t.Fatalf("devices after a good refresh = %d, want 1", len(good.devices))
	}

	fail = true
	if err := st.refresh(testConfig()); !errors.Is(err, boom) {
		t.Fatalf("refresh error = %v, want %v", err, boom)
	}
	after := st.get()
	if len(after.devices) != 1 {
		t.Errorf("devices after a failed refresh = %d, want the previous 1", len(after.devices))
	}
	if !after.updatedAt.Equal(good.updatedAt) {
		t.Errorf("updatedAt moved on a failed refresh: %v -> %v", good.updatedAt, after.updatedAt)
	}
	if !errors.Is(after.lastErr, boom) {
		t.Errorf("lastErr = %v, want %v", after.lastErr, boom)
	}
}

// TestEmptyDiscoveryIsAFailure covers the silent case: discovery returns no
// error and no devices, which is what a lost broadcast looks like from here.
func TestEmptyDiscoveryIsAFailure(t *testing.T) {
	var empty bool
	st := newTestState(func(*Config) ([]Device, []netip.Addr, error) {
		if empty {
			return nil, nil, nil
		}
		return []Device{dev("192.0.2.1"), dev("192.0.2.2")}, nil, nil
	})
	if err := st.refresh(testConfig()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	empty = true
	if err := st.refresh(testConfig()); !errors.Is(err, errNoDevices) {
		t.Fatalf("refresh error = %v, want errNoDevices", err)
	}
	if got := len(st.get().devices); got != 2 {
		t.Errorf("devices after an empty discovery = %d, want the previous 2", got)
	}
}

// TestEmptyFirstDiscoveryIsAccepted is the other side of the rule above, and
// the reason it is conditional on there having been a successful refresh: a
// process that has never found anything must still be able to start, or an
// empty network is an outage rather than an empty page.
func TestEmptyFirstDiscoveryIsAccepted(t *testing.T) {
	st := newTestState(func(*Config) ([]Device, []netip.Addr, error) {
		return nil, nil, nil
	})
	if err := st.refresh(testConfig()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if st.get().updatedAt.IsZero() {
		t.Error("updatedAt is still zero after an accepted refresh")
	}
}

// TestDevicesFoundButUnreachableCount checks that a discovery which finds
// plugs and cannot talk to any of them is a SUCCESS: the broadcast worked, so
// the failed list is real information and replacing the page with it is right.
func TestDevicesFoundButUnreachableCount(t *testing.T) {
	st := newTestState(func(*Config) ([]Device, []netip.Addr, error) {
		return nil, []netip.Addr{netip.MustParseAddr("192.0.2.9")}, nil
	})
	if err := st.refresh(testConfig()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if err := st.refresh(testConfig()); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if got := len(st.get().failed); got != 1 {
		t.Errorf("failed = %d, want 1", got)
	}
}

func TestStale(t *testing.T) {
	interval := time.Minute
	for _, tt := range []struct {
		name string
		snap snapshot
		want bool
	}{
		{"never refreshed", snapshot{}, true},
		{"just refreshed", snapshot{updatedAt: time.Now()}, false},
		{"one missed cycle", snapshot{updatedAt: time.Now().Add(-90 * time.Second)}, false},
		{"two missed cycles", snapshot{updatedAt: time.Now().Add(-3 * time.Minute)}, true},
	} {
		if got := tt.snap.stale(interval); got != tt.want {
			t.Errorf("%s: stale = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestFindAndHasFailed(t *testing.T) {
	snap := snapshot{
		devices: []Device{dev("192.0.2.1")},
		failed:  []netip.Addr{netip.MustParseAddr("192.0.2.9")},
	}
	if _, ok := snap.find("192.0.2.1"); !ok {
		t.Error("find(192.0.2.1) = false, want true")
	}
	if _, ok := snap.find("192.0.2.9"); ok {
		t.Error("find(192.0.2.9) = true, want false: a failed device is not a found one")
	}
	if !snap.hasFailed("192.0.2.9") {
		t.Error("hasFailed(192.0.2.9) = false, want true")
	}
	if snap.hasFailed("192.0.2.1") {
		t.Error("hasFailed(192.0.2.1) = true, want false")
	}
}

func TestReadyz(t *testing.T) {
	st := newTestState(func(*Config) ([]Device, []netip.Addr, error) {
		return []Device{dev("192.0.2.1")}, nil, nil
	})

	rec := httptest.NewRecorder()
	newReadyHandler(st)(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("before the first discovery: %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	if err := st.refresh(testConfig()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	rec = httptest.NewRecorder()
	newReadyHandler(st)(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("after the first discovery: %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestHealthzStaysUpWhenDiscoveryFails is the asymmetry between the two
// probes, written down as a test because it is the thing most likely to be
// "tidied" into matching readiness later: a plug network that has gone away is
// not a reason to restart a server that is answering perfectly well.
func TestHealthzStaysUpWhenDiscoveryFails(t *testing.T) {
	st := newTestState(func(*Config) ([]Device, []netip.Addr, error) {
		return nil, nil, errors.New("nope")
	})
	_ = st.refresh(testConfig())
	rec := httptest.NewRecorder()
	newHealthHandler()(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz = %d, want %d", rec.Code, http.StatusOK)
	}
}
