package main

import (
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"time"

	"github.com/zakijss/minideck/ui"
)

// topAbout describes the card's top-level folders and files on the
// Storage page.
var topAbout = map[string]string{
	"Roms":        "Games",
	"Saves":       "Saved games and states",
	"App":         "Apps",
	"Themes":      "Themes",
	"Icons":       "Icon packs",
	"BIOS":        "BIOS files for emulators",
	"Screenshots": "Screenshots",
	"RetroArch":   "RetroArch and its cores",
	"Emu":         "Emulators",
	"RApp":        "RetroArch apps",
	"Media":       "Music and videos",
	".tmp_update": "Onion OS",
	"miyoo":       "Miyoo system files",
	"miyoo354":    "Miyoo system files",
	"cachefile":   "Onion's swap file",
	"8188fu.ko":   "Wi-Fi driver",
}

// entryItem is a list row for e; top: e is on the card's top level.
func entryItem(e Entry, top bool) ui.Item {
	it := ui.Item{Label: e.Name, Detail: size(e.Size)}
	if e.Dir != nil {
		it.Chevron = true
		it.Sub = count(int64(e.Dir.Files)) + " files"
		if e.Dir.Files == 1 {
			it.Sub = "1 file"
		}
	} else {
		it.Sub = "File"
	}
	if about := topAbout[e.Name]; top && about != "" {
		it.Sub = about + " · " + it.Sub
	}
	return it
}

func entryItems(ents []Entry, more int, top bool) []ui.Item {
	items := make([]ui.Item, 0, len(ents)+1)
	for _, e := range ents {
		items = append(items, entryItem(e, top))
	}
	if more > 0 {
		items = append(items, ui.Item{Label: fmt.Sprintf("And %s smaller files", count(int64(more))), Sub: "Not listed"})
	}
	return items
}

// keepSel selects the row labelled label, if it is still there.
func keepSel(l *ui.List, label string) {
	for i, it := range l.Items {
		if it.Label == label {
			l.Sel = i
			return
		}
	}
	if l.Sel >= len(l.Items) {
		l.Sel = max(0, len(l.Items)-1)
	}
}

// storageSub is the Storage page's subtitle: free space, or why it's
// unknown.
func storageSub(root string) (string, int64, int64) {
	free, total, err := space(root)
	if err != nil || total <= 0 {
		return "Can't read the card's size", 0, 0
	}
	return size(free) + " free of " + size(total), free, total
}

