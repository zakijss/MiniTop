package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zakijss/minideck/ui"
)

// No settings file, or one from before a setting existed, keeps today's
// behaviour: busiest first, Onion's programs listed.
func TestSettingsDefaults(t *testing.T) {
	dir := t.TempDir()
	if s := loadSettings(dir); s != (Settings{}) {
		t.Fatalf("no file: %+v", s)
	}
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"sort":`), 0644)
	if s := loadSettings(dir); s != (Settings{}) {
		t.Fatalf("damaged file: %+v", s)
	}
	want := Settings{Sort: "name", HideOnion: true}
	if err := saveSettings(dir, want); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(dir); got != want {
		t.Fatalf("saved %+v, read %+v", want, got)
	}
}

var sample = []Proc{ // as Sample gives them: busiest first
	{PID: 40, Name: "retroarch", CPU: 30, RSS: 40 << 20},
	{PID: 7, Name: "Onion menu", CPU: 5, RSS: 12 << 20, Protected: "Onion needs it for the Onion menu"},
	{PID: 99, Name: "MiniTop (this app)", CPU: 2, RSS: 8 << 20, Protected: "this app"},
	{PID: 12, Name: "Wi-Fi", CPU: 0, RSS: 3 << 20, Protected: "Onion needs it for Wi-Fi"},
	{PID: 55, Name: "filebrowser", CPU: 0, RSS: 20 << 20},
	{PID: 54, Name: "Filebrowser", CPU: 0, RSS: 1 << 20},
}

func pids(ps []Proc) []int {
	var out []int
	for _, p := range ps {
		out = append(out, p.PID)
	}
	return out
}

func samePIDs(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestArrange(t *testing.T) {
	for _, tc := range []struct {
		s    Settings
		want []int
	}{
		{Settings{}, []int{40, 7, 99, 12, 55, 54}},
		{Settings{Sort: "unknown"}, []int{40, 7, 99, 12, 55, 54}},
		{Settings{Sort: "memory"}, []int{40, 55, 7, 99, 12, 54}},
		{Settings{Sort: "name"}, []int{54, 55, 99, 7, 40, 12}},
		{Settings{HideOnion: true}, []int{40, 99, 55, 54}},
		{Settings{Sort: "memory", HideOnion: true}, []int{40, 55, 99, 54}},
	} {
		got := pids(arrange(sample, tc.s))
		if !samePIDs(got, tc.want) {
			t.Errorf("%+v: %v, want %v", tc.s, got, tc.want)
		}
	}
	if pids(sample)[0] != 40 || sample[1].PID != 7 {
		t.Error("arrange changed the sample")
	}
}

// LEFT and RIGHT step through the orders, wrapping round; A on the second
// row switches Onion's programs.
func TestSettingsChange(t *testing.T) {
	var s Settings
	var got []string
	for range 4 {
		s = s.change(0, 1)
		got = append(got, settingsItems(s)[0].Detail)
	}
	if want := []string{"Memory", "Name", "CPU", "Memory"}; !sameStrings(got, want) {
		t.Errorf("RIGHT: %v", got)
	}
	if s = (Settings{}).change(0, -1); s.Sort != "name" {
		t.Errorf("LEFT from CPU: %q", s.Sort)
	}
	if s = (Settings{}).change(1, 1); !s.HideOnion || settingsItems(s)[1].Detail != "Hidden" {
		t.Errorf("Onion's programs: %+v", s)
	}
	if s.change(1, -1).HideOnion {
		t.Error("Onion's programs didn't switch back")
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Y opens Settings; RIGHT on Sort by and A on Onion's programs are saved,
// and ending a program picks it from the list as shown.
func TestSettingsScreen(t *testing.T) {
	t.Setenv("MINIDECK_UI_NULL", "1")
	scr, err := ui.Open()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	in := make(chan ui.Event, 8)
	for _, b := range []ui.Button{ui.Y, ui.Right, ui.Down, ui.A, ui.B} {
		in <- ui.Event{Button: b, Pressed: true}
	}
	close(in)
	tk := &tasks{scr: scr, input: in, dir: dir, smp: newSampler(), procs: sample}
	tk.run()
	want := Settings{Sort: "memory", HideOnion: true}
	if tk.cfg != want || loadSettings(dir) != want {
		t.Fatalf("settings %+v, saved %+v", tk.cfg, loadSettings(dir))
	}
	tk.drawRunning()
	if got := pids(tk.shown); !samePIDs(got, []int{40, 55, 99, 54}) {
		t.Fatalf("listed %v", got)
	}
	if tk.run1.Items[1].Label != "filebrowser" {
		t.Fatalf("row 1 is %q", tk.run1.Items[1].Label)
	}
}

// TestScreens draws the app's screens to PNG files for review. It runs
// only with MINIDECK_GALLERY=DIR set.
func TestScreens(t *testing.T) {
	dir := os.Getenv("MINIDECK_GALLERY")
	if dir == "" {
		t.Skip("set MINIDECK_GALLERY=DIR to draw the screens")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := func(name string, f func(*tasks)) {
		t.Setenv("MINIDECK_UI_PNG", filepath.Join(dir, "tasks-"+name+".png"))
		scr, err := ui.Open()
		if err != nil {
			t.Fatal(err)
		}
		in := make(chan ui.Event)
		close(in) // screens that wait for input draw once and return
		f(&tasks{scr: scr, input: in, procs: sample})
	}
	shot("1-running", func(tk *tasks) { tk.drawRunning() })
	shot("2-settings", func(tk *tasks) { tk.settings() })
	shot("3-settings-set", func(tk *tasks) { tk.cfg = Settings{Sort: "name", HideOnion: true}; tk.settings() })
	shot("4-running-by-name", func(tk *tasks) { tk.cfg = Settings{Sort: "name", HideOnion: true}; tk.drawRunning() })
}
