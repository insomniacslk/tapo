// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

// newTestFlags builds the root command's flag set and parses args against it,
// so the tests exercise the same flags the program actually has rather than a
// copy that can drift from them.
func newTestFlags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	cmd := newRootCommand()
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("failed to parse %v: %v", args, err)
	}
	return cmd.Flags()
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestPrecedence is the whole point of this file: the four sources have to be
// consulted in the documented order, and each one has to be able to win over
// every source below it without disturbing the settings it does not mention.
func TestPrecedence(t *testing.T) {
	cfgFile := writeConfig(t, `{
	  "listen": ":1111",
	  "username": "from-file",
	  "password": "from-file",
	  "interval": "10s"
	}`)

	t.Run("defaults only", func(t *testing.T) {
		// No config file, no environment. Credentials are mandatory, so the
		// defaults alone must be refused rather than silently started with.
		flags := newTestFlags(t, "--config", filepath.Join(t.TempDir(), "absent.json"))
		if _, err := resolveConfig(flags); err == nil {
			t.Fatal("expected an error with no credentials anywhere, got none")
		}
	})

	t.Run("file over defaults", func(t *testing.T) {
		flags := newTestFlags(t, "--config", cfgFile)
		cfg, err := resolveConfig(flags)
		if err != nil {
			t.Fatal(err)
		}
		assertEqual(t, "listen", cfg.Listen, ":1111")
		assertEqual(t, "username", cfg.Username, "from-file")
		assertEqual(t, "interval", time.Duration(cfg.Interval), 10*time.Second)
		// show_id is not in the file, so the default survives.
		assertEqual(t, "show-id", cfg.ShowID, false)
	})

	t.Run("env over file", func(t *testing.T) {
		t.Setenv("TAPO_USERNAME", "from-env")
		t.Setenv("TAPO_INTERVAL", "20s")
		t.Setenv("TAPO_SHOW_ID", "true")
		flags := newTestFlags(t, "--config", cfgFile)
		cfg, err := resolveConfig(flags)
		if err != nil {
			t.Fatal(err)
		}
		assertEqual(t, "username", cfg.Username, "from-env")
		assertEqual(t, "interval", time.Duration(cfg.Interval), 20*time.Second)
		assertEqual(t, "show-id", cfg.ShowID, true)
		// Untouched by the environment, so the file still wins over the default.
		assertEqual(t, "listen", cfg.Listen, ":1111")
		assertEqual(t, "password", cfg.Password, "from-file")
	})

	t.Run("flags over env", func(t *testing.T) {
		t.Setenv("TAPO_USERNAME", "from-env")
		t.Setenv("TAPO_LISTEN", ":2222")
		flags := newTestFlags(t, "--config", cfgFile, "--username", "from-flag")
		cfg, err := resolveConfig(flags)
		if err != nil {
			t.Fatal(err)
		}
		assertEqual(t, "username", cfg.Username, "from-flag")
		// Not passed as a flag, so the environment still wins over the file.
		assertEqual(t, "listen", cfg.Listen, ":2222")
	})

	t.Run("a flag set to its default value still wins", func(t *testing.T) {
		// The regression this guards: reading flags by value rather than by
		// Changed() makes `--listen :7490` indistinguishable from not passing
		// it at all, so the config file would quietly win.
		t.Setenv("TAPO_LISTEN", ":2222")
		flags := newTestFlags(t, "--config", cfgFile, "--listen", ":7490")
		cfg, err := resolveConfig(flags)
		if err != nil {
			t.Fatal(err)
		}
		assertEqual(t, "listen", cfg.Listen, ":7490")
	})
}

