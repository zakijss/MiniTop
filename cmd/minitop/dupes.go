package main

// Duplicate games: ROM files under Roms/ that are byte for byte the same.
// Files are grouped by size, then by their first and last 64 KB, and only
// then read whole for a SHA-1, so most of the card is never read. Ports,
// ScummVM games (folders of shared engine files), box art and disc tracks
// (.bin, which .cue sheets name) are left out, and a file that a .cue,
// .m3u, .gdi or .ccd in its folder names is marked as in use: deleting it
// alone would break that game.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

// romExts are the single-file game formats looked at, lower case.
var romExts = map[string]bool{}

func init() {
	for _, e := range strings.Fields(`gb gbc gba sgb nes fds unf sfc smc fig swc md gen smd 32x sms gg sg pce sgx
		n64 z64 v64 nds a26 a52 a78 lnx j64 jag ws wsc ngp ngc vb col int vec min
		pbp chd iso cso rvz zip 7z`) {
		romExts["."+e] = true
	}
}

// dupSkipSystems are folders of Roms/ that are never looked in.
var dupSkipSystems = map[string]bool{"PORTS": true, "SCUMMVM": true}

// minDupSize: smaller files aren't looked at (a few KB of homebrew).
const minDupSize = 16 << 10

// maxDupDepth: how deep under Roms/<system> files are looked for.
const maxDupDepth = 4

// DupFile is one copy.
type DupFile struct {
	Path  string
	Rel   string // from the card's root
	InUse string // the .cue/.m3u/.gdi/.ccd in its folder that names it, if any
}

// DupGroup is a set of identical files.
type DupGroup struct {
	Size  int64
	Sum   string // SHA-1
	Files []DupFile
}

// Wasted is the space the extra copies take.
func (g DupGroup) Wasted() int64 { return g.Size * int64(len(g.Files)-1) }

// DupProgress is a running search's progress.
type DupProgress struct {
	Files atomic.Int64 // ROM files found
	Read  atomic.Int64 // bytes read for checksums
	Total atomic.Int64 // bytes to read
}

type dupCand struct {
	path, rel string
	size      int64
	inUse     string
}

// findDuplicates looks for identical games under root/Roms.
func findDuplicates(ctx context.Context, root string, prog *DupProgress) ([]DupGroup, error) {
	if prog == nil {
		prog = &DupProgress{}
	}
	var cands []dupCand
	var walk func(dir, rel string, depth int) error
	walk = func(dir, rel string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return nil // an unreadable folder is skipped
		}
		used := discRefs(dir, ents)
		for _, e := range ents {
			name := e.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "._") {
				continue
			}
			p, r := filepath.Join(dir, name), rel+"/"+name
			switch {
			case e.IsDir():
				if depth == 0 && dupSkipSystems[strings.ToUpper(name)] || strings.EqualFold(name, "Imgs") || depth >= maxDupDepth {
					continue
				}
				if err := walk(p, r, depth+1); err != nil {
					return err
				}
			case e.Type().IsRegular() && depth > 0 && romExts[strings.ToLower(filepath.Ext(name))]:
				fi, err := e.Info()
				if err != nil || fi.Size() < minDupSize {
					continue
				}
				prog.Files.Add(1)
				cands = append(cands, dupCand{p, r, fi.Size(), used[strings.ToLower(name)]})
			}
		}
		return nil
	}
	if err := walk(filepath.Join(root, "Roms"), "Roms", 0); err != nil {
		return nil, err
	}

	bySize := map[int64][]dupCand{}
	for _, c := range cands {
		bySize[c.size] = append(bySize[c.size], c)
	}
	// Ends first: files of one size that differ almost always differ there.
	var full [][]dupCand
	for _, cs := range bySize {
		if len(cs) < 2 {
			continue
		}
		byEnds := map[string][]dupCand{}
		for _, c := range cs {
			k, err := endsKey(c.path, c.size)
			if err != nil {
				continue
			}
			byEnds[k] = append(byEnds[k], c)
		}
		for _, g := range byEnds {
			if len(g) > 1 {
				full = append(full, g)
				for _, c := range g {
					prog.Total.Add(c.size)
				}
			}
		}
	}
	var out []DupGroup
	for _, g := range full {
		bySum := map[string][]dupCand{}
		for _, c := range g {
			sum, err := fileSHA1(ctx, c.path, prog)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			bySum[sum] = append(bySum[sum], c)
		}
		for sum, cs := range bySum {
			if len(cs) < 2 {
				continue
			}
			dg := DupGroup{Size: cs[0].size, Sum: sum}
			sort.Slice(cs, func(i, j int) bool { return cs[i].rel < cs[j].rel })
			for _, c := range cs {
				dg.Files = append(dg.Files, DupFile{Path: c.path, Rel: c.rel, InUse: c.inUse})
			}
			out = append(out, dg)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Wasted() != out[j].Wasted() {
			return out[i].Wasted() > out[j].Wasted()
		}
		return out[i].Files[0].Rel < out[j].Files[0].Rel
	})
	return out, nil
}

