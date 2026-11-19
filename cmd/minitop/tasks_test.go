package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		{"minifin-player", []string{"/mnt/SDCARD/App/MiniFin/minifin-player", "--url", "x"}, "MiniFin video player", false},
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

func TestHandleJumps(t *testing.T) {
	l := ui.List{Items: make([]ui.Item, 10), Sel: 4}
	handle(&l, ui.Event{Button: ui.R2, Pressed: true})
	if l.Sel != 9 {
		t.Errorf("R2: Sel %d, want 9", l.Sel)
	}
	handle(&l, ui.Event{Button: ui.L2, Pressed: true})
	if l.Sel != 0 {
		t.Errorf("L2: Sel %d, want 0", l.Sel)
	}
	handle(&l, ui.Event{Button: ui.Down, Pressed: true})
	if l.Sel != 1 {
		t.Errorf("Down: Sel %d, want 1", l.Sel)
	}
	empty := ui.List{}
	handle(&empty, ui.Event{Button: ui.R2, Pressed: true})
	if empty.Sel != 0 {
		t.Errorf("R2 on an empty list: Sel %d", empty.Sel)
	}
}

func TestClockLine(t *testing.T) {
	for in, want := range map[string]string{
		"2026/11/11 10:00:01 github.com: i/o timeout\n2026/11/11 10:00:02 only 1 server(s) answered: clock not changed\n": "Only 1 server(s) answered: clock not changed",
		"no servers answered": "No servers answered",
		"":                    "",
	} {
		if got := clockLine([]byte(in)); got != want {
			t.Errorf("clockLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// End asks first and forces only programs it may force, and never signals a
// PID that now belongs to another program.
func TestEnd(t *testing.T) {
	termWait, gentleWait = 300*time.Millisecond, 300*time.Millisecond
	start := func(script string) (*exec.Cmd, Proc) {
		t.Helper()
		c := exec.Command("sh", "-c", script)
		if err := c.Start(); err != nil {
			t.Skip("no sh:", err)
		}
		t.Cleanup(func() { c.Process.Kill(); c.Wait() })
		time.Sleep(100 * time.Millisecond) // let the trap be set
		for _, p := range newSampler().Sample() {
			if p.PID == c.Process.Pid {
				return c, p
			}
		}
		t.Skip("no /proc")
		return nil, Proc{}
	}
	alive := func(p Proc) bool { return running(p.PID, p.start) }

	// quits on SIGTERM; left a zombie (not waited for) counts as gone
	_, p := start("sleep 30")
	if err := End(p); err != nil || alive(p) {
		t.Fatalf("sleep: %v, alive %v", err, alive(p))
	}
	// ignores SIGTERM: forced
	_, p = start("trap '' TERM; while :; do sleep 0.1; done")
	if err := End(p); err != nil || alive(p) {
		t.Fatalf("forced: %v, alive %v", err, alive(p))
	}
	// the same, as MiniFin's player: never forced
	_, p = start("trap '' TERM; while :; do sleep 0.1; done")
	p.Comm = "minifin-player"
	if err := End(p); !errors.Is(err, errStillRunning) || !alive(p) {
		t.Fatalf("player: %v, alive %v", err, alive(p))
	}
	// the PID now belongs to another program: left alone
	_, p = start("sleep 30")
	p.start++
	if err := End(p); err != nil || !running(p.PID, p.start-1) {
		t.Fatalf("reused PID: %v", err)
	}
}

func TestMemLine(t *testing.T) {
	m := parseMeminfo("MemTotal:         100352 kB\nMemFree:  1 kB\nMemAvailable:      46080 kB\nSwapTotal:        131072 kB\nSwapFree:         128000 kB\n")
	if got, want := memLine(m), "45.0 MB free of 98.0 MB · swap 3.0 of 128.0 MB"; got != want {
		t.Errorf("memLine = %q, want %q", got, want)
	}
	delete(m, "SwapTotal")
	if got, want := memLine(m), "45.0 MB free of 98.0 MB"; got != want {
		t.Errorf("no swap: %q, want %q", got, want)
	}
}

func TestToggleNote(t *testing.T) {
	if got := toggleNote(StartItem{Name: "clocksync", On: true}); !strings.Contains(got, "off from the next boot") {
		t.Errorf("off: %q", got)
	}
	if got := toggleNote(StartItem{Name: "clocksync"}); got != "clocksync is on from the next boot" {
		t.Errorf("on: %q", got)
	}
}
