package host

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/mohamed-foly/orchdock/internal/wire"
)

// Conn is an attached viewer connection to a host.
type Conn struct {
	net.Conn
	R       *wire.Reader
	W       *wire.Writer
	Welcome wire.WelcomeMsg
}

// Dial connects to the host described by info and completes the handshake.
// The token from info is filled in; hello carries the client kind and size.
func Dial(info Info, hello wire.HelloMsg) (*Conn, error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(info.Port))
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	hello.Token = info.Token
	conn := &Conn{Conn: c, R: wire.NewReader(c), W: wire.NewWriter(c)}
	if err := conn.W.WriteJSON(wire.Hello, hello); err != nil {
		_ = c.Close()
		return nil, err
	}
	_ = c.SetReadDeadline(time.Now().Add(helloTimeout))
	f, err := conn.R.Next()
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("host did not accept the connection: %w", err)
	}
	if f.Type != wire.Welcome {
		_ = c.Close()
		return nil, fmt.Errorf("unexpected first frame %s", f.Type)
	}
	if err := f.JSON(&conn.Welcome); err != nil {
		_ = c.Close()
		return nil, err
	}
	return conn, nil
}

// ErrNoHost means no live host serves the run directory.
var ErrNoHost = errors.New("no live host")

// Alive reports whether the host of dir answers a ping.
func Alive(dir string) bool {
	info, err := ReadInfo(dir)
	if err != nil {
		return false
	}
	return Ping(info) == nil
}

// Ping checks that the host answers.
func Ping(info Info) error {
	c, err := Dial(info, wire.HelloMsg{Client: wire.ClientService})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoHost, err)
	}
	defer c.Close()
	if err := c.W.WriteFrame(wire.Ping, nil); err != nil {
		return err
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		f, err := c.R.Next()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNoHost, err)
		}
		if f.Type == wire.Pong {
			return nil
		}
	}
}

// SendSignal asks the host of dir to interrupt, stop or kill its command. It
// returns once the host has handled the request: a ping follows the signal, and
// closing the connection before the host answers could make the operating
// system discard the signal.
func SendSignal(dir string, m wire.SignalMsg) error {
	return sendSynced(dir, func(c *Conn) error { return c.W.WriteJSON(wire.Signal, m) })
}

// SendInput types data into the terminal of the host of dir, as if a viewer had
// typed it. It returns once the host has received it.
func SendInput(dir string, data []byte) error {
	return sendSynced(dir, func(c *Conn) error { return c.W.WriteData(wire.Input, data) })
}

// sendSynced connects, runs send, then waits for the host to answer a ping so
// that what was sent has been handled before the connection closes.
func sendSynced(dir string, send func(*Conn) error) error {
	info, err := ReadInfo(dir)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoHost, err)
	}
	c, err := Dial(info, wire.HelloMsg{Client: wire.ClientService})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoHost, err)
	}
	defer c.Close()
	if err := send(c); err != nil {
		return err
	}
	if err := c.W.WriteFrame(wire.Ping, nil); err != nil {
		return err
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		f, err := c.R.Next()
		if err != nil {
			return nil // the host ended (a kill closes every connection): the signal was handled
		}
		if f.Type == wire.Pong {
			return nil
		}
	}
}
