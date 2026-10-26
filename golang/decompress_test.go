package main

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/andybalholm/brotli"
)

// TestUnBrotliDataValid pins brotli round-trip through both the direct
// decoder and the Content-Encoding dispatcher.
func TestUnBrotliDataValid(t *testing.T) {
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	if _, err := w.Write([]byte("hello cycle")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := unBrotliData(buf.Bytes())
	if err != nil {
		t.Fatalf("valid brotli: %v", err)
	}
	if string(got) != "hello cycle" {
		t.Fatalf("got %q", got)
	}

	decoded := DecompressBody(buf.Bytes(), []string{"br"}, nil)
	if string(decoded) != "hello cycle" {
		t.Fatalf("DecompressBody got %q", decoded)
	}
}

// TestUnBrotliDataInvalidDoesNotPanic is the HTTP2Shell/CVE-2026-111497
// regression: malformed brotli must surface an error, not panic, and
// DecompressBody must fall back to the original body.
func TestUnBrotliDataInvalidDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("invalid brotli panicked: %v", r)
		}
	}()

	inputs := [][]byte{
		{},
		{0x00},
		[]byte("not brotli"),
		{0x06, 0x00},
		bytes.Repeat([]byte{0xff}, 64),
	}
	for _, in := range inputs {
		got, err := unBrotliData(in)
		out := DecompressBody(in, []string{"br"}, nil)
		if err != nil {
			if !bytes.Equal(out, in) {
				t.Errorf("DecompressBody should return original body on error, got %q for %v (unz=%q err=%v)", out, in, got, err)
			}
		}
	}
}

// TestUnBrotliDataTrailingBytes is the andybalholm/brotli v1.2.x regression:
// on "brotli: excessive input" the decoder keeps decoded output in its
// internal ring buffer and returns n=0, so a plain io.ReadAll yields
// nothing. unBrotliData must keep reading until the buffer drains.
func TestUnBrotliDataTrailingBytes(t *testing.T) {
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	if _, err := w.Write([]byte("hello cycle")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := unBrotliData(append(buf.Bytes(), 0x0a))
	if err != nil {
		t.Fatalf("trailing-byte brotli: %v", err)
	}
	if string(got) != "hello cycle" {
		t.Fatalf("got %q", got)
	}
}

// TestGzipBombRejected caps decompression at maxDecompressedBody so a small
// malicious stream cannot inflate without bound.
func TestGzipBombRejected(t *testing.T) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	chunk := bytes.Repeat([]byte{0xAA}, 1<<20)
	for i := 0; i < int(maxDecompressedBody>>20)+2; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := gUnzipData(buf.Bytes()); err == nil {
		t.Fatal("expected size-limit error for oversized gzip body")
	}
	if out := DecompressBody(buf.Bytes(), []string{"gzip"}, nil); !bytes.Equal(out, buf.Bytes()) {
		t.Fatal("DecompressBody should return the original body when the cap is exceeded")
	}
}
