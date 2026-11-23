// Package wire is the framed protocol spoken between a run's host process and
// its clients (the service, the CLI attach command, tests).
//
// A frame is: type (1 byte) | payload length (uint32, big endian) | payload.
// Payloads are raw bytes for terminal input and output, and JSON for control
// messages. Unknown frame types are skipped so that newer peers can add types
// without breaking older ones.
package wire

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Type identifies a frame.
type Type uint8

// Frame types. The direction is noted as c→h (client to host) or h→c.
const (
	Hello     Type = 0x01 // c→h JSON HelloMsg; must be the first frame
	Welcome   Type = 0x02 // h→c JSON WelcomeMsg
	Output    Type = 0x03 // h→c raw terminal output
	Input     Type = 0x04 // c→h raw terminal input
	Resize    Type = 0x05 // c→h JSON Resize
	Signal    Type = 0x06 // c→h JSON Signal
	Exit      Type = 0x07 // h→c JSON Exit
	Ping      Type = 0x08 // both, empty
	Pong      Type = 0x09 // both, empty
	ReplayEnd Type = 0x0A // h→c empty; scrollback replay finished
	Attention Type = 0x0B // h→c JSON Attention
	Status    Type = 0x0C // c→h empty request; h→c JSON Status reply
)

// MaxPayload is the largest payload a single frame may carry.
const MaxPayload = 1 << 20

// headerSize is the number of bytes before the payload.
const headerSize = 5

// Errors returned by the codec.
var (
	// ErrPayloadTooLarge is returned when writing a payload above MaxPayload.
	ErrPayloadTooLarge = errors.New("wire: payload too large")
	// ErrFrameTooLarge is returned when a peer announces a payload above MaxPayload.
	ErrFrameTooLarge = errors.New("wire: frame too large")
)

var names = map[Type]string{
	Hello: "HELLO", Welcome: "WELCOME", Output: "OUTPUT", Input: "INPUT",
	Resize: "RESIZE", Signal: "SIGNAL", Exit: "EXIT", Ping: "PING", Pong: "PONG",
	ReplayEnd: "REPLAY_END", Attention: "ATTENTION", Status: "STATUS",
}

// Known reports whether t is a frame type this version understands.
func (t Type) Known() bool {
	_, ok := names[t]
	return ok
}

func (t Type) String() string {
	if n, ok := names[t]; ok {
		return n
	}
	return fmt.Sprintf("TYPE(0x%02X)", uint8(t))
}

// Client kinds a HelloMsg may name. Only CLI and UI clients show the terminal
// to a person; the others (the service's pings and monitors) stay silent.
const (
	ClientCLI     = "cli"
	ClientUI      = "ui"
	ClientService = "service"
	ClientMonitor = "monitor"
)

// HelloMsg is the first frame a client sends.
type HelloMsg struct {
	Token  string `json:"token"`
	Client string `json:"client"` // ui | cli | service
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

// WelcomeMsg is the host's reply to a valid HelloMsg.
type WelcomeMsg struct {
	RunID     string `json:"runId"`
	ChildPID  int    `json:"childPid"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	StartedAt string `json:"startedAt"`
}

// ResizeMsg asks the host to resize the terminal.
type ResizeMsg struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// Signal names.
const (
	SigInterrupt = "interrupt"
	SigStop      = "stop"
	SigStopAgent = "stop-agent"
	SigKill      = "kill"
)

// SignalMsg asks the host to interrupt or stop the child.
type SignalMsg struct {
	Signal string `json:"signal"`
	Text   string `json:"text,omitempty"` // optional text sent before a custom stop
}

// ExitMsg tells clients that the child exited.
type ExitMsg struct {
	ExitCode int    `json:"exitCode"`
	Reason   string `json:"reason"`
}

// AttentionMsg tells clients the run wants the user's attention.
type AttentionMsg struct {
	Reason string `json:"reason"`
}

// StatusMsg is the host's reply to a Status request.
type StatusMsg struct {
	ChildPID     int    `json:"childPid"`
	Alive        bool   `json:"alive"`
	Viewers      int    `json:"viewers"`
	BytesOut     int64  `json:"bytesOut"`
	LastOutputAt string `json:"lastOutputAt,omitempty"`
}

// Frame is one decoded frame.
type Frame struct {
	Type    Type
	Payload []byte
}

// JSON decodes the payload into v.
func (f Frame) JSON(v any) error {
	if err := json.Unmarshal(f.Payload, v); err != nil {
		return fmt.Errorf("wire: decode %s: %w", f.Type, err)
	}
	return nil
}

// Writer encodes frames. It is safe for concurrent use: each frame is written
// with a single Write call under a lock, so frames never interleave.
type Writer struct {
	mu sync.Mutex
	w  io.Writer
}

// NewWriter returns a Writer on w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// WriteFrame writes one frame. Payloads above MaxPayload are rejected.
func (w *Writer) WriteFrame(t Type, payload []byte) error {
	if len(payload) > MaxPayload {
		return ErrPayloadTooLarge
	}
	buf := make([]byte, headerSize+len(payload))
	buf[0] = byte(t)
	binary.BigEndian.PutUint32(buf[1:headerSize], uint32(len(payload)))
	copy(buf[headerSize:], payload)
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.w.Write(buf)
	return err
}

// WriteJSON writes v as the JSON payload of a frame of type t.
func (w *Writer) WriteJSON(t Type, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("wire: encode %s: %w", t, err)
	}
	return w.WriteFrame(t, b)
}

// WriteData writes raw bytes as one or more frames of type t, splitting data
// into chunks of at most MaxPayload. It is meant for Output and Input.
func (w *Writer) WriteData(t Type, data []byte) error {
	for len(data) > MaxPayload {
		if err := w.WriteFrame(t, data[:MaxPayload]); err != nil {
			return err
		}
		data = data[MaxPayload:]
	}
	return w.WriteFrame(t, data)
}

// Reader decodes frames.
type Reader struct {
	r *bufio.Reader
}

// NewReader returns a Reader on r.
func NewReader(r io.Reader) *Reader {
	if br, ok := r.(*bufio.Reader); ok {
		return &Reader{r: br}
	}
	return &Reader{r: bufio.NewReaderSize(r, 64<<10)}
}

// Next returns the next frame of a known type, skipping unknown types.
// It returns io.EOF at a clean frame boundary and io.ErrUnexpectedEOF when
// the stream ends inside a frame. Announced payloads above MaxPayload yield
// ErrFrameTooLarge before any allocation.
func (r *Reader) Next() (Frame, error) {
	for {
		var hdr [headerSize]byte
		if _, err := io.ReadFull(r.r, hdr[:]); err != nil {
			return Frame{}, err // io.EOF at a boundary, io.ErrUnexpectedEOF inside
		}
		t := Type(hdr[0])
		n := binary.BigEndian.Uint32(hdr[1:])
		if n > MaxPayload {
			return Frame{}, fmt.Errorf("%w: %s announces %d bytes", ErrFrameTooLarge, t, n)
		}
		if !t.Known() {
			if _, err := r.r.Discard(int(n)); err != nil {
				return Frame{}, unexpected(err)
			}
			continue
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(r.r, payload); err != nil {
			return Frame{}, unexpected(err)
		}
		return Frame{Type: t, Payload: payload}, nil
	}
}

// unexpected turns an EOF inside a frame into io.ErrUnexpectedEOF.
func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
