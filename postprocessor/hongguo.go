package postprocessor

import (
	"fmt"
	"os"
	"os/exec"
)

// IsPlayable verifies that ffmpeg can decode the first video frame. CENC MP4
// files expose stream metadata to ffprobe, so probing containers alone would
// incorrectly classify encrypted files as complete.
func IsPlayable(path, ffmpeg string) bool {
	cmd := exec.Command(ffmpeg, "-v", "error", "-i", path, "-frames:v", "1", "-f", "null", "-")
	return cmd.Run() == nil
}

// DecryptCENC decrypts a downloaded MP4 through a sibling temporary file and
// atomically replaces the ciphertext. Existing playable output is idempotently
// accepted, which makes reruns safe after an interrupted download/postprocess.
func DecryptCENC(path, key, ffmpeg string) error {
	if key == "" {
		return nil
	}
	if IsPlayable(path, ffmpeg) {
		return nil
	}
	tmp := path + ".decrypting.mp4"
	_ = os.Remove(tmp)
	if err := Exec(ffmpeg, "-y", "-decryption_key", key, "-i", path, "-c", "copy", "-movflags", "+faststart", tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("CENC decrypt %s: %w", path, err)
	}
	if !IsPlayable(tmp, ffmpeg) {
		_ = os.Remove(tmp)
		return fmt.Errorf("CENC decrypt %s produced an undecodable file", path)
	}
	backup := path + ".encrypted.bak"
	_ = os.Remove(backup)
	if err := os.Rename(path, backup); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup encrypted file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Rename(backup, path)
		_ = os.Remove(tmp)
		return fmt.Errorf("replace encrypted file: %w", err)
	}
	_ = os.Remove(backup)
	return nil
}
