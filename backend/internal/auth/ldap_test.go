package auth

import "testing"

func TestADGUIDToUUID(t *testing.T) {
	// AD stores the first three GUID fields little-endian; the tools display
	// {33221100-5544-7766-8899-aabbccddeeff} for these bytes.
	raw := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	got := ADGUIDToUUID(raw).String()
	want := "33221100-5544-7766-8899-aabbccddeeff"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
