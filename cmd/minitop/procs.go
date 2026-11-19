package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Proc is one running program.
type Proc struct {
	PID, PPID int
	Comm      string // kernel name
	Args      []string
	Name      string  // what people call it
	RSS       int64   // bytes
	CPU       float64 // percent of the whole CPU since the last sample
	ticks     uint64
	start     uint64 // when it started, in ticks after boot: tells a reused PID apart
	Protected string // why it can't be ended ("" = it can)
}

// friendly names MiniDeck's and Onion's background programs, by the name
// in their command line.
var friendly = map[string]string{
	"tailscaled":      "MiniScale (Tailscale)",
	"miniscale-badge": "MiniScale status icon",
	"miniscale-fwd":   "MiniScale port forwards",
	"MainUI":          "Onion menu",
	"keymon":          "Onion buttons",
	"batmon":          "Onion battery",
	"audioserver":     "Onion audio",
	"wpa_supplicant":  "Wi-Fi",
	"udhcpc":          "Wi-Fi address",
	"gameSwitcher":    "Onion game switcher",
	"retroarch":       "RetroArch (game)",
	"ffplay":          "Audio player (ffplay)",
	"minifin-player":  "MiniFin video player",
	"filebrowser":     "File server (HTTP)",
	"bftpd":           "FTP server",
	"smbd":            "Samba file sharing",
	"telnetd":         "Telnet",
}

// Programs Onion needs: ending them would freeze the menu, the buttons,
// sound or Wi-Fi until a restart.
var essential = map[string]string{
	"init": "the system itself", "linuxrc": "the system itself", "main": "Onion's start-up",
	"runtime.sh": "Onion itself", "MainUI": "the Onion menu", "keymon": "the buttons",
	"batmon": "battery monitoring", "audioserver": "all sound", "wpa_supplicant": "Wi-Fi",
	"udhcpc": "Wi-Fi", "dropbear": "Onion's SSH (turn it off in Tweaks › Network)",
}

type sampler struct {
	last      map[int]uint64
	lastTotal uint64
	self      map[int]bool // this app and the shells that started it
}

func newSampler() *sampler {
	s := &sampler{last: map[int]uint64{}, self: map[int]bool{}}
	for p := os.Getpid(); p > 1; {
		s.self[p] = true
		pp, ok := readPPID(p)
		if !ok || pp <= 1 {
			break
		}
		p = pp
	}
	return s
}

func readPPID(pid int) (int, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	f := statFields(string(b))
	if len(f) < 2 {
		return 0, false
	}
	pp, err := strconv.Atoi(f[1])
	return pp, err == nil
}

// statFields splits /proc/PID/stat after the "(comm)" field, which may
// contain spaces: f[0] is the state, f[1] the parent PID, f[11] utime...
func statFields(s string) []string {
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return nil
	}
	return strings.Fields(s[i+1:])
}

func totalTicks() uint64 {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0
	}
	var t uint64
	for _, x := range strings.Fields(sc.Text())[1:] {
		v, _ := strconv.ParseUint(x, 10, 64)
		t += v
	}
	return t
}

// Sample lists the running programs (not kernel threads), heaviest first.
func (s *sampler) Sample() []Proc {
	total := totalTicks()
	dt := total - s.lastTotal
	ents, _ := os.ReadDir("/proc")
	page := int64(os.Getpagesize())
	seen := map[int]uint64{}
	var out []Proc
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cmd, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil || len(cmd) == 0 {
			continue // a kernel thread, or gone
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		f := statFields(string(stat))
		if len(f) < 22 {
			continue
		}
		p := Proc{PID: pid, Args: strings.Split(strings.TrimRight(string(cmd), "\x00"), "\x00")}
		p.PPID, _ = strconv.Atoi(f[1])
		ut, _ := strconv.ParseUint(f[11], 10, 64)
		st, _ := strconv.ParseUint(f[12], 10, 64)
		p.ticks = ut + st
		p.start, _ = strconv.ParseUint(f[19], 10, 64)
		rss, _ := strconv.ParseInt(f[21], 10, 64)
		p.RSS = rss * page
		if c, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
			p.Comm = strings.TrimSpace(string(c))
		}
		if prev, ok := s.last[pid]; ok && dt > 0 && p.ticks >= prev {
			p.CPU = float64(p.ticks-prev) / float64(dt) * 100
		}
		seen[pid] = p.ticks
		p.Name, p.Protected = describe(p)
		if s.self[pid] {
			p.Protected = "this app"
			if pid == os.Getpid() {
				p.Name = "MiniTop (this app)"
			}
		}
		out = append(out, p)
	}
	s.last, s.lastTotal = seen, total
	sort.Slice(out, func(i, j int) bool {
		if out[i].CPU != out[j].CPU {
			return out[i].CPU > out[j].CPU
		}
		return out[i].RSS > out[j].RSS
	})
	return out
}

