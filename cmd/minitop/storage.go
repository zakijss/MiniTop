package main

// Storage: what fills the SD card, and clean-ups that free space without
// touching games, saves or settings. measure walks the card once, in the
// background, and keeps a tree of folder sizes plus what each clean-up
// would delete; deleting anything else is one item at a time, and
// checkDelete refuses Onion's own files, keys and whole app folders.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// sdcard is the card's root; TASKS_SDCARD replaces it in tests.
func sdcard() string {
	if d := os.Getenv("TASKS_SDCARD"); d != "" {
		return d
	}
	return "/mnt/SDCARD"
}

// Dir is one folder of the measured tree. Sizes are the space the files
// take on the card (whole clusters), so they add up to what the card
// reports as used.
type Dir struct {
	Name   string
	Path   string // full path
	Size   int64  // this folder and everything in it
	Files  int    // files in it, at any depth
	Subs   []*Dir // its folders, largest first
	parent *Dir
}

// Rel is the folder's path from the card's root ("" for the root).
func (d *Dir) Rel(root string) string {
	r, _ := filepath.Rel(root, d.Path)
	if r == "." {
		return ""
	}
	return filepath.ToSlash(r)
}

// Cleanup is one kind of clean-up: what it would delete and how much.
type Cleanup struct {
	ID    string
	Name  string
	About string // one line: what it is
	Ask   string // the question before deleting
	Note  string // what happens after (shown before deleting)
	Paths []string
	Sizes []int64 // the space each path takes
	Count []int   // the files each path holds (1 for a file)
	Size  int64
	Files int // files among the paths, at any depth
}

func (c *Cleanup) add(p string, size int64, files int) {
	c.Paths = append(c.Paths, p)
	c.Sizes = append(c.Sizes, size)
	c.Count = append(c.Count, files)
	c.Size += size
	c.Files += files
}

// Scan is a finished measurement of the card.
type Scan struct {
	Root     string
	Tree     *Dir
	Cleanups []*Cleanup
	Errors   int // folders that couldn't be read
	Measured time.Time
	// Saved: the measurement kept from an earlier session (see
	// savedscan.go): the tree only, no clean-ups.
	Saved bool
}

