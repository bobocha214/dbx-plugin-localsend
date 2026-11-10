package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func pickDirectory() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	if tool, err := exec.LookPath("zenity"); err == nil {
		cmd = exec.CommandContext(ctx, tool, "--file-selection", "--directory", "--title=选择邻间存储文件夹")
	} else if tool, err := exec.LookPath("kdialog"); err == nil {
		cmd = exec.CommandContext(ctx, tool, "--getexistingdirectory", ".", "--title", "选择邻间存储文件夹")
	} else {
		return "", fmt.Errorf("目录选择需要 zenity 或 kdialog；请安装其中一个，或手动填写完整路径")
	}
	output, err := cmd.Output()
	var exit *exec.ExitError
	if ctx.Err() == nil && errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil
	}
	return strings.TrimSuffix(string(output), "\n"), err
}
