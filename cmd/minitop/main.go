// MiniTop is tasks and maintenance for the Miyoo: what's running (memory and
// CPU per program, and ending one), the startup items, turned on and off,
// and what fills the SD card, with clean-ups that free space.
package main

import (
	"context"
	"fmt"
	"image"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	t := &tasks{scr: scr, input: ui.Input(), smp: newSampler(), dir: appdir.Dir(), disk: &measurer{root: sdcard(), dir: appdir.Dir()}}
	t.cfg = loadSettings(t.dir)
	t.smp.Sample() // first sample: CPU needs two
	t.procs = t.smp.Sample()
	t.run()
}

type tasks struct {
	scr   *ui.Screen
	input <-chan ui.Event
	smp   *sampler
	procs []Proc // every program, busiest first
	shown []Proc // the ones listed, in Settings' order
	dir   string
	cfg   Settings
	page  int // pageRunning, pageStartup or pageStorage
	run1  ui.List
	run2  ui.List
	run3  ui.List
	items []StartItem
	disk  *measurer
	// the Storage page's rows, for the scan they came from
	rootScan *Scan
	rootEnts []Entry
	rootMore int
	stale    bool // a delete failed part way: measure again
	dupes    *dupSearch
	dmu      sync.Mutex // guards dupes' results
}

// The pages L1 and R1 move between, in order.
const (
	pageRunning = iota
	pageStartup
	pageStorage
	pages
)

// tryAgain ends a notice whose error is raw, so it says what to do next.
const tryAgain = "\n\nTry again."

func mb(b int64) string { return fmt.Sprintf("%.1f MB", float64(b)/(1<<20)) }

func (t *tasks) run() {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		// while the card is measured, the Storage page redraws its progress
		var measuring <-chan struct{}
		var redraw <-chan time.Time
		switch t.page {
		case pageStartup:
			t.drawStartup()
		case pageStorage:
			t.drawStorage()
			if _, _, done, _ := t.disk.state(); done != nil {
				measuring, redraw = done, time.After(250*time.Millisecond)
			}
		default:
			t.drawRunning()
		}
		select {
		case <-measuring:
		case <-redraw:
		case <-tick.C:
			if t.page == pageRunning {
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
				t.turn(ev.Button == ui.R1)
			case ui.A:
				switch t.page {
				case pageStartup:
					t.toggle()
				case pageStorage:
					t.openStorage()
				default:
					t.end()
				}
			case ui.X:
				switch t.page {
				case pageStartup:
					t.syncClock()
				case pageStorage:
					t.cleanup()
				default:
					t.procs = t.smp.Sample()
				}
			case ui.Start:
				switch t.page {
				case pageRunning:
					t.setSettings(t.cfg.change(0, 1))
				case pageStorage:
					t.measureAgain()
				}
			case ui.Y:
				if t.page == pageStorage {
					t.duplicates()
				} else {
					t.settings()
				}
			case ui.Select:
				t.scr.Appearance(t.input)
			default:
				switch t.page {
				case pageStartup:
					handle(&t.run2, ev)
				case pageStorage:
					handle(&t.run3, ev)
				default:
					handle(&t.run1, ev)
				}
			}
		}
	}
}

// handle moves l's selection. L2 and R2 jump to the first and last row,
// which List.Handle doesn't do.
func handle(l *ui.List, ev ui.Event) {
	switch {
	case ev.Button == ui.L2 && len(l.Items) > 0:
		l.Sel = 0
	case ev.Button == ui.R2 && len(l.Items) > 0:
		l.Sel = len(l.Items) - 1
	default:
		l.Handle(ev)
	}
}

// turn moves to the next page (R1) or the one before (L1), wrapping
// around, and loads what the page shows.
func (t *tasks) turn(next bool) {
	if next {
		t.page = (t.page + 1) % pages
	} else {
		t.page = (t.page + pages - 1) % pages
	}
	switch t.page {
	case pageStartup:
		t.items = StartItems()
	case pageStorage:
		t.disk.start()
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
	// hint gives way to Sort; SELECT's Appearance is also in Settings, so
	// its hint gives way too.
	top := s.StaticFrame("Running", fmt.Sprintf("%s free of %s · CPU %.0f%% · by %s", mb(avail), mb(total), cpu, sorts[sortIndex(t.cfg.Sort)].by),
		ui.Hint{Button: "A", Label: "End"}, ui.Hint{Button: "START", Label: "Sort"}, ui.Hint{Button: "R1", Label: "Startup"}, ui.Hint{Button: "Y", Label: "Settings"}, ui.Hint{Button: "B", Label: "Exit"})
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
		t.scr.Notice(t.input, "Couldn't end it", err.Error()+tryAgain)
	}
	t.procs = t.smp.Sample()
}

func (t *tasks) drawStartup() {
	s := t.scr
	top := s.StaticFrame("Startup", "At boot: "+OnionStartApp()+" (change in Tweaks › System)",
		ui.Hint{Button: "A", Label: "On / off"}, ui.Hint{Button: "X", Label: "Set clock"}, ui.Hint{Button: "R1", Label: "Storage"}, ui.Hint{Button: "B", Label: "Exit"})
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
		t.scr.Notice(t.input, "Couldn't change it", err.Error()+tryAgain)
	}
	t.items = StartItems()
}

// syncClock runs clocksync now (X on Startup), as the boot script does, for
// when Wi-Fi came up after it gave up.
func (t *tasks) syncClock() {
	t.scr.Alert("Setting the clock…", "Asking time servers over HTTPS")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, filepath.Join(t.dir, "clocksync"), "-wait", "15s").CombinedOutput()
	if err != nil {
		why := clockLine(out)
		if why == "" {
			why = err.Error()
		}
		t.scr.Notice(t.input, "Couldn't set the clock", why+". Check that Wi-Fi is on and try again.")
		return
	}
	t.scr.Notice(t.input, "Clock set", "The clock now matches the time servers.")
}
