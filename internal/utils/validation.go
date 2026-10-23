package utils

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
)

func ValidatePort[T interface{ ~int | ~uint }](port T) error {
	if port <= 0 || port > 65535 {
		return errors.New("must be between 1 and 65535")
	}
	return nil
}

func ValidateAddress(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("invalid address")
	}
	parsed, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsed == 0 {
		return errors.New("address port must be between 1 and 65535")
	}
	return nil
}

func ValidateDir(dir string) error {
	if dir == "" {
		return errors.New("dir is not set")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("dir cannot be accessed: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("dir is not a directory: %s", dir)
	}
	return nil
}

func ValidateFile(file string) error {
	info, err := os.Stat(file)
	if err != nil {
		return fmt.Errorf("file cannot be accessed: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", file)
	}
	return nil
}
