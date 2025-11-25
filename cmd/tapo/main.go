// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/insomniacslk/tapo"
	"github.com/kirsle/configdir"
	"github.com/spf13/pflag"
)

const progname = "tapo"

var defaultConfigFile = path.Join(configdir.LocalConfig(progname), "config.json")

var (
	flagConfigFile = pflag.StringP("config", "c", defaultConfigFile, "Configuration file")
	flagAddr       = pflag.IPP("addr", "a", nil, "IP address of the Tapo device")
	flagName       = pflag.StringP("name", "n", "", "Name of the Tapo device. This is slow, it will perform a local discovery first. Ignored if --addr is specified")
	flagEmail      = pflag.StringP("email", "e", "", "E-mail for login")
	flagPassword   = pflag.StringP("password", "p", "", "Password for login")
	flagDebug      = pflag.BoolP("debug", "d", false, "Enable debug logs")
	flagFormat     = pflag.StringP("format", "f", "{{.Idx}}) name={{.Name}} ip={{.IP}} mac={{.MAC}} type={{.Type}} model={{.Model}} deviceid={{.ID}}\n", "Template for printing each line of a discovered device, works with `list`, `discover` and `cloud-list`, fields may differ across commands. It uses Go's text/template syntax")
)

func loadConfig(configFile string) (*cmdCfg, error) {
	var cfg cmdCfg
	// apply overrides at the end of this function
	defer func() {
		if pflag.CommandLine.Changed("email") {
			cfg.Email = *flagEmail
		}
		if pflag.CommandLine.Changed("password") {
			cfg.Password = *flagPassword
		}
		if pflag.CommandLine.Changed("debug") {
			cfg.Debug = *flagDebug
		}
	}()
	configPath := filepath.Dir(configFile)
	if configPath == "" {
		return nil, fmt.Errorf("missing/empty configuration directory")
	}
	err := configdir.MakePath(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("Configuration file does not exist, using defaults")
			return &cfg, nil
		}
		return nil, fmt.Errorf("failed to create config path '%s': %w", configPath, err)
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &cfg, nil
		}
		return nil, fmt.Errorf("failed to open '%s': %w", configFile, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config file: %w", err)
	}
	return &cfg, nil
}

func ipByName(cfg *cmdCfg, name string) (net.IP, error) {
	devices, err := discoverDevices(cfg.logger)
	if err != nil {
		return nil, fmt.Errorf("discovery failed: %w", err)
	}
	for _, dev := range devices {
		plug, err := getPlug(cfg, dev.Result.IP.String())
		if err != nil {
			log.Printf("Warning: skipping plug '%s': %v\n", dev.Result.IP.String(), err)
			continue
		}
		info, err := plug.GetDeviceInfo()
		if err != nil {
			log.Printf("Warning: skipping plug '%s': %v", dev.Result.IP.String(), err)
			continue
		}
		if info.DecodedNickname == name {
			return net.IP(dev.Result.IP), nil
		}
	}
	return nil, nil
}

func getPlug(cfg *cmdCfg, addr string) (*tapo.Plug, error) {
	if addr == "" {
		return nil, fmt.Errorf("no address specified")
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse IP address: %w", err)
	}

	plug := tapo.NewPlug(ip, cfg.logger)
	if err := plug.Handshake(cfg.Email, cfg.Password); err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}
	return plug, nil
}

type cmdCfg struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	logger   *log.Logger
	Debug    bool `json:"debug"`
}

func cmdOn(cfg *cmdCfg, ip net.IP) error {
	plug, err := getPlug(cfg, ip.String())
	if err != nil {
		return err
	}
	return plug.SetDeviceInfo(true)
}

func cmdOff(cfg *cmdCfg, ip net.IP) error {
	plug, err := getPlug(cfg, ip.String())
	if err != nil {
		return err
	}
	return plug.SetDeviceInfo(false)
}

