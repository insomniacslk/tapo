// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/insomniacslk/tapo"
)

func plugInfo(name, ip string, on bool) Device {
	return Device{
		info: &tapo.DeviceInfo{
			DecodedNickname: name,
			IP:              ip,
			MAC:             "AA-BB-CC-DD-EE-FF",
			DeviceID:        "8022" + strings.ReplaceAll(ip, ".", ""),
			DeviceON:        on,
		},
		energy: &tapo.EnergyUsage{TodayEnergy: 1234, MonthEnergy: 56789},
	}
}

func render(t *testing.T, snap snapshot, cfg *Config) string {
	t.Helper()
	rec := httptest.NewRecorder()
	renderPage(rec, snap, cfg)
	if rec.Code != http.StatusOK {
		t.Fatalf("renderPage status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

func TestPageRendersDevices(t *testing.T) {
	cfg := testConfig()
	snap := snapshot{
		devices:   []Device{plugInfo("Desk lamp", "192.0.2.1", true), plugInfo("Kettle", "192.0.2.2", false)},
		failed:    []netip.Addr{netip.MustParseAddr("192.0.2.9")},
		updatedAt: time.Now(),
	}
	body := render(t, snap, cfg)

	for _, want := range []string{"Desk lamp", "Kettle", "192.0.2.1", "192.0.2.9", "1.2", "56.8"} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not mention %q", want)
		}
	}
	// --show-id is off in the default config, so the ID must not leak into the
	// page just because the struct carries it.
	if strings.Contains(body, "8022192021") {
		t.Error("page shows a device ID with --show-id off")
	}
	cfg.ShowID = true
	if !strings.Contains(render(t, snap, cfg), "8022192021") {
		t.Error("page does not show a device ID with --show-id on")
	}
}

// TestPageEscapesDeviceFields is the reason this page is a template at all.
// The old one concatenated the nickname straight into the HTML *and* into an
// inline navigator.clipboard.writeText('...') handler, so a plug named with a
// quote broke the page and a plug named with a script tag ran it.
func TestPageEscapesDeviceFields(t *testing.T) {
	const evil = `<script>alert('pwned')</script>`
	snap := snapshot{
		devices:   []Device{plugInfo(evil, "192.0.2.1", false)},
		updatedAt: time.Now(),
	}
	body := render(t, snap, testConfig())
	if strings.Contains(body, evil) {
		t.Fatal("a device nickname was rendered as raw HTML")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("the escaped nickname is not on the page at all")
	}
}

// TestPageHasNoInlineHandlers pins the "minimum JavaScript" property: every
// interaction on the page must work without it. The old page carried an
// onclick on every cell and every switch, and polled each plug over XHR.
func TestPageHasNoInlineHandlers(t *testing.T) {
	snap := snapshot{devices: []Device{plugInfo("Desk lamp", "192.0.2.1", true)}, updatedAt: time.Now()}
	body := render(t, snap, testConfig())
	for _, forbidden := range []string{"onclick=", "XMLHttpRequest", "setInterval("} {
		if strings.Contains(body, forbidden) {
			t.Errorf("page contains %q", forbidden)
		}
	}
	// ... and the things that replaced them are there.
	if !strings.Contains(body, `<form method="post" action="/">`) {
		t.Error("the switch is not a form")
	}
	if !strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("the page does not refresh itself")
	}
}

func TestPageStaleBanner(t *testing.T) {
	cfg := testConfig()
	fresh := snapshot{devices: []Device{plugInfo("Desk lamp", "192.0.2.1", true)}, updatedAt: time.Now()}
	if strings.Contains(render(t, fresh, cfg), "Stale.") {
		t.Error("a fresh page carries the stale banner")
	}

	stale := fresh
	stale.updatedAt = time.Now().Add(-10 * time.Minute)
	stale.lastErr = errNoDevices
	body := render(t, stale, cfg)
	if !strings.Contains(body, "Stale.") {
		t.Error("a stale page does not carry the stale banner")
	}
	if !strings.Contains(body, errNoDevices.Error()) {
		t.Error("the stale banner does not say why")
	}

	never := render(t, snapshot{}, cfg)
	if !strings.Contains(never, "no discovery yet") {
		t.Error("a page with no successful discovery does not say so")
	}
}

func TestRefreshSecondsHasAFloor(t *testing.T) {
	cfg := testConfig()
	cfg.Interval = 1e9 // one second, as xjson.Duration
	if got := newPageData(snapshot{}, cfg).RefreshSeconds; got != minRefreshSeconds {
		t.Errorf("RefreshSeconds = %d, want the floor %d", got, minRefreshSeconds)
	}
}

// TestGetOnAnUnknownDeviceIs404 also covers the rejection paths. The form is a
// POST because the page used to switch plugs with GET links, which anything
// that follows a link can trip; a command is refused before any device is
// dialled, so none of this needs a plug to be reachable.
func TestGetOnAnUnknownDeviceIs404(t *testing.T) {
	st := newTestState(func(*Config) ([]Device, []netip.Addr, error) {
		return []Device{plugInfo("Desk lamp", "192.0.2.1", true)}, nil, nil
	})
	if err := st.refresh(testConfig()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	h := newRootHandler(st, testConfig())

	for _, tc := range []struct {
		name, target string
		method       string
		body         string
		want         int
	}{
		{"unknown ip", "/?cmd=on&ip=192.0.2.99", http.MethodGet, "", http.StatusNotFound},
		{"no ip", "/?cmd=on", http.MethodGet, "", http.StatusBadRequest},
		{"bad cmd", "/?cmd=explode&ip=192.0.2.1", http.MethodGet, "", http.StatusBadRequest},
		{"post unknown ip", "/", http.MethodPost, "cmd=on&ip=192.0.2.99", http.StatusNotFound},
		{"post bad cmd", "/", http.MethodPost, "cmd=explode&ip=192.0.2.1", http.StatusBadRequest},
	} {
		req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
		if tc.method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}

func TestSetDeviceState(t *testing.T) {
	st := newTestState(func(*Config) ([]Device, []netip.Addr, error) {
		return []Device{plugInfo("Desk lamp", "192.0.2.1", false)}, nil, nil
	})
	if err := st.refresh(testConfig()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	st.setDeviceState("192.0.2.1", true)
	d, ok := st.get().find("192.0.2.1")
	if !ok {
		t.Fatal("device vanished")
	}
	if !d.info.DeviceON {
		t.Error("the switched state was not recorded, so the page after a switch shows the old one")
	}
}

func TestIconHandler(t *testing.T) {
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/icons/on.png", http.StatusOK},
		{"/icons/off.png", http.StatusOK},
		{"/icons/warning.png", http.StatusOK},
		{"/icons/nope.png", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		getIcon(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.path, rec.Code, tc.want)
		}
		if tc.want == http.StatusOK {
			if got := rec.Header().Get("Content-Type"); got != "image/png" {
				t.Errorf("%s: Content-Type = %q", tc.path, got)
			}
			if rec.Body.Len() == 0 {
				t.Errorf("%s: empty body", tc.path)
			}
		}
	}
}
