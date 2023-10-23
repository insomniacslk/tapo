package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	flagAddr       = pflag.StringP("addr", "a", "", "IP address of the Tapo device")
	flagEmail      = pflag.StringP("email", "e", "", "E-mail for login")
	flagPassword   = pflag.StringP("password", "p", "", "Password for login")
	flagDebug      = pflag.BoolP("debug", "d", false, "Enable debug logs")
	flagFormat     = pflag.StringP("discover-format", "f", "{{.Idx}}) ip={{.IP}} mac={{.MAC}} type={{.Type}} model={{.Model}} deviceid={{.ID}}\n", "Template for printing each line of a discovered device. It uses Go's text/template syntax")
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

func getPlug(cfg *cmdCfg, addr string) (*tapo.Plug, error) {
	if addr == "" {
		return nil, fmt.Errorf("no address specified")
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse IP address: %w", err)
	}
	plug := tapo.NewPlug(ip, cfg.logger)
	if err := plug.Login(cfg.Email, cfg.Password); err != nil {
		var te tapo.TapoError
		if errors.As(err, &te) {
			// if the device is running a firmware with the new KLAP protocol,
			// print a more specific error.
			if te == 1003 {
				return nil, fmt.Errorf("login failed: %w. KLAP protocol not implemented yet, see https://github.com/insomniacslk/tapo/issues/1", err)
			}
		}
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

func cmdOn(cfg *cmdCfg, addr string) error {
	plug, err := getPlug(cfg, addr)
	if err != nil {
		return err
	}
	return plug.SetDeviceInfo(true)
}

func cmdOff(cfg *cmdCfg, addr string) error {
	plug, err := getPlug(cfg, addr)
	if err != nil {
		return err
	}
	return plug.SetDeviceInfo(false)
}

func cmdInfo(cfg *cmdCfg, addr string) error {
	plug, err := getPlug(cfg, addr)
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

	eUsage, err := plug.GetEnergyUsage()
	if err != nil {
		return fmt.Errorf("failed to get energy usage: %w", err)
	}
	printEnergyUsage(eUsage)
	return nil
}

func cmdCloudList(cfg *cmdCfg) error {
	client := tapo.NewClient(cfg.logger)
	if err := client.CloudLogin(cfg.Email, cfg.Password); err != nil {
		return err
	}
	devices, err := client.CloudList()
	if err != nil {
		return err
	}
	for idx, d := range devices {
		fmt.Printf("  %d) %s\n    model:%s, fw:%s, hw:%s, mac:%s\n", idx+1, d.DecodedAlias, d.DeviceModel, d.FwVer, d.DeviceHwVer, d.DeviceMAC)
		if cfg.Debug {
			fmt.Printf("    %+v\n", d)
		}
	}
	return nil
}

// cmdList prints a list of all the locally-reachable devices. It runs a
// discovery first, then it calls the info API on each device.
func cmdList(cfg *cmdCfg) error {
	client := tapo.NewClient(cfg.logger)
	devices, _, err := client.Discover()
	if err != nil {
		return fmt.Errorf("Discovery failed: %w", err)
	}
	idx := 0
	for _, device := range devices {
		idx++
		plug, err := getPlug(cfg, device.Result.IP.String())
		if err != nil {
			log.Printf("Warning: skipping plug '%s': %v\n", device.Result.IP.String(), err)
			continue
		}
		info, err := plug.GetDeviceInfo()
		if err != nil {
			log.Printf("Warning: skipping plug '%s': %v", device.Result.IP.String(), err)
			continue
		}
		fmt.Printf("%d) name=%s ip=%s mac=%s type=%s model=%s hw=%s fw=%s\n", idx, info.DecodedNickname, device.Result.IP, device.Result.MAC, device.Result.DeviceType, device.Result.DeviceModel, info.HWVersion, info.FWVersion)
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
	idx := 1
	type obj struct {
		Idx   int
		IP    string
		MAC   string
		Type  string
		Model string
		ID    string
	}
	tmpl, err := template.New("test").Parse(strings.Replace(*flagFormat, "\\n", "\n", -1))
	if err != nil {
		return fmt.Errorf("invalid template string: %w", err)
	}
	for _, dev := range devices {
		o := obj{
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
		idx++
	}
	return nil
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
	switch strings.ToLower(cmd) {
	case "on":
		err = cmdOn(cfg, *flagAddr)
	case "off":
		err = cmdOff(cfg, *flagAddr)
	case "info", "energy":
		err = cmdInfo(cfg, *flagAddr)
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
	fmt.Printf("  State                 : %s\n", string(*i.DefaultStates.State))
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
