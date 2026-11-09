//go:build !windows

package main

import (
	"io"
	"os"
)

func platformLogOutput() io.Writer { return os.Stderr }

func platformServiceMain() (bool, error) { return false, nil }

func defaultConfigPath() string { return "config/config.yaml" }

func platformConfigureEnvironment(string) error { return nil }
