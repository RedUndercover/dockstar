package platform

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/redundercover/dockstar/internal/config"
)

type fakeCommands struct {
	calls    []string
	outputs  map[string]string
	failures map[string]int
}

func (f *fakeCommands) run(_ context.Context, binary string, args ...string) (string, error) {
	key := strings.Join(append([]string{binary}, args...), " ")
	f.calls = append(f.calls, key)
	if f.failures[key] > 0 {
		f.failures[key]--
		return "", errors.New("injected command failure")
	}
	return f.outputs[key], nil
}

func TestUndockRetriesOnlyFailedRestorations(t *testing.T) {
	commands := &fakeCommands{
		outputs: map[string]string{
			"/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status": "enp4s0:ethernet:connected",
		},
		failures: map[string]int{
			"/usr/bin/pactl set-default-sink speakers": 1,
			"/usr/bin/nmcli radio wifi on":             1,
		},
	}
	manager := &Manager{
		oldAudio:        "speakers",
		oldPower:        "balanced",
		startedEthernet: "enp4s0",
		wifiDisabled:    true,
		command:         commands.run,
	}
	cfg := config.Default()

	err := manager.Undock(context.Background(), cfg)
	if err == nil {
		t.Fatal("first undock should report failed restorations")
	}
	for _, want := range []string{"restore audio sink", "restore WiFi radio"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Undock error %q does not contain %q", err, want)
		}
	}
	assertCommands(t, commands.calls, []string{
		"/usr/bin/pactl set-default-sink speakers",
		"/usr/bin/powerprofilesctl set balanced",
		"/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status",
		"/usr/bin/nmcli device disconnect enp4s0",
		"/usr/bin/nmcli radio wifi on",
	})

	if err := manager.Undock(context.Background(), cfg); err != nil {
		t.Fatalf("retry Undock: %v", err)
	}
	assertCommands(t, commands.calls, []string{
		"/usr/bin/pactl set-default-sink speakers",
		"/usr/bin/powerprofilesctl set balanced",
		"/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status",
		"/usr/bin/nmcli device disconnect enp4s0",
		"/usr/bin/nmcli radio wifi on",
		"/usr/bin/pactl set-default-sink speakers",
		"/usr/bin/nmcli radio wifi on",
	})

	if err := manager.Undock(context.Background(), cfg); err != nil {
		t.Fatalf("settled Undock: %v", err)
	}
	if len(commands.calls) != 7 {
		t.Fatalf("settled Undock issued commands: %v", commands.calls[7:])
	}
}

func TestAudioRestoreOptOutAbandonsPendingBaseline(t *testing.T) {
	commands := &fakeCommands{
		outputs: map[string]string{
			"/usr/bin/pactl get-default-sink":   "speakers",
			"/usr/bin/pactl -f json list sinks": `[{"name":"hdmi-output","description":"HDMI"}]`,
		},
		failures: map[string]int{
			"/usr/bin/pactl set-default-sink speakers": 1,
		},
	}
	manager := &Manager{command: commands.run}
	cfg := config.Default()
	cfg.Network.Policy = "unchanged"
	cfg.Power.OnACProfile = "unchanged"

	if err := manager.Dock(context.Background(), cfg); err != nil {
		t.Fatalf("initial Dock: %v", err)
	}
	if err := manager.Undock(context.Background(), cfg); err == nil {
		t.Fatal("initial Undock should retain and report the failed saved restoration")
	}

	cfg.Audio.Restore = false
	if err := manager.Undock(context.Background(), cfg); err != nil {
		t.Fatalf("opt-out Undock: %v", err)
	}

	cfg.Audio.Restore = true
	if err := manager.Dock(context.Background(), cfg); err != nil {
		t.Fatalf("fresh opted-in Dock: %v", err)
	}
	if err := manager.Undock(context.Background(), cfg); err != nil {
		t.Fatalf("fresh opted-in Undock: %v", err)
	}

	assertCommands(t, commands.calls, []string{
		"/usr/bin/pactl get-default-sink",
		"/usr/bin/pactl -f json list sinks",
		"/usr/bin/pactl set-default-sink hdmi-output",
		"/usr/bin/pactl set-default-sink speakers",
		"/usr/bin/pactl get-default-sink",
		"/usr/bin/pactl -f json list sinks",
		"/usr/bin/pactl set-default-sink hdmi-output",
		"/usr/bin/pactl set-default-sink speakers",
	})
}

