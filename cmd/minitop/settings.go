package main

import (
	"encoding/json"
	"errors"
	"image"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zakijss/minideck/atomicfile"
	"github.com/zakijss/minideck/ui"
)

// Settings is settings.json in the app folder. A missing field keeps the
// behaviour from before it existed.
type Settings struct {
	// Sort orders the running programs: "" (CPU, then memory), "memory"
	// or "name".
	Sort string `json:"sort,omitempty"`
	// HideOnion leaves out the programs Onion needs, which can't be ended
	// anyway (default shown).
	HideOnion bool `json:"hide_onion,omitempty"`
}

// sorts are the orders Settings offers, as saved and as shown.
var sorts = []struct{ id, name, about string }{
	{"", "CPU", "Busiest first"},
	{"memory", "Memory", "Largest first"},
	{"name", "Name", "A to Z"},
}

func sortIndex(id string) int {
	for i, s := range sorts {
		if s.id == id {
			return i
		}
	}
	return 0
}

// loadSettings reads settings.json; a missing or damaged file gives the
// defaults.
func loadSettings(dir string) Settings {
	var s Settings
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("settings: %v", err)
		}
		return s
	}
	if err := json.Unmarshal(b, &s); err != nil {
		log.Printf("settings.json: %v", err)
		return Settings{}
	}
	return s
}

// saveSettings writes settings.json.
func saveSettings(dir string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, "settings.json"), append(b, '\n'), 0644)
}

// onion reports whether Onion needs p, so it can't be ended.
func onion(p Proc) bool { return strings.HasPrefix(p.Protected, "Onion needs it") }

// arrange is the running programs as the list shows them: sorted, and
// without Onion's own when they are hidden. procs (from Sample, busiest
// first) is left as it is.
func arrange(procs []Proc, s Settings) []Proc {
	out := make([]Proc, 0, len(procs))
	for _, p := range procs {
		if !s.HideOnion || !onion(p) {
			out = append(out, p)
		}
	}
	switch sorts[sortIndex(s.Sort)].id {
	case "memory":
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].RSS != out[j].RSS {
				return out[i].RSS > out[j].RSS
			}
			return out[i].CPU > out[j].CPU
		})
	case "name":
		sort.SliceStable(out, func(i, j int) bool {
			if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
				return a < b
			}
			return out[i].PID < out[j].PID
		})
	}
	return out
}

// settingsItems are the rows of Settings.
func settingsItems(s Settings) []ui.Item {
	o := sorts[sortIndex(s.Sort)]
	onionRow := ui.Item{Label: "Onion's programs", Icon: "gear", Detail: "Shown", Sub: "Menu, sound, Wi-Fi and more"}
	if s.HideOnion {
		onionRow.Detail, onionRow.Sub = "Hidden", "Only what can be ended is listed"
	}
	return []ui.Item{
		{Label: "Sort by", Icon: "list", Detail: o.name, Sub: o.about},
		onionRow,
		ui.AppearanceItem(),
	}
}

// change steps the setting on row i of Settings by d (1 or -1).
func (s Settings) change(i, d int) Settings {
	switch i {
	case 0:
		n := len(sorts)
		s.Sort = sorts[((sortIndex(s.Sort)+d)%n+n)%n].id
	case 1:
		s.HideOnion = !s.HideOnion
	}
	return s
}

// settings is the Y screen. A and RIGHT step a setting to its next value,
// LEFT to the one before; A on Appearance opens it.
func (t *tasks) settings() {
	l := ui.List{Items: settingsItems(t.cfg)}
	for {
		top := t.scr.StaticFrame("Settings", "", ui.Hint{Button: "A", Label: "Change"}, ui.Hint{Button: "B", Label: "Back"})
		l.Draw(t.scr, image.Rect(20, top, ui.W-26, ui.H-58))
		t.scr.Present()
		ev, ok, frame := t.scr.Next(t.input)
		if !ok {
			return
		}
		if frame || !acts(ev) {
			continue
		}
		d := 0
		switch ev.Button {
		case ui.A, ui.Right:
			d = 1
		case ui.Left:
			d = -1
		case ui.B, ui.Menu:
			return
		}
		appearance := l.Sel == len(l.Items)-1
		switch {
		case d == 0 || (appearance && ev.Button != ui.A):
			l.Handle(ev)
			continue
		case appearance:
			t.scr.Appearance(t.input)
		default:
			old := t.cfg
			t.cfg = t.cfg.change(l.Sel, d)
			if err := saveSettings(t.dir, t.cfg); err != nil {
				t.cfg = old
				t.scr.Notice(t.input, "Couldn't save", err.Error())
			}
		}
		l.Items = settingsItems(t.cfg)
	}
}
