// SPDX-License-Identifier: MIT

package main

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/insomniacslk/tapo"
)

func sortablePlug(name, ip, mac, id string, on bool, day, month int) Device {
	return Device{
		info: &tapo.DeviceInfo{
			DecodedNickname: name,
			IP:              ip,
			MAC:             mac,
			DeviceID:        id,
			DeviceON:        on,
		},
		energy: &tapo.EnergyUsage{TodayEnergy: day, MonthEnergy: month},
	}
}

// Three plugs arranged so that every column orders them differently, which is
// what stops a test for one column passing on another column's ordering. In
// particular none of these orders -- in either direction -- is the name order,
// so a key that fell through to the default branch of compareDevices would
// fail every case rather than silently pass.
func sortableDevices() []Device {
	return []Device{
		sortablePlug("lamp", "192.0.2.10", "AA-00-00-00-00-03", "8022-C", true, 50, 5000),
		sortablePlug("Kettle", "192.0.2.9", "AA-00-00-00-00-01", "8022-A", false, 300, 100),
		sortablePlug("fridge", "192.0.2.100", "AA-00-00-00-00-02", "8022-B", true, 120, 900),
	}
}

// displayOrder renders the devices and reports the order their names came out
// in, which is the only thing about sorting a reader of the page can see.
func displayOrder(t *testing.T, devices []Device, s sorting) string {
	t.Helper()
	snap := snapshot{devices: devices, updatedAt: time.Now()}
	data := newPageData(snap, testConfig(), s)
	names := make([]string, 0, len(data.Devices))
	for _, d := range data.Devices {
		names = append(names, d.Name)
	}
	return strings.Join(names, " ")
}

func TestSortColumns(t *testing.T) {
	for _, tc := range []struct {
		key       sortKey
		asc, desc string
		why       string
	}{
		{sortName, "fridge Kettle lamp", "lamp Kettle fridge",
			"names sort case-insensitively, so Kettle belongs between the lowercase two"},
		{sortIP, "Kettle lamp fridge", "fridge lamp Kettle",
			"addresses sort numerically: .9 before .10 before .100, not as text"},
		{sortMAC, "Kettle fridge lamp", "lamp fridge Kettle", ""},
		{sortState, "fridge lamp Kettle", "Kettle fridge lamp",
			"ascending is on-first, and the two that are on tie and break by name"},
		{sortDay, "lamp fridge Kettle", "Kettle fridge lamp", ""},
		{sortMonth, "Kettle fridge lamp", "lamp fridge Kettle", ""},
		{sortID, "Kettle fridge lamp", "lamp fridge Kettle", ""},
	} {
		if got := displayOrder(t, sortableDevices(), sorting{key: tc.key}); got != tc.asc {
			t.Errorf("sort=%s asc: got [%s], want [%s]. %s", tc.key, got, tc.asc, tc.why)
		}
		if got := displayOrder(t, sortableDevices(), sorting{key: tc.key, desc: true}); got != tc.desc {
			t.Errorf("sort=%s desc: got [%s], want [%s]. %s", tc.key, got, tc.desc, tc.why)
		}
	}
}

// TestSortTiesAlwaysBreakAscending pins the rule that keeps the table from
// reshuffling under the reader: the tiebreak does not follow the direction, so
// reversing a column reverses the column and nothing else.
func TestSortTiesAlwaysBreakAscending(t *testing.T) {
	// Two plugs that are on, one that is off, so the state column is all ties.
	asc := displayOrder(t, sortableDevices(), sorting{key: sortState})
	desc := displayOrder(t, sortableDevices(), sorting{key: sortState, desc: true})
	if !strings.HasPrefix(asc, "fridge lamp") {
		t.Errorf("ascending ties are not in name order: [%s]", asc)
	}
	if !strings.HasSuffix(desc, "fridge lamp") {
		t.Errorf("descending ties are not in name order either: [%s]", desc)
	}
}

