package main

// The last measurement of the card is kept in MiniTop's folder, so the
// Storage page opens with it at once, labelled with its age, while the
// card is measured again in the background. Only the folder tree is kept:
// clean-ups always come from a measurement made this session, so nothing
// is deleted from an old list.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zakijss/minideck/atomicfile"
)

// savedDir is a Dir as it is kept on the card.
type savedDir struct {
	N string      `json:"n"`
	S int64       `json:"s"`
	F int         `json:"f"`
	D []*savedDir `json:"d,omitempty"`
}

type savedScan struct {
	V        int       `json:"v"`
	Root     string    `json:"root"`
	Measured time.Time `json:"measured"`
	Errors   int       `json:"errors"`
	Tree     *savedDir `json:"tree"`
}

// maxSavedDirs: a tree with more folders than this isn't kept (the file
// would be large and slow to read on the device).
const maxSavedDirs = 60000

func scanFile(dir string) string { return filepath.Join(dir, "storage.json") }

// saveScan keeps s's tree in dir.
func saveScan(dir string, s *Scan) error {
	n := 0
	var conv func(d *Dir) *savedDir
	conv = func(d *Dir) *savedDir {
		n++
		sd := &savedDir{N: d.Name, S: d.Size, F: d.Files}
		for _, c := range d.Subs {
			sd.D = append(sd.D, conv(c))
		}
		return sd
	}
	doc := savedScan{V: 1, Root: s.Root, Measured: s.Measured, Errors: s.Errors, Tree: conv(s.Tree)}
	if n > maxSavedDirs {
		return fmt.Errorf("%d folders: too many to keep", n)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return atomicfile.Write(scanFile(dir), b, 0644)
}

// loadScan reads the tree kept in dir for the card at root. A file for
// another root, in another format, or with a folder name that isn't a
// plain name is not used.
func loadScan(dir, root string) (*Scan, error) {
	b, err := os.ReadFile(scanFile(dir))
	if err != nil {
		return nil, err
	}
	var doc savedScan
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if doc.V != 1 || doc.Root != root || doc.Tree == nil || doc.Measured.IsZero() {
		return nil, errors.New("not a measurement of this card")
	}
	bad := errors.New("damaged measurement")
	var conv func(sd *savedDir, parent *Dir, path string, depth int) (*Dir, error)
	conv = func(sd *savedDir, parent *Dir, path string, depth int) (*Dir, error) {
		if depth > 64 || sd.S < 0 || sd.F < 0 {
			return nil, bad
		}
		d := &Dir{Name: sd.N, Path: path, Size: sd.S, Files: sd.F, parent: parent}
		for _, c := range sd.D {
			if c == nil || c.N == "" || c.N == "." || c.N == ".." || strings.ContainsAny(c.N, "/\x00") {
				return nil, bad
			}
			cd, err := conv(c, d, filepath.Join(path, c.N), depth+1)
			if err != nil {
				return nil, err
			}
			d.Subs = append(d.Subs, cd)
		}
		return d, nil
	}
	tree, err := conv(doc.Tree, nil, root, 0)
	if err != nil {
		return nil, err
	}
	return &Scan{Root: root, Tree: tree, Errors: doc.Errors, Measured: doc.Measured, Saved: true}, nil
}

// ago says how long ago t was, for people: "just now", "5 min ago",
// "3 h ago", "2 days ago".
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%d days ago", int(d/(24*time.Hour)))
}
