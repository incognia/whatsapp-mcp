package main

import (
	"strings"
	"testing"
)

func TestBridgeDir(t *testing.T) {
	src := "/Users/ana/code/whatsapp-mcp/whatsapp-bridge/store_dir.go"
	srcDir := "/Users/ana/code/whatsapp-mcp/whatsapp-bridge"
	present := map[string]bool{srcDir + "/main.go": true, srcDir + "/go.mod": true}
	exists := func(p string) bool { return present[p] }

	tests := []struct {
		name      string
		exe, src  string
		wantDir   string
		wantGoRun bool
		wantErr   bool
	}{
		{name: "built binary", exe: srcDir + "/whatsapp-bridge", src: src, wantDir: srcDir},
		{name: "built binary elsewhere", exe: "/opt/wa/whatsapp-bridge", src: src, wantDir: "/opt/wa"},
		{name: "go run on macOS", exe: "/var/folders/j2/abc/T/go-build3835991174/b001/exe/whatsapp-client", src: src, wantDir: srcDir, wantGoRun: true},
		{name: "go run on Linux", exe: "/tmp/go-build123/b001/exe/whatsapp-client", src: src, wantDir: srcDir, wantGoRun: true},
		{name: "go run with GOTMPDIR", exe: "/home/ana/gotmp/go-build9/b001/exe/whatsapp-client", src: src, wantDir: srcDir, wantGoRun: true},
		{name: "cached go run on macOS", exe: "/Users/ana/Library/Caches/go-build/15/15f79cd52b282536590f1893a94e9e670becf1a063e2463cca24391a58cc9a46-d/whatsapp-client", src: src, wantDir: srcDir, wantGoRun: true},
		{name: "cached go run with custom GOCACHE", exe: "/data/gocache/0a/0a" + strings.Repeat("b", 62) + "-d/whatsapp-client", src: src, wantDir: srcDir, wantGoRun: true},
		{name: "folder ending in -d outside the cache", exe: "/opt/build-d/whatsapp-bridge", src: src, wantDir: "/opt/build-d"},
		{name: "trimpath", exe: "/tmp/go-build123/b001/exe/whatsapp-client", src: "whatsapp-client/store_dir.go", wantGoRun: true, wantErr: true},
		{name: "source folder without bridge", exe: "/tmp/go-build123/b001/exe/whatsapp-client", src: "/somewhere/else/store_dir.go", wantGoRun: true, wantErr: true},
		{name: "exe folder outside go-build", exe: "/opt/tools/exe/whatsapp-bridge", src: src, wantDir: "/opt/tools/exe"},
	}
	for _, tc := range tests {
		dir, goRun, err := bridgeDir(tc.exe, tc.src, exists)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
			continue
		}
		if goRun != tc.wantGoRun {
			t.Errorf("%s: goRun = %v, want %v", tc.name, goRun, tc.wantGoRun)
		}
		if !tc.wantErr && dir != tc.wantDir {
			t.Errorf("%s: dir = %q, want %q", tc.name, dir, tc.wantDir)
		}
	}
}