func TestDockDoesNotClaimWiFiThatWasAlreadyDisabled(t *testing.T) {
	commands := &fakeCommands{outputs: map[string]string{
		"/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status": "enp4s0:ethernet:disconnected",
		"/usr/bin/nmcli radio wifi":                            "disabled",
	}}
	manager := &Manager{command: commands.run}
	cfg := config.Default()
	cfg.Audio.Policy = "unchanged"
	cfg.Power.OnACProfile = "unchanged"
	cfg.Network.Policy = "wired-only"

	if err := manager.Dock(context.Background(), cfg); err != nil {
		t.Fatalf("Dock: %v", err)
	}
	commands.outputs["/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status"] = "enp4s0:ethernet:connected"
	if err := manager.Undock(context.Background(), cfg); err != nil {
		t.Fatalf("Undock: %v", err)
	}
	assertCommands(t, commands.calls, []string{
		"/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status",
		"/usr/bin/nmcli device connect enp4s0",
		"/usr/bin/nmcli radio wifi",
		"/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status",
		"/usr/bin/nmcli device disconnect enp4s0",
	})
}

func TestDockCommandFailureKeepsNetworkRestoration(t *testing.T) {
	t.Run("Ethernet connect", func(t *testing.T) {
		commands := &fakeCommands{
			outputs: map[string]string{
				"/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status": "enp4s0:ethernet:disconnected",
			},
			failures: map[string]int{
				"/usr/bin/nmcli device connect enp4s0": 1,
			},
		}
		manager := &Manager{command: commands.run}
		cfg := config.Default()
		cfg.Audio.Policy = "unchanged"
		cfg.Power.OnACProfile = "unchanged"
		cfg.Network.Policy = "prefer-wired"

		if err := manager.Dock(context.Background(), cfg); err == nil {
			t.Fatal("Dock should report the failed Ethernet connect")
		}
		if err := manager.Undock(context.Background(), cfg); err != nil {
			t.Fatalf("Undock after uncertain Ethernet connect: %v", err)
		}
		assertCommands(t, commands.calls, []string{
			"/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status",
			"/usr/bin/nmcli device connect enp4s0",
			"/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status",
		})
	})

	t.Run("WiFi disable", func(t *testing.T) {
		commands := &fakeCommands{
			outputs: map[string]string{
				"/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status": "enp4s0:ethernet:disconnected",
				"/usr/bin/nmcli radio wifi":                            "enabled",
			},
			failures: map[string]int{
				"/usr/bin/nmcli radio wifi off": 1,
			},
		}
		manager := &Manager{command: commands.run}
		cfg := config.Default()
		cfg.Audio.Policy = "unchanged"
		cfg.Power.OnACProfile = "unchanged"
		cfg.Network.Policy = "wired-only"

		if err := manager.Dock(context.Background(), cfg); err == nil {
			t.Fatal("Dock should report the failed WiFi disable")
		}
		commands.outputs["/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status"] = "enp4s0:ethernet:connected"
		if err := manager.Undock(context.Background(), cfg); err != nil {
			t.Fatalf("Undock after uncertain WiFi disable: %v", err)
		}
		assertCommands(t, commands.calls, []string{
			"/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status",
			"/usr/bin/nmcli device connect enp4s0",
			"/usr/bin/nmcli radio wifi",
			"/usr/bin/nmcli radio wifi off",
			"/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status",
			"/usr/bin/nmcli device disconnect enp4s0",
			"/usr/bin/nmcli radio wifi on",
		})
	})
}

