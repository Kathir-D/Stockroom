package stockroom

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
)

// A folder picker for this machine's disks, for the two backup folders on
// Settings and the setup guide.
//
// Both have to be a full path (validateDir), and typing one is where the
// 2026-09-21 backup went into `<repo>/Users/...` instead of the Desktop: a
// path pasted without its leading slash. The server and the admin's browser
// are on the same machine, so the server can simply show its folders and let
// the admin click. Folder names and paths only, never file names or contents,
// and only to an admin -- somebody who can already point the backup at any
// folder they like, so it is deliberately not confined to one root.

// LocalFolder is one folder in the picker.
type LocalFolder struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// LocalFolderList is GET /admin/local-folders?path=.
type LocalFolderList struct {
	Path    string        `json:"path"`
	Parent  string        `json:"parent"`
	Folders []LocalFolder `json:"folders"`
	// Places are the usual starting points: home, Desktop, Documents, and any
	// external drives -- which is where a backup most wants to go.
	Places []LocalFolder `json:"places"`
}

// LocalFolders lists the folders inside dir, or inside the home folder when
// dir is blank.
func (db *DB) LocalFolders(ctx context.Context, actor Actor, dir string) (LocalFolderList, error) {
	if err := RequireAdmin(actor); err != nil {
		return LocalFolderList{}, err
	}
	if strings.TrimSpace(dir) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return LocalFolderList{}, fmt.Errorf("find the home folder: %w", err)
		}
		dir = home
	}
	if !filepath.IsAbs(dir) {
		return LocalFolderList{}, fmt.Errorf("%w: %q is not a full path", ErrInvalid, dir)
	}
	dir = filepath.Clean(dir)
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return LocalFolderList{}, fmt.Errorf("%w: %s does not exist", ErrNotFound, dir)
	case errors.Is(err, fs.ErrPermission):
		return LocalFolderList{}, fmt.Errorf("%w: this machine will not let Stockroom open %s", ErrInvalid, dir)
	case err != nil:
		return LocalFolderList{}, fmt.Errorf("%w: could not open %s: %v", ErrInvalid, dir, err)
	}

	list := LocalFolderList{Path: dir, Folders: []LocalFolder{}, Places: localPlaces()}
	if parent := filepath.Dir(dir); parent != dir {
		list.Parent = parent
	}
	for _, e := range entries {
		name := e.Name()
		if hiddenFolder(name) {
			continue
		}
		full := filepath.Join(dir, name)
		isDir := e.IsDir()
		if !isDir && e.Type()&fs.ModeSymlink != 0 {
			if info, err := os.Stat(full); err == nil {
				isDir = info.IsDir()
			}
		}
		if isDir {
			list.Folders = append(list.Folders, LocalFolder{Name: name, Path: full})
		}
	}
	sort.Slice(list.Folders, func(i, j int) bool {
		return strings.ToLower(list.Folders[i].Name) < strings.ToLower(list.Folders[j].Name)
	})
	return list, nil
}

// CreateLocalFolder is POST /admin/local-folders: one new folder inside parent.
// A folder that already exists is simply returned.
func (db *DB) CreateLocalFolder(ctx context.Context, actor Actor, parent, name string) (LocalFolder, error) {
	if err := RequireAdmin(actor); err != nil {
		return LocalFolder{}, err
	}
	name = strings.TrimSpace(name)
	if err := validFolderName(name); err != nil {
		return LocalFolder{}, err
	}
	if !filepath.IsAbs(parent) {
		return LocalFolder{}, fmt.Errorf("%w: %q is not a full path", ErrInvalid, parent)
	}
	full := filepath.Join(filepath.Clean(parent), name)
	if err := os.Mkdir(full, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return LocalFolder{}, fmt.Errorf("%w: could not make %s: %v", ErrInvalid, full, err)
	}
	return LocalFolder{Name: name, Path: full}, nil
}

// hiddenFolder is what a file manager would not show: dot folders, and on
// Windows the system ones that start with $.
func hiddenFolder(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") ||
		name == "System Volume Information"
}

func localPlaces() []LocalFolder {
	places := []LocalFolder{}
	add := func(name, path string) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			places = append(places, LocalFolder{Name: name, Path: path})
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add("Home", home)
		add("Desktop", filepath.Join(home, "Desktop"))
		add("Documents", filepath.Join(home, "Documents"))
	}
	// External drives: where a backup most wants to go, and the folder a
	// person is least likely to be able to type the path of.
	var drives []string
	switch runtime.GOOS {
	case "darwin":
		drives, _ = filepath.Glob("/Volumes/*")
	case "linux":
		if user := os.Getenv("USER"); user != "" {
			drives, _ = filepath.Glob(filepath.Join("/media", user, "*"))
		}
		more, _ := filepath.Glob("/mnt/*")
		drives = append(drives, more...)
	case "windows":
		for c := 'A'; c <= 'Z'; c++ {
			drives = append(drives, string(c)+`:\`)
		}
	}
	for _, d := range drives {
		name := filepath.Base(d)
		if runtime.GOOS == "windows" {
			name = d
		}
		add(name, d)
	}
	return places
}
