package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakijss/minideck/ui"
)

// card makes a small SD card in a temp folder: files (path -> bytes) and
// empty folders (paths ending in /).
func card(t testing.TB, files map[string]int) string {
	t.Helper()
	root := t.TempDir()
	for p, n := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

var sampleCard = map[string]int{
	"Roms/GBA/Pokemon.gba":                    9000,
	"Roms/GBA/Zelda.gba":                      5000,
	"Roms/GBA/GBA_cache6.db":                  700,
	"Roms/GBA/._Pokemon.gba":                  100,
	"Roms/GBA/Imgs/":                          0,
	"Roms/NES/":                               0,
	"Roms/PORTS/Games/Doom/doom":              3000,
	"Saves/CurrentProfile/states/a.state":     2000,
	"App/MiniMart/cache/ab/thumb.png":         400,
	"App/MiniMart/cache/list.json":            300,
	"App/MiniMart/cache/ab/._thumb.png":       50,
	"App/MiniTape/streams/song.mp3":           6000,
	"App/MiniRack/minirack.log":               (1 << 20) + 10,
	"App/MiniRack/small.log":                  100,
	"App/MiniKey/vault.enc":                   200,
	"App/MiniKey/._vault.enc":                 50,
	"App/MiniScale/state.enc":                 100,
	"App/MiniScale/miniscale":                 800,
	"RetroArch/.retroarch/logs/retroarch.log": 500,
	".tmp_update/logs/runtime.log":            200,
	".tmp_update/startup/clocksync.sh":        100,
	".Trashes/501/deleted.gba":                4000,
	".DS_Store":                               60,
	"Media/old/a/b/":                          0,
	"Media/music/song.mp3":                    1000,
	"cachefile":                               8000,
	"BIOS/gba_bios.bin":                       1600,
	"Donkey Kong.gba":                         100,
}

// diskTotal adds up the space everything under root takes, folders too,
// the same way measure counts it.
func diskTotal(t *testing.T, root string) (size int64, files int) {
	t.Helper()
	filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil || p == root {
			return err
		}
		fi, err := e.Info()
		if err != nil {
			return err
		}
		size += onDisk(fi)
		if !e.IsDir() {
			files++
		}
		return nil
	})
	return
}

func rels(root string, paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		r, _ := filepath.Rel(root, p)
		out[i] = filepath.ToSlash(r)
	}
	sort.Strings(out)
	return out
}

func TestMeasure(t *testing.T) {
	root := card(t, sampleCard)
	prog := &Progress{}
	s, err := measure(context.Background(), root, prog)
	if err != nil {
		t.Fatal(err)
	}
	wantSize, wantFiles := diskTotal(t, root)
	if s.Tree.Size != wantSize || s.Tree.Files != wantFiles {
		t.Fatalf("tree %d bytes %d files, want %d and %d", s.Tree.Size, s.Tree.Files, wantSize, wantFiles)
	}
	if int(prog.Files.Load()) != wantFiles {
		t.Errorf("progress counted %d files", prog.Files.Load())
	}
	// every folder's size is the sum of what's in it
	var check func(d *Dir)
	check = func(d *Dir) {
		sz, n := diskTotal(t, d.Path)
		if d != s.Tree {
			fi, _ := os.Lstat(d.Path)
			sz += onDisk(fi)
		}
		if d.Size != sz || d.Files != n {
			t.Errorf("%s: %d bytes %d files, want %d and %d", d.Rel(root), d.Size, d.Files, sz, n)
		}
		for i, c := range d.Subs {
			if i > 0 && c.Size > d.Subs[i-1].Size {
				t.Errorf("%s: folders not largest first", d.Rel(root))
			}
			check(c)
		}
	}
	check(s.Tree)

	want := map[string][]string{
		"minimart": {"App/MiniMart/cache/ab", "App/MiniMart/cache/list.json"},
		"minitape": {"App/MiniTape/streams/song.mp3"},
		"lists":    {"Roms/GBA/GBA_cache6.db"},
		"logs":     {".tmp_update/logs/runtime.log", "App/MiniRack/minirack.log", "RetroArch/.retroarch/logs/retroarch.log"},
		"junk":     {".DS_Store", ".Trashes", "Roms/GBA/._Pokemon.gba"},
		"empty":    {"Media/old"},
	}
	for id, w := range want {
		c := s.Cleanup(id)
		got := rels(root, c.Paths)
		if strings.Join(got, "|") != strings.Join(w, "|") {
			t.Errorf("%s: %v, want %v", id, got, w)
		}
		var sum int64
		for _, z := range c.Sizes {
			sum += z
		}
		if sum != c.Size || len(c.Sizes) != len(c.Paths) || len(c.Count) != len(c.Paths) {
			t.Errorf("%s: sizes don't add up", id)
		}
	}
	if s.Cleanup("junk").Files != 3 { // .DS_Store, ._Pokemon.gba, the file in .Trashes
		t.Errorf("junk files %d", s.Cleanup("junk").Files)
	}
}