func cmdInfo(cfg *cmdCfg, ip net.IP) error {
	plug, err := getPlug(cfg, ip.String())
	if err != nil {
		return err
	}
	info, err := plug.GetDeviceInfo()
	if err != nil {
		return fmt.Errorf("failed to get device info: %w", err)
	}
	printDeviceInfo(info)

	dUsage, err := plug.GetDeviceUsage()
	if err != nil {
		return fmt.Errorf("failed to get device usage: %w", err)
	}
	printDeviceUsage(dUsage)

	if !info.SupportsEnergyMonitoring() {
		return nil
	}
	eUsage, err := plug.GetEnergyUsage()
	if err != nil {
		return fmt.Errorf("failed to get energy usage: %w", err)
	}
	printEnergyUsage(eUsage)
	return nil
}

type formatObj struct {
	Idx       int
	IP        string
	MAC       string
	Type      string
	Model     string
	ID        string
	Name      string
	FwVersion string
	HwVersion string
}

func cmdCloudList(cfg *cmdCfg) error {
	tmpl, err := template.New("cloud-list").Parse(strings.Replace(*flagFormat, "\\n", "\n", -1))
	if err != nil {
		return fmt.Errorf("invalid template string: %w", err)
	}
	client := tapo.NewClient(cfg.logger)
	if err := client.CloudLogin(cfg.Email, cfg.Password); err != nil {
		return err
	}
	devices, err := client.CloudList()
	if err != nil {
		return err
	}
	for idx, dev := range devices {
		o := formatObj{
			Idx:       idx,
			IP:        "unknown",
			MAC:       dev.DeviceMAC.String(),
			Type:      dev.DeviceType,
			Model:     dev.DeviceModel,
			ID:        dev.DeviceID,
			Name:      dev.DecodedAlias,
			FwVersion: dev.FwVer,
			HwVersion: dev.DeviceHwVer,
		}
		if err := tmpl.Execute(os.Stdout, o); err != nil {
			return fmt.Errorf("template execution failed: %w", err)
		}
		if cfg.Debug {
			fmt.Printf("    %+v\n", dev)
		}
	}
	return nil
}

func discoverDevices(logger *log.Logger) (map[string]tapo.DiscoverResponse, error) {
	client := tapo.NewClient(logger)
	devices, _, err := client.Discover()
	return devices, err
}

// cmdList prints a list of all the locally-reachable devices. It runs a
// discovery first, then it calls the info API on each device.
func cmdList(cfg *cmdCfg) error {
	devices, err := discoverDevices(cfg.logger)
	if err != nil {
		return fmt.Errorf("discovery failed: %w", err)
	}
	tmpl, err := template.New("list").Parse(strings.Replace(*flagFormat, "\\n", "\n", -1))
	if err != nil {
		return fmt.Errorf("invalid template string: %w", err)
	}
	idx := 0
	for _, dev := range devices {
		idx++
		// TODO specify plug parameters from device.Result.MgtEncryptSchm
		plug, err := getPlug(cfg, dev.Result.IP.String())
		if err != nil {
			log.Printf("Warning: skipping plug '%s': %v\n", dev.Result.IP.String(), err)
			continue
		}
		info, err := plug.GetDeviceInfo()
		if err != nil {
			log.Printf("Warning: skipping plug '%s': %v", dev.Result.IP.String(), err)
			continue
		}
		o := formatObj{
			Idx:       idx,
			IP:        dev.Result.IP.String(),
			MAC:       dev.Result.MAC.String(),
			Type:      dev.Result.DeviceType,
			Model:     dev.Result.DeviceModel,
			ID:        dev.Result.DeviceID,
			Name:      info.DecodedNickname,
			FwVersion: info.FWVersion,
			HwVersion: info.HWVersion,
		}
		if err := tmpl.Execute(os.Stdout, o); err != nil {
			return fmt.Errorf("template execution failed: %w", err)
		}
		if cfg.Debug {
			fmt.Printf("    %+v\n", dev)
		}
	}
	return nil
}

