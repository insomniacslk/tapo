package main

// This program runs a small web server showing a list of Tapo devices. It must
// run in the same collision domain as the Tapo devices, since the discovery is
// done via broadcast UDP.

import (
	_ "embed"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/insomniacslk/tapo"
	"github.com/spf13/pflag"
)

//go:embed on.png
var onIcon []byte

//go:embed off.png
var offIcon []byte

//go:embed warning.png
var warningIcon []byte

var (
	flagListen   = pflag.StringP("listen", "l", ":7490", "Listen host:port address")
	flagUsername = pflag.StringP("username", "u", "", "TP-Link username (usually an email)")
	flagPassword = pflag.StringP("password", "p", "", "TP-Link password")
	flagInterval = pflag.DurationP("interval", "i", time.Minute, "Update interval")
)

func getListHTML(devices []Device) string {
	allIPs := make([]string, 0, len(devices))
	for _, d := range devices {
		allIPs = append(allIPs, `"`+d.info.IP+`"`)
	}
	ret := fmt.Sprintf(`<!DOCTYPE html>
<html>
 <head>
  <title>Tapo plugs</title>
  <style>
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
           if (xmlhttp.status == 200) {
               img = document.getElementById(tagID);
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
               alert('failed to turn plug on, got HTTP ' + xmlhttp.status);
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
	ret += "  <table>\n"
	ret += "   <thead><tr><td class=\"text.bold\">Name</td><td class=\"text.bold\">IP</td><td class=\"text.bold\">MAC</td><td class=\"text.bold\">State</td><td class=\"text.bold\">ID</td></tr></thead>\n"
	for _, d := range devices {
		ret += "   <tr>\n"
		ret += "    <td class=\"text-bold\">" + d.info.DecodedNickname + "</em></td>\n"
		ret += "    <td>" + d.info.IP + "</td>\n"
		ret += "    <td>" + d.info.MAC + "</td>\n"
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
		ret += "    <td>" + d.info.DeviceID + "</td>\n"
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

func getRootHandler(username, password string, interval time.Duration) func(http.ResponseWriter, *http.Request) {
	var (
		devices []Device
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
				// RACE CONDITIONS AHEAD!
				found := false
				for _, d := range devices {
					if d.info.IP == ip {
						found = true
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
					}
				}
				if !found {
					status = http.StatusNotFound
					msg = "404 Not Found"
				}
			case "on":
				// RACE CONDITIONS AHEAD!
				found := false
				for _, d := range devices {
					if d.info.IP == ip {
						found = true
						if err := d.plug.SetDeviceInfo(true); err != nil {
							status = http.StatusInternalServerError
							msg = fmt.Sprintf("failed to turn plug on: %v", err)
							break
						}
					}
				}
				if !found {
					status = http.StatusNotFound
					msg = "404 Not Found"
				}
			case "off":
				// RACE CONDITIONS AHEAD!
				found := false
				for _, d := range devices {
					if d.info.IP == ip {
						found = true
						if err := d.plug.SetDeviceInfo(false); err != nil {
							status = http.StatusInternalServerError
							msg = fmt.Sprintf("failed to turn plug off: %v", err)
							break
						}
					}
				}
				if !found {
					status = http.StatusNotFound
					msg = "404 Not Found"
				}
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

type Device struct {
	plug *tapo.Plug
	info *tapo.DeviceInfo
}

func getAllDevices(username, password string) ([]Device, error) {
	client := tapo.NewClient(nil)
	discovered, _, err := client.Discover()
	if err != nil {
		return nil, fmt.Errorf("discover failed: %w", err)
	}
	var (
		unsorted = make(map[string]Device)
		devices  []Device
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
		unsorted[info.DecodedNickname] = Device{plug: plug, info: info}
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
	http.HandleFunc("/icons/on.png", getIconOn)
	http.HandleFunc("/icons/off.png", getIconOff)
	http.HandleFunc("/icons/warning.png", getIconWarning)
	log.Printf("Listening on %s", *flagListen)
	if err := http.ListenAndServe(*flagListen, nil); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}
