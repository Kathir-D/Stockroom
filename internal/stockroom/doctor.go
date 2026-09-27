package stockroom

import (
	"context"
	"fmt"
)

// The few reads `stockroom doctor` needs from the database, exported here so
// the checks can live in internal/setup.

// FreeSpace reports the bytes available on the filesystem holding dir.
func FreeSpace(dir string) (int64, error) { return freeSpace(dir) }

// Folders are the folders an admin chose in the panel.
type Folders struct {
	Backup      string
	PhotoBackup string
	// Recordings is set only while the closet camera is on.
	Recordings string
}

// SavedFolders reads Folders from app_settings and camera_settings.
func (db *DB) SavedFolders(ctx context.Context) (Folders, error) {
	s, err := db.loadSettings(ctx)
	if err != nil {
		return Folders{}, err
	}
	f := Folders{Backup: s.BackupDir, PhotoBackup: s.PhotoBackupDir}
	if db.BackupDir != "" {
		f.Backup = db.BackupDir
	}
	cam, err := db.loadCameraSettings(ctx)
	if err != nil {
		return f, fmt.Errorf("camera settings: %w", err)
	}
	if cam.Enabled {
		f.Recordings = cam.RecordingsDir
	}
	return f, nil
}
