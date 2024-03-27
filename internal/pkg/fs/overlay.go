package fs

import (
	"io/fs"
	"sort"
	"strings"
)

type OverlayFS struct {
	Base     fs.FS
	Overlays map[string]fs.FS
}

func NewOverlayFS(base fs.FS) *OverlayFS {
	return &OverlayFS{
		Base:     base,
		Overlays: make(map[string]fs.FS),
	}
}

func (o *OverlayFS) AddOverlay(mountPoint string, overlay fs.FS) {
	o.Overlays[mountPoint] = overlay
}

func (o *OverlayFS) Open(name string) (fs.File, error) {
	overlayFS, overlayPath := o.resolvePath(name)
	return overlayFS.Open(overlayPath)
}

func (o *OverlayFS) ReadDir(name string) ([]fs.DirEntry, error) {
	// Base directory listing
	entries, err := fs.ReadDir(o.Base, name)
	if err != nil {
		return nil, err
	}

	// Map to track seen entries for de-duplication
	seen := make(map[string]bool)
	for _, entry := range entries {
		seen[entry.Name()] = true
	}

	// Check for overlays and merge their listings
	for mountPoint, overlay := range o.Overlays {
		if strings.HasPrefix(mountPoint, name) {
			// Overlay is within the directory being listed
			overlayName := strings.TrimPrefix(mountPoint, name)
			if !seen[overlayName] {
				// Add the mount point as a directory
				entries = append(entries, &dirEntry{name: overlayName})
				seen[overlayName] = true
			}
		} else if strings.HasPrefix(name, mountPoint) {
			// Directory being listed is within an overlay
			overlayEntries, err := fs.ReadDir(overlay, strings.TrimPrefix(name, mountPoint))
			if err == nil {
				for _, entry := range overlayEntries {
					if !seen[entry.Name()] {
						entries = append(entries, entry)
						seen[entry.Name()] = true
					}
				}
			}
		}
	}

	// Sort the final listing for consistency
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	return entries, nil
}

func (o *OverlayFS) resolvePath(name string) (fs.FS, string) {
	for mountPoint, overlay := range o.Overlays {
		if strings.HasPrefix(name, mountPoint) {
			return overlay, strings.TrimPrefix(name, mountPoint)
		}
	}
	return o.Base, name
}

type dirEntry struct {
	name string
}

func (d *dirEntry) Name() string               { return d.name }
func (d *dirEntry) IsDir() bool                { return true }
func (d *dirEntry) Type() fs.FileMode          { return fs.ModeDir }
func (d *dirEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrNotExist }
