package protocol

import (
	"bytes"
	"testing"
)

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	payload := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	in := Header{
		Version:     Version,
		Type:        PacketAudio,
		Codec:       CodecOpus,
		Channels:    Channels,
		SampleRate:  SampleRate,
		Sequence:    123456,
		TimestampUs: 9876543210,
	}

	raw, err := Marshal(nil, in, payload)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if len(raw) != HeaderSize+len(payload) {
		t.Fatalf("expected len %d, got %d", HeaderSize+len(payload), len(raw))
	}

	out, gotPayload, err := Unmarshal(raw)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if out.Type != in.Type || out.Codec != in.Codec || out.Sequence != in.Sequence || out.TimestampUs != in.TimestampUs || out.SampleRate != in.SampleRate {
		t.Fatalf("header mismatch: got %+v want %+v", out, in)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Fatalf("payload mismatch: got %v want %v", gotPayload, payload)
	}
}

func TestUnmarshalRejectsInvalidPackets(t *testing.T) {
	if _, _, err := Unmarshal([]byte("short")); err != ErrTooShort {
		t.Fatalf("expected ErrTooShort, got %v", err)
	}
	badMagic := make([]byte, HeaderSize)
	copy(badMagic, "XXXX")
	if _, _, err := Unmarshal(badMagic); err != ErrInvalidMagic {
		t.Fatalf("expected ErrInvalidMagic, got %v", err)
	}
	badVer := make([]byte, HeaderSize)
	copy(badVer, Magic)
	badVer[4] = 99
	if _, _, err := Unmarshal(badVer); err != ErrBadVersion {
		t.Fatalf("expected ErrBadVersion, got %v", err)
	}
}
