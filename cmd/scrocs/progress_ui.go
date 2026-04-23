package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// progressUI shows macOS notifications and dialogs during sync so the user
// can see what the background process is doing at each stage.
type progressUI struct {
	// dialogCmd holds the background osascript process that displays the
	// initial "Kindle Scribe detected" progress window.
	dialogCmd *exec.Cmd
}

func newProgressUI() *progressUI {
	return &progressUI{}
}

// start opens a persistent progress dialog and sends an initial notification.
// It is non-blocking: the dialog runs in the background and is closed by
// complete or fail.
func (u *progressUI) start(message string) {
	u.sendNotification("Scrocs", message)

	// Spawn a background dialog that stays open while sync runs.
	// It auto-dismisses after 10 minutes as a safety valve.
	script := fmt.Sprintf(
		`display dialog %s with title "Scrocs — Syncing" buttons {"Cancel"} default button "Cancel" giving up after 600`,
		asString(message),
	)
	u.dialogCmd = exec.Command("/usr/bin/osascript", "-e", script)
	_ = u.dialogCmd.Start()
}

// updateStep sends a macOS notification for an in-progress sync step.
func (u *progressUI) updateStep(step, message string) {
	u.sendNotification("Scrocs — "+step, message)
}

// complete closes the progress dialog and shows a success notification and
// dialog.
func (u *progressUI) complete(summary string) {
	u.closeDialog()
	u.sendNotification("Scrocs — Sync Complete", summary)
	script := fmt.Sprintf(
		`display dialog %s with title "Scrocs — Sync Complete" buttons {"OK"} default button "OK" giving up after 30`,
		asString(summary),
	)
	_ = exec.Command("/usr/bin/osascript", "-e", script).Start()
}

// fail closes the progress dialog and shows an error alert.
func (u *progressUI) fail(message string) {
	u.closeDialog()
	u.sendNotification("Scrocs — Sync Failed", message)
	script := fmt.Sprintf(
		`display alert "Scrocs — Sync Failed" message %s as critical buttons {"OK"} default button "OK" giving up after 60`,
		asString(message),
	)
	_ = exec.Command("/usr/bin/osascript", "-e", script).Start()
}

// closeDialog terminates the background progress dialog if it is still open.
func (u *progressUI) closeDialog() {
	if u.dialogCmd != nil && u.dialogCmd.Process != nil {
		_ = u.dialogCmd.Process.Kill()
		_ = u.dialogCmd.Wait()
		u.dialogCmd = nil
	}
}

// sendNotification shows a macOS notification centre banner.
func (u *progressUI) sendNotification(title, message string) {
	script := fmt.Sprintf(
		`display notification %s with title %s`,
		asString(message),
		asString(title),
	)
	_ = exec.Command("/usr/bin/osascript", "-e", script).Start()
}

// asString returns s as an AppleScript quoted string literal, escaping
// backslashes and double-quote characters so that arbitrary text can be
// safely embedded in an osascript one-liner.
func asString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