// drawStorage draws the Storage page: the card's use, then its top-level
// folders and files, largest first.
func (t *tasks) drawStorage() {
	s := t.scr
	sub, free, total := storageSub(t.disk.root)
	top := s.StaticFrame("Storage", sub,
		ui.Hint{Button: "A", Label: "Open"}, ui.Hint{Button: "X", Label: "Clean up"}, ui.Hint{Button: "Y", Label: "Duplicates"}, ui.Hint{Button: "B", Label: "Exit"})
	card := image.Rect(20, top, ui.W-26, top+62)
	s.Panel(card, 12)
	pct := 0.0
	if total > 0 {
		pct = float64(total-free) / float64(total) * 100
	}
	w := s.Text(card.Min.X+16, card.Min.Y+10, fmt.Sprintf("Used %s · %.0f%%", size(total-free), pct), ui.Heading, ui.Text)
	avail, _ := Memory()
	sfree, stotal := swapInfo()
	mem := fmt.Sprintf("RAM %.0f MB free", float64(avail)/(1<<20))
	room := card.Dx() - 32 - w - 20
	if long := mem + fmt.Sprintf(" · swap %.0f MB", float64(sfree)/(1<<20)); stotal > 0 && ui.Measure(ui.Small, long) <= room {
		mem = long
	}
	if room > 40 {
		s.TextRight(card.Max.X-16, card.Min.Y+14, ui.Fit(ui.Small, mem, room), ui.Small, ui.Muted)
	}
	s.Bar(image.Rect(card.Min.X+16, card.Max.Y-22, card.Max.X-16, card.Max.Y-10), pct/100, ui.Level(pct))
	area := image.Rect(20, card.Max.Y+10, ui.W-26, ui.H-58)

	_, prog, done, err := t.disk.state()
	scan := t.disk.view()
	if scan != nil && scan.Saved {
		// the kept measurement, while the card is measured again
		note := "From " + ago(scan.Measured, time.Now())
		if done != nil {
			note += " · measuring again: " + count(prog.Files.Load()) + " files"
		}
		s.Text(area.Min.X+4, area.Min.Y-2, ui.Fit(ui.Small, note, area.Dx()-8), ui.Small, ui.Muted)
		area.Min.Y += ui.LineHeight(ui.Small) + 2
	}
	switch {
	case scan == nil && done != nil:
		t.drawMeasuring(area, prog)
	case scan == nil:
		msg := "Not measured yet"
		if err != nil {
			msg = "Couldn't measure the card: " + err.Error()
		}
		s.TextCenter(ui.W/2, area.Min.Y+area.Dy()/2-10, ui.Fit(ui.Body, msg, area.Dx()), ui.Body, ui.Muted)
	default:
		if t.rootScan != scan {
			t.rootScan = scan
			t.rootEnts, t.rootMore = Contents(scan.Tree)
		}
		label := ""
		if sel := t.run3.Selected(); sel != nil {
			label = sel.Label
		}
		t.run3.Items = entryItems(t.rootEnts, t.rootMore, true)
		keepSel(&t.run3, label)
		t.run3.Draw(s, area)
	}
	s.Present()
}

// drawMeasuring shows a running measurement's progress in r.
func (t *tasks) drawMeasuring(r image.Rectangle, p *Progress) {
	s := t.scr
	cy := r.Min.Y + r.Dy()/2 - 24
	s.Loading(cy, "Measuring the card…")
	line := count(p.Files.Load()) + " files · " + size(p.Bytes.Load())
	if w := p.Where(); w != "" {
		line += " · " + w
	}
	s.TextCenter(ui.W/2, cy+34, ui.Fit(ui.Small, line, r.Dx()), ui.Small, ui.Muted)
}

// openStorage opens the selected row of the Storage page: a folder's
// contents, or the delete question for a file.
func (t *tasks) openStorage() {
	scan := t.disk.view()
	if scan == nil || t.rootScan != scan || t.run3.Sel >= len(t.rootEnts) {
		return
	}
	e := t.rootEnts[t.run3.Sel]
	if e.Dir != nil {
		t.browse(scan, e.Dir)
	} else {
		t.delete(scan, e)
	}
	t.rootScan = nil // sizes may have changed
	if t.stale {
		t.stale = false
		t.disk.again()
	}
}

// browse lists folder d's contents, largest first. A opens a folder, X
// (or A on a file) asks to delete the selected row, B goes back.
func (t *tasks) browse(scan *Scan, d *Dir) {
	l := ui.List{}
	ents, more := Contents(d)
	for {
		label := ""
		if sel := l.Selected(); sel != nil {
			label = sel.Label
		}
		l.Items = entryItems(ents, more, false)
		keepSel(&l, label)
		sub := size(d.Size) + " · " + count(int64(d.Files)) + " files"
		if rel := d.Rel(scan.Root); strings.Contains(rel, "/") {
			sub = filepath.Dir(rel) + " · " + sub
		}
		top := t.scr.StaticFrame(d.Name, ui.Fit(ui.Small, sub, ui.W-60),
			ui.Hint{Button: "A", Label: "Open"}, ui.Hint{Button: "X", Label: "Delete"}, ui.Hint{Button: "B", Label: "Back"})
		if len(l.Items) == 0 {
			t.scr.TextCenter(ui.W/2, top+80, "Empty", ui.Body, ui.Muted)
		}
		l.Draw(t.scr, image.Rect(20, top, ui.W-26, ui.H-58))
		t.scr.Present()
		ev, ok, frame := t.scr.Next(t.input)
		if !ok {
			return
		}
		if frame || !acts(ev) {
			continue
		}
		switch ev.Button {
		case ui.B, ui.Menu:
			return
		case ui.A, ui.X:
			if l.Sel >= len(ents) {
				continue
			}
			e := ents[l.Sel]
			if e.Dir != nil && ev.Button == ui.A {
				t.browse(scan, e.Dir)
			} else {
				t.delete(scan, e)
			}
			ents, more = Contents(d)
		default:
			l.Handle(ev)
		}
	}
}

