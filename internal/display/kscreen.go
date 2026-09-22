package display

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redundercover/dockstar/internal/config"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

type KScreen struct {
	Runner  Runner
	Doctor  string
	DRMRoot string
}

func NewKScreen() *KScreen {
	return &KScreen{Runner: ExecRunner{}, Doctor: "/usr/bin/kscreen-doctor", DRMRoot: "/sys/class/drm"}
}

func (k *KScreen) Name() string { return "KScreen/Plasma Wayland" }

type doctorDocument struct {
	Outputs []doctorOutput `json:"outputs"`
}

type doctorOutput struct {
	ID                int          `json:"id"`
	UUID              string       `json:"uuid"`
	Name              string       `json:"name"`
	Connected         bool         `json:"connected"`
	Enabled           bool         `json:"enabled"`
	Type              int          `json:"type"`
	Priority          uint32       `json:"priority"`
	Scale             float64      `json:"scale"`
	CurrentModeID     string       `json:"currentModeId"`
	PreferredModes    []string     `json:"preferredModes"`
	Modes             []doctorMode `json:"modes"`
	ReplicationSource int          `json:"replicationSource"`
	Position          struct {
		X int `json:"x"`
		Y int `json:"y"`
	} `json:"pos"`
	Size struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"size"`
}

type doctorMode struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	RefreshRate float64 `json:"refreshRate"`
	Size        struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"size"`
}

func (k *KScreen) Snapshot(ctx context.Context, cfg config.Config) (Snapshot, error) {
	if k.Runner == nil {
		k.Runner = ExecRunner{}
	}
	data, err := k.Runner.Run(ctx, k.Doctor, "-j")
	if err != nil {
		return Snapshot{}, err
	}
	var doc doctorDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return Snapshot{}, fmt.Errorf("decode kscreen output: %w", err)
	}
	result := Snapshot{At: time.Now().UTC(), Outputs: make([]Output, 0, len(doc.Outputs))}
	physical, physicalErr := k.PhysicalSnapshot(ctx, cfg)
	for _, raw := range doc.Outputs {
		panel := raw.Type == 7 || panelConnector(raw.Name)
		id := fallbackDeviceID(raw.Name, raw.UUID)
		output := Output{
			ID: raw.ID, DeviceID: id, UUID: raw.UUID, Connector: raw.Name, Label: raw.Name,
			Panel: panel, Connected: raw.Connected, Enabled: raw.Enabled, Priority: raw.Priority,
			Scale: raw.Scale, X: raw.Position.X, Y: raw.Position.Y, Width: raw.Size.Width, Height: raw.Size.Height,
			CurrentModeID: raw.CurrentModeID, PreferredModeIDs: raw.PreferredModes,
			ReplicationSource: raw.ReplicationSource,
		}
		if physicalErr == nil {
			if current, ok := physicalOutput(physical.Outputs, raw.Name); ok {
				output.Connected = current.Connected
				if current.DeviceID != "" {
					id = current.DeviceID
					output.DeviceID = id
				}
			}
		}
		if rule, ok := cfg.Rule(id); ok && rule.Label != "" {
			output.Label = rule.Label
		}
		output.Approved = panel || approved(cfg, id)
		for _, mode := range raw.Modes {
			output.Modes = append(output.Modes, Mode{ID: mode.ID, Name: mode.Name, Width: mode.Size.Width, Height: mode.Size.Height, Refresh: mode.RefreshRate})
		}
		result.Outputs = append(result.Outputs, output)
	}
	return result, nil
}

func approved(cfg config.Config, id string) bool {
	switch cfg.Detection {
	case "all":
		return true
	case "allowlist":
		rule, ok := cfg.Rule(id)
		return ok && rule.Allowed
	default:
		return false
	}
}

func fallbackDeviceID(connector, uuid string) string {
	if uuid != "" {
		return "kde:" + uuid
	}
	sum := sha256.Sum256([]byte("connector:" + connector))
	return "connector:" + hex.EncodeToString(sum[:])
}

func (k *KScreen) DPMSOn(ctx context.Context) error {
	_, err := k.Runner.Run(ctx, k.Doctor, "--dpms", "on")
	return err
}

