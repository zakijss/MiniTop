package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"path"
	"strings"

	"github.com/zakijss/minideck/ui"
)

// dupSearch is the duplicate search of this session.
type dupSearch struct {
	groups []DupGroup
	done   bool
	err    error
	prog   *DupProgress
	end    chan struct{}
	cancel context.CancelFunc
}

func (t *tasks) startDupes() {
	ctx, cancel := context.WithCancel(context.Background())
	d := &dupSearch{prog: &DupProgress{}, end: make(chan struct{}), cancel: cancel}
	t.dupes = d
	go func() {
		g, err := findDuplicates(ctx, t.disk.root, d.prog)
		t.dmu.Lock()
		d.groups, d.err, d.done = g, err, true
		t.dmu.Unlock()
		close(d.end)
	}()
}

// dupLine is a group's second line: how many copies, and where.
func dupLine(g DupGroup) string {
	var where []string
	seen := map[string]bool{}
	for _, f := range g.Files {
		d := strings.TrimPrefix(path.Dir(f.Rel), "Roms/")
		if !seen[d] {
			seen[d] = true
			where = append(where, d)
		}
	}
	return fmt.Sprintf("%d copies · %s each · %s", len(g.Files), size(g.Size), strings.Join(where, ", "))
}

// duplicates is the Duplicates screen (Y on Storage): identical games,
// the ones wasting most space first. A opens one.
func (t *tasks) duplicates() {
	if t.dupes == nil {
		t.startDupes()
	}
	l := ui.List{}
	for {
		d := t.dupes
		t.dmu.Lock()
		done, groups, err := d.done, d.groups, d.err
		t.dmu.Unlock()
		var wasted int64
		for _, g := range groups {
			wasted += g.Wasted()
		}
		sub := "Games that are byte for byte the same"
		if done && len(groups) > 0 {
			sub = fmt.Sprintf("%d games with extra copies · %s to free", len(groups), size(wasted))
			if len(groups) == 1 {
				sub = "1 game with extra copies · " + size(wasted) + " to free"
			}
		}
		hints := []ui.Hint{{Button: "A", Label: "Open"}, {Button: "Y", Label: "Look again"}, {Button: "B", Label: "Back"}}
		if !done {
			hints = []ui.Hint{{Button: "B", Label: "Stop"}}
		}
		top := t.scr.StaticFrame("Duplicates", sub, hints...)
		area := image.Rect(20, top, ui.W-26, ui.H-58)
		cy := area.Min.Y + area.Dy()/2 - 24
		switch {
		case !done:
			t.scr.Loading(cy, "Looking for duplicates…")
			line := count(d.prog.Files.Load()) + " games found"
			if tot := d.prog.Total.Load(); tot > 0 {
				line += fmt.Sprintf(" · checking %s of %s", size(d.prog.Read.Load()), size(tot))
			}
			t.scr.TextCenter(ui.W/2, cy+34, ui.Fit(ui.Small, line, area.Dx()), ui.Small, ui.Muted)
		case err != nil:
			t.scr.TextCenter(ui.W/2, cy, ui.Fit(ui.Body, "Couldn't look: "+err.Error(), area.Dx()), ui.Body, ui.Muted)
		case len(groups) == 0:
			t.scr.TextCenter(ui.W/2, cy, "No duplicate games", ui.Body, ui.Text)
			t.scr.TextCenter(ui.W/2, cy+30, ui.Fit(ui.Small, "Ports, ScummVM games and box art aren't looked at.", area.Dx()), ui.Small, ui.Muted)
		default:
			l.Items = l.Items[:0]
			for _, g := range groups {
				l.Items = append(l.Items, ui.Item{Label: path.Base(g.Files[0].Rel), Sub: dupLine(g), Detail: size(g.Wasted()), Chevron: true})
			}
			l.Sel = min(l.Sel, len(l.Items)-1)
			l.Draw(t.scr, area)
		}
		t.scr.Present()
		var wait <-chan struct{}
		if !done {
			wait = d.end
		}
		ev, ok, got := t.next(wait)
		if !ok {
			return
		}
		if !got || !acts(ev) {
			continue
		}
		switch ev.Button {
		case ui.B, ui.Menu:
			if !done { // stop: the next visit looks again
				d.cancel()
				<-d.end
				t.dupes = nil
			}
			return
		case ui.Y:
			if done {
				t.startDupes()
			}
		case ui.A:
			if done && l.Sel < len(groups) {
				t.dupGroup(l.Sel)
			}
		default:
			handle(&l, ev)
		}
	}
}