// Cleanup returns the clean-up with the given ID.
func (s *Scan) Cleanup(id string) *Cleanup {
	for _, c := range s.Cleanups {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// Progress is what a running measurement has seen so far. It is read
// from the UI while measure writes it.
type Progress struct {
	Files atomic.Int64
	Bytes atomic.Int64
	where atomic.Value // the top-level folder being measured
}

// Where is the top-level folder being measured.
func (p *Progress) Where() string {
	s, _ := p.where.Load().(string)
	return s
}

// junkNames are macOS leftovers a Mac writes onto the card: folders, and
// single files (AppleDouble "._" files are matched by prefix).
var (
	junkDirs  = map[string]bool{".Trashes": true, ".Spotlight-V100": true, ".fseventsd": true, ".TemporaryItems": true}
	junkFiles = map[string]bool{".DS_Store": true}
)

func junkFile(name string) bool { return junkFiles[name] || strings.HasPrefix(name, "._") }

// systemTops are top-level folders whose empty subfolders are left alone:
// Onion, emulators and apps expect them.
var systemTops = map[string]bool{
	".tmp_update": true, "miyoo": true, "miyoo354": true, "App": true, "Emu": true, "RApp": true,
	"RetroArch": true, "Saves": true, "BIOS": true, "Themes": true, "Icons": true,
}

// emptyOK reports whether an empty folder at rel may be offered for
// deletion: not a top-level folder, nothing in Onion's, an app's or an
// emulator's folders, not a system's folder in Roms (Roms/GBA) and not a
// box-art folder.
func emptyOK(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) < 2 || systemTops[parts[0]] {
		return false
	}
	if parts[0] == "Roms" && len(parts) < 3 {
		return false
	}
	return parts[len(parts)-1] != "Imgs"
}

// protectedRe matches files that hold keys or secrets: never deleted from
// here (minideck.key, hostkey_ed25519, authorized_keys, state.enc, *.pem,
// id_ed25519). "key" must stand as its own word, so Donkey Kong is fine.
var protectedRe = regexp.MustCompile(`(?i)(^|[._ -])(host)?keys?([._ -]|$)|hostkey|authorized_keys|\.enc$|\.pem$|^id_(rsa|ecdsa|ed25519|dsa)`)

func protectedName(name string) bool { return protectedRe.MatchString(name) }

// logRe: the log files clean-up offers in app folders, when large.
var logRe = regexp.MustCompile(`(?i)\.log(\.old|\.[0-9])?$`)

const bigLog = 1 << 20

// onDisk is the space a file takes on the card: its allocated blocks, or
// its length where the file system doesn't say.
func onDisk(fi fs.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Blocks > 0 {
		return int64(st.Blocks) * 512
	}
	return fi.Size()
}

// walker measures one card.
type walker struct {
	ctx    context.Context
	root   string
	prog   *Progress
	errors int
	c      map[string]*Cleanup
	n      int // entries since the last cancellation check
}

func newCleanups() map[string]*Cleanup {
	return map[string]*Cleanup{
		"minimart": {ID: "minimart", Name: "MiniMart downloads", About: "Thumbnails and downloads", Ask: "Delete MiniMart's downloads?",
			Note: "MiniMart downloads them again when it needs them."},
		"minitape": {ID: "minitape", Name: "MiniTape songs", About: "Songs saved while playing", Ask: "Delete MiniTape's songs?",
			Note: "MiniTape downloads songs again when you play them. A song that is downloading right now may stop."},
		"lists": {ID: "lists", Name: "Game list caches", About: "Onion makes them again", Ask: "Delete the game list caches?",
			Note: "Onion makes each list again the next time you open that system, so the first open is slower."},
		"logs": {ID: "logs", Name: "Logs", About: "RetroArch, Onion, big app logs", Ask: "Delete the logs?",
			Note: "Apps start new logs when they need them."},
		"junk": {ID: "junk", Name: "macOS leftovers", About: "._ files, .DS_Store, .Trashes", Ask: "Delete the macOS leftovers?",
			Note: "A Mac writes them again when the card is next plugged into it."},
		"empty": {ID: "empty", Name: "Empty folders", About: "Outside Onion's and apps' folders", Ask: "Delete the empty folders?"},
	}
}

// cleanupOrder is the order the Clean up screen lists them in.
var cleanupOrder = []string{"minimart", "minitape", "lists", "logs", "junk", "empty"}

// measure walks the card at root and returns its tree and clean-ups. It
// stops with ctx's error when ctx is cancelled. Links aren't followed.
func measure(ctx context.Context, root string, prog *Progress) (*Scan, error) {
	if prog == nil {
		prog = &Progress{}
	}
	w := &walker{ctx: ctx, root: root, prog: prog, c: newCleanups()}
	fi, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", root)
	}
	tree := &Dir{Name: filepath.Base(root), Path: root}
	if _, err := w.dir(tree, "", false, false); err != nil {
		return nil, err
	}
	s := &Scan{Root: root, Tree: tree, Errors: w.errors, Measured: time.Now()}
	for _, id := range cleanupOrder {
		s.Cleanups = append(s.Cleanups, w.c[id])
	}
	return s, nil
}

