// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"net/netip"
	"sync"
	"time"

	"github.com/insomniacslk/tapo"
)

// How long to wait before retrying after a failed refresh, and how far that
// wait is allowed to grow. Discovery failures on a home network are usually
// transient -- a lost broadcast, a switch reloading -- so the first retry is
// quick, and a persistent failure backs off rather than filling the log.
const (
	minRetryInterval = 5 * time.Second
	maxRetryInterval = 5 * time.Minute
)

// snapshot is a consistent view of the device list, handed to a request
// handler so it never reads the slice the refresh loop is writing.
type snapshot struct {
	devices []Device
	failed  []netip.Addr
	// updatedAt is when the devices below were last refreshed SUCCESSFULLY.
	// Zero means there has never been a successful refresh, which is the only
	// state in which the device list is empty because nothing is known rather
	// than because nothing was found.
	updatedAt time.Time
	// lastErr is the error from the most recent attempt, successful or not, so
	// a page rendered from stale data can say why it is stale.
	lastErr error
}

// stale reports whether the snapshot is old enough to be worth mentioning: no
// successful refresh has happened in twice the configured interval, which is
// one missed cycle plus slack rather than a number invented for the purpose.
func (s snapshot) stale(interval time.Duration) bool {
	return time.Since(s.updatedAt) > 2*interval
}

// state holds the device list the handlers serve.
//
// The previous version shared three package-level variables between the
// refresh goroutine and every request handler with no synchronisation at all,
// and said so in three separate `// RACE CONDITIONS AHEAD!` comments. The
// device list is written wholesale once a minute and read on every request, so
// an RWMutex around a pointer swap costs nothing measurable.
type state struct {
	mu   sync.RWMutex
	snap snapshot
	// discover is what refresh calls to find the plugs. It is a field rather
	// than a direct call to getAllDevices because everything worth testing
	// here is what happens when discovery FAILS, and a real discovery cannot
	// be asked to fail on demand.
	discover func(cfg *Config) ([]Device, []netip.Addr, error)
}

func newState() *state {
	return &state{
		discover: func(cfg *Config) ([]Device, []netip.Addr, error) {
			return getAllDevices(cfg.Username, cfg.Password)
		},
	}
}

func (s *state) get() snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// refresh runs one discovery pass and stores the result.
//
// A failure LEAVES THE PREVIOUS DEVICE LIST IN PLACE. That is the whole point:
// the old code called log.Fatalf here, so a single lost broadcast -- the most
// common thing that can go wrong with a program whose only device path is
// broadcast UDP -- killed the process. Under systemd's Restart=always that
// merely looked like a blip; anywhere else it is an outage.
func (s *state) refresh(cfg *Config) error {
	devices, failed, err := s.discover(cfg)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.lastErr = err
	if err != nil {
		return err
	}
	// A discovery that succeeds and finds NOTHING is treated as a failure
	// rather than as an empty network. Losing the broadcast is silent -- the
	// call returns no error, just an empty list -- so believing it would
	// replace a good page with a blank one and report nothing anywhere.
	// Refusing to do that costs only the case where every plug really has been
	// unplugged, which resolves itself into a stale page that says so.
	if len(devices) == 0 && len(failed) == 0 && !s.snap.updatedAt.IsZero() {
		return errNoDevices
	}
	s.snap.devices = devices
	s.snap.failed = failed
	s.snap.updatedAt = time.Now()
	return nil
}

// refreshLoop refreshes until ctx is cancelled, backing off on failure.
func (s *state) refreshLoop(ctx context.Context, cfg *Config) {
	backoff := minRetryInterval
	for {
		wait := time.Duration(cfg.Interval)
		if err := s.refresh(cfg); err != nil {
			log.Printf("Warning: refresh failed, serving the previous device list: %v", err)
			wait = backoff
			if backoff *= 2; backoff > maxRetryInterval {
				backoff = maxRetryInterval
			}
		} else {
			snap := s.get()
			log.Printf("Got %d devices and %d failed devices", len(snap.devices), len(snap.failed))
			backoff = minRetryInterval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// find returns the device with the given IP, if the snapshot has one.
func (s snapshot) find(ip string) (Device, bool) {
	for _, d := range s.devices {
		if d.info.IP == ip {
			return d, true
		}
	}
	return Device{}, false
}

// hasFailed reports whether the given IP is one discovery found but could not
// talk to.
func (s snapshot) hasFailed(ip string) bool {
	for _, addr := range s.failed {
		if addr.String() == ip {
			return true
		}
	}
	return false
}

// Device is a discovered plug and the last thing it said about itself.
type Device struct {
	plug   *tapo.Plug
	info   *tapo.DeviceInfo
	energy *tapo.EnergyUsage
}