// TestSortUnmeasuredEnergyIsAlwaysLast: a plug that does not report energy has
// not reported zero, so it must not float to the top of a descending column.
func TestSortUnmeasuredEnergyIsAlwaysLast(t *testing.T) {
	devices := sortableDevices()
	mute := sortablePlug("mute", "192.0.2.50", "AA-00-00-00-00-04", "8022-D", true, 0, 0)
	mute.energy = nil
	devices = append(devices, mute)

	for _, key := range []sortKey{sortDay, sortMonth} {
		for _, desc := range []bool{false, true} {
			got := displayOrder(t, devices, sorting{key: key, desc: desc})
			if !strings.HasSuffix(got, " mute") {
				t.Errorf("sort=%s desc=%v: the plug with no reading is not last: [%s]", key, desc, got)
			}
		}
	}
}

// TestSortIPFallsBackToTextForHostnames covers a plug configured by name
// rather than by address, which has nothing to compare numerically.
func TestSortIPFallsBackToTextForHostnames(t *testing.T) {
	devices := []Device{
		sortablePlug("byname", "kettle.lan", "AA-00-00-00-00-01", "8022-A", true, 1, 1),
		sortablePlug("byaddr", "192.0.2.10", "AA-00-00-00-00-02", "8022-B", true, 1, 1),
	}
	if got := displayOrder(t, devices, sorting{key: sortIP}); got != "byaddr byname" {
		t.Errorf("got [%s], want the addressed plug before the named one", got)
	}
}

// TestSortNumbersRowsInDisplayOrder: the # column is the row's position in the
// table, so it always counts 1..n however the table is sorted.
func TestSortNumbersRowsInDisplayOrder(t *testing.T) {
	snap := snapshot{devices: sortableDevices(), updatedAt: time.Now()}
	data := newPageData(snap, testConfig(), sorting{key: sortDay, desc: true})
	for i, d := range data.Devices {
		if d.Index != i+1 {
			t.Errorf("row %d is numbered %d", i+1, d.Index)
		}
	}
}

func TestParseSorting(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  sorting
	}{
		{"", defaultSorting},
		{"sort=ip", sorting{key: sortIP}},
		{"sort=ip&order=desc", sorting{key: sortIP, desc: true}},
		{"sort=month&order=asc", sorting{key: sortMonth}},
		// Junk is the default rather than an error: these values come from a
		// URL a person can edit.
		{"sort=nonsense", defaultSorting},
		{"sort=IP", defaultSorting},
		{"order=sideways", defaultSorting},
		{"sort=&order=", defaultSorting},
	} {
		q, err := url.ParseQuery(tc.query)
		if err != nil {
			t.Fatalf("ParseQuery(%q): %v", tc.query, err)
		}
		if got := parseSorting(q); got != tc.want {
			t.Errorf("parseSorting(%q) = %+v, want %+v", tc.query, got, tc.want)
		}
	}
}

// TestSortingHrefRoundTrips: every URL the page emits must parse back to the
// sorting it was emitted for, or a header link would sort by something other
// than the column it sits on.
func TestSortingHrefRoundTrips(t *testing.T) {
	for _, key := range []sortKey{sortName, sortIP, sortMAC, sortState, sortDay, sortMonth, sortID} {
		for _, desc := range []bool{false, true} {
			s := sorting{key: key, desc: desc}
			u, err := url.Parse(s.href())
			if err != nil {
				t.Fatalf("%+v produced an unparseable href %q: %v", s, s.href(), err)
			}
			if got := parseSorting(u.Query()); got != s {
				t.Errorf("%q parsed back as %+v, want %+v", s.href(), got, s)
			}
		}
	}
	// The default sorting leaves the URL alone, so a reader who never sorts
	// anything never sees a query string.
	if got := defaultSorting.href(); got != "/" {
		t.Errorf("the default sorting renders as %q, want \"/\"", got)
	}
}

