// SPDX-License-Identifier: MIT

package main

// This program runs a small web server showing a list of Tapo devices. It must
// run in the same collision domain as the Tapo devices, since the discovery is
// done via broadcast UDP.

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/insomniacslk/tapo"
	"github.com/spf13/cobra"
)

//go:embed on.png
var onIcon []byte

//go:embed off.png
var offIcon []byte

//go:embed warning.png
var warningIcon []byte

// getIcon serves the three icons compiled into the binary. One handler rather
// than the three near-identical ones it replaces, and without the Go 1.22
// pattern matching the old TODO was waiting for -- trimming the prefix works
// on every version this module builds with.
func getIcon(w http.ResponseWriter, r *http.Request) {
	var icon []byte
	switch strings.TrimPrefix(r.URL.Path, "/icons/") {
	case "on.png":
		icon = onIcon
	case "off.png":
		icon = offIcon
	case "warning.png":
		icon = warningIcon
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	// The icons are embedded, so they change only when the binary does.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if _, err := w.Write(icon); err != nil {
		log.Printf("Warning: failed to write icon: %v", err)
	}
}

// newRootHandler serves the device list and the commands that act on it.
//
// There are two interfaces here on purpose. The PAGE switches a plug with a
// form, so it is a POST, and a reload, a prefetch or a crawler cannot turn
// anything on -- the old page did it with GET links driven by XMLHttpRequest,
// which every one of those can follow. The GET query interface is kept exactly
// as it was, because it is the scripting interface and something outside this
// repository may be using it.
func newRootHandler(st *state, cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// One consistent view for the whole request. The refresh goroutine can
		// replace the device list between two statements otherwise, which is
		// what the three `// RACE CONDITIONS AHEAD!` comments this replaces
		// were about.
		snap := st.get()

		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid form", http.StatusBadRequest)
				return
			}
			cmd, ip := r.PostFormValue("cmd"), r.PostFormValue("ip")
			if err := switchPlug(st, snap, cmd, ip); err != nil {
				http.Error(w, err.Error(), statusFor(err))
				return
			}
			// Redirect rather than render, so that reloading the page after a
			// switch does not switch it again. The sorting rides in the form's
			// action and is echoed back here, so switching a plug does not
			// bounce the reader back to a table sorted by name.
			http.Redirect(w, r, parseSorting(r.URL.Query()).href(), http.StatusSeeOther)
			return
		}

		cmd, ip := r.URL.Query().Get("cmd"), r.URL.Query().Get("ip")
		switch cmd {
		case "", "list":
			renderPage(w, snap, cfg, parseSorting(r.URL.Query()))
		case "status":
			d, found := snap.find(ip)
			switch {
			case ip == "":
				http.Error(w, "Missing IP address", http.StatusBadRequest)
			case found:
				info, err := d.plug.GetDeviceInfo()
				if err != nil {
					http.Error(w, fmt.Sprintf("failed to get plug status: %v", err), http.StatusInternalServerError)
					return
				}
				state := "off"
				if info.DeviceON {
					state = "on"
				}
				writeString(w, state)
			case snap.hasFailed(ip):
				http.Error(w, fmt.Sprintf("device with IP %s failed to respond", ip), http.StatusGone)
			default:
				http.NotFound(w, r)
			}
		case "on", "off":
			if err := switchPlug(st, snap, cmd, ip); err != nil {
				http.Error(w, err.Error(), statusFor(err))
				return
			}
			writeString(w, cmd)
		default:
			http.Error(w, fmt.Sprintf("invalid cmd '%s'", cmd), http.StatusBadRequest)
		}
	}
}

// errNoSuchDevice and errBadRequest carry the HTTP status a switch failure
// should get without making switchPlug know about HTTP.
var (
	errNoSuchDevice = errors.New("no such device")
	errBadRequest   = errors.New("bad request")
)

func statusFor(err error) int {
	switch {
	case errors.Is(err, errNoSuchDevice):
		return http.StatusNotFound
	case errors.Is(err, errBadRequest):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// switchPlug turns one plug on or off and records the new state.
//
// Recording it matters because the page is rendered from the cached device
// list: without this the redirect after a switch would show the OLD state
// until the next refresh, which reads exactly like the switch having failed.
func switchPlug(st *state, snap snapshot, cmd, ip string) error {
	on := cmd == "on"
	if !on && cmd != "off" {
		return fmt.Errorf("%w: invalid cmd '%s'", errBadRequest, cmd)
	}
	if ip == "" {
		return fmt.Errorf("%w: missing IP address", errBadRequest)
	}
	d, found := snap.find(ip)
	if !found {
		return fmt.Errorf("%w: %s", errNoSuchDevice, ip)
	}
	if err := d.plug.SetDeviceInfo(on); err != nil {
		return fmt.Errorf("failed to turn plug %s: %w", cmd, err)
	}
	st.setDeviceState(ip, on)
	return nil
}

func writeString(w http.ResponseWriter, s string) {
	if _, err := io.WriteString(w, s); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

// newHealthHandler answers a liveness probe. It is deliberately unconditional:
// the process being able to serve a request is the whole claim, and tying
// liveness to the state of the plugs would restart a perfectly healthy server
// because the network went away.
func newHealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, "ok\n"); err != nil {
			log.Printf("Failed to write response: %v", err)
		}
	}
}