func cmdDiscover(cfg *cmdCfg) error {
	client := tapo.NewClient(cfg.logger)
	devices, failed, err := client.Discover()
	if err != nil {
		return err
	}
	fmt.Printf("Found %d devices and %d errors\n", len(devices), len(failed))
	idx := 0
	tmpl, err := template.New("discover").Parse(strings.Replace(*flagFormat, "\\n", "\n", -1))
	if err != nil {
		return fmt.Errorf("invalid template string: %w", err)
	}
	for _, dev := range devices {
		idx++
		o := formatObj{
			Idx:   idx,
			IP:    dev.Result.IP.String(),
			MAC:   dev.Result.MAC.String(),
			Type:  dev.Result.DeviceType,
			Model: dev.Result.DeviceModel,
			ID:    dev.Result.DeviceID,
		}
		if err := tmpl.Execute(os.Stdout, o); err != nil {
			return fmt.Errorf("template execution failed: %w", err)
		}
		if cfg.Debug {
			fmt.Printf("    %+v\n", dev)
		}
	}
	return nil
}

func getIPFromIPOrName(cfg *cmdCfg, ip net.IP, name string) (net.IP, error) {
	if ip != nil {
		return ip, nil
	}
	if name != "" {
		a, err := ipByName(cfg, *flagName)
		if err != nil {
			return nil, err
		}
		if a == nil {
			return nil, fmt.Errorf("unknown device name")
		}
		return a, nil
	}
	return nil, fmt.Errorf("no device name nor IP address specified")
}

func main() {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s <flags> [command]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "command is one of on, off, info, energy, cloud-list, list, discover (local broadcast)\n")
		fmt.Fprintf(os.Stderr, "\n")
		pflag.PrintDefaults()
	}
	pflag.Parse()
	cmd := pflag.Arg(0)

	cfg, err := loadConfig(*flagConfigFile)
	if err != nil {
		log.Fatalf("Failed to load config file: %v", err)
	}

	var logger *log.Logger
	if cfg.Debug {
		logger = log.New(os.Stderr, "[tapo] ", log.Ltime|log.Lshortfile)
	}

	cfg.logger = logger
	var ip net.IP
	switch strings.ToLower(cmd) {
	case "on":
		ip, err = getIPFromIPOrName(cfg, *flagAddr, *flagName)
		if err != nil {
			break
		}
		err = cmdOn(cfg, ip)
	case "off":
		ip, err = getIPFromIPOrName(cfg, *flagAddr, *flagName)
		if err != nil {
			break
		}
		err = cmdOff(cfg, ip)
	case "info", "energy":
		ip, err = getIPFromIPOrName(cfg, *flagAddr, *flagName)
		if err != nil {
			break
		}
		err = cmdInfo(cfg, ip)
	case "cloud-list":
		err = cmdCloudList(cfg)
	case "list":
		err = cmdList(cfg)
	case "discover":
		err = cmdDiscover(cfg)
	case "":
		log.Fatalf("No command specified")
	default:
		log.Fatalf("Unknown command '%s'", cmd)
	}
	if err != nil {
		log.Fatalf("Failed to execute command '%s': %v", cmd, err)
	}

}

