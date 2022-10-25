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

func main() {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s <flags> [command]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "command is one of on, off, info, energy\n")
		fmt.Fprintf(os.Stderr, "\n")
	}
	pflag.Parse()
	cmd := pflag.Arg(0)
	ip, err := netip.ParseAddr(*flagAddr)
	if err != nil {
		log.Fatalf("Failed to parse IP address: %v", err)
	}

	var logger *log.Logger
	if *flagDebug {
		logger = log.New(os.Stderr, "[tapo] ", log.Ltime|log.Lshortfile)
	}
	p100 := tapo.NewPlug(ip, *flagEmail, *flagPassword, logger)
	if err := p100.Login(*flagEmail, *flagPassword); err != nil {
		log.Fatalf("Login failed: %v", err)
	}
	info, err := p100.GetDeviceInfo()
	if err != nil {
		log.Fatalf("Failed to get device info: %v", err)
	}
	printDeviceInfo(info)

	dUsage, err := p100.GetDeviceUsage()
	if err != nil {
		log.Fatalf("Failed to get device usage: %v", err)
	}
	printDeviceUsage(dUsage)

	eUsage, err := p100.GetEnergyUsage()
	if err != nil {
		log.Fatalf("Failed to get energy usage: %v", err)
	}
	printEnergyUsage(eUsage)

	switch strings.ToLower(cmd) {
	case "on":
		err = p100.SetDeviceInfo(true)
	case "off":
		err = p100.SetDeviceInfo(false)
	case "info", "energy", "":
		// no command
		err = nil
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
