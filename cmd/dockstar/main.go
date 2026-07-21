package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/redundercover/dockstar/internal/config"
	"github.com/redundercover/dockstar/internal/daemon"
	"github.com/redundercover/dockstar/internal/display"
	"github.com/redundercover/dockstar/internal/ipc"
	"github.com/redundercover/dockstar/internal/version"
	"github.com/redundercover/dockstar/internal/wake"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dockstar:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(help)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch args[0] {
	case "daemon":
		cancel()
		return runDaemon()
	case "version", "--version":
		fmt.Printf("dockstar %s (%s, %s)\n", version.Version, version.Commit, version.Date)
		return nil
	case "completion":
		if len(args) != 2 {
			return errors.New("usage: dockstar completion bash|zsh|fish")
		}
		return printCompletion(args[1])
	case "config":
		return configCommand(args[1:])
	case "wake":
		return wakeCommand(ctx, args[1:])
	}
	client, err := ipc.Connect()
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer client.Close()
	switch args[0] {
	case "status":
		value, err := client.Status(ctx)
		return printJSON(value, err)
	case "devices":
		value, err := client.Status(ctx)
		if err != nil {
			return err
		}
		var state struct {
			Outputs any `json:"outputs"`
		}
		if err := json.Unmarshal([]byte(value), &state); err != nil {
			return err
		}
		return printValue(state.Outputs)
	case "apply":
		return client.Call(ctx, "Apply")
	case "diagnose":
		value, err := client.JSONCall(ctx, "Diagnose")
		return printJSON(value, err)
	case "allow", "deny":
		if len(args) != 2 {
			return fmt.Errorf("usage: dockstar %s DEVICE_ID", args[0])
		}
		return client.Call(ctx, "ApproveDisplay", args[1], args[0] == "allow")
	case "trust":
		if len(args) != 2 || !contains([]string{"all", "allowlist", "off"}, args[1]) {
			return errors.New("usage: dockstar trust all|allowlist|off")
		}
		return client.Call(ctx, "SetTrustPolicy", args[1])
	case "mode":
		if len(args) != 2 || !contains([]string{"extend", "mirror", "external-only", "internal-only"}, args[1]) {
			return errors.New("usage: dockstar mode extend|mirror|external-only|internal-only")
		}
		return client.Call(ctx, "SetLayout", args[1])
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], help)
	}
}

func runDaemon() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	store, err := config.Open("")
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	if os.Getenv("DOCKSTAR_DEBUG") == "1" {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	controller := daemon.New(store, display.NewKScreen(), logger)
	server, err := ipc.Listen(controller)
	if err != nil {
		return err
	}
	defer server.Close()
	controller.SetEmitter(server)
	logger.Info("Dockstar started", "config", store.Path(), "backend", "KScreen/Plasma Wayland")
	return controller.Run(ctx)
}

func configCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: dockstar config show|validate [PATH]")
	}
	switch args[0] {
	case "show":
		store, err := config.Open("")
		if err != nil {
			return err
		}
		return printValue(store.Get())
	case "validate":
		var reader io.Reader = os.Stdin
		if len(args) > 1 {
			file, err := os.Open(args[1])
			if err != nil {
				return err
			}
			defer file.Close()
			reader = file
		} else if path, err := config.DefaultPath(); err == nil {
			file, openErr := os.Open(path)
			if openErr != nil {
				return openErr
			}
			defer file.Close()
			reader = file
		}
		data, err := io.ReadAll(io.LimitReader(reader, 1<<20))
		if err != nil {
			return err
		}
		if _, _, err := config.Decode(data); err != nil {
			return err
		}
		fmt.Println("configuration is valid")
		return nil
	default:
		return fmt.Errorf("unknown config command %q", args[0])
	}
}

func wakeCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: dockstar wake status|enable|disable|restore")
	}
	switch args[0] {
	case "status":
		status, err := wake.GetStatus("")
		if err != nil {
			return err
		}
		return printValue(status)
	case "enable":
		return wake.Setup(ctx, true)
	case "disable":
		return wake.Setup(ctx, false)
	case "restore":
		return wake.SetupAction(ctx, "restore")
	default:
		return fmt.Errorf("unknown wake command %q", args[0])
	}
}

func printJSON(value string, err error) error {
	if err != nil {
		return err
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return err
	}
	return printValue(decoded)
}

func printValue(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printCompletion(shell string) error {
	name := filepath.Base(os.Args[0])
	switch shell {
	case "bash":
		fmt.Printf("complete -W 'status devices allow deny trust mode apply diagnose wake config completion version' %s\n", name)
	case "zsh":
		fmt.Printf("#compdef %s\n_arguments '1:command:(status devices allow deny trust mode apply diagnose wake config completion version)'\n", name)
	case "fish":
		fmt.Printf("complete -c %s -f -a 'status devices allow deny trust mode apply diagnose wake config completion version'\n", name)
	default:
		return fmt.Errorf("unsupported shell %q", shell)
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

const help = `Dockstar manages display and docking behavior on KDE Plasma 6 Wayland.

Usage:
  dockstar daemon
  dockstar status
  dockstar devices
  dockstar allow DEVICE_ID
  dockstar deny DEVICE_ID
  dockstar trust all|allowlist|off
  dockstar mode extend|mirror|external-only|internal-only
  dockstar apply
  dockstar diagnose
  dockstar wake status|enable|disable|restore
  dockstar config show|validate [PATH]
  dockstar completion bash|zsh|fish
  dockstar version
`
