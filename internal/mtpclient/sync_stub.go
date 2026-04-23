//go:build !mtp || !cgo

package mtpclient

import (
	"fmt"
	"log"
)

func SyncRawKindleFiles(_ string, _ string, _ *log.Logger) error {
	return fmt.Errorf("native MTP support is not enabled; rebuild with '-tags mtp' and libusb available")
}
