package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Process string entry to bool value.
func getBoolVal(input string) bool {
	input = strings.ToLower(input)
	if input == "yes" || input == "true" {
		return true
	}
	return false
}

// Parse Timestamps from kiteworks
func timeParse(input string) (time.Time, error) {
	input = strings.Replace(input, "+0000", "Z", 1)
	return time.Parse(time.RFC3339, input)
}

// Create a local folder
func MkDir(path string) (err error) {
	finfo, err := os.Stat(path)
	if err != nil && os.IsNotExist(err) {
		err = os.Mkdir(path, 0755)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if finfo != nil && !finfo.IsDir() {
		os.Remove(path)
		err = os.Mkdir(path, 0755)
		if err != nil {
			return err
		}
	}
	return
}

// Move file from one directory to another.
func moveFile(src, dst string) (err error) {
	s_file, err := os.Open(src)
	if err != nil {
		return err
	}

	d_file, err := os.Create(dst)
	if err != nil {
		s_file.Close()
		return err
	}

	_, err = io.Copy(d_file, s_file)
	if err != nil && err != io.EOF {
		s_file.Close()
		d_file.Close()
		return err
	}

	s_file.Close()
	d_file.Close()

	return os.Remove(src)
}

// Fatal error handler.
func errChk(err error, desc ...string) {
	if err != nil {
		if len(desc) > 0 {
			fmt.Printf("fatal: [%s] %s\n", strings.Join(desc, "->"), err.Error())
		} else {
			fmt.Printf("fatal: %s\n", err.Error())
		}
		os.Exit(1)
	}
}

// Provides a clean path, for Windows ... and everyone else.
func cleanPath(input string) string {
	return filepath.Clean(input)
}
