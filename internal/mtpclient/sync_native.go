//go:build mtp && cgo

package mtpclient

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hanwen/go-mtpfs/mtp"
)

const noParentID = 0xFFFFFFFF

// selectRetries is the number of times to attempt SelectDevice before giving
// up.  On macOS, system services such as icdd (Image Capture Device Daemon)
// may claim the USB device immediately after it connects, causing the first
// attempt to fail with LIBUSB_ERROR_NOT_FOUND.  Retrying with a short delay
// works around this reliably — the same mechanism used by interactive MTP
// clients such as OpenMTP.
const (
	selectRetries    = 5
	selectRetryDelay = 3 * time.Second
)

func SyncRawKindleFiles(rawDir string, devicePattern string, logger *log.Logger) (retErr error) {
	// Safety net: recover from any panic in the MTP/USB layer so the caller
	// always gets an error instead of a crash.
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("unexpected MTP panic: %v", r)
		}
	}()

	dev, err := selectDeviceWithRetry(devicePattern, logger)
	if err != nil {
		return fmt.Errorf("select MTP device: %w", err)
	}
	defer dev.Done()
	defer dev.Close()

	if err := dev.OpenSession(); err != nil {
		return fmt.Errorf("open MTP session: %w", err)
	}
	// Note: do NOT defer dev.CloseSession() here. dev.Close() (deferred above)
	// already tears down the session internally using runTransaction (the
	// unexported path), which suppresses errors silently.  Calling the public
	// CloseSession() before Close() causes it to go through RunTransaction,
	// which logs "fatal error LIBUSB_ERROR_NOT_FOUND; closing connection." to
	// the default logger every time the Kindle ends the session on its side.

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
			logger.Printf("Skipping storage %d (%q): unsupported filesystem type %d", sid, si.StorageDescription, si.FilesystemType)
			continue
		}
		logger.Printf("Syncing storage %d (%q)", sid, si.StorageDescription)
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

// selectDeviceWithRetry attempts mtp.SelectDevice up to selectRetries times,
// waiting selectRetryDelay between each attempt.  On macOS, system services
// such as icdd (Image Capture Device Daemon) briefly claim the USB device
// after it connects; retrying with a short pause reliably wins back access.
func selectDeviceWithRetry(pattern string, logger *log.Logger) (*mtp.Device, error) {
	var lastErr error
	for attempt := 1; attempt <= selectRetries; attempt++ {
		dev, err := callSelectDevice(pattern)
		if err == nil {
			if attempt > 1 {
				logger.Printf("MTP device selected on attempt %d/%d", attempt, selectRetries)
			}
			return dev, nil
		}
		lastErr = err
		if attempt < selectRetries {
			logger.Printf("MTP select attempt %d/%d failed (%v); retrying in %s",
				attempt, selectRetries, err, selectRetryDelay)
			time.Sleep(selectRetryDelay)
		}
	}
	return nil, lastErr
}

// callSelectDevice wraps mtp.SelectDevice and recovers from any panic that
// originates in the underlying hanwen/usb or libusb layer.
//
// The most common trigger on macOS is an empty USB device list: when
// libusb_get_device_list returns 0 entries, usb.DeviceList.Done() accesses
// d[0] on a zero-length slice and panics instead of returning an error.
// This happens when the process lacks the com.apple.security.device.usb
// entitlement or when a system service (icdd, Photos Agent) is holding an
// exclusive claim on the device.  Converting the panic to an error lets the
// retry loop in selectDeviceWithRetry operate normally.
func callSelectDevice(pattern string) (dev *mtp.Device, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("MTP library panic — libusb returned no USB devices "+
				"(macOS may be denying USB access; check com.apple.security.device.usb "+
				"entitlement or wait for icdd to release the device): %v", r)
		}
	}()
	return mtp.SelectDevice(pattern)
}
