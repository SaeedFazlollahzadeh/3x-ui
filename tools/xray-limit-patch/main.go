// xray-limit-patch applies the panel patches to a writable copy of the pinned core.
package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed dispatcher.go.txt
var dispatcher string

func main() {
	if len(os.Args) != 2 {
		panic("usage: xray-limit-patch <writable-xray-source-directory>")
	}
	pkg := filepath.Join(os.Args[1], "app", "dispatcher")
	path := filepath.Join(pkg, "default.go")
	source, err := os.ReadFile(path)
	check(err)
	const anchor = "\thandler.Dispatch(ctx, link)"
	if strings.Count(string(source), anchor) != 1 {
		panic("pinned Xray dispatcher changed")
	}
	check(os.WriteFile(path, []byte(strings.ReplaceAll(string(source), anchor, "\tapplyPanelSpeedLimits(ctx, link)\n"+anchor)), 0o644))
	check(os.WriteFile(filepath.Join(pkg, "panel_speed_limits.go"), []byte(dispatcher), 0o644))

	path = filepath.Join(os.Args[1], "app", "log", "log.go")
	source, err = os.ReadFile(path)
	check(err)
	const accessCase = "\tcase *log.AccessMessage:\n"
	if strings.Count(string(source), accessCase) != 1 {
		panic("pinned Xray access logger changed")
	}
	const skipAPI = "\t\tif msg.Detour == \"api -> api\" && msg.Email == \"\" {\n\t\t\treturn\n\t\t}\n"
	check(os.WriteFile(path, []byte(strings.ReplaceAll(string(source), accessCase, accessCase+skipAPI)), 0o644))
}

func check(err error) {
	if err != nil {
		panic(fmt.Errorf("patch Xray core: %w", err))
	}
}
