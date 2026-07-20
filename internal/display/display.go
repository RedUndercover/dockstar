package display

import (
	"context"
	"errors"
	"time"

	"github.com/redundercover/dockstar/internal/config"
)

var ErrNoApprovedDisplay = errors.New("no approved external display is connected")

type Mode struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	Refresh float64 `json:"refreshHz"`
}

type Output struct {
	ID                int      `json:"id"`
	DeviceID          string   `json:"deviceId"`
	UUID              string   `json:"uuid,omitempty"`
	Connector         string   `json:"connector"`
	Label             string   `json:"label"`
	Panel             bool     `json:"panel"`
	Connected         bool     `json:"connected"`
	Enabled           bool     `json:"enabled"`
	Approved          bool     `json:"approved"`
	Priority          uint32   `json:"priority"`
	Scale             float64  `json:"scale"`
	X                 int      `json:"x"`
	Y                 int      `json:"y"`
	Width             int      `json:"width"`
	Height            int      `json:"height"`
	CurrentModeID     string   `json:"currentModeId"`
	PreferredModeIDs  []string `json:"preferredModeIds"`
	Modes             []Mode   `json:"modes"`
	ReplicationSource int      `json:"replicationSource"`
}

func (o Output) PreferredMode() Mode {
	for _, preferred := range o.PreferredModeIDs {
		for _, mode := range o.Modes {
			if mode.ID == preferred {
				return mode
			}
		}
	}
	for _, mode := range o.Modes {
		if mode.ID == o.CurrentModeID {
			return mode
		}
	}
	if len(o.Modes) > 0 {
		return o.Modes[0]
	}
	return Mode{}
}

type Snapshot struct {
	Outputs []Output  `json:"outputs"`
	At      time.Time `json:"at"`
}

type ApplyResult struct {
	Layout  string `json:"layout"`
	Primary string `json:"primary"`
}

type Backend interface {
	Snapshot(context.Context, config.Config) (Snapshot, error)
	Apply(context.Context, config.Config, string) (ApplyResult, error)
	DPMSOn(context.Context) error
	Name() string
}
