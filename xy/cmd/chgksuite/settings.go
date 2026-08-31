package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The two files chgksuite reads besides the command line:
//
//   - ~/.chgksuite/settings.toml, whose [default_overrides] table changes what a
//     flag defaults to (and whose top level holds the handful of settings that
//     are not flags at all);
//   - the --config JSON, applied AFTER parsing, so it wins over a flag given on
//     the command line. That is chgksuite's order, and reversing it would make
//     the two tools disagree about which of the two the user meant.

// settings is ~/.chgksuite/settings.toml, flattened: "default_overrides.font"
// and the top-level keys both live here under their dotted names.
type settings map[string]string

var loadedSettings settings

// loadSettings reads the file once. A missing or unreadable one is no error:
// chgksuite treats it as an empty table.
func loadSettings() settings {
	if loadedSettings != nil {
		return loadedSettings
	}
	loadedSettings = settings{}
	home, err := os.UserHomeDir()
	if err != nil {
		return loadedSettings
	}
	raw, err := os.ReadFile(filepath.Join(home, ".chgksuite", "settings.toml"))
	if err != nil {
		return loadedSettings
	}
	table := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			table = strings.TrimSpace(line[1:len(line)-1]) + "."
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		loadedSettings[table+strings.TrimSpace(key)] = unquote(strings.TrimSpace(value))
	}
	return loadedSettings
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// override is a flag's default after [default_overrides] has had its say, which
// is what chgksuite's `default_overrides.get(name) or <default>` computes.
func override(name, fallback string) string {
	if v := loadSettings()["default_overrides."+name]; v != "" {
		return v
	}
	return fallback
}

func overrideInt(name string, fallback int) int {
	if v, err := strconv.Atoi(override(name, "")); err == nil {
		return v
	}
	return fallback
}

// setting reads a top-level key, the ones that are settings rather than flags.
func setting(name string) string { return loadSettings()[name] }

// applyConfig applies a --config JSON over the flags already parsed. Every key
// must name a flag of this command; chgksuite silently sets whatever it is
// given, which quietly swallows a typo, so this says so instead.
func applyConfig(fs *flag.FlagSet, path string) error {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for key, value := range cfg {
		if fs.Lookup(key) == nil {
			continue // a key for another command, as chgksuite's one config file holds
		}
		if err := fs.Set(key, configValue(value)); err != nil {
			return fmt.Errorf("%s: %s: %w", path, key, err)
		}
	}
	return nil
}

// configValue renders a JSON value as the string a flag parses, and resolves a
// value naming a file that exists to its absolute path, as chgksuite does.
func configValue(v any) string {
	s, ok := v.(string)
	if !ok {
		switch t := v.(type) {
		case bool:
			return strconv.FormatBool(t)
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		default:
			b, _ := json.Marshal(v)
			return string(b)
		}
	}
	if abs, err := filepath.Abs(s); err == nil {
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return s
}

// configFlag declares --config on a command's own flag set, since this CLI has
// no flags of its own before the command name.
func configFlag(fs *flag.FlagSet) *string {
	return fs.String("config", "", "a JSON file of flag values, applied over the command line")
}
