package base

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type SubpathWriter struct {
	MaxFilesize int
	OwnerName   string
	Basepath    string
}

type Subpath string

type FileStatus uint16

const (
	FILE_STATUS_OK FileStatus = iota
	FILE_ERROR_NOT_EXISTS
	FILE_ERROR_PERMISSION
	FILE_ERROR_STAT
	FILE_ERROR_GENERAL
)

func (this *SubpathWriter) GetStatusByError(err error) FileStatus {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return FILE_ERROR_NOT_EXISTS
	case errors.Is(err, os.ErrPermission):
		return FILE_ERROR_PERMISSION
	default:
		return FILE_ERROR_GENERAL
	}
}

func (this *SubpathWriter) ReadFile(path Subpath) ([]byte, FileStatus, error) {
	status := FILE_STATUS_OK
	basepath := this.Basepath
	fullpath := filepath.Join(basepath, string(path))

	data, err := os.ReadFile(fullpath)
	if err != nil {
		status = this.GetStatusByError(err)
		return nil, status, err
	}

	return data, status, nil
}

func (this *SubpathWriter) WriteFile(path Subpath, exist_only bool, data []byte) (FileStatus, error) {
	status := FILE_STATUS_OK
	basepath := this.Basepath
	fullpath := filepath.Join(basepath, string(path))

	stat, err := os.Stat(fullpath)
	if err != nil {
		status = this.GetStatusByError(err)
		if status == FILE_ERROR_NOT_EXISTS {
			if exist_only {
				return status, err
			}
		} else {
			return FILE_ERROR_STAT, err
		}
	}

	var permission fs.FileMode
	if stat != nil {
		permission = stat.Mode().Perm()
	} else {
		permission = 0755
	}

	err = os.WriteFile(fullpath, data, permission)
	if err != nil {
		status = this.GetStatusByError(err)
		return status, err
	}

	return status, nil
}

func (this *SubpathWriter) ReadSubpath(subpath Subpath, emptyable bool) ([]byte, error) {
	data, status, err := this.ReadFile(subpath)
	if status != FILE_STATUS_OK || err != nil {
		if status == FILE_ERROR_NOT_EXISTS {
			return nil, fmt.Errorf("invalid %s struct: file `%s` not found", this.OwnerName, subpath)
		}

		return nil, err
	}

	data = bytes.TrimSpace(data)

	data_length := len(data)
	if data_length > this.MaxFilesize {
		return nil, fmt.Errorf("invalid %s struct: %s too large", this.OwnerName, subpath) // as field name, needn't reversed quotation mark. the same at next if
	}

	if !emptyable && data_length <= 0 {
		return nil, fmt.Errorf("invalid %s struct: empty %s", this.OwnerName, subpath)
	}

	return data, nil
}

func (this *SubpathWriter) WriteSubpath(subpath Subpath, exist_only bool, data []byte) error {
	status, err := this.WriteFile(subpath, exist_only, data)
	if status != FILE_STATUS_OK || err != nil {
		if status == FILE_ERROR_NOT_EXISTS {
			return fmt.Errorf("invalid %s struct: file `%s` not found", this.OwnerName, subpath)
		}

		return err
	}

	return nil
}