func TestContents(t *testing.T) {
	root := card(t, sampleCard)
	s, err := measure(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	ents, more := Contents(s.dirOf(filepath.Join(root, "Roms", "GBA")))
	if more != 0 || len(ents) != 5 {
		t.Fatalf("%d entries, %d more", len(ents), more)
	}
	if ents[0].Name != "Pokemon.gba" || ents[1].Name != "Zelda.gba" {
		t.Errorf("order: %v, %v", ents[0].Name, ents[1].Name)
	}
	for i := 1; i < len(ents); i++ {
		if ents[i].Size > ents[i-1].Size {
			t.Error("not largest first")
		}
	}
}

func TestProtectedName(t *testing.T) {
	for name, want := range map[string]bool{
		"minideck.key": true, "hostkey_ed25519": true, "authorized_keys": true, "state.enc": true,
		"cacert.pem": true, "id_ed25519": true, "vault.enc": true, "keys": true, "api_key.txt": true,
		"Donkey Kong.gba": false, "Monkey Island.zip": false, "keyfob": false, "Pokemon.gba": false,
		"turnkey.sh": false,
	} {
		if got := protectedName(name); got != want {
			t.Errorf("%q: %v", name, got)
		}
	}
}

func TestCheckDelete(t *testing.T) {
	root := card(t, sampleCard)
	j := func(p string) string { return filepath.Join(root, filepath.FromSlash(p)) }
	for p, ok := range map[string]bool{
		"Roms/GBA/Pokemon.gba":             true,
		"Roms/GBA":                         true,
		"Saves/CurrentProfile":             true,
		"BIOS/gba_bios.bin":                true,
		"App/MiniMart/cache":               true,
		"App/MiniRack/minirack.log":        true,
		"Roms":                             false, // top level
		"cachefile":                        false, // the swap file
		"Donkey Kong.gba":                  false, // top level, even though it's a game
		"Saves":                            false,
		".tmp_update/startup/clocksync.sh": false,
		".tmp_update/logs":                 false,
		"App/MiniKey/vault.enc":            false,
		"App/MiniTop":                      false,
		"App/MiniRack":                     false, // whole app: MiniMart removes it
		"App/MiniScale/state.enc":          false,
		"App":                              false,
	} {
		err := checkDelete(root, j(p))
		if (err == nil) != ok {
			t.Errorf("%s: %v", p, err)
		}
		if err != nil && !errors.Is(err, errRefused) {
			t.Errorf("%s: not a refusal: %v", p, err)
		}
	}
	// a folder holding a key, deeper down
	os.MkdirAll(j("Media/stuff/deep"), 0o755)
	os.WriteFile(j("Media/stuff/deep/minideck.key"), []byte("k"), 0o600)
	if err := checkDelete(root, j("Media/stuff")); err == nil || !strings.Contains(err.Error(), "minideck.key") {
		t.Errorf("folder with a key: %v", err)
	}
	for _, p := range []string{root, filepath.Dir(root), "/etc/passwd", j("../outside"), j("Roms/../../x")} {
		if err := checkDelete(root, p); err == nil {
			t.Errorf("%s: allowed", p)
		}
	}
	// a link is deleted as a link: what it points to stays
	os.Symlink(j("BIOS"), j("Media/link"))
	if err := checkDelete(root, j("Media/link")); err != nil {
		t.Fatal(err)
	}
	if err := remove(j("Media/link")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(j("BIOS/gba_bios.bin")); err != nil {
		t.Fatal("followed the link")
	}
}

func TestCleanupRun(t *testing.T) {
	root := card(t, sampleCard)
	s, err := measure(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	j := func(p string) string { return filepath.Join(root, filepath.FromSlash(p)) }
	for _, id := range cleanupOrder {
		c := s.Cleanup(id)
		steps := 0
		n, err := c.run(root, func(i, total int) { steps++ })
		if err != nil || n != len(c.Paths) || steps != len(c.Paths) {
			t.Errorf("%s: removed %d of %d, %d steps, %v", id, n, len(c.Paths), steps, err)
		}
		for _, p := range c.Paths {
			if _, err := os.Lstat(p); err == nil {
				t.Errorf("%s: %s still there", id, p)
			}
		}
	}
	// what must stay
	for _, p := range []string{"App/MiniMart/cache", "App/MiniTape/streams", "Roms/GBA/Pokemon.gba", "Roms/NES", "Roms/GBA/Imgs",
		"Saves/CurrentProfile/states/a.state", "App/MiniKey/._vault.enc", "App/MiniKey/vault.enc", "App/MiniRack/small.log",
		".tmp_update/startup/clocksync.sh", ".tmp_update/logs", "BIOS/gba_bios.bin", "cachefile", "Media/music/song.mp3"} {
		if _, err := os.Lstat(j(p)); err != nil {
			t.Errorf("%s was deleted", p)
		}
	}
	// measured again, there's nothing left to clean but the empty folder
	// the clean-ups left behind (Media/old had none to begin with)
	s, _ = measure(context.Background(), root, nil)
	for _, c := range s.Cleanups {
		if c.ID != "empty" && len(c.Paths) > 0 {
			t.Errorf("%s still lists %v", c.ID, rels(root, c.Paths))
		}
	}
	// a damaged list can't make it delete Onion's or MiniKey's files
	bad := &Cleanup{}
	for _, p := range []string{root, j("Saves"), j(".tmp_update/startup/clocksync.sh"), j("App/MiniKey/vault.enc"),
		j("App/MiniScale/state.enc"), filepath.Dir(root), "/tmp"} {
		bad.add(p, 1, 1)
	}
	if n, err := bad.run(root, nil); n != 0 || err == nil {
		t.Errorf("bad list: removed %d, %v", n, err)
	}
	for _, p := range []string{"Saves", ".tmp_update/startup/clocksync.sh", "App/MiniKey/vault.enc", "App/MiniScale/state.enc"} {
		if _, err := os.Lstat(j(p)); err != nil {
			t.Errorf("%s was deleted by a bad list", p)
		}
	}
}

func TestForget(t *testing.T) {
	root := card(t, sampleCard)
	s, err := measure(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	gba := s.dirOf(filepath.Join(root, "Roms", "GBA"))
	roms := gba.parent
	before, beforeRoot, files := roms.Size, s.Tree.Size, s.Tree.Files
	if err := remove(gba.Path); err != nil {
		t.Fatal(err)
	}
	s.Forget(gba.Path, 0, 0, gba)
	if roms.Size != before-gba.Size || s.Tree.Size != beforeRoot-gba.Size || s.Tree.Files != files-gba.Files {
		t.Errorf("sizes not taken off: roms %d->%d", before, roms.Size)
	}
	if s.dirOf(gba.Path) != nil {
		t.Error("folder still in the tree")
	}
	if len(s.Cleanup("lists").Paths) != 0 || len(s.Cleanup("junk").Paths) != 2 {
		t.Errorf("clean-ups still list deleted files: %v %v", s.Cleanup("lists").Paths, rels(root, s.Cleanup("junk").Paths))
	}
	// a file
	song := filepath.Join(root, "Media", "music", "song.mp3")
	fi, _ := os.Lstat(song)
	music := s.dirOf(filepath.Dir(song))
	m0 := music.Size
	remove(song)
	s.Forget(song, onDisk(fi), 1, nil)
	if music.Size != m0-onDisk(fi) || music.Files != 0 {
		t.Errorf("file not taken off: %d", music.Size)
	}
	// folders on tmpfs shrink as entries go (on FAT they don't), so allow
	// for that
	if want, _ := diskTotal(t, root); s.Tree.Size < want || s.Tree.Size-want > 1024 {
		t.Errorf("tree %d after deletes, card %d", s.Tree.Size, want)
	}
}

func TestMeasureCancel(t *testing.T) {
	files := map[string]int{}
	for i := 0; i < 3000; i++ {
		files[fmt.Sprintf("Roms/D%d/f%d", i%30, i)] = 1
	}
	root := card(t, files)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := measure(ctx, root, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if _, err := measure(context.Background(), filepath.Join(root, "missing"), nil); err == nil {
		t.Fatal("missing root measured")
	}
}

// The measurer runs in the background while the screen reads its
// progress; run with -race.
func TestMeasurerConcurrent(t *testing.T) {
	files := map[string]int{}
	for i := 0; i < 4000; i++ {
		files[fmt.Sprintf("Roms/S%d/g%d.bin", i%40, i)] = 10
	}
	root := card(t, files)
	m := &measurer{root: root}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, p, _, _ := m.state()
				if p != nil {
					_ = p.Files.Load() + p.Bytes.Load()
					_ = p.Where()
				}
				m.start()
			}
		}()
	}
	for i := 0; i < 20; i++ {
		m.again()
		if i%3 == 0 {
			m.stop()
		}
	}
	m.again()
	m.wait()
	close(stop)
	wg.Wait()
	m.wait()
	s, _, done, err := m.state()
	if s == nil || err != nil || done != nil || s.Tree.Files != 4000 {
		t.Fatalf("scan %v err %v done %v", s != nil, err, done)
	}
}

// TestStress measures a big, deep card. MINITOP_STRESS=1 makes it 50,000
// files (the default is 6,000, so CI under qemu stays quick).
func TestStress(t *testing.T) {
	n := 6000
	if os.Getenv("MINITOP_STRESS") == "1" {
		n = 50000
	}
	root := t.TempDir()
	t0 := time.Now()
	for i := 0; i < n; i++ {
		d := filepath.Join(root, "Roms", fmt.Sprintf("SYS%02d", i%40), fmt.Sprintf("set%03d", (i/40)%50))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("game%05d.bin", i)
		if i%10 == 0 {
			name = "._" + name
		}
		if err := os.WriteFile(filepath.Join(d, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 300 levels deep, ending in an empty folder
	deep := filepath.Join(root, "Media")
	for i := 0; i < 300; i++ {
		deep = filepath.Join(deep, "d")
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	made := time.Since(t0)
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	t1 := time.Now()
	s, err := measure(context.Background(), root, nil)
	took := time.Since(t1)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&m1)
	if s.Tree.Files != n {
		t.Fatalf("%d files, want %d", s.Tree.Files, n)
	}
	if got := len(s.Cleanup("junk").Paths); got != n/10 {
		t.Errorf("junk %d, want %d", got, n/10)
	}
	if e := s.Cleanup("empty").Paths; len(e) != 1 || !strings.HasSuffix(e[0], filepath.Join("Media", "d")) {
		t.Errorf("empty: %d paths", len(e))
	}
	heap := int64(m1.HeapAlloc) - int64(m0.HeapAlloc)
	runtime.KeepAlive(s)
	t.Logf("%d files, 300 levels: made in %v, measured in %v (%.0f files/s), scan holds %.1f MB of heap",
		n, made.Round(time.Millisecond), took.Round(time.Millisecond), float64(n)/took.Seconds(), float64(heap)/(1<<20))
	c := s.Cleanup("junk")
	t2 := time.Now()
	removed, err := c.run(root, nil)
	if err != nil || removed != n/10 {
		t.Fatalf("removed %d, %v", removed, err)
	}
	t.Logf("deleted %d junk files in %v", removed, time.Since(t2).Round(time.Millisecond))
	if heap > 64<<20 {
		t.Errorf("the scan holds %d MB", heap>>20)
	}
}

func TestFormat(t *testing.T) {
	for b, want := range map[int64]string{0: "0 B", 512: "512 B", 1536: "1.5 KB", 5 << 20: "5.0 MB", 3 << 30: "3.00 GB", 200 << 30: "200 GB"} {
		if got := size(b); got != want {
			t.Errorf("size(%d) = %q", b, got)
		}
	}
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4200: "-4,200"} {
		if got := count(n); got != want {
			t.Errorf("count(%d) = %q", n, got)
		}
	}
}

func headless(t *testing.T, root string, keys ...ui.Button) *tasks {
	t.Helper()
	t.Setenv("MINIDECK_UI_NULL", "1")
	scr, err := ui.Open()
	if err != nil {
		t.Fatal(err)
	}
	in := make(chan ui.Event, len(keys)+1)
	for _, b := range keys {
		in <- ui.Event{Button: b, Pressed: true}
	}
	close(in)
	return &tasks{scr: scr, input: in, dir: t.TempDir(), smp: newSampler(), procs: sample, disk: &measurer{root: root}}
}

// R1 twice reaches Storage and starts measuring; L1 goes back to Startup.
func TestPages(t *testing.T) {
	root := card(t, sampleCard)
	tk := headless(t, root, ui.R1, ui.R1)
	tk.run()
	if tk.page != pageStorage {
		t.Fatalf("page %d", tk.page)
	}
	tk.disk.wait()
	if s, _, _, _ := tk.disk.state(); s == nil {
		t.Fatal("not measured")
	}
	tk.drawStorage()
	if len(tk.run3.Items) != len(tk.rootEnts) || tk.run3.Items[0].Label != "App" || !strings.HasPrefix(tk.run3.Items[0].Sub, "Apps · ") {
		t.Errorf("rows %+v", tk.run3.Items)
	}
	tk.turn(true)
	if tk.page != pageRunning {
		t.Errorf("R1 on Storage: page %d", tk.page)
	}
	tk.turn(false)
	tk.turn(false)
	if tk.page != pageStartup {
		t.Errorf("L1 twice from Running: page %d", tk.page)
	}
}

// In Roms, X on the largest folder asks, A deletes it; X on a protected
// row only explains.
func TestBrowseDelete(t *testing.T) {
	root := card(t, sampleCard)
	tk := headless(t, root, ui.X, ui.A, ui.B)
	tk.disk.start()
	tk.disk.wait()
	s, _, _, _ := tk.disk.state()
	roms := s.dirOf(filepath.Join(root, "Roms"))
	first := roms.Subs[0]
	tk.browse(s, roms)
	if _, err := os.Stat(first.Path); err == nil {
		t.Fatalf("%s not deleted", first.Name)
	}
	if s.dirOf(first.Path) != nil {
		t.Error("still in the tree")
	}
	if want, _ := diskTotal(t, root); s.Tree.Size < want || s.Tree.Size-want > 1024 {
		t.Errorf("tree %d, card %d", s.Tree.Size, want)
	}

	// App/MiniRack as a whole is refused: X shows why, A closes it
	tk = headless(t, root, ui.X, ui.A, ui.B)
	tk.disk.start()
	tk.disk.wait()
	s, _, _, _ = tk.disk.state()
	app := s.dirOf(filepath.Join(root, "App"))
	tk.browse(s, app)
	for _, d := range []string{"MiniRack", "MiniMart", "MiniTape", "MiniKey", "MiniScale"} {
		if _, err := os.Stat(filepath.Join(root, "App", d)); err != nil {
			t.Errorf("App/%s deleted", d)
		}
	}
}

// Clean up › macOS leftovers › START › Delete removes them and measures
// the card again.
func TestCleanupScreen(t *testing.T) {
	root := card(t, sampleCard)
	keys := []ui.Button{}
	for range indexOf("junk") {
		keys = append(keys, ui.Down)
	}
	keys = append(keys, ui.A, ui.Start, ui.A, ui.A)
	tk := headless(t, root, keys...)
	tk.disk.start()
	tk.disk.wait()
	tk.cleanup()
	for _, p := range []string{".DS_Store", ".Trashes", "Roms/GBA/._Pokemon.gba"} {
		if _, err := os.Lstat(filepath.Join(root, p)); err == nil {
			t.Errorf("%s still there", p)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "App/MiniKey/._vault.enc")); err != nil {
		t.Error("MiniKey's file deleted")
	}
	tk.disk.wait()
	if s, _, _, _ := tk.disk.state(); s == nil || len(s.Cleanup("junk").Paths) != 0 {
		t.Error("not measured again")
	}
}

func indexOf(id string) int {
	for i, c := range cleanupOrder {
		if c == id {
			return i
		}
	}
	return -1
}

// TestStorageScreens draws the Storage screens to PNG files for review,
// with MINIDECK_GALLERY=DIR set.
func TestStorageScreens(t *testing.T) {
	dir := os.Getenv("MINIDECK_GALLERY")
	if dir == "" {
		t.Skip("set MINIDECK_GALLERY=DIR to draw the screens")
	}
	// sparse files: real-looking sizes (a few GB) that take no space
	root := card(t, nil)
	grow := func(p string, n int64) {
		full := filepath.Join(root, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/") {
			os.MkdirAll(full, 0o755)
			return
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		f, err := os.Create(full)
		if err != nil {
			t.Fatal(err)
		}
		f.Truncate(n)
		f.Close()
	}
	for k, v := range sampleCard {
		grow(k, min(int64(v)*40000, 2<<30))
	}
	for i := 0; i < 30; i++ {
		grow(fmt.Sprintf("Roms/SYS%d/game.bin", i), int64(i+1)*3<<20)
	}
	shot := func(name string, f func(*tasks)) {
		t.Setenv("MINIDECK_UI_PNG", filepath.Join(dir, "tasks-"+name+".png"))
		scr, err := ui.Open()
		if err != nil {
			t.Fatal(err)
		}
		in := make(chan ui.Event)
		close(in)
		tk := &tasks{scr: scr, input: in, procs: sample, disk: &measurer{root: root}}
		f(tk)
	}
	shot("5-storage-measuring", func(tk *tasks) {
		p := &Progress{}
		p.Files.Store(12345)
		p.Bytes.Store(3 << 30)
		p.where.Store("Roms")
		tk.disk.prog, tk.disk.done = p, make(chan struct{})
		tk.drawStorage()
	})
	measured := func(tk *tasks) *Scan {
		tk.disk.start()
		tk.disk.wait()
		s, _, _, _ := tk.disk.state()
		return s
	}
	shot("6-storage", func(tk *tasks) { measured(tk); tk.drawStorage() })
	shot("7-storage-roms", func(tk *tasks) { s := measured(tk); tk.browse(s, s.dirOf(filepath.Join(root, "Roms"))) })
	shot("8-storage-delete", func(tk *tasks) {
		s := measured(tk)
		g := s.dirOf(filepath.Join(root, "Roms", "GBA"))
		tk.delete(s, Entry{Name: g.Name, Path: g.Path, Size: g.Size, Dir: g})
	})
	shot("9-storage-refused", func(tk *tasks) {
		s := measured(tk)
		tk.delete(s, Entry{Name: "MiniRack", Path: filepath.Join(root, "App", "MiniRack"), Dir: s.dirOf(filepath.Join(root, "App", "MiniRack"))})
	})
	shot("10-cleanup", func(tk *tasks) { measured(tk); tk.cleanup() })
	shot("11-cleanup-list", func(tk *tasks) { s := measured(tk); tk.cleanupList(s, s.Cleanup("junk")) })
	shot("12-cleanup-confirm", func(tk *tasks) { s := measured(tk); tk.runCleanup(s, s.Cleanup("minimart")) })
	// the kept measurement, shown while the card is measured again
	kept := t.TempDir()
	shot("13-storage-kept", func(tk *tasks) {
		s := measured(tk)
		s.Measured = time.Now().Add(-5 * time.Hour)
		saveScan(kept, s)
		old, _ := loadScan(kept, root)
		p := &Progress{}
		p.Files.Store(4321)
		tk.disk.scan, tk.disk.old, tk.disk.prog, tk.disk.done = nil, old, p, make(chan struct{})
		tk.drawStorage()
	})
	game := blob(4, 300<<10)
	for _, p := range []string{"Roms/GBA/Pokemon Emerald.gba", "Roms/GBA/Hacks/Pokemon Emerald.gba", "Roms/PS/FF7 (Disc 1).chd", "Roms/PS/FF7/FF7 (Disc 1).chd"} {
		writeAt(t, root, p, game)
	}
	writeAt(t, root, "Roms/PS/FF7/FF7.m3u", []byte("FF7 (Disc 1).chd\n"))
	groups, err := findDuplicates(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	finished := func() *dupSearch {
		end := make(chan struct{})
		close(end)
		return &dupSearch{groups: groups, done: true, prog: &DupProgress{}, end: end, cancel: func() {}}
	}
	shot("14-duplicates-searching", func(tk *tasks) {
		p := &DupProgress{}
		p.Files.Store(18234)
		p.Total.Store(2 << 30)
		p.Read.Store(700 << 20)
		tk.dupes = &dupSearch{prog: p, end: make(chan struct{}), cancel: func() {}}
		tk.duplicates()
	})
	shot("15-duplicates", func(tk *tasks) { tk.dupes = finished(); tk.duplicates() })
	shot("16-duplicates-game", func(tk *tasks) { tk.dupes = finished(); tk.dupGroup(0) })
}
