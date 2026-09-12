// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"
)

//go:embed index.html
var indexHTML string

// The template is parsed once, at package initialisation, so a broken template
// is a panic the moment the binary starts rather than a 500 the first time
// somebody looks at the page.
var indexTemplate = template.Must(template.New("index").Parse(indexHTML))

// minRefreshSeconds bounds the page's meta refresh. Rendering is cheap -- it
// reads the list the background loop already keeps, and talks to no plug -- but
// a one-second --interval would otherwise turn into a one-second page reload.
const minRefreshSeconds = 10

// pageDevice is one plug, formatted for the template.
//
// The template is given strings rather than the tapo types on purpose: every
// value it renders is escaped by html/template, and nothing in here can reach
// for a live device while a page is being written.
type pageDevice struct {
	Index       int
	Name        string
	IP          string
	MAC         string
	ID          string
	On          bool
	EnergyDay   string
	EnergyMonth string
}

type pageData struct {
	Devices        []pageDevice
	Failed         []string
	ShowID         bool
	Stale          bool
	StaleFor       string
	UpdatedAgo     string
	NeverUpdated   bool
	LastErr        string
	RefreshSeconds int
}

func newPageData(snap snapshot, cfg *Config) pageData {
	interval := time.Duration(cfg.Interval)
	data := pageData{
		ShowID:         cfg.ShowID,
		Stale:          snap.stale(interval),
		NeverUpdated:   snap.updatedAt.IsZero(),
		RefreshSeconds: int(interval.Seconds()),
	}
	if data.RefreshSeconds < minRefreshSeconds {
		data.RefreshSeconds = minRefreshSeconds
	}
	if !data.NeverUpdated {
		age := time.Since(snap.updatedAt).Truncate(time.Second)
		data.UpdatedAgo = age.String()
		data.StaleFor = age.String()
	}
	if snap.lastErr != nil {
		data.LastErr = snap.lastErr.Error()
	}
	for i, d := range snap.devices {
		pd := pageDevice{
			Index: i + 1,
			Name:  d.info.DecodedNickname,
			IP:    d.info.IP,
			MAC:   d.info.MAC,
			ID:    d.info.DeviceID,
			On:    d.info.DeviceON,
		}
		// A plug that does not report energy leaves both blank, which the
		// template renders as a dash: zero would be a measurement, and this is
		// the absence of one.
		if d.energy != nil {
			pd.EnergyDay = fmt.Sprintf("%.1f", float64(d.energy.TodayEnergy)/1000)
			pd.EnergyMonth = fmt.Sprintf("%.1f", float64(d.energy.MonthEnergy)/1000)
		}
		data.Devices = append(data.Devices, pd)
	}
	for _, addr := range snap.failed {
		data.Failed = append(data.Failed, addr.String())
	}
	return data
}

// renderPage writes the device list page.
//
// It renders into a buffer first so that a template error cannot leave a half
// written 200 on the wire: either the whole page is written, or the client is
// told the request failed.
func renderPage(w http.ResponseWriter, snap snapshot, cfg *Config) {
	var buf bytes.Buffer
	if err := indexTemplate.Execute(&buf, newPageData(snap, cfg)); err != nil {
		log.Printf("Failed to render the page: %v", err)
		http.Error(w, "failed to render the page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("Failed to write the page: %v", err)
	}
}