func (k *KScreen) Apply(ctx context.Context, cfg config.Config, layout string) (ApplyResult, error) {
	snapshot, err := k.Snapshot(ctx, cfg)
	if err != nil {
		return ApplyResult{}, err
	}
	var panels, external, accepted []Output
	for _, output := range snapshot.Outputs {
		if !output.Connected {
			continue
		}
		if output.Panel {
			panels = append(panels, output)
		} else {
			external = append(external, output)
			if output.Approved {
				accepted = append(accepted, output)
			}
		}
	}
	if len(accepted) == 0 && layout != "internal-only" {
		return ApplyResult{}, ErrNoApprovedDisplay
	}
	primary := choosePrimary(accepted, cfg)
	if layout == "mirror" {
		if len(panels) == 0 {
			return ApplyResult{}, errors.New("mirroring requires an internal display")
		}
		if err := applyWaylandMirror(ctx, snapshot, cfg, panels[0], primary); err != nil {
			return ApplyResult{}, err
		}
		return ApplyResult{Layout: layout, Primary: primary.Connector}, nil
	}
	args := make([]string, 0, len(snapshot.Outputs)*4)
	disable := func(out Output) { args = append(args, outputArg(out, "disable")) }
	enable := func(out Output) { args = append(args, outputArg(out, "enable")) }

	for _, out := range external {
		if !out.Approved || layout == "internal-only" {
			disable(out)
		}
	}
	for _, panel := range panels {
		if layout == "external-only" {
			disable(panel)
		} else {
			enable(panel)
		}
	}
	if layout == "internal-only" {
		for _, panel := range panels {
			args = append(args, outputArg(panel, "priority", "1"))
		}
	} else {
		for _, out := range accepted {
			enable(out)
			priority := "2"
			if out.ID == primary.ID {
				priority = "1"
			}
			args = append(args, outputArg(out, "priority", priority))
			if rule, ok := cfg.Rule(out.DeviceID); ok {
				if rule.Scale != 0 {
					args = append(args, outputArg(out, "scale", strconv.FormatFloat(rule.Scale, 'f', -1, 64)))
				}
				if rule.Mode != "" && rule.Mode != "preferred" {
					args = append(args, outputArg(out, "mode", rule.Mode))
				}
			}
		}
	}

	switch layout {
	case "extend":
		args = append(args, extendArguments(panels, accepted, cfg)...)
	case "external-only", "internal-only":
	default:
		return ApplyResult{}, fmt.Errorf("unsupported layout %q", layout)
	}
	if len(args) == 0 {
		return ApplyResult{}, errors.New("display transaction is empty")
	}
	if _, err := k.Runner.Run(ctx, k.Doctor, args...); err != nil {
		return ApplyResult{}, err
	}
	name := primary.Connector
	if layout == "internal-only" && len(panels) > 0 {
		name = panels[0].Connector
	}
	return ApplyResult{Layout: layout, Primary: name}, nil
}

func outputArg(out Output, setting string, value ...string) string {
	parts := []string{"output", out.Connector, setting}
	parts = append(parts, value...)
	return strings.Join(parts, ".")
}

func choosePrimary(outputs []Output, cfg config.Config) Output {
	copyOutputs := append([]Output(nil), outputs...)
	sort.SliceStable(copyOutputs, func(i, j int) bool {
		ri, iok := cfg.Rule(copyOutputs[i].DeviceID)
		rj, jok := cfg.Rule(copyOutputs[j].DeviceID)
		if iok && jok && ri.PrimaryRank != rj.PrimaryRank {
			return ri.PrimaryRank > rj.PrimaryRank
		}
		mi, mj := copyOutputs[i].PreferredMode(), copyOutputs[j].PreferredMode()
		return mi.Width*mi.Height > mj.Width*mj.Height
	})
	if len(copyOutputs) == 0 {
		return Output{}
	}
	return copyOutputs[0]
}

func extendArguments(panels, outputs []Output, cfg config.Config) []string {
	args := []string{}
	left, right, top, bottom := 0, 0, 0, 0
	if len(panels) > 0 {
		panel := panels[0]
		args = append(args, outputArg(panel, "position", "0,0"), outputArg(panel, "priority", "2"))
		mode := panel.PreferredMode()
		right = logical(mode.Width, panel.Scale)
		bottom = logical(mode.Height, panel.Scale)
	}
	for _, output := range outputs {
		rule, _ := cfg.Rule(output.DeviceID)
		mode := output.PreferredMode()
		width, height := logical(mode.Width, effectiveScale(output, rule)), logical(mode.Height, effectiveScale(output, rule))
		x, y := right, 0
		switch rule.Position {
		case "left":
			left -= width
			x = left
		case "above":
			top -= height
			x, y = 0, top
		case "below":
			y = bottom
			bottom += height
		default:
			right += width
		}
		args = append(args, outputArg(output, "position", fmt.Sprintf("%d,%d", x, y)))
	}
	return args
}

func effectiveScale(output Output, rule config.DisplayRule) float64 {
	if rule.Scale > 0 {
		return rule.Scale
	}
	if output.Scale > 0 {
		return output.Scale
	}
	return 1
}

func logical(value int, scale float64) int {
	if scale <= 0 {
		scale = 1
	}
	return int(float64(value)/scale + 0.5)
}

func commonMode(a, b Output, maxRefresh float64) (Mode, bool) {
	var choices []Mode
	for _, left := range a.Modes {
		if left.Refresh > maxRefresh+0.5 {
			continue
		}
		for _, right := range b.Modes {
			if left.Width == right.Width && left.Height == right.Height && right.Refresh <= maxRefresh+0.5 && abs(left.Refresh-right.Refresh) < 1 {
				choices = append(choices, left)
			}
		}
	}
	sort.Slice(choices, func(i, j int) bool {
		pi, pj := choices[i].Width*choices[i].Height, choices[j].Width*choices[j].Height
		if pi == pj {
			return choices[i].Refresh > choices[j].Refresh
		}
		return pi > pj
	})
	if len(choices) == 0 {
		return Mode{}, false
	}
	return choices[0], true
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func signature(snapshot Snapshot) string {
	parts := make([]string, 0, len(snapshot.Outputs))
	for _, output := range snapshot.Outputs {
		if output.Connected {
			parts = append(parts, fmt.Sprintf("%s:%t", output.DeviceID, output.Approved))
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

func Signature(snapshot Snapshot) string { return signature(snapshot) }
