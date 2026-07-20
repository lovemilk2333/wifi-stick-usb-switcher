package base

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

type PathStatus uint16

const (
	PATH_STATUS_OK PathStatus = iota
	PATH_ERROR_NOT_EXISTS
	PATH_ERROR_ABSOLUTE_FAILED
	PATH_ERROR_UNMATCHED_PREFIX
	PATH_INVALID_FILE_FOLDER_TYPE
	PATH_ERROR_NO_SUBPATH
	PATH_ERROR_STAT_FAILED
)

type PathChecker struct{}

/*
path: the path to check
base: the path `path` must base on
file_required: `path` must be a file if true, must be a folder otherwise
subpath_required: `path` must have subpath (not equals to `base`) if true
*/
func (this *PathChecker) IsValidPath(path string, base string, file_required bool, subpath_required bool) PathStatus {
	abs_path, err := filepath.Abs(path)
	if err != nil {
		log.Printf("WARN: cannot get absolute path `%s`: %s", path, err.Error())
		return PATH_ERROR_ABSOLUTE_FAILED
	}

	abs_base, err := filepath.Abs(base)
	if err != nil {
		log.Printf("WARN: cannot get absolute path `%s`: %s", base, err.Error())
		return PATH_ERROR_ABSOLUTE_FAILED
	}

	rel, err := filepath.Rel(abs_base, abs_path)
	if err != nil {
		return PATH_ERROR_UNMATCHED_PREFIX
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return PATH_ERROR_UNMATCHED_PREFIX
	}

	if subpath_required && rel == "." {
		return PATH_ERROR_NO_SUBPATH
	}

	stat, err := os.Stat(abs_path)
	if err != nil {
		if os.IsNotExist(err) {
			return PATH_ERROR_NOT_EXISTS
		}
		log.Printf("WARN: cannot stat path `%s`: %s", abs_path, err.Error())
		return PATH_ERROR_STAT_FAILED
	}

	is_dir := stat.IsDir()

	if (file_required && is_dir) || (!file_required && !is_dir) {
		return PATH_INVALID_FILE_FOLDER_TYPE
	}

	return PATH_STATUS_OK
}
