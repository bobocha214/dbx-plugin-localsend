package main

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

func pickDirectory() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	script := `try
return POSIX path of (choose folder with prompt "选择邻间存储文件夹")
on error number -128
return ""
end try`
	output, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	return strings.TrimSuffix(string(output), "\n"), err
}
