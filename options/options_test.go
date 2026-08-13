package options

import "testing"

func TestParse_NewFlags(t *testing.T) {
	args := []string{
		"-f", "bestvideo+bestaudio",
		"-o", "%(title)s.%(ext)s",
		"--no-overwrite",
		"--download-archive", "archive.txt",
		"--dateafter", "20200101",
		"--playlist-items", "1-3,7",
		"-x", "--audio-format", "mp3",
		"--write-subs", "--sub-langs", "en,zh-Hans",
		"--convert-subs", "srt",
		"--trim-filenames", "40",
		"--print", "%(id)s",
		"https://example.com/v/1",
	}
	o, urls, err := Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	if o.Format != "bestvideo+bestaudio" {
		t.Fatalf("format = %q", o.Format)
	}
	if o.NoOverwrites != true {
		t.Fatal("no-overwrite not parsed")
	}
	if o.DownloadArchive != "archive.txt" {
		t.Fatalf("archive = %q", o.DownloadArchive)
	}
	if o.DateAfter != "20200101" {
		t.Fatalf("dateafter = %q", o.DateAfter)
	}
	if !o.WantsPlaylistItem(2) || !o.WantsPlaylistItem(7) || o.WantsPlaylistItem(5) {
		t.Fatal("playlist items filter wrong")
	}
	if !o.ExtractAudio || o.AudioFormat != "mp3" {
		t.Fatal("extract-audio/format wrong")
	}
	if !o.WriteSubs || o.SubLangs != "en,zh-Hans" || o.ConvertSubs != "srt" {
		t.Fatal("subtitle options wrong")
	}
	if o.TrimFilenames != 40 {
		t.Fatalf("trim = %d", o.TrimFilenames)
	}
	if o.PrintField != "%(id)s" {
		t.Fatalf("print = %q", o.PrintField)
	}
	if len(urls) != 1 || urls[0] != "https://example.com/v/1" {
		t.Fatalf("urls = %v", urls)
	}
}

func TestInDateRange(t *testing.T) {
	o := &Options{DateAfter: "20200101", DateBefore: "20241231"}
	if !o.InDateRange("20220615") {
		t.Fatal("2022 should be in range")
	}
	if o.InDateRange("20190101") {
		t.Fatal("2019 should be before range")
	}
	if o.InDateRange("20250101") {
		t.Fatal("2025 should be after range")
	}
}

func TestWantsPlaylistItem_All(t *testing.T) {
	o := &Options{}
	if !o.WantsPlaylistItem(999) {
		t.Fatal("empty spec should allow all")
	}
}