func TestSortHrefFlipsTheSortedColumn(t *testing.T) {
	p := pageData{sort: sorting{key: sortIP}}

	// The sorted column links to itself, reversed.
	if got, err := p.SortHref("ip"); err != nil || got != "/?order=desc&sort=ip" {
		t.Errorf("SortHref(ip) = %q, %v; want the same column descending", got, err)
	}
	// Every other column links to itself, ascending.
	if got, err := p.SortHref("mac"); err != nil || got != "/?sort=mac" {
		t.Errorf("SortHref(mac) = %q, %v; want mac ascending", got, err)
	}
	// And a column whose ascending form is the default drops the query.
	if got, err := p.SortHref("name"); err != nil || got != "/" {
		t.Errorf("SortHref(name) = %q, %v; want the bare default URL", got, err)
	}

	// Clicking the same column twice comes back to where it started.
	back := sorting{key: sortIP}.flip(sortIP).flip(sortIP)
	if back != (sorting{key: sortIP}) {
		t.Errorf("two clicks on one column ended at %+v", back)
	}
}

func TestAriaSort(t *testing.T) {
	p := pageData{sort: sorting{key: sortIP, desc: true}}
	for name, want := range map[string]string{"ip": "descending", "name": "none", "month": "none"} {
		got, err := p.AriaSort(name)
		if err != nil {
			t.Fatalf("AriaSort(%q): %v", name, err)
		}
		if got != want {
			t.Errorf("AriaSort(%q) = %q, want %q", name, got, want)
		}
	}
	if got, err := (pageData{sort: defaultSorting}).AriaSort("name"); err != nil || got != "ascending" {
		t.Errorf("AriaSort(name) under the default sorting = %q, %v", got, err)
	}
}

// TestSortMethodsRejectUnknownColumns: these take a string because the template
// passes a literal, so a typo in index.html has to fail loudly rather than
// render a link to nowhere.
func TestSortMethodsRejectUnknownColumns(t *testing.T) {
	p := pageData{sort: defaultSorting}
	if _, err := p.SortHref("nonsense"); err == nil {
		t.Error("SortHref accepted a column that does not exist")
	}
	if _, err := p.AriaSort("nonsense"); err == nil {
		t.Error("AriaSort accepted a column that does not exist")
	}
}

func TestPageHeadersAreSortLinks(t *testing.T) {
	snap := snapshot{devices: sortableDevices(), updatedAt: time.Now()}
	cfg := testConfig()
	cfg.ShowID = true
	body := renderSorted(t, snap, cfg, sorting{key: sortDay, desc: true})

	// Every column is a link. The ampersand is escaped because html/template
	// escapes attribute values, which is correct HTML and what a browser turns
	// back into "&".
	for _, want := range []string{
		`href="/"`, // name, whose ascending form is the default
		`href="/?sort=ip"`,
		`href="/?sort=mac"`,
		`href="/?sort=state"`,
		`href="/?sort=month"`,
		`href="/?sort=id"`,
		// The sorted column offers the flip, which is back to ascending, and
		// ascending is spelled by leaving ?order= out entirely.
		`href="/?sort=day"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the header links do not include %s", want)
		}
	}
	// The sorted column says so, and only it does.
	if !strings.Contains(body, `aria-sort="descending"`) {
		t.Error("the sorted column is not marked with aria-sort")
	}
	if strings.Count(body, `aria-sort="none"`) != 6 {
		t.Errorf("want 6 unsorted columns marked aria-sort=none, got %d", strings.Count(body, `aria-sort="none"`))
	}
}

// TestSwitchKeepsSorting: the sorting rides in the form's action so that the
// handler can echo it into the redirect after a switch. It stops at the form
// and the redirect target because switching a plug for real dials it, and no
// plug answers in a test.
func TestSwitchKeepsSorting(t *testing.T) {
	snap := snapshot{devices: sortableDevices(), updatedAt: time.Now()}
	s := sorting{key: sortIP, desc: true}
	body := renderSorted(t, snap, testConfig(), s)
	if !strings.Contains(body, `<form method="post" action="/?order=desc&amp;sort=ip">`) {
		t.Error("the switch form does not post back to the sorted URL, so switching a plug loses the sorting")
	}
	u, err := url.Parse(s.href())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := parseSorting(u.Query()).href(); got != s.href() {
		t.Errorf("the redirect after a switch would go to %q, want %q", got, s.href())
	}
}
