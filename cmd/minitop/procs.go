package main

import (
	"bufio"
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

// End asks a program to quit, then forces it after two seconds.
func End(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if syscall.Kill(pid, 0) != nil {
			return nil
		}
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

// Memory returns available and total RAM in bytes.
func Memory() (avail, total int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), ":")
		n, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
		switch k {
		case "MemTotal":
			total = n * 1024
		case "MemAvailable":
			avail = n * 1024
		}
	}
	return
}
