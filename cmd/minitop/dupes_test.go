package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zakijss/minideck/ui"
)

// writeAt makes a file with the given contents under root.
func writeAt(t testing.TB, root, rel string, b []byte) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// blob is n bytes of a pattern seeded by s.
func blob(s byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = s + byte(i*7)
	}
	return b
}

func TestFindDuplicates(t *testing.T) {
	root := t.TempDir()
	game := blob(1, 200<<10)
	writeAt(t, root, "Roms/GBA/Emerald.gba", game)
	writeAt(t, root, "Roms/GBA/Hacks/Emerald (copy).gba", game)
	writeAt(t, root, "Roms/GBA/old/Emerald.gba", game)
	// same size and the same first and last 64 KB, different in the middle
	mid := append([]byte(nil), game...)
	mid[100<<10] ^= 0xff
	writeAt(t, root, "Roms/GBA/Emerald (rev1).gba", mid)
	// identical, but in places that aren't looked at
	writeAt(t, root, "Roms/PORTS/Games/A/libSDL2.zip", game)
	writeAt(t, root, "Roms/SCUMMVM/monkey/data.zip", game)
	writeAt(t, root, "Roms/GBA/Imgs/Emerald.zip", game)
	writeAt(t, root, "Roms/Emerald.gba", game)           // directly in Roms/
	writeAt(t, root, "Roms/PS/Game (Track 1).bin", game) // disc tracks aren't looked at
	writeAt(t, root, "Roms/GBA/notes.txt", game)         // not a game format
	writeAt(t, root, "Roms/GB/tiny.gb", blob(3, 8<<10))  // too small
	writeAt(t, root, "Roms/GB/tiny copy.gb", blob(3, 8<<10))
	// a multi-disc game: both discs listed by the .m3u, plus a loose copy
	disc := blob(9, 300<<10)
	writeAt(t, root, "Roms/PS/FF7/FF7 (Disc 1).chd", disc)
	writeAt(t, root, "Roms/PS/FF7/FF7.m3u", []byte("# discs\nFF7 (Disc 1).chd\nFF7 (Disc 2).chd\n"))
	writeAt(t, root, "Roms/PS/FF7 (Disc 1).chd", disc)

	groups, err := findDuplicates(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		for _, g := range groups {
			t.Log(g.Files)
		}
		t.Fatalf("%d groups", len(groups))
	}
	gba, ps := groups[0], groups[1] // most space wasted first: 2 x 200 KB, then 1 x 300 KB
	if gba.Size != int64(len(game)) || len(gba.Files) != 3 || gba.Wasted() != 2*int64(len(game)) {
		t.Fatalf("gba group %+v", gba)
	}
	for _, f := range gba.Files {
		if strings.Contains(f.Rel, "rev1") {
			t.Fatal("a file that differs in the middle was taken for a copy")
		}
	}
	if len(ps.Files) != 2 || ps.Files[0].Rel != "Roms/PS/FF7 (Disc 1).chd" || ps.Files[0].InUse != "" || ps.Files[1].InUse != "FF7.m3u" {
		t.Fatalf("ps group %+v", ps.Files)
	}

	// deleting: the in-use copy and the last copy are refused
	if _, err := deleteCopy(root, ps, 1); !errors.Is(err, errRefused) {
		t.Fatalf("deleted a disc the playlist uses: %v", err)
	}
	cache := writeAt(t, root, "Roms/PS/PS_cache6.db", []byte("x"))
	ng, err := deleteCopy(root, ps, 0)
	if err != nil || len(ng.Files) != 1 || fileExists(ps.Files[0].Path) {
		t.Fatalf("delete loose copy: %v %+v", err, ng)
	}
	if fileExists(cache) {
		t.Error("Onion's list cache for the system wasn't cleared")
	}
	if _, err := deleteCopy(root, ng, 0); !errors.Is(err, errRefused) {
		t.Fatal("deleted the last copy")
	}
	// a copy deleted behind its back: the remaining one is still the last
	os.Remove(gba.Files[2].Path)
	g2, err := deleteCopy(root, gba, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deleteCopy(root, g2, 0); !errors.Is(err, errRefused) {
		t.Fatal("deleted the only copy left on the card")
	}
}

func TestDiscRefs(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, "a.cue", []byte("FILE \"Game (Track 1).bin\" BINARY\n  TRACK 01 MODE2/2352\nFILE track2.bin BINARY\n"))
	writeAt(t, dir, "b.gdi", []byte("3\n1 0 4 2352 track01.bin 0\n2 600 0 2352 \"track 02.raw\" 0\n"))
	writeAt(t, dir, "c.ccd", []byte("[CloneCD]\n"))
	writeAt(t, dir, "d.m3u", []byte("#EXTM3U\nsub\\Disc 2.chd\n\n"))
	ents, _ := os.ReadDir(dir)
	got := discRefs(dir, ents)
	want := map[string]string{"game (track 1).bin": "a.cue", "track2.bin": "a.cue", "track01.bin": "b.gdi", "track 02.raw": "b.gdi",
		"c.img": "c.ccd", "c.sub": "c.ccd", "disc 2.chd": "d.m3u"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%q: %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %v", got)
	}
}

