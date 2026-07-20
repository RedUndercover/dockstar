package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/redundercover/dockstar/internal/config"
)

type Manager struct {
	mu              sync.Mutex
	oldAudio        string
	oldPower        string
	startedEthernet string
	wifiDisabled    bool
}

func (m *Manager) Dock(ctx context.Context, cfg config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var failures []error
	if cfg.Audio.Restore && m.oldAudio == "" {
		m.oldAudio, _ = command(ctx, "/usr/bin/pactl", "get-default-sink")
	}
	if cfg.Power.Restore && m.oldPower == "" {
		m.oldPower, _ = command(ctx, "/usr/bin/powerprofilesctl", "get")
	}
	if cfg.Audio.Policy == "prefer-display" {
		if err := preferDisplayAudio(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	if cfg.Network.Policy != "unchanged" {
		device, err := connectWired(ctx)
		if err != nil {
			failures = append(failures, err)
		} else if device != "" {
			m.startedEthernet = device
		}
		if cfg.Network.Policy == "wired-only" && device != "" {
			if _, err := command(ctx, "/usr/bin/nmcli", "radio", "wifi", "off"); err != nil {
				failures = append(failures, err)
			} else {
				m.wifiDisabled = true
			}
		}
	}
	if cfg.Power.OnACProfile != "unchanged" && onACPower() {
		if _, err := command(ctx, "/usr/bin/powerprofilesctl", "set", cfg.Power.OnACProfile); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (m *Manager) Undock(ctx context.Context, cfg config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var failures []error
	if cfg.Audio.Restore && m.oldAudio != "" {
		_, err := command(ctx, "/usr/bin/pactl", "set-default-sink", m.oldAudio)
		failures = appendIf(failures, err)
		m.oldAudio = ""
	}
	if cfg.Power.Restore && m.oldPower != "" {
		_, err := command(ctx, "/usr/bin/powerprofilesctl", "set", m.oldPower)
		failures = appendIf(failures, err)
		m.oldPower = ""
	}
	if m.startedEthernet != "" {
		_, err := command(ctx, "/usr/bin/nmcli", "device", "disconnect", m.startedEthernet)
		failures = appendIf(failures, err)
		m.startedEthernet = ""
	}
	if m.wifiDisabled {
		_, err := command(ctx, "/usr/bin/nmcli", "radio", "wifi", "on")
		failures = appendIf(failures, err)
		m.wifiDisabled = false
	}
	return errors.Join(failures...)
}

func appendIf(values []error, err error) []error {
	if err != nil {
		return append(values, err)
	}
	return values
}

func command(ctx context.Context, binary string, args ...string) (string, error) {
	child, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(child, binary, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", binary, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func preferDisplayAudio(ctx context.Context) error {
	output, err := command(ctx, "/usr/bin/pactl", "-f", "json", "list", "sinks")
	if err != nil {
		return err
	}
	var sinks []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(output), &sinks); err != nil {
		return fmt.Errorf("decode pactl sinks: %w", err)
	}
	for _, sink := range sinks {
		candidate := strings.ToLower(sink.Name + " " + sink.Description)
		if strings.Contains(candidate, "hdmi") || strings.Contains(candidate, "displayport") {
			_, err := command(ctx, "/usr/bin/pactl", "set-default-sink", sink.Name)
			return err
		}
	}
	return errors.New("no HDMI or DisplayPort audio sink is available")
}

func connectWired(ctx context.Context) (string, error) {
	output, err := command(ctx, "/usr/bin/nmcli", "-t", "-f", "DEVICE,TYPE,STATE", "device", "status")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 3 && fields[1] == "ethernet" {
			if fields[2] == "connected" {
				return "", nil
			}
			if fields[2] == "disconnected" {
				if _, err := command(ctx, "/usr/bin/nmcli", "device", "connect", fields[0]); err != nil {
					return "", err
				}
				return fields[0], nil
			}
		}
	}
	return "", nil
}

func onACPower() bool {
	matches, _ := filepathGlob("/sys/class/power_supply/*/type")
	for _, path := range matches {
		kind, err := readTrimmed(path)
		if err != nil || kind != "Mains" {
			continue
		}
		online, _ := readTrimmed(strings.TrimSuffix(path, "/type") + "/online")
		if online == "1" {
			return true
		}
	}
	return false
}

func LidClosed(ctx context.Context) (bool, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false, err
	}
	defer conn.Close()
	call := conn.Object("org.kde.Solid.PowerManagement", "/org/kde/Solid/PowerManagement").CallWithContext(
		ctx, "org.kde.Solid.PowerManagement.isLidClosed", 0,
	)
	var closed bool
	if err := call.Store(&closed); err != nil {
		return false, err
	}
	return closed, nil
}

func Lock(ctx context.Context) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Object("org.freedesktop.ScreenSaver", "/ScreenSaver").CallWithContext(
		ctx, "org.freedesktop.ScreenSaver.Lock", dbus.FlagNoReplyExpected,
	).Err
}

func Suspend(ctx context.Context) error {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Object("org.freedesktop.login1", "/org/freedesktop/login1").CallWithContext(
		ctx, "org.freedesktop.login1.Manager.Suspend", dbus.FlagNoReplyExpected, true,
	).Err
}
