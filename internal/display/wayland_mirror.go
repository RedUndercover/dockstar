package display

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	"hazelnut.eclair.cafe/wlcl"

	"github.com/redundercover/dockstar/internal/config"
	"github.com/redundercover/dockstar/internal/protocol"
)

type waylandClient struct {
	protocol.WlRegistryStub
	protocol.KdeOutputDeviceRegistryV2Stub

	mu         sync.Mutex
	registry   protocol.WlRegistry
	management protocol.KdeOutputManagementV2
	devices    []*waylandDevice
}

type waylandDevice struct {
	protocol.KdeOutputDeviceV2Stub
	object  protocol.KdeOutputDeviceV2
	name    string
	uuid    string
	enabled bool
	modes   []*waylandMode
}

type waylandMode struct {
	protocol.KdeOutputDeviceModeV2Stub
	object    protocol.KdeOutputDeviceModeV2
	width     int
	height    int
	refresh   float64
	preferred bool
}

type applyWaiter struct {
	protocol.KdeOutputConfigurationV2Stub
	result chan error
	reason string
}

func (c *waylandClient) HandleWlRegistryGlobal(_ context.Context, name uint32, iface string, version uint32) {
	switch iface {
	case protocol.KdeOutputManagementV2Name:
		c.management = protocol.As[protocol.KdeOutputManagementV2](c.registry.Bind(name, iface, min(version, 22)))
	case protocol.KdeOutputDeviceRegistryV2Name:
		registry := protocol.As[protocol.KdeOutputDeviceRegistryV2](c.registry.Bind(name, iface, min(version, 24)))
		registry.SetUserData(c)
	}
}

func (c *waylandClient) HandleKdeOutputDeviceRegistryV2Output(_ context.Context, output protocol.KdeOutputDeviceV2) {
	device := &waylandDevice{object: output}
	output.SetUserData(device)
	c.mu.Lock()
	c.devices = append(c.devices, device)
	c.mu.Unlock()
}

func (d *waylandDevice) HandleKdeOutputDeviceV2Name(_ context.Context, name string) { d.name = name }
func (d *waylandDevice) HandleKdeOutputDeviceV2Uuid(_ context.Context, uuid string) { d.uuid = uuid }
func (d *waylandDevice) HandleKdeOutputDeviceV2Enabled(_ context.Context, enabled int32) {
	d.enabled = enabled != 0
}
func (d *waylandDevice) HandleKdeOutputDeviceV2Mode(_ context.Context, mode protocol.KdeOutputDeviceModeV2) {
	value := &waylandMode{object: mode}
	mode.SetUserData(value)
	d.modes = append(d.modes, value)
}
func (d *waylandDevice) HandleKdeOutputDeviceV2Removed(context.Context) { d.object.Release() }

func (m *waylandMode) HandleKdeOutputDeviceModeV2Size(_ context.Context, width, height int32) {
	m.width, m.height = int(width), int(height)
}
func (m *waylandMode) HandleKdeOutputDeviceModeV2Refresh(_ context.Context, refresh int32) {
	m.refresh = float64(refresh) / 1000
}
func (m *waylandMode) HandleKdeOutputDeviceModeV2Preferred(context.Context) { m.preferred = true }

func (w *applyWaiter) HandleKdeOutputConfigurationV2Applied(context.Context) {
	select {
	case w.result <- nil:
	default:
	}
}
func (w *applyWaiter) HandleKdeOutputConfigurationV2Failed(context.Context) {
	message := w.reason
	if message == "" {
		message = "KWin rejected the mirror configuration"
	}
	select {
	case w.result <- errors.New(message):
	default:
	}
}
func (w *applyWaiter) HandleKdeOutputConfigurationV2FailureReason(_ context.Context, reason string) {
	w.reason = reason
}

func applyWaylandMirror(ctx context.Context, snapshot Snapshot, cfg config.Config, panel, primary Output) error {
	conn, err := wlcl.Connect(ctx, "")
	if err != nil {
		return fmt.Errorf("connect to Wayland: %w", err)
	}
	defer conn.Close()
	client := &waylandClient{}
	displayObject := protocol.CreateDisplay(conn)
	client.registry = displayObject.GetRegistry()
	client.registry.SetUserData(client)
	if err := wlcl.Roundtrip(ctx, displayObject); err != nil {
		return fmt.Errorf("discover Wayland globals: %w", err)
	}
	if !client.management.IsSet() {
		return errors.New("KWin does not advertise kde_output_management_v2")
	}
	if err := wlcl.Roundtrip(ctx, displayObject); err != nil {
		return fmt.Errorf("discover output devices: %w", err)
	}
	if err := wlcl.Roundtrip(ctx, displayObject); err != nil {
		return fmt.Errorf("read output capabilities: %w", err)
	}

	devices := make(map[string]*waylandDevice, len(client.devices))
	for _, device := range client.devices {
		devices[device.name] = device
	}
	panelDevice, panelOK := devices[panel.Connector]
	primaryDevice, primaryOK := devices[primary.Connector]
	if !panelOK || !primaryOK || primaryDevice.uuid == "" {
		return errors.New("KWin output identity is incomplete; cannot mirror safely")
	}
	mode, ok := commonMode(panel, primary, float64(cfg.Mirror.FallbackMaxRefreshHz))
	if !ok {
		return errors.New("no common mirror mode is available")
	}
	panelMode := findWaylandMode(panelDevice, mode)
	primaryMode := findWaylandMode(primaryDevice, mode)
	if panelMode == nil || primaryMode == nil {
		return fmt.Errorf("KWin did not expose the selected %dx%d@%.2f mirror mode", mode.Width, mode.Height, mode.Refresh)
	}

	transaction := client.management.CreateConfiguration()
	waiter := &applyWaiter{result: make(chan error, 1)}
	transaction.SetUserData(waiter)
	for _, output := range snapshot.Outputs {
		device := devices[output.Connector]
		if device == nil {
			continue
		}
		enable := int32(0)
		if output.Connector == panel.Connector || output.Connector == primary.Connector {
			enable = 1
		}
		transaction.Enable(device.object, enable)
	}
	transaction.Mode(panelDevice.object, panelMode.object)
	transaction.Mode(primaryDevice.object, primaryMode.object)
	transaction.Position(panelDevice.object, 0, 0)
	transaction.Position(primaryDevice.object, 0, 0)
	transaction.Scale(panelDevice.object, 1)
	transaction.Scale(primaryDevice.object, 1)
	transaction.SetPriority(primaryDevice.object, 1)
	transaction.SetPriority(panelDevice.object, 2)
	transaction.SetReplicationSource(panelDevice.object, primaryDevice.uuid)
	transaction.SetReplicationSource(primaryDevice.object, "")
	transaction.Apply()
	for {
		select {
		case err := <-waiter.result:
			transaction.Destroy()
			return err
		default:
			if err := conn.Dispatch(ctx); err != nil {
				return fmt.Errorf("apply Wayland mirror transaction: %w", err)
			}
		}
	}
}

func findWaylandMode(device *waylandDevice, wanted Mode) *waylandMode {
	for _, mode := range device.modes {
		if mode.width == wanted.Width && mode.height == wanted.Height && math.Abs(mode.refresh-wanted.Refresh) < 1 {
			return mode
		}
	}
	return nil
}
