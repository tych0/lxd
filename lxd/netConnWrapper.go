package main

import (
	"io"
	"fmt"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

type WebsocketToNetConn struct {
	conn *websocket.Conn
	cur  io.Reader
}

func (c *WebsocketToNetConn) Read(b []byte) (int, error) {
	if c.cur == nil {
		mt, r, err := c.conn.NextReader()
		if err != nil {
			return 0, err
		}

		if mt != websocket.BinaryMessage {
			return 0, fmt.Errorf("got bad message type from wrapped websocket")
		}

		c.cur = r
	}

	n, err := c.cur.Read(b)
	if n < len(b) {
		c.cur = nil
	}

	return n, err
}

func (c *WebsocketToNetConn) Write(b []byte) (int, error) {
	w, err := c.conn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return 0, err
	}

	n, err := w.Write(b)
	if err != nil {
		return n, err
	}

	return n, w.Close()
}

func (c *WebsocketToNetConn) Close() error {
	return c.conn.Close()
}

func (c *WebsocketToNetConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *WebsocketToNetConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *WebsocketToNetConn) SetDeadline(t time.Time) error {
	err := c.SetReadDeadline(t)
	if err != nil {
		return err
	}

	return c.SetWriteDeadline(t)
}

func (c *WebsocketToNetConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *WebsocketToNetConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}
