package wake

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHelperRecordsAndRestoresBaseline(t *testing.T) {
	root := t.TempDir()
	device := filepath.Join(root, "sys/bus/pci/devices/0000:00:02.0")
	if err := os.MkdirAll(filepath.Join(device, "power"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"class": "0x030000\n", "vendor": "0x8086\n", "device": "0x7d55\n", "power/wakeup": "disabled\n",
	} {
		if err := os.WriteFile(filepath.Join(device, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Helper("enable", root); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(device, "power/wakeup"), "enabled")
	status, err := GetStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !status.Enabled || len(status.Sources) != 1 {
		t.Fatalf("status = %#v", status)
	}
	if err := Helper("restore", root); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(device, "power/wakeup"), "disabled")
}

func TestHelperRejectsUnknownAction(t *testing.T) {
	if err := Helper("surprise", t.TempDir()); err == nil {
		t.Fatal("unknown helper action should fail")
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}