func printDeviceInfo(i *tapo.DeviceInfo) {
	fmt.Printf("Info:\n")
	fmt.Printf("Device ID               : %s\n", i.DeviceID)
	fmt.Printf("FW version              : %s\n", i.FWVersion)
	fmt.Printf("HW version              : %s\n", i.HWVersion)
	fmt.Printf("Type                    : %s\n", i.Type)
	fmt.Printf("Model                   : %s\n", i.Model)
	fmt.Printf("MAC                     : %s\n", i.MAC)
	fmt.Printf("HW ID                   : %s\n", i.HWID)
	fmt.Printf("FW ID                   : %s\n", i.FWID)
	fmt.Printf("OEM ID                  : %s\n", i.OEMID)
	fmt.Printf("IP                      : %s\n", i.IP)
	fmt.Printf("Time Diff               : %d\n", i.TimeDiff)
	// TODO check if DecodedSSID is printable
	fmt.Printf("SSID                    : %s (decoded: %s)\n", i.SSID, i.DecodedSSID)
	fmt.Printf("RSSI                    : %d\n", i.RSSI)
	fmt.Printf("SignalLevel             : %d\n", i.SignalLevel)
	fmt.Printf("Latitude                : %d\n", i.Latitude)
	fmt.Printf("Longitude               : %d\n", i.Longitude)
	fmt.Printf("Lang                    : %s\n", i.Lang)
	fmt.Printf("Avatar                  : %s\n", i.Avatar)
	fmt.Printf("Region                  : %s\n", i.Region)
	fmt.Printf("Specs                   : %s\n", i.Specs)
	// TODO check if DecodedNickname is printable
	fmt.Printf("Nickname                : %s (decoded: %s)\n", i.Nickname, i.DecodedNickname)
	fmt.Printf("Has Set Location Info   : %v\n", i.HasSetLocationInfo)
	fmt.Printf("Device ON               : %v\n", i.DeviceON)
	fmt.Printf("ON time                 : %d\n", i.OnTime)
	fmt.Printf("Default states\n")
	fmt.Printf("  Type                  : %s\n", i.DefaultStates.Type)
	if i.DefaultStates.State != nil {
		fmt.Printf("  State                 : %s\n", string(*i.DefaultStates.State))
	} else {
		fmt.Printf("  State                 : <unset>\n")
	}
	fmt.Printf("Overheated              : %v\n", i.OverHeated)
	fmt.Printf("Power Protection Status : %s\n", i.PowerProtectionStatus)
	fmt.Printf("Location                : %s\n", i.Location)
	fmt.Printf("\n")
}

func printDeviceUsage(u *tapo.DeviceUsage) {
	fmt.Printf("Time usage:\n")
	fmt.Printf("  Today                 : %d minutes\n", u.TimeUsage.Today)
	fmt.Printf("  Past 7 days           : %d minutes\n", u.TimeUsage.Past7)
	fmt.Printf("  Past 30 days          : %d minutes\n", u.TimeUsage.Past30)
	fmt.Printf("\n")
	fmt.Printf("Power usage:\n")
	fmt.Printf("  Today                 : %d kWh\n", u.PowerUsage.Today)
	fmt.Printf("  Past 7 days           : %d kWh\n", u.PowerUsage.Past7)
	fmt.Printf("  Past 30 days          : %d kWh\n", u.PowerUsage.Past30)
	fmt.Printf("\n")
	fmt.Printf("Saved power:\n")
	fmt.Printf("  Today                 : %d kWh\n", u.SavedPower.Today)
	fmt.Printf("  Past 7 days           : %d kWh\n", u.SavedPower.Past7)
	fmt.Printf("  Past 30 days          : %d kWh\n", u.SavedPower.Past30)
	fmt.Printf("\n")
}

func printEnergyUsage(u *tapo.EnergyUsage) {
	fmt.Printf("Energy usage:\n")
	fmt.Printf("  Today runtime         : %d\n", u.TodayRuntime)
	fmt.Printf("  Month runtime         : %d\n", u.MonthRuntime)
	fmt.Printf("  Today energy          : %d\n", u.TodayEnergy)
	fmt.Printf("  Month energy          : %d\n", u.MonthEnergy)
	fmt.Printf("  Local time            : %s\n", u.LocalTime)
	fmt.Printf("  Electricity charge    : %v\n", u.ElectricityCharge)
	fmt.Printf("  Current power         : %d\n", u.CurrentPower)
	fmt.Printf("\n")
}