func TestFindDuplicatesCancel(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 4; i++ {
		writeAt(t, root, fmt.Sprintf("Roms/GBA/g%d.gba", i), blob(1, 4<<20))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := findDuplicates(ctx, root, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

// TestDuplicatesStress: 20,000 games in 40 systems, with 2,000 of one
// size that differ only in the middle, and 300 real copies.
func TestDuplicatesStress(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	root := t.TempDir()
	base := blob(5, 64<<10+512) // ends are the same for all, middles differ
	for i := 0; i < 20000; i++ {
		var b []byte
		switch {
		case i < 2000:
			b = append([]byte(nil), base...)
			copy(b[64<<10-8:], fmt.Sprintf("%08d", i)) // just before the last 64 KB... still in the middle
			b[(64<<10)+100] = byte(i)
			b[(64<<10)+101] = byte(i >> 8)
		case i < 2300:
			b = blob(byte(i%250), 20<<10+i) // pairs below
		default:
			b = []byte(fmt.Sprintf("%020d", i))
			b = bytes.Repeat(b, (16<<10)/len(b)+1+i%7)
		}
		writeAt(t, root, fmt.Sprintf("Roms/S%02d/g%05d.gba", i%40, i), b)
		if i >= 2000 && i < 2300 {
			writeAt(t, root, fmt.Sprintf("Roms/S%02d/copy/g%05d.gba", (i+1)%40, i), b)
		}
	}
	prog := &DupProgress{}
	start := time.Now()
	groups, err := findDuplicates(context.Background(), root, prog)
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if len(groups) != 300 {
		t.Fatalf("%d groups, want 300", len(groups))
	}
	t.Logf("%d files, %d groups, read %d of %d bytes for checksums, %v", prog.Files.Load(), len(groups), prog.Read.Load(), prog.Total.Load(), took)
}

// The Duplicates screen, scripted: Y on Storage opens it, A opens the
// first game, X deletes the selected copy after asking (A), and the
// game leaves the list once one copy is left.
func TestDuplicatesScreen(t *testing.T) {
	root := t.TempDir()
	game := blob(2, 100<<10)
	a := writeAt(t, root, "Roms/GB/Tetris.gb", game)
	b := writeAt(t, root, "Roms/GB/dupes/Tetris.gb", game)
	tk := headless(t, root, ui.R1, ui.R1, ui.Y, ui.A, ui.Down, ui.X, ui.A, ui.A, ui.B, ui.B)
	tk.disk.dir = t.TempDir()
	// a finished search, as when the user presses after the results show
	groups, err := findDuplicates(context.Background(), root, nil)
	if err != nil || len(groups) != 1 {
		t.Fatalf("search: %v %d", err, len(groups))
	}
	end := make(chan struct{})
	close(end)
	tk.dupes = &dupSearch{groups: groups, done: true, prog: &DupProgress{}, end: end, cancel: func() {}}
	done := make(chan struct{})
	go func() { tk.run(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("screens didn't finish")
	}
	if !fileExists(a) || fileExists(b) {
		t.Fatalf("kept %v, deleted %v", fileExists(a), !fileExists(b))
	}
	if tk.dupes == nil || len(tk.dupes.groups) != 0 {
		t.Fatalf("groups left: %+v", tk.dupes)
	}
}

func TestSavedScan(t *testing.T) {
	root := card(t, sampleCard)
	dir := t.TempDir()
	s, err := measure(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveScan(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := loadScan(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Saved || got.Cleanups != nil || got.Tree.Size != s.Tree.Size || got.Tree.Files != s.Tree.Files || !got.Measured.Equal(s.Measured) {
		t.Fatalf("loaded %+v", got)
	}
	// paths and parents are rebuilt: a folder can be found and forgotten
	gba := got.dirOf(filepath.Join(root, "Roms", "GBA"))
	if gba == nil || gba.Path != filepath.Join(root, "Roms", "GBA") || gba.parent == nil || gba.parent.Name != "Roms" {
		t.Fatalf("Roms/GBA: %+v", gba)
	}
	before := got.Tree.Size
	got.Forget(gba.Path, 0, 0, gba)
	if got.Tree.Size != before-gba.Size {
		t.Fatal("forget on a loaded scan")
	}
	if _, err := loadScan(dir, root+"x"); err == nil {
		t.Fatal("used a measurement of another card")
	}
	for _, bad := range []string{`{`, `{"v":2}`, `{"v":1,"root":"` + root + `","measured":"2026-01-01T00:00:00Z","tree":{"n":"x","d":[{"n":"../etc"}]}}`,
		`{"v":1,"root":"` + root + `","measured":"2026-01-01T00:00:00Z","tree":{"n":"x","s":-5}}`} {
		os.WriteFile(scanFile(dir), []byte(bad), 0o644)
		if _, err := loadScan(dir, root); err == nil {
			t.Errorf("loaded %s", bad)
		}
	}
	if got, want := ago(time.Now().Add(-3*time.Hour-time.Minute), time.Now()), "3 h ago"; got != want {
		t.Errorf("ago %q", got)
	}
}

// The measurer shows the kept measurement at once, measures again, and
// then shows (and keeps) the new one.
func TestMeasurerShowsKeptScan(t *testing.T) {
	root := card(t, sampleCard)
	dir := t.TempDir()
	m := &measurer{root: root, dir: dir}
	m.start()
	m.wait()
	first := m.view()
	if first == nil || first.Saved {
		t.Fatal("first measurement")
	}
	writeAt(t, root, "Roms/NES/new.nes", make([]byte, 50000))
	m2 := &measurer{root: root, dir: dir} // the next session
	m2.start()
	if v := m2.view(); v == nil || !v.Saved || v.Tree.Size != first.Tree.Size {
		t.Fatalf("kept measurement not shown at once: %+v", v)
	}
	m2.wait()
	if v := m2.view(); v == nil || v.Saved || v.Tree.Size <= first.Tree.Size {
		t.Fatal("new measurement not shown")
	}
	if kept, _ := loadScan(dir, root); kept == nil || kept.Tree.Size != m2.view().Tree.Size {
		t.Fatal("new measurement not kept")
	}
}
