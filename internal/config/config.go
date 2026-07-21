package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const SchemaVersion = 2

type Config struct {
	SchemaVersion   int           `json:"schemaVersion"`
	Revision        uint64        `json:"revision"`
	Detection       string        `json:"detection"`
	DefaultLayout   string        `json:"defaultLayout"`
	LidClosedLayout string        `json:"lidClosedLayout"`
	Mirror          Mirror        `json:"mirror"`
	Audio           Audio         `json:"audio"`
	Network         Network       `json:"network"`
	Power           Power         `json:"power"`
	Security        Security      `json:"security"`
	Wake            Wake          `json:"wake"`
	Displays        []DisplayRule `json:"displays"`
}

type Mirror struct {
	Source               string `json:"source"`
	FallbackMaxRefreshHz int    `json:"fallbackMaxRefreshHz"`
}

type Audio struct {
	Policy  string `json:"policy"`
	Restore bool   `json:"restore"`
}

type Network struct {
	Policy string `json:"policy"`
}

type Power struct {
	OnACProfile          string `json:"onAcProfile"`
	Restore              bool   `json:"restore"`
	KeepAwakeWhileDocked bool   `json:"keepAwakeWhileDocked"`
}

type Security struct {
	LockOnClosedUndock    bool `json:"lockOnClosedUndock"`
	SuspendOnClosedUndock bool `json:"suspendOnClosedUndock"`
}

type Wake struct {
	Enabled bool `json:"enabled"`
}

type DisplayRule struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Allowed     bool    `json:"allowed"`
	PrimaryRank int     `json:"primaryRank"`
	Scale       float64 `json:"scale"`
	Mode        string  `json:"mode"`
	Position    string  `json:"position"`
	AudioSink   string  `json:"audioSink,omitempty"`
}

func Default() Config {
	return Config{
		SchemaVersion:   SchemaVersion,
		Revision:        1,
		Detection:       "allowlist",
		DefaultLayout:   "extend",
		LidClosedLayout: "external-only",
		Mirror: Mirror{
			Source:               "primary-external",
			FallbackMaxRefreshHz: 60,
		},
		Audio:    Audio{Policy: "prefer-display", Restore: true},
		Network:  Network{Policy: "prefer-wired"},
		Power:    Power{OnACProfile: "performance", Restore: true, KeepAwakeWhileDocked: true},
		Security: Security{},
		Wake:     Wake{Enabled: false},
		Displays: []DisplayRule{},
	}
}

// Decode validates current configuration and upgrades supported older schemas.
func Decode(data []byte) (Config, bool, error) {
	var header struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return Config{}, false, err
	}
	if header.SchemaVersion != 1 && header.SchemaVersion != SchemaVersion {
		return Config{}, false, fmt.Errorf("unsupported schema version %d", header.SchemaVersion)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, err
	}
	migrated := cfg.SchemaVersion == 1
	if migrated {
		if cfg.Revision == 0 {
			return Config{}, false, errors.New("revision must be greater than zero")
		}
		cfg.SchemaVersion = SchemaVersion
		cfg.Revision++
		cfg.Power.KeepAwakeWhileDocked = true
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, false, err
	}
	return cfg, migrated, nil
}

func (c Config) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", c.SchemaVersion)
	}
	if c.Revision == 0 {
		return errors.New("revision must be greater than zero")
	}
	if !slices.Contains([]string{"allowlist", "all", "off"}, c.Detection) {
		return fmt.Errorf("invalid detection policy %q", c.Detection)
	}
	for field, layout := range map[string]string{"defaultLayout": c.DefaultLayout, "lidClosedLayout": c.LidClosedLayout} {
		if !slices.Contains([]string{"extend", "mirror", "external-only", "internal-only"}, layout) {
			return fmt.Errorf("invalid %s %q", field, layout)
		}
	}
	if c.Mirror.Source != "primary-external" || c.Mirror.FallbackMaxRefreshHz < 24 || c.Mirror.FallbackMaxRefreshHz > 240 {
		return errors.New("invalid mirror configuration")
	}
	if !slices.Contains([]string{"prefer-display", "unchanged"}, c.Audio.Policy) {
		return fmt.Errorf("invalid audio policy %q", c.Audio.Policy)
	}
	if !slices.Contains([]string{"prefer-wired", "wired-only", "unchanged"}, c.Network.Policy) {
		return fmt.Errorf("invalid network policy %q", c.Network.Policy)
	}
	if !slices.Contains([]string{"performance", "balanced", "power-saver", "unchanged"}, c.Power.OnACProfile) {
		return fmt.Errorf("invalid power profile %q", c.Power.OnACProfile)
	}
	seen := make(map[string]struct{}, len(c.Displays))
	for i, display := range c.Displays {
		if display.ID == "" {
			return fmt.Errorf("display %d has an empty id", i)
		}
		if _, ok := seen[display.ID]; ok {
			return fmt.Errorf("duplicate display id %q", display.ID)
		}
		seen[display.ID] = struct{}{}
		if display.Scale < 0.5 || display.Scale > 4 {
			return fmt.Errorf("display %q has invalid scale", display.ID)
		}
		if !slices.Contains([]string{"left", "right", "above", "below"}, display.Position) {
			return fmt.Errorf("display %q has invalid position", display.ID)
		}
	}
	return nil
}

func (c Config) Rule(id string) (DisplayRule, bool) {
	for _, rule := range c.Displays {
		if rule.ID == id {
			return rule, true
		}
	}
	return DisplayRule{}, false
}
