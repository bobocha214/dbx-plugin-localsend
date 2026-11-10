//go:build !windows

package main

import "errors"

func pickDirectory() (string, error) {
	return "", errors.New("当前平台暂不支持本机目录选择，请填写完整路径")
}
