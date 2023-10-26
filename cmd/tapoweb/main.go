package main

// This program runs a small web server showing a list of Tapo devices. It must
// run in the same collision domain as the Tapo devices, since the discovery is
// done via broadcast UDP.

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"time"

	"github.com/insomniacslk/tapo"
	"github.com/spf13/pflag"
)

var (
	flagListen   = pflag.StringP("listen", "l", ":7490", "Listen host:port address")
	flagUsername = pflag.StringP("username", "u", "", "TP-Link username (usually an email)")
	flagPassword = pflag.StringP("password", "p", "", "TP-Link password")
	flagInterval = pflag.DurationP("interval", "i", time.Minute, "Update interval")
)

func getListHTML(devices []*tapo.DeviceInfo) string {
	ret := "<table border=\"1px;\">\n"
	ret += "<thead><tr><td>Name</td><td>IP</td><td>MAC</td><td>State</td></tr></thead>\n"
	for _, d := range devices {
		ret += "<tr>\n"
		ret += "<td>" + d.DecodedNickname + "</td>\n"
		ret += "<td>" + d.IP + "</td>\n"
		ret += "<td>" + d.MAC + "</td>\n"
		state := "off"
		if d.DeviceON {
			state = "on"
		}
		ret += "<td>" + state + "</td>\n"
		ret += "</tr>"
	}
	return ret + "</table>\n"
}

func getRootHandler(username, password string, interval time.Duration) func(http.ResponseWriter, *http.Request) {
	var (
		devices []*tapo.DeviceInfo
		err     error
	)
	go func() {
		for {
			devices, err = getAllDevices(username, password)
			if err != nil {
				log.Fatalf("Failed to get devices: %v", err)
			}
			log.Printf("Got %d devices", len(devices))
			time.Sleep(interval)
		}
	}()
	return func(w http.ResponseWriter, r *http.Request) {
		cmd := r.URL.Query().Get("cmd")
		ip := r.URL.Query().Get("ip")
		var (
			status = http.StatusOK
			msg    string
		)
		if ip == "" && (cmd == "status" || cmd == "on" || cmd == "off") {
			status = http.StatusBadRequest
			msg = "Missing IP address"
		} else {
			switch cmd {
			case "status":
				msg = "Status: not implemented yet"
			case "on":
				msg = "On: not implemented yet"
			case "off":
				msg = "Off: not implemented yet"
			case "", "list":
				status = http.StatusOK
				msg = getListHTML(devices)
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

func getAllDevices(username, password string) ([]*tapo.DeviceInfo, error) {
	client := tapo.NewClient(nil)
	discovered, _, err := client.Discover()
	if err != nil {
		return nil, fmt.Errorf("discover failed: %w", err)
	}
	var (
		unsorted = make(map[string]*tapo.DeviceInfo)
		devices  []*tapo.DeviceInfo
		keys     []string
	)
	for _, d := range discovered {
		addr, ok := netip.AddrFromSlice(net.IP(d.Result.IP).To4())
		if !ok {
			return nil, fmt.Errorf("invalid IP '%s': %w", d.Result.IP.String(), err)
		}
		log.Printf("Getting info for '%s'", addr)
		plug := tapo.NewPlug(addr, nil)
		if err := plug.Handshake(username, password); err != nil {
			return nil, fmt.Errorf("handshake failed for %s: %w", addr, err)
		}
		info, err := plug.GetDeviceInfo()
		if err != nil {
			return nil, fmt.Errorf("device info failed for %s: %w", d.Result.IP.String(), err)
		}
		unsorted[info.DecodedNickname] = info
		keys = append(keys, info.DecodedNickname)
	}
	sort.Strings(keys)
	for _, k := range keys {
		devices = append(devices, unsorted[k])
	}
	return devices, nil
}

func main() {
	pflag.Parse()

	http.HandleFunc("/", getRootHandler(*flagUsername, *flagPassword, *flagInterval))
	log.Printf("Listening on %s", *flagListen)
	if err := http.ListenAndServe(*flagListen, nil); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}