// describe names a process and says whether it's protected.
func describe(p Proc) (name, protected string) {
	exe := filepath.Base(p.Args[0])
	exe = strings.TrimPrefix(exe, "-")
	why := essential[p.Comm]
	if why == "" {
		why = essential[exe]
	}
	if why != "" {
		protected = "Onion needs it for " + why
	}
	// shell scripts: name them after the script
	if (exe == "sh" || exe == "bash") && len(p.Args) > 1 {
		script := filepath.Base(p.Args[len(p.Args)-1])
		for _, a := range p.Args[1:] {
			if strings.HasSuffix(a, ".sh") {
				script = filepath.Base(a)
				break
			}
		}
		if script == "miniscale.sh" {
			return "MiniScale (saver)", protected
		}
		if script == "runtime.sh" {
			return "Onion runtime", "Onion needs it for itself"
		}
		return "Script: " + script, protected
	}
	if exe == "minitape" && len(p.Args) > 1 && p.Args[1] == "daemon" {
		return "MiniTape player", protected
	}
	if n, ok := friendly[exe]; ok {
		return n, protected
	}
	if n, ok := friendly[p.Comm]; ok {
		return n, protected
	}
	return exe, protected
}

// termOnly are the programs End never forces (SIGKILL): a video player
// killed outright can wedge the display driver and freeze the screen until
// a restart, so MiniFin only ever asks its players to quit too.
var termOnly = map[string]bool{"minifin-player": true, "ffplay": true}

// gentle reports whether End only asks p to quit.
func gentle(p Proc) bool {
	return termOnly[p.Comm] || len(p.Args) > 0 && termOnly[filepath.Base(p.Args[0])]
}

// errStillRunning: a program End only asks to quit hasn't yet.
var errStillRunning = errors.New("asked to quit, still running")

// termWait is how long End waits after SIGTERM, and gentleWait for a
// program it won't force (as long as MiniFin gives its players).
var termWait, gentleWait = 2 * time.Second, 5 * time.Second

// End asks a program to quit, then forces it if it is still there after
// termWait, unless it is gentle. Before each signal it checks that the PID
// is still the program that was listed: one that ended while the question
// was on screen may have handed its PID to another.
func End(p Proc) error {
	if !running(p.PID, p.start) {
		return nil
	}
	if err := syscall.Kill(p.PID, syscall.SIGTERM); err != nil {
		return err
	}
	wait := termWait
	if gentle(p) {
		wait = gentleWait
	}
	for end := time.Now().Add(wait); time.Now().Before(end); {
		time.Sleep(100 * time.Millisecond)
		if !running(p.PID, p.start) {
			return nil
		}
	}
	if gentle(p) {
		return errStillRunning
	}
	if err := syscall.Kill(p.PID, syscall.SIGKILL); err != nil {
		return err
	}
	// gone for the list sampled next
	for i := 0; i < 10 && running(p.PID, p.start); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// running reports whether pid is still the program that started at start:
// not gone, not a zombie left for its parent to collect, not a newer
// program with the same PID.
func running(pid int, start uint64) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	f := statFields(string(b))
	return len(f) > 19 && f[0] != "Z" && f[19] == strconv.FormatUint(start, 10)
}

// meminfo is /proc/meminfo's fields, in bytes.
func meminfo() map[string]int64 {
	b, _ := os.ReadFile("/proc/meminfo")
	return parseMeminfo(string(b))
}

func parseMeminfo(s string) map[string]int64 {
	m := map[string]int64{}
	for _, l := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64); ok && err == nil {
			m[k] = n * 1024
		}
	}
	return m
}

// Memory returns available and total RAM in bytes.
func Memory() (avail, total int64) {
	m := meminfo()
	return m["MemAvailable"], m["MemTotal"]
}

// memLine is the memory part of the Running subtitle: free RAM, and the
// swap in use when there is any (Onion can add a swap file).
func memLine(m map[string]int64) string {
	s := fmt.Sprintf("%.1f of %s free", float64(m["MemAvailable"])/(1<<20), mb(m["MemTotal"]))
	if t := m["SwapTotal"]; t > 0 {
		// short enough to fit the subtitle at 640 px with the swap shown
		s += fmt.Sprintf(" · %s swap", mb(t-m["SwapFree"]))
	}
	return s
}