// reason is a refusal's text for the screen.
func reason(err error) string {
	msg := strings.TrimPrefix(err.Error(), errRefused.Error()+": ")
	if msg == "" {
		return msg
	}
	return strings.ToUpper(msg[:1]) + msg[1:] + "."
}

// delete asks before deleting e for good, unless checkDelete refuses it.
func (t *tasks) delete(scan *Scan, e Entry) {
	if err := checkDelete(scan.Root, e.Path); err != nil {
		if errors.Is(err, errRefused) {
			t.scr.Notice(t.input, "Can't delete "+e.Name, reason(err))
		} else {
			t.scr.Notice(t.input, "Can't delete "+e.Name, err.Error())
		}
		return
	}
	what := "This file"
	if e.Dir != nil {
		what = "This folder and the " + count(int64(e.Dir.Files)) + " files in it"
		if e.Dir.Files == 1 {
			what = "This folder and the file in it"
		}
	}
	body := fmt.Sprintf("%s (%s) are deleted for good: the card has no trash.%s", what, size(e.Size), warning(scan.Root, e.Path))
	if e.Dir == nil {
		body = fmt.Sprintf("This file (%s) is deleted for good: the card has no trash.%s", size(e.Size), warning(scan.Root, e.Path))
	}
	if !t.scr.Confirm(t.input, "Delete "+e.Name+"?", body, "Delete") {
		return
	}
	t.scr.Alert("Deleting…", e.Name)
	if err := remove(e.Path); err != nil {
		t.stale = true // part of it may be gone: measure again
		t.scr.Notice(t.input, "Couldn't delete all of it", err.Error()+tryAgain)
		return
	}
	scan.Forget(e.Path, e.Size, 1, e.Dir)
	t.disk.keep(scan)
}

// next waits for input; while a measurement runs (done is not nil) it
// also returns, with got false, when it ends or a quarter second passes,
// so the screen can show progress.
func (t *tasks) next(done <-chan struct{}) (ev ui.Event, ok, got bool) {
	if done == nil {
		ev, ok, frame := t.scr.Next(t.input)
		return ev, ok, !frame
	}
	select {
	case ev, ok = <-t.input:
		return ev, ok, true
	case <-done:
	case <-time.After(250 * time.Millisecond):
	}
	return ui.Event{}, true, false
}

// cleanupItem is the Clean up row for c.
func cleanupItem(c *Cleanup) ui.Item {
	it := ui.Item{Label: c.Name, Sub: c.About, Detail: "Nothing", Chevron: len(c.Paths) > 0}
	if len(c.Paths) > 0 {
		it.Detail = size(c.Size)
	}
	return it
}

