// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/netip"
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

	// The values the sort actually compares, which are not the ones above: the
	// energy strings are rounded to a tenth of a kWh, so sorting on them would
	// call 0.04 and 0.0 equal, and the IP is text, which orders .10 before .9.
	// They are unexported, so the template cannot render them by mistake.
	addr        netip.Addr
	energyDay   int
	energyMonth int
	hasEnergy   bool
}

type pageData struct {
	// sort is the column and direction the table is in. It is unexported
	// because the template has no business rendering it: what the template
	// needs are the three methods below.
	sort           sorting
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

func newPageData(snap snapshot, cfg *Config, s sorting) pageData {
	interval := time.Duration(cfg.Interval)
	data := pageData{
		sort:           s,
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
	for _, d := range snap.devices {
		pd := pageDevice{
			Name: d.info.DecodedNickname,
			IP:   d.info.IP,
			MAC:  d.info.MAC,
			ID:   d.info.DeviceID,
			On:   d.info.DeviceON,
		}
		// A plug can be configured by hostname, in which case there is no
		// address to sort numerically and compareIP falls back to the text.
		pd.addr, _ = netip.ParseAddr(d.info.IP)
		// A plug that does not report energy leaves both blank, which the
		// template renders as a dash: zero would be a measurement, and this is
		// the absence of one.
		if d.energy != nil {
			pd.hasEnergy = true
			pd.energyDay, pd.energyMonth = d.energy.TodayEnergy, d.energy.MonthEnergy
			pd.EnergyDay = fmt.Sprintf("%.1f", float64(d.energy.TodayEnergy)/1000)
			pd.EnergyMonth = fmt.Sprintf("%.1f", float64(d.energy.MonthEnergy)/1000)
		}
		data.Devices = append(data.Devices, pd)
	}
	sortDevices(data.Devices, s)
	// The number is numbered after the sort, because it is the row's position
	// in the table as displayed: it counts 1..n whatever the table is sorted
	// by, rather than following one plug around as the order changes.
	for i := range data.Devices {
		data.Devices[i].Index = i + 1
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
func renderPage(w http.ResponseWriter, snap snapshot, cfg *Config, s sorting) {
	var buf bytes.Buffer
	if err := indexTemplate.Execute(&buf, newPageData(snap, cfg, s)); err != nil {
		log.Printf("Failed to render the page: %v", err)
		http.Error(w, "failed to render the page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("Failed to write the page: %v", err)
	}
}

// SortHref is where a column header links: the same table sorted by that
// column, flipped to the other direction if it is the sorted column already.
func (p pageData) SortHref(name string) (string, error) {
	k, err := lookupSortKey(name)
	if err != nil {
		return "", err
	}
	return p.sort.flip(k).href(), nil
}

// AriaSort is a header's aria-sort attribute: "ascending", "descending" or
// "none". The stylesheet draws the arrow from that attribute, so the sorted
// column is recorded in exactly one place and what a screen reader announces
// cannot drift from what everyone else sees.
func (p pageData) AriaSort(name string) (string, error) {
	k, err := lookupSortKey(name)
	if err != nil {
		return "", err
	}
	switch {
	case p.sort.key != k:
		return "none", nil
	case p.sort.desc:
		return "descending", nil
	default:
		return "ascending", nil
	}
}

// SelfHref is the page's own URL, which the switch forms post to so that
// switching a plug does not throw the sorting away.
func (p pageData) SelfHref() string {
	return p.sort.href()
}
