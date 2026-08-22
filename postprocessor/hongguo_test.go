package postprocessor

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDecryptCENCAndPlayableSkip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	ffmpeg := filepath.Join(dir, "ffmpeg")
	script := `#!/bin/sh
in=""; out=""; prev=""; probe=0
for a in "$@"; do
  if [ "$prev" = "-i" ]; then in="$a"; fi
  if [ "$a" = "-frames:v" ]; then probe=1; fi
  prev="$a"
  out="$a"
done
if [ "$probe" = 1 ]; then
  case "$(cat "$in" 2>/dev/null)" in DECRYPTED*) exit 0;; *) exit 1;; esac
fi
printf DECRYPTED > "$out"
cat "$in" >> "$out"
`
	if err := os.WriteFile(ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(input, []byte("CIPHERTEXT"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DecryptCENC(input, "00112233445566778899aabbccddeeff", ffmpeg); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(input)
	if string(got) != "DECRYPTEDCIPHERTEXT" {
		t.Fatalf("output=%q", got)
	}
	if !IsPlayable(input, ffmpeg) {
		t.Fatal("decrypted file was not playable")
	}
	if err := DecryptCENC(input, "00112233445566778899aabbccddeeff", ffmpeg); err != nil {
		t.Fatal(err)
	}
	got2, _ := os.ReadFile(input)
	if string(got2) != string(got) {
		t.Fatal("playable file was decrypted twice")
	}
}
