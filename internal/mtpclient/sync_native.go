//go:build mtp && cgo

package mtpclient

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hanwen/go-mtpfs/mtp"
)

const noParentID = 0xFFFFFFFF

func SyncRawKindleFiles(rawDir string, devicePattern string, logger *log.Logger) error {
	dev, err := mtp.SelectDevice(devicePattern)
	if err != nil {
		return fmt.Errorf("select MTP device: %w", err)
	}
	defer dev.Done()
	defer dev.Close()

	if err := dev.OpenSession(); err != nil {
		return fmt.Errorf("open MTP session: %w", err)
	}
	defer dev.CloseSession()

	var sids mtp.Uint32Array
	if err := dev.GetStorageIDs(&sids); err != nil {
		return fmt.Errorf("get storage IDs: %w", err)
	}

	for _, sid := range sids.Values {
		var si mtp.StorageInfo
		if err := dev.GetStorageInfo(sid, &si); err != nil {
			logger.Printf("Skipping storage %d: %v", sid, err)
			continue
		}
		if !si.IsHierarchical() && !si.IsDCF() {
			continue
		}
		if err := syncFolder(dev, sid, noParentID, "", rawDir, logger); err != nil {
			logger.Printf("Storage %d sync warning: %v", sid, err)
		}
	}

	return nil
}

func syncFolder(dev *mtp.Device, storageID uint32, parent uint32, rel string, rawDir string, logger *log.Logger) error {
	var handles mtp.Uint32Array
	if err := dev.GetObjectHandles(storageID, 0, parent, &handles); err != nil {
		return fmt.Errorf("GetObjectHandles(parent=%d): %w", parent, err)
	}

	for _, handle := range handles.Values {
		var obj mtp.ObjectInfo
		if err := dev.GetObjectInfo(handle, &obj); err != nil {
			logger.Printf("GetObjectInfo(%d) failed: %v", handle, err)
			continue
		}
		name := sanitizeName(obj.Filename)
		if name == "" {
			continue
		}
		nextRel := filepath.Join(rel, name)

		if obj.ObjectFormat == mtp.OFC_Association {
			if err := syncFolder(dev, storageID, handle, nextRel, rawDir, logger); err != nil {
				logger.Printf("Folder sync warning for %s: %v", nextRel, err)
			}
			continue
		}
		if !isWantedFile(nextRel) {
			continue
		}

		dest := filepath.Join(rawDir, nextRel)
		if !isSafePath(rawDir, dest) {
			logger.Printf("Skipping unsafe path: %s", nextRel)
			continue
		}
		if isObjectCurrent(dest, &obj) {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			logger.Printf("mkdir failed for %s: %v", dest, err)
			continue
		}
		tmp := dest + ".part"
		f, err := os.Create(tmp)
		if err != nil {
			logger.Printf("create failed for %s: %v", tmp, err)
			continue
		}
		copyErr := dev.GetObject(handle, f)
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(tmp)
			if copyErr != nil {
				logger.Printf("download failed for %s: %v", nextRel, copyErr)
			} else {
				logger.Printf("close failed for %s: %v", nextRel, closeErr)
			}
			continue
		}
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Remove(tmp)
			logger.Printf("rename failed for %s: %v", dest, err)
			continue
		}
		if !obj.ModificationDate.IsZero() {
			_ = os.Chtimes(dest, time.Now(), obj.ModificationDate)
		}
		logger.Printf("Synced %s", nextRel)
	}

	return nil
}

func isWantedFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".notebook" || ext == ".note" || ext == ".pdf"
}

func isObjectCurrent(dest string, obj *mtp.ObjectInfo) bool {
	fi, err := os.Stat(dest)
	if err != nil {
		return false
	}

	knownSize := obj.CompressedSize != 0xFFFFFFFF
	if knownSize {
		if fi.Size() != int64(obj.CompressedSize) {
			return false
		}
	}

	if !obj.ModificationDate.IsZero() {
		if fi.ModTime().Before(obj.ModificationDate.Add(-1 * time.Second)) {
			return false
		}
	}

	if !knownSize && obj.ModificationDate.IsZero() {
		return false
	}
	return true
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	return name
}

func isSafePath(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

var _ io.Writer
