package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zakijss/minideck/ui"
)

func TestDescribe(t *testing.T) {
	for _, tc := range []struct {
		comm string
		args []string
		name string
		prot bool
	}{
		{"tailscaled", []string{"/mnt/SDCARD/App/MiniScale/bin/tailscaled", "--tun=userspace-networking"}, "MiniScale (Tailscale)", false},
		{"minitape", []string{"/mnt/SDCARD/App/MiniTape/minitape", "daemon"}, "MiniTape player", false},
		{"MainUI", []string{"./MainUI"}, "Onion menu", true},
		{"sh", []string{"/bin/sh", "/mnt/SDCARD/.tmp_update/runtime.sh"}, "Onion runtime", true},
		{"sh", []string{"sh", "/mnt/SDCARD/App/MiniScale/miniscale.sh", "saver"}, "MiniScale (saver)", false},
		{"dropbear", []string{"dropbear", "-R"}, "dropbear", true},
		{"dropbearmulti", []string{"./dropbearmulti", "dropbear", "-s"}, "dropbearmulti", false},
		{"sleep", []string{"sleep", "300"}, "sleep", false},
	} {
		name, prot := describe(Proc{Comm: tc.comm, Args: tc.args})
		if name != tc.name || (prot != "") != tc.prot {
			t.Errorf("%v: got %q protected=%q", tc.args, name, prot)
		}
	}
}

func TestStartupToggle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASKS_SYSDIR", dir)
	os.MkdirAll(filepath.Join(dir, "startup"), 0755)
	os.WriteFile(filepath.Join(dir, "startup", "custom.sh"), []byte("#!/bin/sh\n# Mounts the extra drive. More text.\necho hi\n"), 0644)
	items := StartItems()
	if len(items) != 1 || !items[0].On || items[0].Desc != "Mounts the extra drive." {
		t.Fatalf("items: %+v", items)
	}
	if err := items[0].Toggle(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "startup", "custom.sh.off")); err != nil {
		t.Fatal("not turned off")
	}
	items = StartItems()
	if items[0].On {
		t.Fatal("still on")
	}
	items[0].Toggle()
	if _, err := os.Stat(filepath.Join(dir, "startup", "custom.sh")); err != nil {
		t.Fatal("not turned back on")
	}
}

// Holding a button acts once, except on the d-pad, which scrolls.
func TestActs(t *testing.T) {
	for _, tc := range []struct {
		ev   ui.Event
		want bool
	}{
		{ui.Event{Button: ui.A, Pressed: true}, true},
		{ui.Event{Button: ui.A, Pressed: true, Repeat: true}, false},
		{ui.Event{Button: ui.A}, false}, // release
		{ui.Event{Button: ui.L1, Pressed: true, Repeat: true}, false},
		{ui.Event{Button: ui.R1, Pressed: true, Repeat: true}, false},
		{ui.Event{Button: ui.X, Pressed: true, Repeat: true}, false},
		{ui.Event{Button: ui.B, Pressed: true, Repeat: true}, false},
		{ui.Event{Button: ui.Down, Pressed: true, Repeat: true}, true},
		{ui.Event{Button: ui.Up, Pressed: true, Repeat: true}, true},
		{ui.Event{Button: ui.Left, Pressed: true, Repeat: true}, true},
		{ui.Event{Button: ui.Right, Pressed: true, Repeat: true}, true},
	} {
		if got := acts(tc.ev); got != tc.want {
			t.Errorf("%+v: got %v", tc.ev, got)
		}
	}
}
