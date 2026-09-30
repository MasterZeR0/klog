package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// profilesPath is $KLOG_PROFILES, else <user config dir>/klog/profiles.json.
func profilesPath() string {
	if p := os.Getenv("KLOG_PROFILES"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "" // no $HOME: no profiles
	}
	return filepath.Join(dir, "klog", "profiles.json")
}

// loadProfiles reads the profiles file. A missing file means no profiles.
func loadProfiles(path string) (map[string][]string, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p map[string][]string
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("profiles file %s: %w", path, err)
	}
	return p, nil
}

// expandProfile replaces a leading @name in args with that profile's argv, so
// flags given after it override the profile's (last wins; --field accumulates).
func expandProfile(args []string) ([]string, error) {
	if len(args) == 0 || !strings.HasPrefix(args[0], "@") {
		return args, nil
	}
	path := profilesPath()
	profiles, err := loadProfiles(path)
	if err != nil {
		return nil, err
	}
	name := args[0][1:]
	p, ok := profiles[name]
	if !ok {
		names := make([]string, 0, len(profiles))
		for n := range profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		avail := "none"
		if len(names) > 0 {
			avail = strings.Join(names, ", ")
		}
		return nil, fmt.Errorf("unknown profile %q (available: %s; profiles file: %s)", name, avail, path)
	}
	return append(append([]string{}, p...), args[1:]...), nil
}
