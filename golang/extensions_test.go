package main

import (
	"encoding/binary"
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
	spec, err := JA4RStringToSpec(ja4r, "test-agent", false, false, "example.com", "")
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

// extIDs marshals each extension and returns the wire IDs in emission order.
func extIDs(t *testing.T, spec *utls.ClientHelloSpec) []uint16 {
	t.Helper()
	ids := make([]uint16, len(spec.Extensions))
	for i, ext := range spec.Extensions {
		buf := make([]byte, ext.Len())
		if n, err := ext.Read(buf); err != nil && err.Error() != "EOF" {
			t.Fatalf("extension %d (%T) marshal failed: %v", i, ext, err)
		} else if n >= 2 {
			ids[i] = binary.BigEndian.Uint16(buf[:2])
		}
	}
	return ids
}

// TestJA4RSpecRestoresJA3WireOrder verifies that when the profile's JA3 is
// passed as an ordering hint, the emitted extension and cipher order match
// the browser capture instead of JA4_r's sorted order. Firefox 148's JA3
// puts SNI first and ECH last; Chrome shuffles its extension order per
// connection, so either way the emitted order must follow the JA3, not the
// sorted JA4_r list.
func TestJA4RSpecRestoresJA3WireOrder(t *testing.T) {
	ja4r := "t13d1516h2_002f,0035,009c,009d,1301,1302,1303,c013,c014,c02b,c02c,c02f,c030,cca8,cca9_0005,000a,000b,000d,0012,0017,001b,0023,002b,002d,0033,44cd,fe0d,ff01_0403,0804,0401,0503,0805,0501,0806,0601"
	// Firefox-style JA3: sni, ems, reneg, groups, points, ticket, alpn, ...
	ja3 := "771,4865-4867-4866-49195-49199-52393-52392-49196-49200-49162-49161-49171-49172-156-157-47-53,0-23-65281-10-11-35-16-5-34-18-51-43-13-45-28-27-65037,4588-29-23-24-25-256-257,0"

	spec, err := JA4RStringToSpec(ja4r, "test-agent", false, false, "example.com", ja3)
	if err != nil {
		t.Fatalf("JA4RStringToSpec failed: %v", err)
	}

	// JA3 ext order (decimal): 0,23,65281,10,11,35,16,5,34,18,51,43,13,45,28,27,65037
	// JA4_r provides: 0005,000a,000b,000d,0012,0017,001b,0023,002b,002d,0033,44cd,fe0d,ff01
	// plus auto-added SNI (0000) and ALPN (0010). After reordering, every
	// extension whose id appears in the JA3 list must appear in JA3 order.
	ja3Order := []uint16{0x0000, 0x0017, 0xff01, 0x000a, 0x000b, 0x0023, 0x0010, 0x0005, 0x0022, 0x0012, 0x0033, 0x002b, 0x000d, 0x002d, 0x001c, 0x001b, 0xfe0d}
	emitted := extIDs(t, spec)
	pos := map[uint16]int{}
	for i, id := range emitted {
		pos[id] = i
	}
	for i := 0; i+1 < len(ja3Order); i++ {
		a, b := ja3Order[i], ja3Order[i+1]
		ai, aOK := pos[a]
		bi, bOK := pos[b]
		if aOK && bOK && ai > bi {
			t.Fatalf("extension %04x emitted after %04x; emitted order: %v", a, b, emitted)
		}
	}
}

// TestJA4RSpecRestoresJA3CipherOrder checks that cipher suites follow the
// JA3 (wire) order rather than the JA4_r sorted order.
func TestJA4RSpecRestoresJA3CipherOrder(t *testing.T) {
	ja4r := "t13d1516h2_002f,0035,009c,009d,1301,1302,1303,c013_0005,000a,002b,0033_0403"
	// JA3 ciphers (decimal) deliberately list 0x1302 before 0x1301.
	ja3 := "771,4866-4865-156,0-10-43-5,29-23,0"
	spec, err := JA4RStringToSpec(ja4r, "test-agent", false, false, "example.com", ja3)
	if err != nil {
		t.Fatalf("JA4RStringToSpec failed: %v", err)
	}
	if len(spec.CipherSuites) < 2 || spec.CipherSuites[0] != 0x1302 || spec.CipherSuites[1] != 0x1301 {
		t.Fatalf("cipher order not restored from JA3: %v", spec.CipherSuites)
	}
}

// TestJA4RSpecWithoutJA3KeepsSortedOrder asserts the fallback: no JA3 hint
// means the JA4_r sorted order is preserved (previous behavior).
func TestJA4RSpecWithoutJA3KeepsSortedOrder(t *testing.T) {
	ja4r := "t13d1516h2_002f,0035,009c,009d,1301,1302,1303,c013_0005,000a,002b,0033_0403"
	spec, err := JA4RStringToSpec(ja4r, "test-agent", false, false, "example.com", "")
	if err != nil {
		t.Fatalf("JA4RStringToSpec failed: %v", err)
	}
	emitted := extIDs(t, spec)
	for i := 1; i < len(emitted); i++ {
		if emitted[i] < emitted[i-1] && emitted[i] != 0x0010 && emitted[i] != 0x0000 {
			t.Fatalf("without JA3 hint, extensions should stay JA4_r-sorted (SNI first/ALPN last aside): %v", emitted)
		}
	}
}

// TestJA4RSpecRestoresJA3Curves checks that supported_groups uses the JA3
// curves field (real browser list incl. P-521/FFDHE for Firefox) instead of
// the generic [MLKEM, X25519, P256, P384] default.
func TestJA4RSpecRestoresJA3Curves(t *testing.T) {
	ja4r := "t13d1516h2_002f,0035,009c,009d,1301,1302,1303,c013_0005,000a,002b,0033_0403"
	ja3 := "771,4865,0-10-43-5,4588-29-23-24-25-256-257,0"
	spec, err := JA4RStringToSpec(ja4r, "test-agent", false, false, "example.com", ja3)
	if err != nil {
		t.Fatalf("JA4RStringToSpec failed: %v", err)
	}
	for _, ext := range spec.Extensions {
		if sc, ok := ext.(*utls.SupportedCurvesExtension); ok {
			want := []utls.CurveID{0x11ec, 0x1d, 0x17, 0x18, 0x19, 0x100, 0x101}
			if len(sc.Curves) != len(want) {
				t.Fatalf("supported_groups = %v, want %v", sc.Curves, want)
			}
			for i := range want {
				if sc.Curves[i] != want[i] {
					t.Fatalf("supported_groups = %v, want %v", sc.Curves, want)
				}
			}
			return
		}
	}
	t.Fatal("no SupportedCurvesExtension in spec")
}

// TestJA4RSpecChromiumGrease checks that a Chromium user agent inserts the
// GREASE placeholders Chrome sends (uTLS randomizes them at ApplyPreset),
// while a Firefox UA or disableGrease emits none.
func TestJA4RSpecChromiumGrease(t *testing.T) {
	ja4r := "t13d1516h2_002f,0035,009c,009d,1301,1302,1303,c013_0005,000a,002b,0033_0403"
	spec, err := JA4RStringToSpec(ja4r, "Mozilla/5.0 Chrome/146.0.0.0", false, false, "example.com", "")
	if err != nil {
		t.Fatalf("JA4RStringToSpec failed: %v", err)
	}
	if len(spec.CipherSuites) == 0 || spec.CipherSuites[0] != 0x0a0a {
		t.Fatalf("expected GREASE cipher first, got %v", spec.CipherSuites)
	}
	if _, ok := spec.Extensions[0].(*utls.UtlsGREASEExtension); !ok {
		t.Fatalf("expected GREASE extension first, got %T", spec.Extensions[0])
	}
	if _, ok := spec.Extensions[len(spec.Extensions)-1].(*utls.UtlsGREASEExtension); !ok {
		t.Fatalf("expected GREASE extension last, got %T", spec.Extensions[len(spec.Extensions)-1])
	}

	spec, err = JA4RStringToSpec(ja4r, "Mozilla/5.0 Firefox/155.0", false, false, "example.com", "")
	if err != nil {
		t.Fatalf("JA4RStringToSpec failed: %v", err)
	}
	if len(spec.CipherSuites) == 0 || spec.CipherSuites[0] == 0x0a0a {
		t.Fatal("Firefox UA must not emit GREASE cipher")
	}
	for i, ext := range spec.Extensions {
		if _, ok := ext.(*utls.UtlsGREASEExtension); ok {
			t.Fatalf("Firefox UA must not emit GREASE extension (index %d)", i)
		}
	}

	spec, err = JA4RStringToSpec(ja4r, "Mozilla/5.0 Chrome/146.0.0.0", false, true, "example.com", "")
	if err != nil {
		t.Fatalf("JA4RStringToSpec failed: %v", err)
	}
	if len(spec.CipherSuites) == 0 || spec.CipherSuites[0] == 0x0a0a {
		t.Fatal("disableGrease must suppress the GREASE cipher")
	}
}

// TestKeyShareOffersPostQuantum verifies the TLS 1.3 JA4R path advertises an
// X25519MLKEM768 share, consistent with the supported_groups list that leads
// with the same hybrid group. uTLS fills in the key material at ApplyPreset.
func TestKeyShareOffersPostQuantum(t *testing.T) {
	ext := CreateExtensionFromID(0x0033, utls.VersionTLS13, nil, false, "example.com")
	ks, ok := ext.(*utls.KeyShareExtension)
	if !ok {
		t.Fatalf("expected *utls.KeyShareExtension, got %T", ext)
	}
	if len(ks.KeyShares) == 0 || ks.KeyShares[0].Group != utls.X25519MLKEM768 {
		t.Fatalf("first key share should be X25519MLKEM768, got %v", ks.KeyShares)
	}
}