const endBytes = 64 << 10

// endsKey is the file's first and last 64 KB, hashed.
func endsKey(p string, size int64) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.CopyN(h, f, min(size, endBytes)); err != nil {
		return "", err
	}
	if size > endBytes {
		if _, err := f.Seek(max(size-endBytes, endBytes), io.SeekStart); err != nil {
			return "", err
		}
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fileSHA1 reads the whole file, stopping when ctx is cancelled.
func fileSHA1(ctx context.Context, p string, prog *DupProgress) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		h.Write(buf[:n])
		prog.Read.Add(int64(n))
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var (
	cueFileRe = regexp.MustCompile(`(?i)^\s*FILE\s+(?:"([^"]+)"|(\S+))`)
	gdiNameRe = regexp.MustCompile(`(?i)(?:"([^"]+\.(?:bin|raw|iso))"|(\S+\.(?:bin|raw|iso)))`)
)

// discRefs reads the disc sheets and playlists in a folder and returns the
// file names they name (lower case) with the sheet naming each.
func discRefs(dir string, ents []os.DirEntry) map[string]string {
	out := map[string]string{}
	for _, e := range ents {
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !e.Type().IsRegular() || ext != ".cue" && ext != ".m3u" && ext != ".gdi" && ext != ".ccd" {
			continue
		}
		if ext == ".ccd" { // CloneCD: the image and subchannel share its name
			stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
			out[stem+".img"], out[stem+".sub"] = name, name
			continue
		}
		b, err := readSmall(filepath.Join(dir, name), 256<<10)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			var ref string
			switch ext {
			case ".cue":
				if m := cueFileRe.FindStringSubmatch(line); m != nil {
					ref = m[1] + m[2]
				}
			case ".m3u":
				if line != "" && !strings.HasPrefix(line, "#") {
					ref = line
				}
			case ".gdi":
				if m := gdiNameRe.FindStringSubmatch(line); m != nil {
					ref = m[1] + m[2]
				}
			}
			if ref != "" {
				out[strings.ToLower(filepath.Base(strings.ReplaceAll(ref, `\`, "/")))] = name
			}
		}
	}
	return out
}

func readSmall(p string, limit int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("too big")
	}
	return b, err
}

// deleteCopy deletes one copy of g, refusing the last one still on the
// card, one a disc sheet or playlist uses, and anything checkDelete
// refuses. It returns the group without it, and makes Onion re-read the
// game list of the system the copy was in.
func deleteCopy(root string, g DupGroup, i int) (DupGroup, error) {
	f := g.Files[i]
	if f.InUse != "" {
		return g, refuse(f.InUse + " uses it: delete the whole game instead")
	}
	others := 0
	for j, o := range g.Files {
		if j != i && fileExists(o.Path) {
			others++
		}
	}
	if others == 0 {
		return g, refuse("it's the only copy left")
	}
	if err := checkDelete(root, f.Path); err != nil {
		return g, err
	}
	if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
		return g, err
	}
	if parts := strings.Split(f.Rel, "/"); len(parts) > 2 {
		sys := parts[1]
		for _, v := range []string{"_cache6.db", "_cache2.db"} {
			os.Remove(filepath.Join(root, "Roms", sys, sys+v))
		}
	}
	g.Files = append(g.Files[:i:i], g.Files[i+1:]...)
	return g, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
