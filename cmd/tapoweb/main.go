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

func getListHTML(snap snapshot, cfg *Config) string {
	devices, showID := snap.devices, cfg.ShowID
	allIPs := make([]string, 0, len(devices))
	for _, d := range devices {
		allIPs = append(allIPs, `"`+d.info.IP+`"`)
	}
	ret := fmt.Sprintf(`<!DOCTYPE html>
<html>
 <head>
  <title>Tapo plugs</title>
  <style>
  body {
    background-color: #282828;
    color: #d3d3d3;
  }
  color: white;
  a {
  color: white
  }
  a:link {
    color: white;
  }
  a:visited {
    color: white;
  }
  a:hover {
    color: yellow;
  }
  a:active {
    color: yellow;
  }
  thead {
   font-weight: bold;
  }
  .text-bold {
   font-weight: bold;
  }
  table, tr, td {
   border: 1px solid black;
  }
  </style>
  <script>
   var allIPs = [%s];
   function updateAll() {
    console.log("Updating status for " + allIPs);
    for (let i=0; i<allIPs.length; i++) {
     updateStatus("status_" + allIPs[i].replaceAll(".", "_"), allIPs[i]);
    }
   }
   setInterval(updateAll, 10000);

   function updateStatus(tagID, ip) {
    var xmlhttp = new XMLHttpRequest();

    xmlhttp.onreadystatechange = function() {
        if (xmlhttp.readyState == XMLHttpRequest.DONE) { // XMLHttpRequest.DONE == 4
           img = document.getElementById(tagID);
           if (xmlhttp.status == 200) {
               if (xmlhttp.response == "on") {
                img.src = "/icons/on.png";
               img.setAttribute("onclick", "turnOff('" + tagID + "', '" + ip + "');");
               } else if (xmlhttp.response == "off") {
                img.src = "/icons/off.png";
                img.setAttribute("onclick", "turnOn('" + tagID + "', '" + ip + "');");
               } else {
                console.log("failed to get status for " + ip + ": " + xmlhttp.response);
               }
           } else {
               img.src = "/icons/warning.png";
               console.log("failed to get status for " + ip + ": " + xmlhttp.status);
           }
        }
    };

    xmlhttp.open("GET", "/?cmd=status&ip=" + ip, true);
    xmlhttp.send();
   }

   function turnOn(tagID, ip) {
    var xmlhttp = new XMLHttpRequest();

    xmlhttp.onreadystatechange = function() {
        if (xmlhttp.readyState == XMLHttpRequest.DONE) { // XMLHttpRequest.DONE == 4
           if (xmlhttp.status == 200) {
               updateStatus(tagID, ip);
           } else {
               console.log('failed to turn plug on, got HTTP ' + xmlhttp.status);
           }
        }
    };

    xmlhttp.open("GET", "/?cmd=on&ip=" + ip, true);
    xmlhttp.send();
   }

   function turnOff(tagID, ip) {
    var xmlhttp = new XMLHttpRequest();

    xmlhttp.onreadystatechange = function() {
        if (xmlhttp.readyState == XMLHttpRequest.DONE) { // XMLHttpRequest.DONE == 4
           if (xmlhttp.status == 200) {
               updateStatus(tagID, ip);
           } else {
               alert('failed to turn plug off, got HTTP ' + xmlhttp.status);
           }
        }
    };

    xmlhttp.open("GET", "/?cmd=off&ip=" + ip, true);
    xmlhttp.send();
   }
  </script>
 </head>
 <body>
`, strings.Join(allIPs, ", "))
	if snap.stale(time.Duration(cfg.Interval)) {
		ret += fmt.Sprintf("  <p><strong>Stale:</strong> last successful update %s ago (%v)</p>\n",
			time.Since(snap.updatedAt).Truncate(time.Second), snap.lastErr)
	}
	ret += "  <table>\n"
	ret += "   <thead><tr><td class=\"text.bold\">#</td><td class=\"text.bold\">Name</td><td class=\"text.bold\">IP</td><td class=\"text.bold\">MAC</td><td class=\"text.bold\">State</td><td class=\"\">Energy<br />today (kWh)</td><td>Energy <br />month (kWh)</td>"
	if showID {
		ret += "<td class=\"text.bold\">ID</td>"
	}
	ret += "</tr></thead>\n"
	for idx, d := range devices {
		ret += "   <tr>\n"
		ret += fmt.Sprintf("    <td>%d</td>\n", idx+1)
		ret += "    <td class=\"text-bold\" onclick=\"navigator.clipboard.writeText('" + d.info.DecodedNickname + "')\">" + d.info.DecodedNickname + "</td>\n"
		ret += "    <td onclick=\"navigator.clipboard.writeText('" + d.info.IP + "')\">" + d.info.IP + "</td>\n"
		ret += "    <td onclick=\"navigator.clipboard.writeText('" + d.info.MAC + "')\">" + d.info.MAC + "</td>\n"
		statusTagID := "status_" + strings.Replace(d.info.IP, ".", "_", -1)
		callback := "turnOn('" + statusTagID + "', '" + d.info.IP + "')"
		if d.info.DeviceON {
			callback = "turnOff('" + statusTagID + "', '" + d.info.IP + "')"
		}
		state := "<img id='" + statusTagID + "' src=\"/icons/off.png\" height=\"16px;\" onclick=\"" + callback + "\" />"
		if d.info.DeviceON {
			state = "<img id='" + statusTagID + "' src=\"/icons/on.png\" height=\"16px;\" onclick=\"" + callback + "\" />"
		}

		ret += "    <td>" + state + "</td>\n"
		var energyInfoDay, energyInfoMonth string
		if d.energy != nil {
			energyInfoDay = fmt.Sprintf("%.1f", float64(d.energy.TodayEnergy)/1000)
			energyInfoMonth = fmt.Sprintf("%.1f", float64(d.energy.MonthEnergy)/1000)
		}
		ret += "    <td>" + energyInfoDay + "</td>\n"
		ret += "    <td>" + energyInfoMonth + "</td>\n"
		if showID {
			ret += "    <td onclick=\"navigator.clipboard.writeText('" + d.info.DeviceID + "')\">" + d.info.DeviceID + "</td>\n"
		}
		ret += "   </tr>\n"
	}
	return ret + "  </table>\n </body>\n</html>\n"
}

