package wake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	PolicyPath   = "/etc/dockstar/wake.json"
	BaselinePath = "/var/lib/dockstar/wake-baseline.json"
)

var pciID = regexp.MustCompile(`^0000:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)
var usbRootID = regexp.MustCompile(`^usb[0-9]+$`)

type Source struct {
	ID      string `json:"id"`
	Class   string `json:"class"`
	Vendor  string `json:"vendor"`
	Device  string `json:"device"`
	State   string `json:"state"`
	Managed bool   `json:"managed"`
}

type Status struct {
	Installed bool     `json:"installed"`
	Enabled   bool     `json:"enabled"`
	Sources   []Source `json:"sources"`
}

type policy struct {
	Enabled bool     `json:"enabled"`
	Sources []string `json:"sources"`
}

func GetStatus(root string) (Status, error) {
	if root == "" {
		root = "/"
	}
	sources, err := Discover(root)
	if err != nil {
		return Status{}, err
	}
	policyPath := rooted(root, PolicyPath)
	data, err := os.ReadFile(policyPath)
	if errors.Is(err, os.ErrNotExist) {
		return Status{Installed: false, Sources: sources}, nil
	}
	if err != nil {
		return Status{}, err
	}
	var cfg policy
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Status{}, err
	}
	return Status{Installed: true, Enabled: cfg.Enabled, Sources: sources}, nil
}

func PolicyEnabled(root string) (bool, error) {
	if root == "" {
		root = "/"
	}
	var cfg policy
	if err := readJSON(rooted(root, PolicyPath), &cfg); err != nil {
		return false, err
	}
	return cfg.Enabled, nil
}

func Discover(root string) ([]Source, error) {
	paths, err := filepath.Glob(filepath.Join(root, "sys/bus/pci/devices", "*"))
	if err != nil {
		return nil, err
	}
	var sources []Source
	for _, path := range paths {
		id := filepath.Base(path)
		if !pciID.MatchString(id) {
			continue
		}
		class := read(path + "/class")
		managed := strings.HasPrefix(class, "0x03") || strings.HasPrefix(class, "0x0c03")
		wakeupPath := path + "/power/wakeup"
		stateBytes, err := os.ReadFile(wakeupPath)
		if err != nil || !managed {
			continue
		}
		sources = append(sources, Source{
			ID: "pci:" + id, Class: class, Vendor: read(path + "/vendor"), Device: read(path + "/device"),
			State: strings.TrimSpace(string(stateBytes)), Managed: true,
		})
	}
	usbPaths, err := filepath.Glob(filepath.Join(root, "sys/bus/usb/devices", "usb*"))
	if err != nil {
		return nil, err
	}
	for _, path := range usbPaths {
		id := filepath.Base(path)
		if !usbRootID.MatchString(id) {
			continue
		}
		stateBytes, err := os.ReadFile(filepath.Join(path, "power/wakeup"))
		if err != nil {
			continue
		}
		sources = append(sources, Source{
			ID:      "usb:" + id,
			Class:   "usb-root-hub",
			Vendor:  read(filepath.Join(path, "idVendor")),
			Device:  read(filepath.Join(path, "idProduct")),
			State:   strings.TrimSpace(string(stateBytes)),
			Managed: true,
		})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	return sources, nil
}

func Setup(ctx context.Context, enabled bool) error {
	action := "disable"
	if enabled {
		action = "enable"
	}
	return SetupAction(ctx, action)
}

func SetupAction(ctx context.Context, action string) error {
	if action != "enable" && action != "disable" && action != "restore" {
		return fmt.Errorf("unsupported setup action %q", action)
	}
	helper := os.Getenv("DOCKSTAR_WAKE_HELPER")
	if helper == "" {
		helper = "/usr/bin/dockstar-wake-helper"
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/pkexec", helper, action)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func Helper(action, root string) error {
	if root == "" {
		root = "/"
	}
	if root == "/" && os.Geteuid() != 0 {
		return errors.New("wake-helper must run as root")
	}
	sources, err := Discover(root)
	if err != nil {
		return err
	}
	policyPath, baselinePath := rooted(root, PolicyPath), rooted(root, BaselinePath)
	switch action {
	case "enable", "disable":
		if err := recordBaseline(baselinePath, sources); err != nil {
			return err
		}
		cfg := policy{Enabled: action == "enable"}
		for _, source := range sources {
			cfg.Sources = append(cfg.Sources, source.ID)
		}
		if err := writeJSON(policyPath, cfg, 0o644); err != nil {
			return err
		}
		return apply(root, cfg)
	case "apply":
		var cfg policy
		if err := readJSON(policyPath, &cfg); err != nil {
			return err
		}
		return apply(root, cfg)
	case "restore":
		var baseline map[string]string
		if err := readJSON(baselinePath, &baseline); err != nil {
			return err
		}
		for id, state := range baseline {
			if err := set(root, id, state); err != nil {
				return err
			}
		}
		_ = os.Remove(policyPath)
		_ = os.Remove(baselinePath)
		return nil
	default:
		return fmt.Errorf("unsupported wake-helper action %q", action)
	}
}

func recordBaseline(path string, sources []Source) error {
	baseline := make(map[string]string, len(sources))
	if err := readJSON(path, &baseline); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	changed := false
	for _, source := range sources {
		if _, exists := baseline[source.ID]; !exists {
			baseline[source.ID] = source.State
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return writeJSON(path, baseline, 0o600)
}

func apply(root string, cfg policy) error {
	state := "disabled"
	if cfg.Enabled {
		state = "enabled"
	}
	for _, id := range cfg.Sources {
		if err := set(root, id, state); err != nil {
			return err
		}
	}
	return nil
}

func set(root, id, state string) error {
	if state != "enabled" && state != "disabled" {
		return fmt.Errorf("refusing invalid wake state %q", state)
	}
	var path string
	switch {
	case strings.HasPrefix(id, "pci:") && pciID.MatchString(strings.TrimPrefix(id, "pci:")):
		path = filepath.Join(root, "sys/bus/pci/devices", strings.TrimPrefix(id, "pci:"), "power/wakeup")
	case strings.HasPrefix(id, "usb:") && usbRootID.MatchString(strings.TrimPrefix(id, "usb:")):
		path = filepath.Join(root, "sys/bus/usb/devices", strings.TrimPrefix(id, "usb:"), "power/wakeup")
	default:
		return fmt.Errorf("refusing invalid wake source %q", id)
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("unavailable wake source %s: %w", id, err)
	}
	return os.WriteFile(path, []byte(state), 0o644)
}

func read(path string) string {
	data, _ := os.ReadFile(path)
	return strings.TrimSpace(string(data))
}

func rooted(root, path string) string { return filepath.Join(root, strings.TrimPrefix(path, "/")) }

func readJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func writeJSON(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dockstar-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
