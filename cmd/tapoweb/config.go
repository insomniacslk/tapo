// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/insomniacslk/xjson"
	"github.com/kirsle/configdir"
	"github.com/spf13/pflag"
)

const progname = "tapoweb"

// envPrefix is TAPO_ rather than TAPOWEB_ on purpose: what these variables
// mostly carry is the TP-Link account, which is the same account cmd/tapo uses,
// so a shell that has exported it once can run either program.
const envPrefix = "TAPO_"

var defaultConfigFile = path.Join(configdir.LocalConfig(progname), "config.json")

// Config is the resolved configuration.
//
// Every setting can come from four places, and the later ones win:
//
//  1. the defaults in defaultConfig(),
//  2. the JSON config file (--config, $TAPO_CONFIG),
//  3. the environment ($TAPO_LISTEN, $TAPO_USERNAME, ...),
//  4. command line flags.
//
// There is one name per setting rather than three: the JSON key is the flag
// name with dashes turned into underscores, and the environment variable is
// TAPO_ plus the same name upper-cased. So --password-file is "password_file"
// in the config file and $TAPO_PASSWORD_FILE in the environment.
//
// A key that is absent from the config file leaves the default in place; a key
// present with a zero value ("", false, 0) is indistinguishable from an absent
// one, which is the usual encoding/json limitation and is not worth a pointer
// field for any of these settings.
type Config struct {
	Listen       string         `json:"listen"`
	Username     string         `json:"username"`
	Password     string         `json:"password"`
	PasswordFile string         `json:"password_file"`
	Interval     xjson.Duration `json:"interval"`
	ShowID       bool           `json:"show_id"`
}

func defaultConfig() Config {
	return Config{
		Listen:   ":7490",
		Interval: xjson.Duration(time.Minute),
	}
}

// envName returns the environment variable that corresponds to a flag name, so
// that the two can never drift: --password-file is $TAPO_PASSWORD_FILE.
func envName(flagName string) string {
	return envPrefix + strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
}

// resolveConfig walks the four sources in precedence order and returns the
// result. flags must already be parsed, since it is asked which flags the user
// actually set.
func resolveConfig(flags *pflag.FlagSet) (*Config, error) {
	cfg := defaultConfig()

	// The config file's own path obviously cannot come from the config file, so
	// it is resolved first and from one level fewer.
	configFile, err := flags.GetString("config")
	if err != nil {
		return nil, err
	}
	explicit := flags.Changed("config")
	if !explicit {
		if v, ok := os.LookupEnv(envName("config")); ok {
			configFile, explicit = v, true
		}
	}

	if err := applyConfigFile(configFile, explicit, &cfg); err != nil {
		return nil, err
	}
	if err := applyEnv(&cfg); err != nil {
		return nil, err
	}
	if err := applyFlags(flags, &cfg); err != nil {
		return nil, err
	}
	if err := resolvePassword(&cfg); err != nil {
		return nil, err
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf(
			"no TP-Link credentials: set --username/--password, $%s/$%s, or \"username\"/\"password\" in %s",
			envName("username"), envName("password"), configFile,
		)
	}
	return &cfg, nil
}

// applyConfigFile unmarshals the config file over cfg, leaving every field the
// file does not mention alone.
//
// A missing file is an error only when the path was ASKED FOR. Falling back to
// the per-user default and finding nothing there is the normal case for a
// process configured entirely from flags or the environment — a container, say
// — while a --config or $TAPO_CONFIG pointing at nothing is a typo, and
// starting up with silently different settings is the worst answer to it.
func applyConfigFile(configFile string, explicit bool, cfg *Config) error {
	data, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return nil
		}
		return fmt.Errorf("failed to read config file: %w", err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("failed to parse config file '%s': %w", configFile, err)
	}
	return nil
}

func applyEnv(cfg *Config) error {
	if v, ok := os.LookupEnv(envName("listen")); ok {
		cfg.Listen = v
	}
	if v, ok := os.LookupEnv(envName("username")); ok {
		cfg.Username = v
	}
	if v, ok := os.LookupEnv(envName("password")); ok {
		cfg.Password = v
	}
	if v, ok := os.LookupEnv(envName("password-file")); ok {
		cfg.PasswordFile = v
	}
	if v, ok := os.LookupEnv(envName("interval")); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid $%s '%s': %w", envName("interval"), v, err)
		}
		cfg.Interval = xjson.Duration(d)
	}
	if v, ok := os.LookupEnv(envName("show-id")); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid $%s '%s': %w", envName("show-id"), v, err)
		}
		cfg.ShowID = b
	}
	return nil
}

func applyFlags(flags *pflag.FlagSet, cfg *Config) error {
	var err error
	get := func(name string, apply func()) {
		if err != nil || !flags.Changed(name) {
			return
		}
		apply()
	}
	get("listen", func() { cfg.Listen, err = flags.GetString("listen") })
	get("username", func() { cfg.Username, err = flags.GetString("username") })
	get("password", func() { cfg.Password, err = flags.GetString("password") })
	get("password-file", func() { cfg.PasswordFile, err = flags.GetString("password-file") })
	get("interval", func() {
		var d time.Duration
		d, err = flags.GetDuration("interval")
		cfg.Interval = xjson.Duration(d)
	})
	get("show-id", func() { cfg.ShowID, err = flags.GetBool("show-id") })
	return err
}

// resolvePassword turns --password-file into the password itself.
//
// The two are alternative spellings of one setting, so setting both is refused
// rather than resolved by an invented precedence: whichever way it were
// decided, half the people who hit it would have meant the other one.
func resolvePassword(cfg *Config) error {
	if cfg.PasswordFile == "" {
		return nil
	}
	if cfg.Password != "" {
		return fmt.Errorf("password and password-file are both set: use one or the other")
	}
	data, err := os.ReadFile(cfg.PasswordFile)
	if err != nil {
		return fmt.Errorf("failed to read password file: %w", err)
	}
	// Only the line ending is stripped. A trailing space is more likely to be
	// part of the password than to be a mistake, while a trailing newline is
	// what every editor and `echo` puts there.
	cfg.Password = strings.TrimRight(string(data), "\r\n")
	if cfg.Password == "" {
		return fmt.Errorf("password file '%s' is empty", cfg.PasswordFile)
	}
	return nil
}
