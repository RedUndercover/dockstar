package ipc

import (
	"context"
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
	"github.com/redundercover/dockstar/internal/config"
)

const (
	BusName   = "io.github.redundercover.Dockstar"
	Interface = "io.github.redundercover.Dockstar1"
	Path      = dbus.ObjectPath("/io/github/redundercover/Dockstar")
)

type Handler interface {
	StatusJSON() (string, error)
	ConfigJSON() (string, uint64, error)
	UpdateConfig(string, uint64) (uint64, error)
	SetLayout(context.Context, string) error
	ApproveDisplay(string, bool) error
	SetTrustPolicy(string) error
	Reload() error
	Evaluate(context.Context, bool) error
	WakeStatusJSON() (string, error)
	DiagnosticsJSON(context.Context) (string, error)
}

type Server struct {
	conn    *dbus.Conn
	handler Handler
}

func Listen(handler Handler) (*Server, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	reply, err := conn.RequestName(BusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return nil, fmt.Errorf("%s is already owned", BusName)
	}
	server := &Server{conn: conn, handler: handler}
	if err := conn.Export(server, Path, Interface); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.Export(introspectable, Path, "org.freedesktop.DBus.Introspectable"); err != nil {
		conn.Close()
		return nil, err
	}
	return server, nil
}

func (s *Server) Close() error { return s.conn.Close() }

func (s *Server) GetStatus() (string, *dbus.Error) {
	value, err := s.handler.StatusJSON()
	return value, dbusError(err)
}

func (s *Server) GetConfig() (string, uint64, *dbus.Error) {
	value, revision, err := s.handler.ConfigJSON()
	return value, revision, dbusError(err)
}

func (s *Server) UpdateConfig(value string, revision uint64) (uint64, *dbus.Error) {
	next, err := s.handler.UpdateConfig(value, revision)
	return next, dbusError(err)
}

func (s *Server) Apply() *dbus.Error {
	return dbusError(s.handler.Evaluate(context.Background(), true))
}

func (s *Server) SetLayout(layout string) *dbus.Error {
	return dbusError(s.handler.SetLayout(context.Background(), layout))
}

func (s *Server) ApproveDisplay(id string, allowed bool) *dbus.Error {
	return dbusError(s.handler.ApproveDisplay(id, allowed))
}

func (s *Server) SetTrustPolicy(policy string) *dbus.Error {
	return dbusError(s.handler.SetTrustPolicy(policy))
}

func (s *Server) Reload() *dbus.Error { return dbusError(s.handler.Reload()) }

func (s *Server) GetWakeStatus() (string, *dbus.Error) {
	value, err := s.handler.WakeStatusJSON()
	return value, dbusError(err)
}

func (s *Server) Diagnose() (string, *dbus.Error) {
	value, err := s.handler.DiagnosticsJSON(context.Background())
	return value, dbusError(err)
}

func (s *Server) StateChanged(value string) error {
	return s.conn.Emit(Path, Interface+".StateChanged", value)
}

func (s *Server) PendingDisplay(id, label string) error {
	return s.conn.Emit(Path, Interface+".PendingDisplay", id, label)
}

func (s *Server) ApplyFailed(message string) error {
	return s.conn.Emit(Path, Interface+".ApplyFailed", message)
}

func (s *Server) WakeCapabilitiesChanged(value string) error {
	return s.conn.Emit(Path, Interface+".WakeCapabilitiesChanged", value)
}

func dbusError(err error) *dbus.Error {
	if err == nil {
		return nil
	}
	name := Interface + ".Error.Failed"
	if errors.Is(err, config.ErrRevisionConflict) {
		name = Interface + ".Error.RevisionConflict"
	}
	return dbus.NewError(name, []any{err.Error()})
}

type introspection struct{}

func (introspection) Introspect() (string, *dbus.Error) { return introspectionXML, nil }

var introspectable introspection

const introspectionXML = `<!DOCTYPE node PUBLIC "-//freedesktop//DTD D-BUS Object Introspection 1.0//EN" "http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd">
<node>
  <interface name="io.github.redundercover.Dockstar1">
    <method name="GetStatus"><arg direction="out" type="s" name="statusJSON"/></method>
    <method name="GetConfig"><arg direction="out" type="s" name="configJSON"/><arg direction="out" type="t" name="revision"/></method>
    <method name="UpdateConfig"><arg direction="in" type="s" name="configJSON"/><arg direction="in" type="t" name="expectedRevision"/><arg direction="out" type="t" name="revision"/></method>
    <method name="Apply"/>
    <method name="SetLayout"><arg direction="in" type="s" name="layout"/></method>
    <method name="ApproveDisplay"><arg direction="in" type="s" name="id"/><arg direction="in" type="b" name="allowed"/></method>
    <method name="SetTrustPolicy"><arg direction="in" type="s" name="policy"/></method>
    <method name="Reload"/>
    <method name="GetWakeStatus"><arg direction="out" type="s" name="statusJSON"/></method>
    <method name="Diagnose"><arg direction="out" type="s" name="diagnosticsJSON"/></method>
    <signal name="StateChanged"><arg type="s" name="statusJSON"/></signal>
    <signal name="PendingDisplay"><arg type="s" name="id"/><arg type="s" name="label"/></signal>
    <signal name="ApplyFailed"><arg type="s" name="message"/></signal>
    <signal name="WakeCapabilitiesChanged"><arg type="s" name="statusJSON"/></signal>
  </interface>
</node>`
