package fileutil

import (
	"errors"
	"io"
	"os"
)

// ReadRegular reads a bounded ordinary file and detects replacement while opening.
// Missing files return exists=false; errors never return partial contents.
func ReadRegular(path string, limit int64) (content []byte, mode os.FileMode, exists bool, err error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0600, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, errors.New("文件必须是独立的普通文件")
	}
	if info.Size() > limit {
		return nil, 0, false, errors.New("文件超过大小限制")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, 0, false, err
	}
	if !os.SameFile(info, opened) {
		return nil, 0, false, errors.New("读取时文件已被替换")
	}
	content, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, 0, false, err
	}
	if int64(len(content)) > limit {
		return nil, 0, false, errors.New("文件超过大小限制")
	}
	return content, info.Mode().Perm(), true, nil
}
