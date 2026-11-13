package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	MaxENIFileSize = 16 << 20
	MaxINIFileSize = 4 << 20
)

var ErrBusConfigFileMissing = errors.New("bus configuration file is not uploaded")

// UploadBusConfigFile stores one file beside the SQLite database and updates
// only its relative path in the database. The old file is retained until the
// new file and its database reference have both been written successfully.
func (s *Store) UploadBusConfigFile(id uint, kind string, data []byte) error {
	if len(data) == 0 {
		return errors.New("uploaded file is empty")
	}
	if kind == "eni" && len(data) > MaxENIFileSize {
		return fmt.Errorf("ENI file exceeds %d MiB", MaxENIFileSize>>20)
	}
	if kind == "ini" && len(data) > MaxINIFileSize {
		return fmt.Errorf("INI file exceeds %d MiB", MaxINIFileSize>>20)
	}
	if kind != "eni" && kind != "ini" {
		return errors.New("file kind must be eni or ini")
	}

	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	var row BusConfig
	if err := s.DB.First(&row, id).Error; err != nil {
		return err
	}
	path, err := s.writeBusConfigFile(id, kind, data)
	if err != nil {
		return err
	}
	column, oldPath := "eni_path", row.ENIPath
	if kind == "ini" {
		column, oldPath = "ini_path", row.INIPath
	}
	if err := s.DB.Model(&BusConfig{}).Where("id = ?", id).Update(column, path).Error; err != nil {
		_ = s.removeBusFile(path)
		return err
	}
	_ = s.removeBusFile(oldPath)
	return nil
}

// ReadBusConfigFile returns one persisted file for download or SDK use.
func (s *Store) ReadBusConfigFile(id uint, kind string) ([]byte, error) {
	if kind != "eni" && kind != "ini" {
		return nil, errors.New("file kind must be eni or ini")
	}
	s.filesMu.RLock()
	defer s.filesMu.RUnlock()
	var row BusConfig
	if err := s.DB.First(&row, id).Error; err != nil {
		return nil, err
	}
	path := row.ENIPath
	if kind == "ini" {
		path = row.INIPath
	}
	return s.readBusFile(path)
}

// ReadBusConfigFiles returns both files from local storage for the in-memory
// SDK download path.
func (s *Store) ReadBusConfigFiles(id uint) ([]byte, []byte, error) {
	s.filesMu.RLock()
	defer s.filesMu.RUnlock()
	var row BusConfig
	if err := s.DB.First(&row, id).Error; err != nil {
		return nil, nil, err
	}
	eni, err := s.readBusFile(row.ENIPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read ENI: %w", err)
	}
	ini, err := s.readBusFile(row.INIPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read INI: %w", err)
	}
	return eni, ini, nil
}

func (s *Store) writeBusConfigFile(id uint, kind string, data []byte) (string, error) {
	dir := filepath.Join(s.busFilesDir, strconv.FormatUint(uint64(id), 10))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create bus config file directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", fmt.Errorf("create upload staging file: %w", err)
	}
	tempName := temp.Name()
	cleanup := func() { _ = os.Remove(tempName) }
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		cleanup()
		return "", fmt.Errorf("write uploaded file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		cleanup()
		return "", fmt.Errorf("sync uploaded file: %w", err)
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return "", fmt.Errorf("close uploaded file: %w", err)
	}
	ext := "." + kind
	token := strings.TrimPrefix(filepath.Base(tempName), ".upload-")
	finalPath := filepath.Join(dir, kind+"-"+token+ext)
	if err := os.Rename(tempName, finalPath); err != nil {
		cleanup()
		return "", fmt.Errorf("save uploaded file: %w", err)
	}
	relative, err := filepath.Rel(s.storageRoot, finalPath)
	if err != nil {
		_ = os.Remove(finalPath)
		return "", fmt.Errorf("make uploaded file path relative: %w", err)
	}
	return relative, nil
}

func (s *Store) readBusFile(relative string) ([]byte, error) {
	if relative == "" {
		return nil, ErrBusConfigFileMissing
	}
	path, err := s.resolveBusFile(relative)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrBusConfigFileMissing
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func (s *Store) resolveBusFile(relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", errors.New("bus config file path must be relative")
	}
	clean := filepath.Clean(relative)
	prefix := "bus-configs" + string(filepath.Separator)
	if !strings.HasPrefix(clean, prefix) {
		return "", errors.New("bus config file path is outside the storage directory")
	}
	root, err := filepath.Abs(s.storageRoot)
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(root, clean))
	if err != nil {
		return "", err
	}
	if path == root || !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return "", errors.New("bus config file path is outside the storage directory")
	}
	return path, nil
}

func (s *Store) removeBusFile(relative string) error {
	if relative == "" {
		return nil
	}
	path, err := s.resolveBusFile(relative)
	if err != nil {
		return err
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else {
		return err
	}
}

func (s *Store) setBusConfigFileStatus(row *BusConfig) {
	row.ENIUploaded = s.busFileExists(row.ENIPath)
	row.INIUploaded = s.busFileExists(row.INIPath)
}

func (s *Store) busFileExists(relative string) bool {
	path, err := s.resolveBusFile(relative)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