func TestConfigFilePath(t *testing.T) {
	t.Setenv("TAPO_USERNAME", "u")
	t.Setenv("TAPO_PASSWORD", "p")

	t.Run("absent default path is not an error", func(t *testing.T) {
		// The container case: everything comes from the environment and there
		// is no config file anywhere.
		cmd := newRootCommand()
		if err := cmd.Flags().Parse(nil); err != nil {
			t.Fatal(err)
		}
		// Point the default at somewhere that certainly does not exist.
		if err := cmd.Flags().Lookup("config").Value.Set(filepath.Join(t.TempDir(), "absent.json")); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveConfig(cmd.Flags()); err != nil {
			t.Fatalf("an absent config file at the default path must not be an error: %v", err)
		}
	})

	t.Run("absent explicit path is an error", func(t *testing.T) {
		flags := newTestFlags(t, "--config", filepath.Join(t.TempDir(), "absent.json"))
		if _, err := resolveConfig(flags); err == nil {
			t.Fatal("expected an error for an explicitly named missing config file")
		}
	})

	t.Run("the path itself can come from the environment", func(t *testing.T) {
		cfgFile := writeConfig(t, `{"listen": ":3333"}`)
		t.Setenv("TAPO_CONFIG", cfgFile)
		flags := newTestFlags(t)
		cfg, err := resolveConfig(flags)
		if err != nil {
			t.Fatal(err)
		}
		assertEqual(t, "listen", cfg.Listen, ":3333")
	})
}

func TestPasswordFile(t *testing.T) {
	t.Setenv("TAPO_USERNAME", "u")

	write := func(t *testing.T, content string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "password")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("trailing newline is stripped", func(t *testing.T) {
		flags := newTestFlags(t, "--password-file", write(t, "hunter2\n"))
		cfg, err := resolveConfig(flags)
		if err != nil {
			t.Fatal(err)
		}
		assertEqual(t, "password", cfg.Password, "hunter2")
	})

	t.Run("a trailing space is not stripped", func(t *testing.T) {
		flags := newTestFlags(t, "--password-file", write(t, "hunter2 \n"))
		cfg, err := resolveConfig(flags)
		if err != nil {
			t.Fatal(err)
		}
		assertEqual(t, "password", cfg.Password, "hunter2 ")
	})

	t.Run("empty file is an error", func(t *testing.T) {
		flags := newTestFlags(t, "--password-file", write(t, "\n"))
		if _, err := resolveConfig(flags); err == nil {
			t.Fatal("expected an error for an empty password file")
		}
	})

	t.Run("both password and password-file is an error", func(t *testing.T) {
		flags := newTestFlags(t, "--password-file", write(t, "hunter2\n"), "--password", "hunter2")
		if _, err := resolveConfig(flags); err == nil {
			t.Fatal("expected an error when both are set")
		}
	})

	t.Run("the file can be named by the environment", func(t *testing.T) {
		t.Setenv("TAPO_PASSWORD_FILE", write(t, "hunter2\n"))
		flags := newTestFlags(t)
		cfg, err := resolveConfig(flags)
		if err != nil {
			t.Fatal(err)
		}
		assertEqual(t, "password", cfg.Password, "hunter2")
	})
}

func TestEnvName(t *testing.T) {
	for flag, want := range map[string]string{
		"listen":        "TAPO_LISTEN",
		"password-file": "TAPO_PASSWORD_FILE",
		"show-id":       "TAPO_SHOW_ID",
	} {
		if got := envName(flag); got != want {
			t.Errorf("envName(%q) = %q, want %q", flag, got, want)
		}
	}
}

func TestInvalidEnvIsRefused(t *testing.T) {
	t.Setenv("TAPO_USERNAME", "u")
	t.Setenv("TAPO_PASSWORD", "p")
	for _, tc := range []struct{ name, value string }{
		{"TAPO_INTERVAL", "every so often"},
		{"TAPO_SHOW_ID", "perhaps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			if _, err := resolveConfig(newTestFlags(t)); err == nil {
				t.Fatalf("expected an error for %s=%q", tc.name, tc.value)
			}
		})
	}
}

func assertEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}