// cleanup is the Clean up screen (X on Storage): each clean-up with how
// much it frees. A lists what one would delete.
func (t *tasks) cleanup() {
	t.disk.start()
	l := ui.List{}
	for {
		scan, prog, done, err := t.disk.state()
		top := t.scr.StaticFrame("Clean up", "Frees space; games, saves and settings stay",
			ui.Hint{Button: "A", Label: "Show"}, ui.Hint{Button: "B", Label: "Back"})
		area := image.Rect(20, top, ui.W-26, ui.H-58)
		switch {
		case scan != nil:
			l.Items = l.Items[:0]
			for _, c := range scan.Cleanups {
				l.Items = append(l.Items, cleanupItem(c))
			}
			l.Draw(t.scr, area)
		case done != nil:
			t.drawMeasuring(area, prog)
		default:
			msg := "Not measured yet"
			if err != nil {
				msg = "Couldn't measure the card: " + err.Error()
			}
			t.scr.TextCenter(ui.W/2, area.Min.Y+area.Dy()/2-10, ui.Fit(ui.Body, msg, area.Dx()), ui.Body, ui.Muted)
		}
		t.scr.Present()
		ev, ok, got := t.next(done)
		if !ok {
			return
		}
		if !got || !acts(ev) {
			continue
		}
		switch ev.Button {
		case ui.B, ui.Menu:
			return
		case ui.A:
			if scan == nil || l.Sel >= len(scan.Cleanups) {
				continue
			}
			c := scan.Cleanups[l.Sel]
			if len(c.Paths) == 0 {
				t.scr.Notice(t.input, c.Name, "Nothing to clean up here.")
				continue
			}
			t.cleanupList(scan, c)
		default:
			l.Handle(ev)
		}
	}
}

// cleanupList shows exactly what clean-up c deletes; START deletes it all
// after asking.
func (t *tasks) cleanupList(scan *Scan, c *Cleanup) {
	items := make([]ui.Item, len(c.Paths))
	for i, p := range c.Paths {
		rel, _ := filepath.Rel(scan.Root, p)
		items[i] = ui.Item{Label: filepath.ToSlash(rel), Detail: size(c.Sizes[i])}
	}
	l := ui.List{Items: items}
	for {
		sub := fmt.Sprintf("%s items · %s", count(int64(len(c.Paths))), size(c.Size))
		top := t.scr.StaticFrame(c.Name, sub,
			ui.Hint{Button: "START", Label: "Delete all"}, ui.Hint{Button: "B", Label: "Back"})
		l.Draw(t.scr, image.Rect(20, top, ui.W-26, ui.H-58))
		t.scr.Present()
		ev, ok, frame := t.scr.Next(t.input)
		if !ok {
			return
		}
		if frame || !acts(ev) {
			continue
		}
		switch ev.Button {
		case ui.B, ui.Menu:
			return
		case ui.Start:
			t.runCleanup(scan, c)
			return
		default:
			l.Handle(ev)
		}
	}
}

// runCleanup asks, deletes everything c lists with a progress line, says
// how much was freed, and measures the card again.
func (t *tasks) runCleanup(scan *Scan, c *Cleanup) {
	n := len(c.Paths)
	noun := "items"
	if n == 1 {
		noun = "item"
	}
	body := fmt.Sprintf("%s %s (%s) are deleted for good: the card has no trash.", count(int64(n)), noun, size(c.Size))
	if c.Note != "" {
		body += " " + c.Note
	}
	if !t.scr.Confirm(t.input, c.Ask, body, "Delete") {
		return
	}
	before, _, _ := space(scan.Root)
	last := time.Time{}
	done, err := c.run(scan.Root, func(i, total int) {
		if time.Since(last) > 150*time.Millisecond || i == total {
			last = time.Now()
			t.scr.Alert("Deleting…", fmt.Sprintf("%s of %s", count(int64(i)), count(int64(total))))
		}
	})
	after, _, _ := space(scan.Root)
	freed := after - before
	if freed <= 0 {
		freed = c.Size
	}
	msg := fmt.Sprintf("Deleted %s of %s and freed %s.", count(int64(done)), count(int64(n)), size(freed))
	if err != nil {
		msg += " Some were left: " + reason(err)
	}
	t.disk.again()
	t.rootScan = nil
	t.scr.Notice(t.input, "Cleaned up", msg)
}
