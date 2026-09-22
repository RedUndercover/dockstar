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

type commandFunc func(context.Context, string, ...string) (string, error)

type Manager struct {
	mu              sync.Mutex
	oldAudio        string
	oldPower        string
	startedEthernet string
	wifiDisabled    bool
	command         commandFunc
}

func (m *Manager) Dock(ctx context.Context, cfg config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var failures []error
	if cfg.Audio.Policy == "prefer-display" {
		canChange := true
		if cfg.Audio.Restore && m.oldAudio == "" {
			previous, err := m.runCommand(ctx, "/usr/bin/pactl", "get-default-sink")
			if err != nil {
				failures = append(failures, fmt.Errorf("save audio sink: %w", err))
				canChange = false
			} else if previous == "" {
				failures = append(failures, errors.New("save audio sink: pactl returned an empty sink"))
				canChange = false
			} else {
				m.oldAudio = previous
			}
		}
		if canChange {
			if err := preferDisplayAudio(ctx, m.runCommand); err != nil {
				failures = append(failures, err)
			}
		}
	}

	if cfg.Network.Policy != "unchanged" {
		device, started, err := connectWired(ctx, m.runCommand, m.startedEthernet)
		if started && m.startedEthernet == "" {
			m.startedEthernet = device
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("connect Ethernet: %w", err))
		}
		if cfg.Network.Policy == "wired-only" && device != "" && err == nil && !m.wifiDisabled {
			state, err := m.runCommand(ctx, "/usr/bin/nmcli", "radio", "wifi")
			if err != nil {
				failures = append(failures, fmt.Errorf("read WiFi radio state: %w", err))
			} else if state == "enabled" {
				m.wifiDisabled = true
				if _, err := m.runCommand(ctx, "/usr/bin/nmcli", "radio", "wifi", "off"); err != nil {
					failures = append(failures, fmt.Errorf("disable WiFi: %w", err))
				}
			} else if state != "disabled" {
				failures = append(failures, fmt.Errorf("read WiFi radio state: unexpected state %q", state))
			}
		}
	}

	if cfg.Power.OnACProfile != "unchanged" && onACPower() {
		canChange := true
		if cfg.Power.Restore && m.oldPower == "" {
			previous, err := m.runCommand(ctx, "/usr/bin/powerprofilesctl", "get")
			if err != nil {
				failures = append(failures, fmt.Errorf("save power profile: %w", err))
				canChange = false
			} else if previous == "" {
				failures = append(failures, errors.New("save power profile: powerprofilesctl returned an empty profile"))
				canChange = false
			} else {
				m.oldPower = previous
			}
		}
		if canChange {
			if _, err := m.runCommand(ctx, "/usr/bin/powerprofilesctl", "set", cfg.Power.OnACProfile); err != nil {
				failures = append(failures, fmt.Errorf("set docked power profile: %w", err))
			}
		}
	}
	return errors.Join(failures...)
}

func (m *Manager) Undock(ctx context.Context, cfg config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var failures []error
	if !cfg.Audio.Restore {
		m.oldAudio = ""
	} else if m.oldAudio != "" {
		if _, err := m.runCommand(ctx, "/usr/bin/pactl", "set-default-sink", m.oldAudio); err != nil {
			failures = append(failures, fmt.Errorf("restore audio sink %q: %w", m.oldAudio, err))
		} else {
			m.oldAudio = ""
		}
	}
	if !cfg.Power.Restore {
		m.oldPower = ""
	} else if m.oldPower != "" {
		if _, err := m.runCommand(ctx, "/usr/bin/powerprofilesctl", "set", m.oldPower); err != nil {
			failures = append(failures, fmt.Errorf("restore power profile %q: %w", m.oldPower, err))
		} else {
			m.oldPower = ""
		}
	}
	if m.startedEthernet != "" {
		device, state, err := ethernetState(ctx, m.runCommand, m.startedEthernet)
		if err != nil {
			failures = append(failures, fmt.Errorf("inspect Ethernet device %q: %w", m.startedEthernet, err))
		} else if device == "" || state == "disconnected" {
			m.startedEthernet = ""
		} else if state != "connected" {
			failures = append(failures, fmt.Errorf("restore Ethernet device %q: unexpected state %q", m.startedEthernet, state))
		} else if _, err := m.runCommand(ctx, "/usr/bin/nmcli", "device", "disconnect", m.startedEthernet); err != nil {
			failures = append(failures, fmt.Errorf("disconnect Ethernet device %q: %w", m.startedEthernet, err))
		} else {
			m.startedEthernet = ""
		}
	}
	if m.wifiDisabled {
		if _, err := m.runCommand(ctx, "/usr/bin/nmcli", "radio", "wifi", "on"); err != nil {
			failures = append(failures, fmt.Errorf("restore WiFi radio: %w", err))
		} else {
			m.wifiDisabled = false
		}
	}
	return errors.Join(failures...)
}

func (m *Manager) runCommand(ctx context.Context, binary string, args ...string) (string, error) {
	if m.command != nil {
		return m.command(ctx, binary, args...)
	}
	return command(ctx, binary, args...)
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

func preferDisplayAudio(ctx context.Context, run commandFunc) error {
	output, err := run(ctx, "/usr/bin/pactl", "-f", "json", "list", "sinks")
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
			_, err := run(ctx, "/usr/bin/pactl", "set-default-sink", sink.Name)
			return err
		}
	}
	return errors.New("no HDMI or DisplayPort audio sink is available")
}

func connectWired(ctx context.Context, run commandFunc, pending string) (string, bool, error) {
	device, state, err := ethernetState(ctx, run, pending)
	if err != nil {
		return "", false, err
	}
	if device == "" {
		if pending != "" {
			return pending, false, fmt.Errorf("saved Ethernet device %q is unavailable", pending)
		}
		return "", false, nil
	}
	if state == "connected" {
		return device, false, nil
	}
	if state == "disconnected" {
		_, err := run(ctx, "/usr/bin/nmcli", "device", "connect", device)
		return device, true, err
	}
	return device, false, fmt.Errorf("cannot connect Ethernet device %q while it is %s", device, state)
}

func ethernetState(ctx context.Context, run commandFunc, pending string) (string, string, error) {
	output, err := run(ctx, "/usr/bin/nmcli", "-t", "-f", "DEVICE,TYPE,STATE", "device", "status")
	if err != nil {
		return "", "", err
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 3 || fields[1] != "ethernet" {
			continue
		}
		if pending != "" && fields[0] == pending || pending == "" && fields[2] == "disconnected" {
			return fields[0], fields[2], nil
		}
	}
	return "", "", nil
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
		ctx, "org.freedesktop.login1.Manager.Suspend", 0, false,
	).Err
}
