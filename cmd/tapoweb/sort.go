// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// The columns the table can be sorted by.
//
// These strings are the value of the ?sort= query parameter and the argument
// the template passes to SortHref and AriaSort, so they are part of the page's
// URL surface: renaming one breaks every bookmark of a sorted table.
type sortKey string

const (
	sortName  sortKey = "name"
	sortIP    sortKey = "ip"
	sortMAC   sortKey = "mac"
	sortState sortKey = "state"
	sortDay   sortKey = "day"
	sortMonth sortKey = "month"
	sortID    sortKey = "id"
)

var sortKeys = map[string]sortKey{
	string(sortName):  sortName,
	string(sortIP):    sortIP,
	string(sortMAC):   sortMAC,
	string(sortState): sortState,
	string(sortDay):   sortDay,
	string(sortMonth): sortMonth,
	string(sortID):    sortID,
}

// sorting is one column and one direction: the order the table is rendered in.
type sorting struct {
	key  sortKey
	desc bool
}

// defaultSorting is by name, ascending. That is the order getAllDevices
// already returns devices in, so a URL with no sorting in it renders exactly
// what the page rendered before the columns became sortable.
var defaultSorting = sorting{key: sortName}

// parseSorting reads the sorting out of a query string.
//
// Anything it does not recognise becomes the default rather than an error.
// These values arrive in a URL that a person can edit and a browser can
// truncate, and a mistyped column name is not a reason to refuse to draw the
// page. The names are matched exactly, so ?sort=IP is a mistyped column too.
func parseSorting(q url.Values) sorting {
	s := defaultSorting
	if k, ok := sortKeys[q.Get("sort")]; ok {
		s.key = k
	}
	s.desc = q.Get("order") == "desc"
	return s
}

// query renders the sorting back into a query string, empty for the default.
//
// It is built from the parsed sorting rather than copied from the request, so
// every URL the page emits is one the page would have produced itself, however
// much junk was in the URL that asked for it.
func (s sorting) query() string {
	if s == defaultSorting {
		return ""
	}
	v := url.Values{"sort": {string(s.key)}}
	if s.desc {
		v.Set("order", "desc")
	}
	return "?" + v.Encode()
}

// href is the page's own URL under this sorting.
func (s sorting) href() string {
	return "/" + s.query()
}

// flip returns the sorting that a click on the given column asks for: the same
// column in the opposite direction if it is the sorted one already, and
// otherwise that column, ascending.
//
// Every column starts ascending, the numeric ones included. Opening the energy
// columns descending would answer "which plug is using the most" in one click
// rather than two, but then a first click would point the arrow up in some
// columns and down in others, which reads as the sorting being broken.
func (s sorting) flip(k sortKey) sorting {
	if s.key == k {
		return sorting{key: k, desc: !s.desc}
	}
	return sorting{key: k}
}

// lookupSortKey resolves a column name coming from the template.
//
// Unlike parseSorting, which is fed by a URL, an unknown name here is a typo in
// index.html. It is an error so that the template fails to execute, which makes
// it a logged 500 and a failing test rather than a header that quietly links to
// the wrong column.
func lookupSortKey(name string) (sortKey, error) {
	k, ok := sortKeys[name]
	if !ok {
		return "", fmt.Errorf("unknown sort column '%s'", name)
	}
	return k, nil
}

// sortDevices puts the rows in display order.
//
// It sorts the slice newPageData has just built, never the snapshot's own: that
// one is shared with the refresh goroutine and with every other request in
// flight, and reordering it in place would be the race this program spent three
// `// RACE CONDITIONS AHEAD!` comments on.
func sortDevices(devices []pageDevice, s sorting) {
	sort.SliceStable(devices, func(i, j int) bool {
		a, b := devices[i], devices[j]
		// A row with no reading at all sinks to the bottom whichever way the
		// column is sorted, which is why this happens before the direction is
		// applied. A plug that does not measure energy has not measured zero,
		// and a descending sort that floated it to the top would say it had.
		if c := compareUnmeasured(a, b, s.key); c != 0 {
			return c < 0
		}
		c := compareDevices(a, b, s.key)
		if s.desc {
			c = -c
		}
		if c != 0 {
			return c < 0
		}
		// Ties break by name, ascending, in both directions. Nicknames are
		// unique -- getAllDevices keys a map by them -- so the order of the
		// table is fully determined, and two renderings of the same devices
		// cannot come out shuffled differently.
		return compareName(a, b) < 0
	})
}

// compareUnmeasured orders a plug that reports energy before one that does not,
// and says nothing about any other pair or about any other column.
func compareUnmeasured(a, b pageDevice, k sortKey) int {
	if (k != sortDay && k != sortMonth) || a.hasEnergy == b.hasEnergy {
		return 0
	}
	if a.hasEnergy {
		return -1
	}
	return 1
}

func compareDevices(a, b pageDevice, k sortKey) int {
	switch k {
	case sortIP:
		return compareIP(a, b)
	case sortMAC:
		return strings.Compare(a.MAC, b.MAC)
	case sortState:
		// Ascending is on-first. A boolean has no natural order for this to
		// respect, and "which plugs are on" is the question the column gets
		// asked, so that is the one the first click answers.
		return compareBool(a.On, b.On)
	case sortDay:
		return compareInt(a.energyDay, b.energyDay)
	case sortMonth:
		return compareInt(a.energyMonth, b.energyMonth)
	case sortID:
		return strings.Compare(a.ID, b.ID)
	default:
		return compareName(a, b)
	}
}

// compareName is case-insensitive, so "kettle" sorts next to "Kettle" instead
// of after every capitalised name, which is where comparing bytes puts it.
// Names differing only in case fall back to the bytes, so the order stays
// total.
func compareName(a, b pageDevice) int {
	if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
		return c
	}
	return strings.Compare(a.Name, b.Name)
}

// compareIP compares addresses numerically, so 192.168.1.9 comes before
// 192.168.1.10 rather than after it, which is where comparing the text puts it.
// A plug configured by hostname has no address to compare, so those sort as
// text, after every plug that has one.
func compareIP(a, b pageDevice) int {
	switch {
	case a.addr.IsValid() && b.addr.IsValid():
		return a.addr.Compare(b.addr)
	case a.addr.IsValid():
		return -1
	case b.addr.IsValid():
		return 1
	default:
		return strings.Compare(a.IP, b.IP)
	}
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return -1
	default:
		return 1
	}
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
