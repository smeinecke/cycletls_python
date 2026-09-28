package main

import (
	"testing"

	utls "github.com/refraction-networking/utls"
)

// TestECHExtensionEmitsValidGREASEEncoding is a regression test for the
// "remote error: tls: error decoding message" handshake failures against
// strict TLS servers (Google, Cloudflare, Fastly).
//
// Profiles whose JA4_r extension list contains fe0d (65037, ECH) previously
// got a hardcoded 8-byte all-zero placeholder from NewCustomECHExtension.
// That is not a valid ECHClientHello in any draft, and BoringSSL-class
// parsers reject the whole ClientHello with a decodeError alert.
// The JA3 extension map already uses utls.BoringGREASEECH(); the JA4R path
// must emit the same kind of structurally valid GREASE ECH.
func TestECHExtensionEmitsValidGREASEEncoding(t *testing.T) {
	ext := CreateExtensionFromID(0xfe0d, utls.VersionTLS13, nil, false, "example.com")
	if ext == nil {
		t.Fatal("CreateExtensionFromID(0xfe0d) returned nil")
	}
	if _, ok := ext.(*utls.GREASEEncryptedClientHelloExtension); !ok {
		t.Fatalf("expected *utls.GREASEEncryptedClientHelloExtension, got %T", ext)
	}

	buf := make([]byte, ext.Len())
	if _, err := ext.Read(buf); err != nil && err.Error() != "EOF" {
		t.Fatalf("marshal failed: %v", err)
	}

	// Wire layout: ext id (2) | ext len (2) | ECHClientHello
	if buf[0] != 0xfe || buf[1] != 0x0d {
		t.Fatalf("wrong extension id: %02x%02x", buf[0], buf[1])
	}
	declared := int(buf[2])<<8 | int(buf[3])
	data := buf[4:]
	if declared != len(data) {
		t.Fatalf("extension length mismatch: declared %d, wrote %d", declared, len(data))
	}
	allZero := true
	for _, b := range data {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("ECH extension payload is all zero bytes — strict servers reject this")
	}
	// ECHClientHello must start with a type byte: outer(0) or inner(1).
	if data[0] != 0x00 {
		t.Fatalf("ECHClientHello type byte = %02x, expected outer(0)", data[0])
	}
}

// TestSupportedGroupsNoDuplicates is a regression test for the duplicated
// X25519 (0x001d) emitted on the JA4R path: the TLS 1.3 branch prepended
// X25519 to a list that already started with X25519, producing
// [1d 1d 17 18]. It now prepends the post-quantum hybrid X25519MLKEM768
// (0x11ec), matching real Chrome hellos.
func TestSupportedGroupsNoDuplicates(t *testing.T) {
	ext := CreateExtensionFromID(0x000a, utls.VersionTLS13, nil, false, "example.com")
	sc, ok := ext.(*utls.SupportedCurvesExtension)
	if !ok {
		t.Fatalf("expected *utls.SupportedCurvesExtension, got %T", ext)
	}
	seen := map[utls.CurveID]bool{}
	for _, c := range sc.Curves {
		if seen[c] {
			t.Fatalf("duplicate group %04x in %v", uint16(c), sc.Curves)
		}
		seen[c] = true
	}
	if sc.Curves[0] != utls.X25519MLKEM768 {
		t.Errorf("TLS 1.3 group list should lead with X25519MLKEM768, got %04x", uint16(sc.Curves[0]))
	}
}

// TestJA4RSpecMarshalsCleanly builds a full ClientHelloSpec from a JA4_r
// string that includes fe0d and marshals every extension, asserting the
// extension list is internally consistent (no zero-length garbage that
// strict parsers reject).
func TestJA4RSpecMarshalsCleanly(t *testing.T) {
	ja4r := "t13d1717h2_002f,0035,009c,009d,1301,1302,1303,c009,c00a,c013,c014,c02b,c02c,c02f,c030,cca8,cca9_0005,000a,000b,000d,0012,0017,001b,001c,0022,0023,002b,002d,0033,fe0d,ff01_0403,0503,0603,0804,0805,0806,0401,0501,0601,0203,0201"
	spec, err := JA4RStringToSpec(ja4r, "test-agent", false, false, "example.com")
	if err != nil {
		t.Fatalf("JA4RStringToSpec failed: %v", err)
	}
	for i, ext := range spec.Extensions {
		l := ext.Len()
		buf := make([]byte, l)
		n, err := ext.Read(buf)
		if err != nil && err.Error() != "EOF" {
			t.Fatalf("extension %d (type %T) failed to marshal: %v", i, ext, err)
		}
		if n != l {
			t.Errorf("extension %d (type %T): Len()=%d but Read() wrote %d — length mismatch corrupts the hello", i, ext, l, n)
		}
	}
}
