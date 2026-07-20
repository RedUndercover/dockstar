package ipc

import (
	"context"

	"github.com/godbus/dbus/v5"
)

type Client struct {
	conn *dbus.Conn
	obj  dbus.BusObject
}

func Connect() (*Client, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, obj: conn.Object(BusName, Path)}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) Status(ctx context.Context) (string, error) {
	var value string
	err := c.obj.CallWithContext(ctx, Interface+".GetStatus", 0).Store(&value)
	return value, err
}

func (c *Client) Config(ctx context.Context) (string, uint64, error) {
	var value string
	var revision uint64
	err := c.obj.CallWithContext(ctx, Interface+".GetConfig", 0).Store(&value, &revision)
	return value, revision, err
}

func (c *Client) UpdateConfig(ctx context.Context, value string, revision uint64) (uint64, error) {
	var next uint64
	err := c.obj.CallWithContext(ctx, Interface+".UpdateConfig", 0, value, revision).Store(&next)
	return next, err
}

func (c *Client) Call(ctx context.Context, method string, args ...any) error {
	return c.obj.CallWithContext(ctx, Interface+"."+method, 0, args...).Err
}

func (c *Client) JSONCall(ctx context.Context, method string) (string, error) {
	var value string
	err := c.obj.CallWithContext(ctx, Interface+"."+method, 0).Store(&value)
	return value, err
}
