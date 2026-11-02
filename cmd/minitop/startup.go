package main

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// StartItem is a script Onion runs at boot from .tmp_update/startup/.
// MiniScale also keeps an "autostart" flag in its folder, which its hook
// checks; for it, on/off is the flag, the same switch the app itself uses.
type StartItem struct {
	File string // full path, with .sh or .sh.off
	Name string
	Desc string
	On   bool
	flag string // the app's autostart flag ("" for plain scripts)
}

// sysdir is Onion's system folder; TASKS_SYSDIR replaces it in tests.
func sysdir() string {
	if d := os.Getenv("TASKS_SYSDIR"); d != "" {
		return d
	}
	return "/mnt/SDCARD/.tmp_update"
}

// known names the startup hooks of apps that keep an autostart flag.
var known = map[string]struct{ name, desc, flag string }{
	"miniscale": {"MiniScale", "Joins your tailnet at boot", "/mnt/SDCARD/App/MiniScale/autostart"},
}

// StartItems lists the startup scripts, on and off, sorted by name.
func StartItems() []StartItem {
	dir := filepath.Join(sysdir(), "startup")
	ents, _ := os.ReadDir(dir)
	var out []StartItem
	for _, e := range ents {
		n := e.Name()
		on := strings.HasSuffix(n, ".sh")
		if e.IsDir() || !(on || strings.HasSuffix(n, ".sh.off")) {
			continue
		}
		base := strings.TrimSuffix(strings.TrimSuffix(n, ".off"), ".sh")
		it := StartItem{File: filepath.Join(dir, n), Name: base, On: on, Desc: describeScript(filepath.Join(dir, n))}
		if k, ok := known[base]; ok {
			it.Name, it.Desc, it.flag = k.name, k.desc, k.flag
			_, err := os.Stat(k.flag)
			it.On = on && err == nil
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// describeScript returns the script's first comment sentence, if any.
func describeScript(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var words []string
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "#!") {
			continue
		}
		if !strings.HasPrefix(l, "#") {
			break
		}
		words = append(words, strings.TrimSpace(strings.TrimPrefix(l, "#")))
	}
	s := strings.Join(words, " ")
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	return s
}

// Toggle turns an item on or off. Plain scripts are renamed (.sh <->
// .sh.off: Onion only runs *.sh); an app's autostart flag is created or
// removed.
func (it StartItem) Toggle() error {
	if it.flag != "" {
		if it.On {
			return os.Remove(it.flag)
		}
		if strings.HasSuffix(it.File, ".off") {
			if err := os.Rename(it.File, strings.TrimSuffix(it.File, ".off")); err != nil {
				return err
			}
		}
		return os.WriteFile(it.flag, nil, 0644)
	}
	if it.On {
		return os.Rename(it.File, it.File+".off")
	}
	return os.Rename(it.File, strings.TrimSuffix(it.File, ".off"))
}

// OnionStartApp says what Onion opens at boot (Tweaks › System › Startup).
func OnionStartApp() string {
	b, _ := os.ReadFile(filepath.Join(sysdir(), "config/startup/app"))
	switch strings.TrimSpace(string(b)) {
	case "1":
		return "Game switcher"
	case "2":
		return "RetroArch"
	case "3":
		return "AdvanceMENU"
	}
	return "Main menu"
}

// OnionServices lists Onion's network services and whether each starts.
func OnionServices() []StartItem {
	cfg := filepath.Join(sysdir(), "config")
	var out []StartItem
	for _, s := range []struct{ flag, name string }{
		{".sshState", "SSH"}, {".ftpState", "FTP"}, {".httpState", "File server (HTTP)"},
		{".smbdState", "Samba"}, {".telnetState", "Telnet"}, {".hotspotState", "Hotspot"},
		{".ntpState", "Time sync"},
	} {
		_, err := os.Stat(filepath.Join(cfg, s.flag))
		out = append(out, StartItem{Name: s.name, On: err == nil})
	}
	return out
}
