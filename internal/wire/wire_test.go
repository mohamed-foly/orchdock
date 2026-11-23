package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"sync"
	"testing"
	"testing/iotest"
)

var allTypes = []Type{Hello, Welcome, Output, Input, Resize, Signal, Exit, Ping, Pong, ReplayEnd, Attention, Status}

func TestRoundTripEveryType(t *testing.T) {
	for _, typ := range allTypes {
		t.Run(typ.String(), func(t *testing.T) {
			var buf bytes.Buffer
			payload := []byte(`{"k":"v"}`)
			if err := NewWriter(&buf).WriteFrame(typ, payload); err != nil {
				t.Fatal(err)
			}
			f, err := NewReader(&buf).Next()
			if err != nil {
				t.Fatal(err)
			}
			if f.Type != typ || !bytes.Equal(f.Payload, payload) {
				t.Fatalf("got %v %q", f.Type, f.Payload)
			}
		})
	}
}

func TestEmptyPayload(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteFrame(Ping, nil); err != nil {
		t.Fatal(err)
	}
	f, err := NewReader(&buf).Next()
	if err != nil || f.Type != Ping || len(f.Payload) != 0 {
		t.Fatalf("got %+v, %v", f, err)
	}
}

func TestJSONMessages(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	in := HelloMsg{Token: "tok", Client: "cli", Cols: 120, Rows: 32}
	if err := w.WriteJSON(Hello, in); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteJSON(Signal, SignalMsg{Signal: SigStopAgent, Text: "/exit\r"}); err != nil {
		t.Fatal(err)
	}
	r := NewReader(&buf)
	f, _ := r.Next()
	var h HelloMsg
	if err := f.JSON(&h); err != nil || h != in {
		t.Fatalf("hello: %+v %v", h, err)
	}
	f, _ = r.Next()
	var s SignalMsg
	if err := f.JSON(&s); err != nil || s.Signal != SigStopAgent || s.Text != "/exit\r" {
		t.Fatalf("signal: %+v %v", s, err)
	}
}

func TestBadJSON(t *testing.T) {
	f := Frame{Type: Hello, Payload: []byte("{nope")}
	var h HelloMsg
	if err := f.JSON(&h); err == nil {
		t.Fatal("want error")
	}
}

func TestSplitReads(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	want := [][]byte{[]byte("alpha"), nil, bytes.Repeat([]byte("z"), 70000), []byte("tail")}
	for _, p := range want {
		if err := w.WriteFrame(Output, p); err != nil {
			t.Fatal(err)
		}
	}
	readers := map[string]io.Reader{
		"one byte at a time": iotest.OneByteReader(bytes.NewReader(buf.Bytes())),
		"half reads":         iotest.HalfReader(bytes.NewReader(buf.Bytes())),
		"data then EOF":      iotest.DataErrReader(bytes.NewReader(buf.Bytes())),
	}
	for name, src := range readers {
		t.Run(name, func(t *testing.T) {
			r := NewReader(src)
			for i, p := range want {
				f, err := r.Next()
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if !bytes.Equal(f.Payload, p) {
					t.Fatalf("frame %d mismatch", i)
				}
			}
			if _, err := r.Next(); !errors.Is(err, io.EOF) {
				t.Fatalf("want EOF, got %v", err)
			}
		})
	}
}

func TestRandomChunking(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var buf bytes.Buffer
	w := NewWriter(&buf)
	var want [][]byte
	for i := 0; i < 200; i++ {
		p := make([]byte, rng.Intn(5000))
		rng.Read(p)
		want = append(want, p)
		if err := w.WriteFrame(Output, p); err != nil {
			t.Fatal(err)
		}
	}
	r := NewReader(&chunkReader{data: buf.Bytes(), rng: rng})
	for i, p := range want {
		f, err := r.Next()
		if err != nil || !bytes.Equal(f.Payload, p) {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
}

type chunkReader struct {
	data []byte
	rng  *rand.Rand
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := 1 + c.rng.Intn(17)
	n = min(n, len(p), len(c.data))
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

func TestTruncated(t *testing.T) {
	var buf bytes.Buffer
	if err := NewWriter(&buf).WriteFrame(Output, []byte("hello world")); err != nil {
		t.Fatal(err)
	}
	full := buf.Bytes()
	for _, cut := range []int{1, 3, 5, 8, len(full) - 1} {
		_, err := NewReader(bytes.NewReader(full[:cut])).Next()
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("cut at %d: got %v, want ErrUnexpectedEOF", cut, err)
		}
	}
	if _, err := NewReader(bytes.NewReader(nil)).Next(); !errors.Is(err, io.EOF) {
		t.Errorf("empty stream: got %v, want EOF", err)
	}
}

func TestOversizedAnnouncementRejected(t *testing.T) {
	hdr := make([]byte, headerSize)
	hdr[0] = byte(Output)
	binary.BigEndian.PutUint32(hdr[1:], MaxPayload+1)
	// No payload follows: the reader must fail on the header alone.
	_, err := NewReader(bytes.NewReader(hdr)).Next()
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("got %v", err)
	}
	// An unknown type announcing a huge frame is rejected as well.
	hdr[0] = 0xEE
	binary.BigEndian.PutUint32(hdr[1:], 0xFFFFFFFF)
	if _, err := NewReader(bytes.NewReader(hdr)).Next(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("unknown type: got %v", err)
	}
}

func TestWriteFrameTooLarge(t *testing.T) {
	var buf bytes.Buffer
	err := NewWriter(&buf).WriteFrame(Output, make([]byte, MaxPayload+1))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("got %v", err)
	}
	if buf.Len() != 0 {
		t.Fatal("nothing must be written for a rejected frame")
	}
	if err := NewWriter(&buf).WriteFrame(Output, make([]byte, MaxPayload)); err != nil {
		t.Fatalf("exactly MaxPayload must be accepted: %v", err)
	}
}