// newReadyHandler answers a readiness probe: ready once there has ever been a
// successful refresh.
//
// It does NOT go unready when a later refresh fails, and that asymmetry is the
// point. Serving a stale page that says it is stale is better than being
// pulled out of rotation and serving nothing at all, whereas answering before
// the first discovery has finished would serve a convincingly empty table.
func newReadyHandler(st *state) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		snap := st.get()
		if snap.updatedAt.IsZero() {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := io.WriteString(w, "no successful discovery yet\n"); err != nil {
				log.Printf("Failed to write response: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprintf(w, "ok, last discovery %s\n", snap.updatedAt.Format(time.RFC3339)); err != nil {
			log.Printf("Failed to write response: %v", err)
		}
	}
}

// errNoDevices is returned by a refresh that succeeded and found nothing. See
// state.refresh for why that is a failure rather than an empty result.
var errNoDevices = errors.New("discovery found no devices at all")

func getAllDevices(username, password string) ([]Device, []netip.Addr, error) {
	client := tapo.NewClient(nil)
	discovered, _, err := client.Discover()
	if err != nil {
		return nil, nil, fmt.Errorf("discover failed: %w", err)
	}
	var (
		unsorted = make(map[string]Device)
		failed   = make([]netip.Addr, 0)
		devices  []Device
		keys     []string
	)
	for _, d := range discovered {
		addr, ok := netip.AddrFromSlice(net.IP(d.Result.IP).To4())
		if !ok {
			return nil, nil, fmt.Errorf("invalid IP '%s': %w", d.Result.IP.String(), err)
		}
		log.Printf("Getting info for '%s'", addr)
		plug := tapo.NewPlug(addr.String(), nil)
		if err := plug.Handshake(username, password); err != nil {
			log.Printf("Warning: handshake failed for %s: %v", addr, err)
			failed = append(failed, addr)
			continue
		}
		info, err := plug.GetDeviceInfo()
		if err != nil {
			log.Printf("Warning: GetDeviceInfo failed for %s: %v", addr, err)
			failed = append(failed, addr)
			continue
		}
		// TODO add more devices that support GetEnergyUsage
		var energy *tapo.EnergyUsage
		if info.Model == "P110" {
			energy, err = plug.GetEnergyUsage()
			if err != nil {
				log.Printf("Warning: GetEnergyInfo failed for %s: %v", addr, err)
			}
		}
		unsorted[info.DecodedNickname] = Device{plug: plug, info: info, energy: energy}
		keys = append(keys, info.DecodedNickname)
	}
	sort.Strings(keys)
	for _, k := range keys {
		devices = append(devices, unsorted[k])
	}
	return devices, failed, nil
}

func newRootCommand() *cobra.Command {
	def := defaultConfig()
	cmd := &cobra.Command{
		Use:   progname,
		Short: "A web view of the Tapo plugs on the local network",
		Long: progname + ` serves a page listing every Tapo plug it can find, with a
switch for each one. Discovery is broadcast UDP, so it has to run in the same
collision domain as the plugs.

Settings come from four places, each overriding the one before it: the built-in
defaults, the JSON config file, the environment, and these flags. There is one
name per setting, not three -- the config key is the flag name with underscores
and the environment variable is ` + envPrefix + ` plus the flag name upper-cased,
so --password-file is "password_file" and $` + envName("password-file") + `.

Prefer the environment or --password-file to --password: a password on the
command line is readable by every local user in /proc/<pid>/cmdline, and is
copied into journald's _CMDLINE field on every line the process logs.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig(cmd.Flags())
			if err != nil {
				return err
			}
			return run(cfg)
		},
	}
	f := cmd.Flags()
	f.StringP("config", "c", defaultConfigFile, "Configuration file. Absent is fine at this default path, an error if the path is given explicitly")
	f.StringP("listen", "l", def.Listen, "Listen host:port address")
	f.StringP("username", "u", def.Username, "TP-Link username (usually an email)")
	f.StringP("password", "p", def.Password, "TP-Link password. Visible to every local user in /proc/<pid>/cmdline -- prefer $"+envName("password")+" or --password-file")
	f.String("password-file", def.PasswordFile, "Read the TP-Link password from this file, without its trailing newline. Mutually exclusive with --password")
	f.DurationP("interval", "i", time.Duration(def.Interval), "Device refresh interval")
	f.BoolP("show-id", "I", def.ShowID, "Show the Tapo device ID column")
	return cmd
}

func run(cfg *Config) error {
	// SIGINT/SIGTERM cancel the context, which stops the refresh loop and
	// starts a graceful shutdown. Without this the process is killed mid
	// response, which under Kubernetes is every rollout.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st := newState()
	go st.refreshLoop(ctx, cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/", newRootHandler(st, cfg))
	mux.HandleFunc("/healthz", newHealthHandler())
	mux.HandleFunc("/readyz", newReadyHandler(st))
	mux.HandleFunc("/icons/", getIcon)

	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: mux,
		// A handler can talk to a plug, and the library gives those calls a
		// 10s timeout of their own, so the write timeout has to leave room for
		// one of them plus the response.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("Listening on %s", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Printf("Shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
