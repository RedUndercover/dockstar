package display

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/redundercover/dockstar/internal/config"
)

func edidID(data []byte) string {
	if len(data) < 128 {
		return ""
	}
	sum := sha256.Sum256(data)
	return "edid:" + hex.EncodeToString(sum[:])
}

func panelConnector(name string) bool {
	return strings.HasPrefix(name, "eDP-") || strings.HasPrefix(name, "LVDS-") || strings.HasPrefix(name, "DSI-")
}

// DRMSnapshot reads physical connections without a user session or compositor.
// Missing EDID never grants allowlist approval, even if a connector was approved
// previously. A disconnected connector's cached EDID is deliberately ignored.
func DRMSnapshot(root string, cfg config.Config) (Snapshot, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read DRM connectors: %w", err)
	}
	result := Snapshot{At: time.Now().UTC(), Outputs: []Output{}}
	for _, entry := range entries {
		card, connector, ok := strings.Cut(entry.Name(), "-")
		if !ok || !strings.HasPrefix(card, "card") || len(card) == 4 {
			continue
		}
		validCard := true
		for _, digit := range card[4:] {
			if digit < '0' || digit > '9' {
				validCard = false
				break
			}
		}
		if !validCard {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		data, err := os.ReadFile(filepath.Join(dir, "status"))
		if err != nil {
			return Snapshot{}, fmt.Errorf("read %s connection: %w", entry.Name(), err)
		}
		status := strings.TrimSpace(string(data))
		if status != "connected" && status != "disconnected" && status != "unknown" {
			return Snapshot{}, fmt.Errorf("invalid %s connection status %q", entry.Name(), status)
		}
		output := Output{Connector: connector, Label: connector, Panel: panelConnector(connector), Connected: status == "connected"}
		if output.Connected {
			edid, err := os.ReadFile(filepath.Join(dir, "edid"))
			if err != nil && !os.IsNotExist(err) {
				return Snapshot{}, fmt.Errorf("read %s EDID: %w", entry.Name(), err)
			}
			output.DeviceID = edidID(edid)
			output.Approved = output.Panel || approved(cfg, output.DeviceID)
			if rule, ok := cfg.Rule(output.DeviceID); ok && rule.Label != "" {
				output.Label = rule.Label
			}
		}
		result.Outputs = append(result.Outputs, output)
	}
	return result, nil
}

func (k *KScreen) PhysicalSnapshot(ctx context.Context, cfg config.Config) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return DRMSnapshot(k.DRMRoot, cfg)
}

// ResolvePhysical keeps kernel connection state authoritative while preserving
// explicitly approved session-only identities on EDID-less displays. A cached
// EDID never identifies an unknown replacement. The input snapshots are unchanged.
func ResolvePhysical(physical, session Snapshot, cfg config.Config) Snapshot {
	copied := false
	for index, output := range physical.Outputs {
		if !output.Connected || output.Panel || output.DeviceID != "" {
			continue
		}
		if _, unique := physicalOutput(physical.Outputs, output.Connector); !unique {
			continue
		}
		candidate, unique := physicalOutput(session.Outputs, output.Connector)
		if !unique || !candidate.Connected || candidate.Panel {
			continue
		}
		if !strings.HasPrefix(candidate.DeviceID, "kde:") && !strings.HasPrefix(candidate.DeviceID, "connector:") {
			continue
		}
		rule, exists := cfg.Rule(candidate.DeviceID)
		if !exists || !rule.Allowed {
			continue
		}
		if !copied {
			physical.Outputs = slices.Clone(physical.Outputs)
			copied = true
		}
		output.DeviceID = candidate.DeviceID
		output.Approved = approved(cfg, candidate.DeviceID)
		if rule.Label != "" {
			output.Label = rule.Label
		}
		physical.Outputs[index] = output
	}
	return physical
}

// physicalOutput avoids borrowing another GPU's EDID when KScreen supplies a
// connector name that is not unique across DRM cards.
func physicalOutput(outputs []Output, connector string) (Output, bool) {
	var candidate Output
	matches := 0
	for _, output := range outputs {
		if output.Connector == connector {
			candidate = output
			matches++
		}
	}
	return candidate, matches == 1
}