// dir measures d (at rel from the root) and everything in it. claimed:
// d is inside a folder a clean-up already lists whole (a macOS leftover
// folder, a cache's subfolder), so nothing in it is listed again; secret:
// inside MiniKey, where nothing is offered for clean-up. It reports
// whether d holds no files at any depth.
func (w *walker) dir(d *Dir, rel string, claimed, secret bool) (empty bool, err error) {
	ents, err := os.ReadDir(d.Path)
	if err != nil {
		w.errors++
		if len(ents) == 0 {
			return false, nil
		}
	}
	empty = true
	emptyMark := len(w.c["empty"].Paths)
	cacheDir := ""
	switch rel {
	case "App/MiniMart/cache":
		cacheDir = "minimart"
	case "App/MiniTape/streams":
		cacheDir = "minitape"
	}
	logsDir := rel == "RetroArch/.retroarch/logs" || rel == ".tmp_update/logs" ||
		strings.HasPrefix(rel, "RetroArch/.retroarch/logs/") || strings.HasPrefix(rel, ".tmp_update/logs/")
	for _, e := range ents {
		if w.n++; w.n >= 256 {
			w.n = 0
			if err := w.ctx.Err(); err != nil {
				return false, err
			}
		}
		name := e.Name()
		p := filepath.Join(d.Path, name)
		r := name
		if rel != "" {
			r = rel + "/" + name
		}
		if rel == "" {
			w.prog.where.Store(name)
		}
		fi, err := e.Info()
		if err != nil {
			continue // gone since ReadDir
		}
		sz := onDisk(fi)
		if e.IsDir() {
			sub := &Dir{Name: name, Path: p, parent: d, Size: sz}
			isJunk := !claimed && !secret && junkDirs[name]
			subEmpty, err := w.dir(sub, r, claimed || isJunk || cacheDir != "", secret || r == "App/MiniKey")
			if err != nil {
				return false, err
			}
			d.Subs = append(d.Subs, sub)
			d.Size += sub.Size
			d.Files += sub.Files
			if !subEmpty {
				empty = false
			}
			switch {
			case claimed || secret:
			case isJunk:
				w.c["junk"].add(p, sub.Size, sub.Files)
			case cacheDir != "":
				w.c[cacheDir].add(p, sub.Size, sub.Files)
			}
			continue
		}
		// files (and links, which count as files: they aren't followed)
		empty = false
		d.Size += sz
		d.Files++
		w.prog.Files.Add(1)
		w.prog.Bytes.Add(sz)
		if claimed || secret || protectedName(name) || !fi.Mode().IsRegular() {
			continue
		}
		switch {
		case cacheDir != "":
			w.c[cacheDir].add(p, sz, 1)
		case junkFile(name):
			w.c["junk"].add(p, sz, 1)
		case strings.HasPrefix(r, "Roms/") && strings.Count(r, "/") == 2 && strings.HasSuffix(name, "_cache6.db"):
			w.c["lists"].add(p, sz, 1)
		case logsDir:
			w.c["logs"].add(p, sz, 1)
		case strings.HasPrefix(r, "App/") && logRe.MatchString(name) && fi.Size() > bigLog:
			w.c["logs"].add(p, sz, 1)
		}
	}
	sort.SliceStable(d.Subs, func(i, j int) bool { return d.Subs[i].Size > d.Subs[j].Size })
	if empty && !claimed && !secret && emptyOK(rel) {
		// one entry for the outermost empty folder, not one per level
		e := w.c["empty"]
		for _, s := range e.Sizes[emptyMark:] {
			e.Size -= s
		}
		e.Paths, e.Sizes, e.Count = e.Paths[:emptyMark], e.Sizes[:emptyMark], e.Count[:emptyMark]
		e.add(d.Path, d.Size, 0)
	}
	return empty, nil
}

// Entry is one row of a folder's contents: a folder from the tree or a
// file read when the folder is opened.
type Entry struct {
	Name string
	Path string
	Size int64
	Dir  *Dir // nil for a file
}

// maxEntries is how many rows a folder shows; the rest are its smallest.
const maxEntries = 2000

