package platform

import (
	"os"
	"path/filepath"
	"strings"
)

var filepathGlob = filepath.Glob

func readTrimmed(path string) (string, error) {
	data, err := os.ReadFile(path)
	return strings.TrimSpace(string(data)), err
}