// TODO consolidate into a single function for /icons/*
func getIconOn(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "image/png")
	if _, err := w.Write(onIcon); err != nil {
		log.Printf("Warning: failed to write ON icon: %v", err)
	}
}

// Waiting for the new HTTP mux in Go 1.22
/*
func getIcon(w http.ResponseWriter, r *http.Request) {
       status := http.StatusOK
       var iconBytes []byte
       icon := r.PathValue("icon")
       switch icon {
       case "on":
               iconBytes = onIcon
       case "off":
               iconBytes = offIcon
       case "warning":
               iconBytes = warningIcon
       default:
               status = http.StatusNotFound
               iconBytes = nil
       }
}
*/

func getIconOff(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "image/png")
	if _, err := w.Write(offIcon); err != nil {
		log.Printf("Warning: failed to write OFF icon: %v", err)
	}
}

func getIconWarning(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "image/png")
	if _, err := w.Write(warningIcon); err != nil {
		log.Printf("Warning: failed to write WARNING icon: %v", err)
	}
}

func newRootHandler(st *state, cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cmd := r.URL.Query().Get("cmd")
		ip := r.URL.Query().Get("ip")
		var (
			status = http.StatusOK
			msg    string
		)
		// One consistent view for the whole request. The refresh goroutine can
		// replace the device list between two statements otherwise, which is
		// what the three `// RACE CONDITIONS AHEAD!` comments this replaces
		// were about.
		snap := st.get()
		if ip == "" && (cmd == "status" || cmd == "on" || cmd == "off") {
			status = http.StatusBadRequest
			msg = "Missing IP address"
		} else {
			switch cmd {
			case "status":
				d, found := snap.find(ip)
				switch {
				case found:
					info, err := d.plug.GetDeviceInfo()
					if err != nil {
						status = http.StatusInternalServerError
						msg = fmt.Sprintf("failed to get plug status: %v", err)
						break
					}
					msg = "off"
					if info.DeviceON {
						msg = "on"
					}
				case snap.hasFailed(ip):
					status = http.StatusGone
					msg = fmt.Sprintf("device with IP %s failed to respond", ip)
				default:
					status = http.StatusNotFound
					msg = "404 Not Found"
				}
			case "on", "off":
				d, found := snap.find(ip)
				if !found {
					status = http.StatusNotFound
					msg = "404 Not Found"
					break
				}
				if err := d.plug.SetDeviceInfo(cmd == "on"); err != nil {
					status = http.StatusInternalServerError
					msg = fmt.Sprintf("failed to turn plug %s: %v", cmd, err)
				}
			case "", "list":
				status = http.StatusOK
				msg = getListHTML(snap, cfg)
			default:
				status = http.StatusBadRequest
				msg = fmt.Sprintf("invalid cmd '%s'", cmd)
			}
		}
		w.WriteHeader(status)
		if _, err := io.WriteString(w, msg); err != nil {
			log.Printf("Failed to write response: %v", err)
		}
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
	// waiting for Go 1.22...
	/*
		mux.HandleFunc("/icons/{icon}.png", getIcon)
	*/
	mux.HandleFunc("/icons/on.png", getIconOn)
	mux.HandleFunc("/icons/off.png", getIconOff)
	mux.HandleFunc("/icons/warning.png", getIconWarning)

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