// Contents lists d's folders and files, largest first, and how many small
// files were left out to keep the list at maxEntries.
func Contents(d *Dir) ([]Entry, int) {
	var out []Entry
	for _, s := range d.Subs {
		out = append(out, Entry{Name: s.Name, Path: s.Path, Size: s.Size, Dir: s})
	}
	ents, _ := os.ReadDir(d.Path)
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Entry{Name: e.Name(), Path: filepath.Join(d.Path, e.Name()), Size: onDisk(fi)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if len(out) > maxEntries {
		return out[:maxEntries], len(out) - maxEntries
	}
	return out, 0
}

// errRefused wraps the reason a delete is refused.
var errRefused = errors.New("refused")

func refuse(why string) error { return fmt.Errorf("%w: %s", errRefused, why) }

// checkDelete reports why p must not be deleted from the Storage screen,
// or nil. It refuses anything outside the card, the card's top level
// (Onion's layout and the swap file), Onion's system folders, whole app
// folders (MiniMart removes apps with their boot scripts), MiniKey and
// MiniTop, and files that hold keys or secrets, also inside a folder.
func checkDelete(root, p string) error {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return refuse("it isn't on the SD card")
	}
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	switch {
	case len(parts) == 1:
		return refuse("the card's top level is Onion's layout. Open the folder and delete what's in it")
	case parts[0] == ".tmp_update" || parts[0] == "miyoo" || parts[0] == "miyoo354":
		return refuse("these are Onion's own files")
	case parts[0] == "App" && (parts[1] == "MiniKey" || parts[1] == "MiniTop"):
		return refuse(parts[1] + "'s files are kept")
	case parts[0] == "App" && len(parts) == 2:
		return refuse("remove apps in MiniMart, which also takes out their boot scripts")
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if protectedName(fi.Name()) {
		return refuse(fi.Name() + " holds a key or a secret")
	}
	if !fi.IsDir() {
		return nil
	}
	var found string
	stop := errors.New("found")
	filepath.WalkDir(p, func(q string, e fs.DirEntry, err error) error {
		if err == nil && q != p && protectedName(e.Name()) {
			found = q
			return stop
		}
		return nil
	})
	if found != "" {
		r, _ := filepath.Rel(root, found)
		return refuse("it holds " + filepath.ToSlash(r) + ", a key or a secret")
	}
	return nil
}

// warning is an extra line for the delete confirmation, by area.
func warning(root, p string) string {
	rel, _ := filepath.Rel(root, p)
	switch strings.Split(filepath.ToSlash(rel), "/")[0] {
	case "Saves":
		return " Saved games in it are lost."
	case "BIOS":
		return " Games that need it stop working."
	case "Roms":
		return ""
	case "Emu", "RApp", "RetroArch":
		return " Games that use it may stop working."
	}
	return ""
}

// remove deletes p (a file or a folder with everything in it). A path
// that's already gone counts as removed.
func remove(p string) error {
	err := os.RemoveAll(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Forget takes a deleted path out of the scan: its size leaves every
// folder above it, its folder leaves the tree, and clean-ups no longer
// list it or anything in it.
func (s *Scan) Forget(p string, size int64, files int, d *Dir) {
	if d != nil {
		if par := d.parent; par != nil {
			for i, c := range par.Subs {
				if c == d {
					par.Subs = append(par.Subs[:i:i], par.Subs[i+1:]...)
					break
				}
			}
		}
		size, files = d.Size, d.Files
	}
	for a := s.dirOf(filepath.Dir(p)); a != nil; a = a.parent {
		a.Size -= size
		a.Files -= files
	}
	for _, c := range s.Cleanups {
		c.forget(p)
	}
}

// forget drops p and everything in it from c's list.
func (c *Cleanup) forget(p string) {
	old := *c
	c.Paths, c.Sizes, c.Count, c.Size, c.Files = nil, nil, nil, 0, 0
	for i, q := range old.Paths {
		if q == p || strings.HasPrefix(q, p+string(filepath.Separator)) {
			continue
		}
		c.add(q, old.Sizes[i], old.Count[i])
	}
}

// dirOf finds the tree's folder for path p, or nil.
func (s *Scan) dirOf(p string) *Dir {
	rel, err := filepath.Rel(s.Root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}
	d := s.Tree
	if rel == "." {
		return d
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		var next *Dir
		for _, c := range d.Subs {
			if c.Name == part {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		d = next
	}
	return d
}

// run deletes everything c lists, calling step after each path. It
// returns how many paths were removed and the first error; it goes on
// past errors. Every path is checked again: inside the card, not the
// card's top level, not Onion's or MiniKey's (logs in .tmp_update/logs
// excepted), not a key.
func (c *Cleanup) run(root string, step func(done, total int)) (int, error) {
	var first error
	n := 0
	for i, p := range c.Paths {
		if err := cleanupOK(root, p); err != nil {
			if first == nil {
				first = err
			}
		} else if err := remove(p); err != nil {
			if first == nil {
				first = err
			}
		} else {
			n++
		}
		if step != nil {
			step(i+1, len(c.Paths))
		}
	}
	return n, first
}

// cleanupOK re-checks a clean-up path just before it's deleted. On the
// card's top level only macOS leftovers (.DS_Store, .Trashes) may go.
func cleanupOK(root, p string) error {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return refuse(p + " isn't on the SD card")
	}
	rel = filepath.ToSlash(rel)
	if !strings.Contains(rel, "/") && !junkFile(rel) && !junkDirs[rel] {
		return refuse(rel + " is on the card's top level")
	}
	if strings.HasPrefix(rel, "App/MiniKey/") || protectedName(filepath.Base(p)) {
		return refuse(rel + " is kept")
	}
	if strings.HasPrefix(rel, ".tmp_update/") && !strings.HasPrefix(rel, ".tmp_update/logs/") && !junkFile(filepath.Base(rel)) {
		return refuse(rel + " is Onion's")
	}
	return nil
}

// space returns the card's free and total bytes.
func space(root string) (free, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(root, &st); err != nil {
		return 0, 0, err
	}
	bs := int64(st.Bsize)
	return int64(st.Bavail) * bs, int64(st.Blocks) * bs, nil
}

// size formats bytes for the screen: 512 B, 3.4 KB, 12.3 MB, 1.25 GB.
func size(b int64) string {
	switch {
	case b < 1<<10:
		return fmt.Sprintf("%d B", b)
	case b < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	case b < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b < 100<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%.0f GB", float64(b)/(1<<30))
}

// count formats a number with thousands separators: 12,345.
func count(n int64) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// measurer runs one measurement at a time in the background and keeps
// the last result for the session.
type measurer struct {
	root   string
	dir    string // where the last measurement is kept between sessions ("": not kept)
	mu     sync.Mutex
	scan   *Scan
	old    *Scan // the kept measurement, shown until scan is done
	err    error
	prog   *Progress
	done   chan struct{} // closed when the running measurement ends; nil if none
	cancel context.CancelFunc
}

// start begins measuring unless a measurement is running or done.
func (m *measurer) start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.done != nil || m.scan != nil {
		return
	}
	if m.old == nil && m.dir != "" {
		if s, err := loadScan(m.dir, m.root); err == nil {
			m.old = s
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.prog, m.cancel, m.err = &Progress{}, cancel, nil
	done := make(chan struct{})
	m.done = done
	prog := m.prog
	go func() {
		s, err := measure(ctx, m.root, prog)
		if err == nil && ctx.Err() == nil && m.dir != "" {
			saveScan(m.dir, s) // best effort: the page works without it
		}
		m.mu.Lock()
		if ctx.Err() == nil {
			m.scan, m.err = s, err
			if err == nil {
				m.old = nil
			}
		}
		m.done = nil
		m.mu.Unlock()
		close(done)
	}()
}

// again drops the last result and measures again.
func (m *measurer) again() {
	m.stop()
	m.mu.Lock()
	m.scan, m.err = nil, nil
	m.mu.Unlock()
	m.start()
}

// stop cancels a running measurement and waits for it to end.
func (m *measurer) stop() {
	m.mu.Lock()
	done, cancel := m.done, m.cancel
	m.mu.Unlock()
	if done != nil {
		cancel()
		<-done
	}
}

// state returns the finished scan (nil while measuring), the running
// measurement's progress, a channel closed when it ends, and the last
// measurement's error.
func (m *measurer) state() (*Scan, *Progress, <-chan struct{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scan, m.prog, m.done, m.err
}

// view is the measurement to show: this session's, or else the kept one.
func (m *measurer) view() *Scan {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scan != nil {
		return m.scan
	}
	return m.old
}

// keep saves s again after a delete changed it, if it is this session's
// measurement.
func (m *measurer) keep(s *Scan) {
	m.mu.Lock()
	cur := m.scan
	m.mu.Unlock()
	if s == cur && s != nil && m.dir != "" {
		saveScan(m.dir, s)
	}
}

// wait blocks until no measurement is running.
func (m *measurer) wait() {
	if _, _, done, _ := m.state(); done != nil {
		<-done
	}
}

// swapInfo returns free and total swap from /proc/meminfo, in bytes.
func swapInfo() (free, total int64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(line, ":")
		var n int64
		fmt.Sscan(strings.TrimSuffix(strings.TrimSpace(v), " kB"), &n)
		switch k {
		case "SwapTotal":
			total = n * 1024
		case "SwapFree":
			free = n * 1024
		}
	}
	return
}
