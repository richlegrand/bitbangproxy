package protocol

import (
	"testing"
)

func TestBuildAndParseFrame(t *testing.T) {
	payload := []byte("hello world")
	raw := BuildFrame(42, FlagSYN, payload)

	frame, err := ParseFrame(raw)
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}

	if frame.StreamID != 42 {
		t.Errorf("StreamID = %d, want 42", frame.StreamID)
	}
	if frame.Flags != FlagSYN {
		t.Errorf("Flags = %d, want %d", frame.Flags, FlagSYN)
	}
	if string(frame.Payload) != "hello world" {
		t.Errorf("Payload = %q, want %q", frame.Payload, "hello world")
	}
}

func TestBuildAndParseEmptyPayload(t *testing.T) {
	raw := BuildFrame(1, FlagFIN, nil)

	frame, err := ParseFrame(raw)
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}

	if frame.StreamID != 1 {
		t.Errorf("StreamID = %d, want 1", frame.StreamID)
	}
	if !frame.IsFIN() {
		t.Error("expected FIN flag")
	}
	if len(frame.Payload) != 0 {
		t.Errorf("Payload length = %d, want 0", len(frame.Payload))
	}
}

func TestSYNFINFrame(t *testing.T) {
	raw := BuildFrame(5, FlagSYN|FlagFIN, []byte("{}"))

	frame, err := ParseFrame(raw)
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}

	if !frame.IsSYN() {
		t.Error("expected SYN flag")
	}
	if !frame.IsFIN() {
		t.Error("expected FIN flag")
	}
}

func TestParseRequest(t *testing.T) {
	payload := []byte(`{"method":"GET","pathname":"/api/status"}`)
	raw := BuildFrame(1, FlagSYN|FlagFIN, payload)

	frame, err := ParseFrame(raw)
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}

	req, err := frame.ParseRequest()
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}

	if req.Method != "GET" {
		t.Errorf("Method = %q, want %q", req.Method, "GET")
	}
	if req.Pathname != "/api/status" {
		t.Errorf("Pathname = %q, want %q", req.Pathname, "/api/status")
	}
}

func TestBuildResponseFrames(t *testing.T) {
	headers := map[string]string{"Content-Type": "text/plain"}
	body := []byte("OK")

	frames := BuildResponseFrames(7, 200, headers, body)

	// Should be: SYN + DAT + FIN = 3 frames
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(frames))
	}

	// Check SYN frame
	syn, err := ParseFrame(frames[0])
	if err != nil {
		t.Fatalf("parse SYN: %v", err)
	}
	if !syn.IsSYN() {
		t.Error("first frame should be SYN")
	}
	if syn.StreamID != 7 {
		t.Errorf("StreamID = %d, want 7", syn.StreamID)
	}

	// Check DAT frame
	dat, err := ParseFrame(frames[1])
	if err != nil {
		t.Fatalf("parse DAT: %v", err)
	}
	if string(dat.Payload) != "OK" {
		t.Errorf("DAT payload = %q, want %q", dat.Payload, "OK")
	}

	// Check FIN frame
	fin, err := ParseFrame(frames[2])
	if err != nil {
		t.Fatalf("parse FIN: %v", err)
	}
	if !fin.IsFIN() {
		t.Error("last frame should be FIN")
	}
}

func TestBuildResponseFramesLargeBody(t *testing.T) {
	// Body larger than MaxChunkSize should split into multiple DAT frames
	body := make([]byte, MaxChunkSize+100)
	for i := range body {
		body[i] = byte(i % 256)
	}

	frames := BuildResponseFrames(1, 200, nil, body)

	// SYN + 2 DAT + FIN = 4 frames
	if len(frames) != 4 {
		t.Fatalf("got %d frames, want 4", len(frames))
	}

	// Verify DAT payloads reconstruct the body
	dat1, _ := ParseFrame(frames[1])
	dat2, _ := ParseFrame(frames[2])

	if len(dat1.Payload) != MaxChunkSize {
		t.Errorf("first DAT payload = %d bytes, want %d", len(dat1.Payload), MaxChunkSize)
	}
	if len(dat2.Payload) != 100 {
		t.Errorf("second DAT payload = %d bytes, want 100", len(dat2.Payload))
	}
}

func TestParseFrameTooShort(t *testing.T) {
	_, err := ParseFrame([]byte{1, 2, 3})
	if err == nil {
		t.Error("expected error for short frame")
	}
}