// dupGroup lists the copies of one game; X deletes the selected copy
// after asking. The last copy can't be deleted.
func (t *tasks) dupGroup(gi int) {
	l := ui.List{}
	msg := ""
	for {
		t.dmu.Lock()
		if gi >= len(t.dupes.groups) {
			t.dmu.Unlock()
			return
		}
		g := t.dupes.groups[gi]
		t.dmu.Unlock()
		l.Items = l.Items[:0]
		for _, f := range g.Files {
			sub := ""
			if f.InUse != "" {
				sub = "Used by " + f.InUse + ": part of a game"
			}
			l.Items = append(l.Items, ui.Item{Label: strings.TrimPrefix(f.Rel, "Roms/"), Sub: sub})
		}
		l.Sel = min(l.Sel, len(l.Items)-1)
		top := t.scr.StaticFrame(path.Base(g.Files[0].Rel), fmt.Sprintf("%d identical copies · %s each", len(g.Files), size(g.Size)),
			ui.Hint{Button: "X", Label: "Delete this copy"}, ui.Hint{Button: "B", Label: "Back"})
		area := image.Rect(20, top, ui.W-26, ui.H-58)
		if msg != "" {
			t.scr.Text(area.Min.X+4, area.Max.Y-ui.LineHeight(ui.Small), ui.Fit(ui.Small, msg, area.Dx()-8), ui.Small, ui.Warn)
			area.Max.Y -= ui.LineHeight(ui.Small) + 4
		}
		l.Draw(t.scr, area)
		t.scr.Present()
		ev, ok, got := t.next(nil)
		if !ok {
			return
		}
		if !got || !acts(ev) {
			continue
		}
		switch ev.Button {
		case ui.B, ui.Menu:
			return
		case ui.X:
			msg = ""
			f := g.Files[l.Sel]
			if f.InUse == "" && len(g.Files) > 1 {
				var keep []string
				for j, o := range g.Files {
					if j != l.Sel {
						keep = append(keep, strings.TrimPrefix(o.Rel, "Roms/"))
					}
				}
				body := fmt.Sprintf("This copy (%s) is deleted for good: the card has no trash. Still on the card: %s. Saves made with this copy stay in Saves, but another copy with a different file name doesn't use them.",
					size(g.Size), strings.Join(keep, ", "))
				if !t.scr.Confirm(t.input, "Delete "+path.Base(f.Rel)+"?", body, "Delete") {
					break
				}
			}
			ng, err := deleteCopy(t.disk.root, g, l.Sel)
			if err != nil {
				if errors.Is(err, errRefused) {
					msg = reason(err)
				} else {
					msg = err.Error()
				}
				break
			}
			if s := t.disk.view(); s != nil {
				s.Forget(f.Path, g.Size, 1, nil)
				t.disk.keep(s)
			}
			t.rootScan = nil
			t.dmu.Lock()
			if len(ng.Files) < 2 {
				t.dupes.groups = append(t.dupes.groups[:gi:gi], t.dupes.groups[gi+1:]...)
			} else {
				t.dupes.groups[gi] = ng
			}
			t.dmu.Unlock()
			if len(ng.Files) < 2 {
				t.scr.Notice(t.input, "Deleted", fmt.Sprintf("Freed %s. One copy is left: %s.", size(g.Size), strings.TrimPrefix(ng.Files[0].Rel, "Roms/")))
				return
			}
		default:
			handle(&l, ev)
		}
	}
}
