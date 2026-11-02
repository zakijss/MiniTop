// MiniTop shows what's running on the Miyoo (memory and CPU per program) and
// can end a program; its second tab turns startup items on and off.
package main

import (
	"fmt"
	"image"
	"log"
	"strings"
	"time"

	"github.com/zakijss/minideck/appdir"
	"github.com/zakijss/minideck/ui"
	"github.com/zakijss/minideck/version"
)

func main() {
	version.Flag("minitop")
	version.Note("minitop")
	scr, err := ui.Open()
	if err != nil {
		log.Fatal(err)
	}
	defer scr.Close()
	t := &tasks{scr: scr, input: ui.Input(), smp: newSampler(), dir: appdir.Dir()}
	t.cfg = loadSettings(t.dir)
	t.smp.Sample() // first sample: CPU needs two
	t.procs = t.smp.Sample()
	t.run()
}

type tasks struct {
	scr     *ui.Screen
	input   <-chan ui.Event
	smp     *sampler
	procs   []Proc // every program, busiest first
	shown   []Proc // the ones listed, in Settings' order
	dir     string
	cfg     Settings
	startup bool // which tab
	run1    ui.List
	run2    ui.List
	items   []StartItem
}

func mb(b int64) string { return fmt.Sprintf("%.1f MB", float64(b)/(1<<20)) }

func (t *tasks) run() {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		if t.startup {
			t.drawStartup()
		} else {
			t.drawRunning()
		}
		select {
		case <-tick.C:
			if !t.startup {
				t.procs = t.smp.Sample()
			}
		case ev, ok := <-t.input:
			if !ok {
				return
			}
			if !acts(ev) {
				continue
			}
			switch ev.Button {
			case ui.B, ui.Menu:
				return
			case ui.L1, ui.R1:
				t.startup = !t.startup
				if t.startup {
					t.items = StartItems()
				}
			case ui.A:
				if t.startup {
					t.toggle()
				} else {
					t.end()
				}
			case ui.X:
				t.procs = t.smp.Sample()
			case ui.Y:
				t.settings()
			case ui.Select:
				t.scr.Appearance(t.input)
			default:
				if t.startup {
					t.run2.Handle(ev)
				} else {
					t.run1.Handle(ev)
				}
			}
		}
	}
}

// acts reports whether the loop should act on ev. Releases never count,
// and held buttons repeat only for the d-pad: a held A would turn a startup
// item on and off with every repeat, a held L1 would flip the tabs.
func acts(ev ui.Event) bool {
	if !ev.Pressed {
		return false
	}
	if !ev.Repeat {
		return true
	}
	switch ev.Button {
	case ui.Up, ui.Down, ui.Left, ui.Right:
		return true
	}
	return false
}

func (t *tasks) drawRunning() {
	s := t.scr
	avail, total := Memory()
	var cpu float64
	for _, p := range t.procs {
		cpu += p.CPU
	}
	// X refreshes too, but the list does that every 2 s by itself, so its
	// hint gives way to Settings.
	top := s.StaticFrame("Running", fmt.Sprintf("%s free of %s · CPU %.0f%%", mb(avail), mb(total), cpu),
		ui.Hint{Button: "A", Label: "End"}, ui.Hint{Button: "R1", Label: "Startup"}, ui.Hint{Button: "Y", Label: "Settings"}, ui.AppearanceHint, ui.Hint{Button: "B", Label: "Exit"})
	t.shown = arrange(t.procs, t.cfg)
	items := make([]ui.Item, len(t.shown))
	for i, p := range t.shown {
		sub := fmt.Sprintf("PID %d", p.PID)
		if p.Protected != "" {
			sub += " · protected"
		}
		items[i] = ui.Item{Label: p.Name, Sub: sub, Detail: fmt.Sprintf("%s  %.0f%%", mb(p.RSS), p.CPU)}
	}
	// keep the same program selected when the order changes
	if sel := t.run1.Selected(); sel != nil && t.run1.Sel < len(t.shown) {
		want := sel.Sub
		for i, it := range items {
			if it.Sub == want {
				t.run1.Sel = i
				break
			}
		}
	}
	t.run1.Items = items
	if t.run1.Sel >= len(items) {
		t.run1.Sel = max(0, len(items)-1)
	}
	t.run1.Draw(s, image.Rect(20, top, ui.W-26, ui.H-58))
	s.Present()
}

func (t *tasks) end() {
	if t.run1.Sel >= len(t.shown) {
		return
	}
	p := t.shown[t.run1.Sel]
	if p.Protected != "" {
		t.scr.Notice(t.input, p.Name, "Can't end this: "+p.Protected+".")
		return
	}
	warn := "It stops at once. Anything it hasn't saved is lost."
	switch {
	case strings.HasPrefix(p.Comm, "dropbear"):
		warn = "This ends SSH access through this server until it is started again."
	case p.Name == "MiniScale (Tailscale)" || p.Name == "MiniScale (saver)":
		warn = "Better: open MiniScale and turn it off, so it saves its state. Ending it here drops the tailnet until it's started again."
	case p.Name == "MiniTape player":
		warn = "The music stops. MiniTape starts it again when you open it."
	}
	if !t.scr.Confirm(t.input, "End "+p.Name+"?", warn, "End") {
		return
	}
	if err := End(p.PID); err != nil {
		t.scr.Notice(t.input, "Couldn't end it", err.Error())
	}
	t.procs = t.smp.Sample()
}

func (t *tasks) drawStartup() {
	s := t.scr
	top := s.StaticFrame("Startup", "At boot: "+OnionStartApp()+" (change in Tweaks › System)",
		ui.Hint{Button: "A", Label: "On / off"}, ui.Hint{Button: "L1", Label: "Running"}, ui.AppearanceHint, ui.Hint{Button: "B", Label: "Exit"})
	var items []ui.Item
	for _, it := range t.items {
		state := "Off"
		if it.On {
			state = "On"
		}
		desc := it.Desc
		if desc == "" {
			desc = "Runs at boot"
		}
		items = append(items, ui.Item{Label: it.Name, Sub: desc, Detail: state})
	}
	// Onion's own services, for reference (Tweaks › Network turns them on and off)
	for _, sv := range OnionServices() {
		state := "Off"
		if sv.On {
			state = "On"
		}
		items = append(items, ui.Item{Label: sv.Name, Sub: "Onion · change in Tweaks", Detail: state})
	}
	t.run2.Items = items
	if t.run2.Sel >= len(items) {
		t.run2.Sel = max(0, len(items)-1)
	}
	t.run2.Draw(s, image.Rect(20, top, ui.W-26, ui.H-58))
	s.Present()
}

func (t *tasks) toggle() {
	if t.run2.Sel >= len(t.items) {
		t.scr.Notice(t.input, "Onion setting", "Turn this on or off in Tweaks: it also starts or stops the service.")
		return
	}
	it := t.items[t.run2.Sel]
	if err := it.Toggle(); err != nil {
		t.scr.Notice(t.input, "Couldn't change it", err.Error())
	}
	t.items = StartItems()
}
