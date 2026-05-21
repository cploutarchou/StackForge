package osdetect

import "testing"

func TestParseSupportedOS(t *testing.T) {
	if _, err := ParseOSRelease("ID=ubuntu\nVERSION_ID=\"24.04\"\n"); err != nil {
		t.Fatalf("expected supported OS: %v", err)
	}
}

func TestParseUnsupportedOS(t *testing.T) {
	if _, err := ParseOSRelease("ID=centos\nVERSION_ID=\"9\"\n"); err == nil {
		t.Fatal("expected unsupported OS error")
	}
}