func TestWriteDataSplits(t *testing.T) {
	data := make([]byte, 2*MaxPayload+MaxPayload/2)
	rand.New(rand.NewSource(1)).Read(data)
	var buf bytes.Buffer
	if err := NewWriter(&buf).WriteData(Output, data); err != nil {
		t.Fatal(err)
	}
	r := NewReader(&buf)
	var got []byte
	frames := 0
	for {
		f, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if f.Type != Output || len(f.Payload) > MaxPayload {
			t.Fatalf("bad frame %v len %d", f.Type, len(f.Payload))
		}
		got = append(got, f.Payload...)
		frames++
	}
	if frames != 3 || !bytes.Equal(got, data) {
		t.Fatalf("frames=%d equal=%v", frames, bytes.Equal(got, data))
	}
}

func TestUnknownTypesSkipped(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	raw := func(typ byte, payload []byte) {
		hdr := make([]byte, headerSize)
		hdr[0] = typ
		binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
		buf.Write(hdr)
		buf.Write(payload)
	}
	_ = w.WriteFrame(Hello, []byte("1"))
	raw(0x00, []byte("ignored"))
	raw(0xFF, bytes.Repeat([]byte("x"), 100000))
	raw(0x7F, nil)
	_ = w.WriteFrame(Exit, []byte("2"))
	r := NewReader(iotest.OneByteReader(&buf))
	for _, want := range []Type{Hello, Exit} {
		f, err := r.Next()
		if err != nil || f.Type != want {
			t.Fatalf("want %v, got %v %v", want, f.Type, err)
		}
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestUnknownTypeTruncated(t *testing.T) {
	hdr := make([]byte, headerSize)
	hdr[0] = 0xEE
	binary.BigEndian.PutUint32(hdr[1:], 50)
	data := append(hdr, []byte("short")...)
	if _, err := NewReader(bytes.NewReader(data)).Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v", err)
	}
}

func TestConcurrentWritersDoNotInterleave(t *testing.T) {
	var buf safeBuffer
	w := NewWriter(&buf)
	const writers, perWriter = 8, 200
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id byte) {
			defer wg.Done()
			payload := bytes.Repeat([]byte{id}, 3000)
			for j := 0; j < perWriter; j++ {
				if err := w.WriteFrame(Output, payload); err != nil {
					t.Error(err)
					return
				}
			}
		}(byte('a' + i))
	}
	wg.Wait()
	r := NewReader(bytes.NewReader(buf.Bytes()))
	n := 0
	for {
		f, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range f.Payload {
			if b != f.Payload[0] {
				t.Fatal("interleaved frame")
			}
		}
		n++
	}
	if n != writers*perWriter {
		t.Fatalf("got %d frames", n)
	}
}

// safeBuffer splits large writes so that interleaving would show up if the
// Writer did not serialize whole frames.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) Bytes() []byte { return s.buf.Bytes() }

func TestTypeStringAndKnown(t *testing.T) {
	if Output.String() != "OUTPUT" || !Output.Known() {
		t.Fatal("OUTPUT")
	}
	if Type(0xEE).Known() || Type(0xEE).String() != "TYPE(0xEE)" {
		t.Fatal("unknown type")
	}
	for _, typ := range allTypes {
		if !typ.Known() {
			t.Errorf("%v must be known", typ)
		}
	}
}
