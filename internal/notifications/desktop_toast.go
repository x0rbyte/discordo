//go:build !darwin

package notifications

import (
	"log/slog"
	"os/exec"

	"github.com/gen2brain/beeep"
)

func sendDesktopNotification(title string, message string, image string, playSound bool, duration int) error {
	if err := beeep.Notify(title, message, image); err != nil {
		return err
	}

	if playSound {
		slog.Info("playing notification sound")
		// Try to play system notification sound using paplay
		// This is much more reliable than beeep.Beep() on Linux
		go func() {
			// Try common notification sounds
			sounds := []string{
				"/usr/share/sounds/freedesktop/stereo/message-new-instant.oga",
				"/usr/share/sounds/freedesktop/stereo/complete.oga",
				"/usr/share/sounds/ubuntu/stereo/message.ogg",
			}

			for _, sound := range sounds {
				cmd := exec.Command("paplay", sound)
				if err := cmd.Run(); err == nil {
					slog.Debug("played sound", "file", sound)
					return
				}
			}

			// Fallback: use beep command if available
			cmd := exec.Command("beep", "-f", "800", "-l", "200")
			if err := cmd.Run(); err != nil {
				slog.Debug("beep command failed, trying aplay with tone")
				// Last resort: try to generate a tone with speaker-test
				cmd = exec.Command("speaker-test", "-t", "sine", "-f", "1000", "-l", "1")
				_ = cmd.Run()
			}
		}()
	}

	return nil
}