func TestRedockReappliesPendingEthernetAndWiFiPolicy(t *testing.T) {
	const status = "/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status"
	commands := &fakeCommands{
		outputs: map[string]string{
			status:                      "enp4s0:ethernet:disconnected",
			"/usr/bin/nmcli radio wifi": "enabled",
		},
		failures: map[string]int{
			"/usr/bin/nmcli device disconnect enp4s0": 1,
		},
	}
	manager := &Manager{command: commands.run}
	cfg := config.Default()
	cfg.Audio.Policy = "unchanged"
	cfg.Power.OnACProfile = "unchanged"
	cfg.Network.Policy = "wired-only"

	if err := manager.Dock(context.Background(), cfg); err != nil {
		t.Fatalf("initial Dock: %v", err)
	}
	commands.outputs[status] = "enp4s0:ethernet:connected"
	if err := manager.Undock(context.Background(), cfg); err == nil {
		t.Fatal("initial Undock should report the failed Ethernet restoration")
	}

	commands.outputs[status] = "enp9s0:ethernet:disconnected\nenp4s0:ethernet:disconnected"
	if err := manager.Dock(context.Background(), cfg); err != nil {
		t.Fatalf("redock: %v", err)
	}
	commands.outputs[status] = "enp9s0:ethernet:disconnected\nenp4s0:ethernet:connected"
	if err := manager.Undock(context.Background(), cfg); err != nil {
		t.Fatalf("restoration after redock: %v", err)
	}

	assertCommands(t, commands.calls, []string{
		status,
		"/usr/bin/nmcli device connect enp4s0",
		"/usr/bin/nmcli radio wifi",
		"/usr/bin/nmcli radio wifi off",
		status,
		"/usr/bin/nmcli device disconnect enp4s0",
		"/usr/bin/nmcli radio wifi on",
		status,
		"/usr/bin/nmcli device connect enp4s0",
		"/usr/bin/nmcli radio wifi",
		"/usr/bin/nmcli radio wifi off",
		status,
		"/usr/bin/nmcli device disconnect enp4s0",
		"/usr/bin/nmcli radio wifi on",
	})
}

func TestUndockClearsAbsentEthernetWithoutDisconnect(t *testing.T) {
	const status = "/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status"
	commands := &fakeCommands{outputs: map[string]string{
		status: "enp9s0:ethernet:connected",
	}}
	manager := &Manager{
		startedEthernet: "enp4s0",
		command:         commands.run,
	}

	if err := manager.Undock(context.Background(), config.Default()); err != nil {
		t.Fatalf("Undock with absent saved Ethernet: %v", err)
	}
	if err := manager.Undock(context.Background(), config.Default()); err != nil {
		t.Fatalf("settled Undock: %v", err)
	}
	assertCommands(t, commands.calls, []string{status})
}

func TestUndockRetainsEthernetWhenStatusFails(t *testing.T) {
	const status = "/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status"
	commands := &fakeCommands{
		outputs: map[string]string{
			status: "enp4s0:ethernet:disconnected",
		},
		failures: map[string]int{
			status: 1,
		},
	}
	manager := &Manager{
		startedEthernet: "enp4s0",
		command:         commands.run,
	}

	if err := manager.Undock(context.Background(), config.Default()); err == nil {
		t.Fatal("Undock should report the failed Ethernet status query")
	}
	if err := manager.Undock(context.Background(), config.Default()); err != nil {
		t.Fatalf("retry after Ethernet status recovery: %v", err)
	}
	assertCommands(t, commands.calls, []string{status, status})
}

func TestFreshDockSelectsDisconnectedEthernetWithoutClaimingActiveAdapter(t *testing.T) {
	const status = "/usr/bin/nmcli -t -f DEVICE,TYPE,STATE device status"
	commands := &fakeCommands{outputs: map[string]string{
		status: "enp1s0:ethernet:connected\nenp2s0:ethernet:unavailable\nenp4s0:ethernet:disconnected",
	}}
	device, started, err := connectWired(context.Background(), commands.run, "")
	if err != nil || device != "enp4s0" || !started {
		t.Fatalf("fresh Ethernet selection = %q, %t, %v", device, started, err)
	}
	assertCommands(t, commands.calls, []string{status, "/usr/bin/nmcli device connect enp4s0"})
}

func assertCommands(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n got: %q\nwant: %q", got, want)
	}
}
