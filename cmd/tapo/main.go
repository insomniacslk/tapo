package main

import (
	"fmt"
	"log"
	"net/netip"
	"os"
	"strings"

	"github.com/insomniacslk/tapo"
	"github.com/spf13/pflag"
)

var (
	flagAddr     = pflag.StringP("addr", "a", "", "IP address of the Tapo device")
	flagEmail    = pflag.StringP("email", "e", "", "E-mail for login")
	flagPassword = pflag.StringP("password", "p", "", "Password for login")
	flagDebug    = pflag.BoolP("debug", "d", false, "Enable debug logs")
)

func getPlug(addr, email, password string, logger *log.Logger) (*tapo.Plug, error) {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse IP address: %w", err)
	}
	plug := tapo.NewPlug(ip, logger)
	if err := plug.Login(*flagEmail, *flagPassword); err != nil {
		log.Fatalf("Login failed: %v", err)
	}
	return plug, nil
}

type cmdCfg struct {
	email    string
	password string
	logger   *log.Logger
}

func cmdOn(cfg cmdCfg, addr string) error {
	plug, err := getPlug(addr, cfg.email, cfg.password, cfg.logger)
	if err != nil {
		return err
	}
	return plug.SetDeviceInfo(true)
}

func cmdOff(cfg cmdCfg, addr string) error {
	plug, err := getPlug(addr, cfg.email, cfg.password, cfg.logger)
	if err != nil {
		return err
	}
	return plug.SetDeviceInfo(false)
}

func cmdInfo(cfg cmdCfg, addr string) error {
	plug, err := getPlug(addr, cfg.email, cfg.password, cfg.logger)
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

func cmdList(cfg cmdCfg) error {
	client := tapo.NewClient(cfg.logger)
	if err := client.Login(cfg.email, cfg.password); err != nil {
		return err
	}
	devices, err := client.List()
	if err != nil {
		return err
	}
	for idx, d := range devices {
		fmt.Printf("  %d) %s\n    model:%s, fw:%s, hw:%s, mac:%s\n", idx+1, d.DecodedAlias, d.DeviceModel, d.FwVer, d.DeviceHwVer, d.DeviceMAC)
	}
	return nil
}

func main() {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s <flags> [command]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "command is one of on, off, info, energy, list\n")
		fmt.Fprintf(os.Stderr, "\n")
		pflag.PrintDefaults()
	}
	pflag.Parse()
	cmd := pflag.Arg(0)

	var logger *log.Logger
	if *flagDebug {
		logger = log.New(os.Stderr, "[tapo] ", log.Ltime|log.Lshortfile)
	}

	var err error
	cfg := cmdCfg{
		email:    *flagEmail,
		password: *flagPassword,
		logger:   logger,
	}
	switch strings.ToLower(cmd) {
	case "on":
		err = cmdOn(cfg, *flagAddr)
	case "off":
		err = cmdOff(cfg, *flagAddr)
	case "info", "energy", "":
		err = cmdInfo(cfg, *flagAddr)
	case "list":
		err = cmdList(cfg)
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
