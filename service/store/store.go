package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var ErrNotFound = gorm.ErrRecordNotFound

type AxisConfig struct {
	ID             uint   `json:"id" yaml:"id" gorm:"primaryKey"`
	CardNo         uint16 `json:"card_no" yaml:"card_no" gorm:"uniqueIndex:uidx_axis_card_axis"`
	Axis           uint16 `json:"axis" yaml:"axis" gorm:"uniqueIndex:uidx_axis_card_axis"`
	Resolution     int    `json:"resolution" yaml:"resolution"`
	ReductionRatio int    `json:"reduction_ratio" yaml:"reduction_ratio"`
	MoveLock       int    `json:"move_lock" yaml:"move_lock"`
	ErrBand        int    `json:"err_band" yaml:"err_band"`
	HoldTime       int    `json:"hold_time" yaml:"hold_time"`
	CreatedAt      int64  `json:"created_at" yaml:"created_at" gorm:"autoCreateTime:nano"`
	UpdatedAt      int64  `json:"updated_at" yaml:"updated_at" gorm:"autoUpdateTime:nano"`
}

type BusConfig struct {
	ID              uint   `json:"id" gorm:"primaryKey"`
	Name            string `json:"name" gorm:"uniqueIndex;size:128"`
	CycleTimeMicros uint32 `json:"cycle_time_micros" gorm:"not null;default:1000"`
	ENIPath         string `json:"-" gorm:"not null;default:''"`
	INIPath         string `json:"-" gorm:"not null;default:''"`
	ENIUploaded     bool   `json:"eni_uploaded" gorm:"-"`
	INIUploaded     bool   `json:"ini_uploaded" gorm:"-"`
	CreatedAt       int64  `json:"created_at" gorm:"autoCreateTime:nano"`
	UpdatedAt       int64  `json:"updated_at" gorm:"autoUpdateTime:nano"`
}

type Store struct {
	DB          *gorm.DB
	storageRoot string
	busFilesDir string
	temporary   string
	filesMu     sync.RWMutex
}

func Open(path string) (*Store, error) {
	if path == "" {
		path = "./data/nano_ctl.db"
	}
	storageRoot := ""
	temporary := ""
	if path != ":memory:" {
		storageRoot, _ = filepath.Abs(filepath.Dir(path))
		if err := os.MkdirAll(storageRoot, 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	} else {
		var err error
		temporary, err = os.MkdirTemp("", "nano-ctl-memory-")
		if err != nil {
			return nil, fmt.Errorf("create temporary storage directory: %w", err)
		}
		storageRoot = temporary
	}
	dsn := path
	if path != ":memory:" {
		dsn = fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=on", filepath.ToSlash(path))
	} else {
		dsn = "file::memory:?cache=shared&_busy_timeout=5000&_foreign_keys=on"
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), PrepareStmt: true})
	if err != nil {
		if temporary != "" {
			_ = os.RemoveAll(temporary)
		}
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	busFilesDir := filepath.Join(storageRoot, "bus-configs")
	if err := os.MkdirAll(busFilesDir, 0o750); err != nil {
		_ = sqlDB.Close()
		if temporary != "" {
			_ = os.RemoveAll(temporary)
		}
		return nil, fmt.Errorf("create bus configuration directory: %w", err)
	}
	if err := createMissingTables(db); err != nil {
		_ = sqlDB.Close()
		if temporary != "" {
			_ = os.RemoveAll(temporary)
		}
		return nil, fmt.Errorf("initialize sqlite schema: %w", err)
	}
	return &Store{DB: db, storageRoot: storageRoot, busFilesDir: busFilesDir, temporary: temporary}, nil
}

// createMissingTables creates current schema structures when absent.
func createMissingTables(db *gorm.DB) error {
	statements := []string{
		"CREATE TABLE IF NOT EXISTS axis_configs (id INTEGER PRIMARY KEY AUTOINCREMENT, card_no INTEGER, axis INTEGER, resolution INTEGER, reduction_ratio INTEGER, move_lock INTEGER, err_band INTEGER, hold_time INTEGER, created_at INTEGER, updated_at INTEGER)",
		"CREATE UNIQUE INDEX IF NOT EXISTS uidx_axis_card_axis ON axis_configs (card_no, axis)",
		"CREATE TABLE IF NOT EXISTS bus_configs (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, cycle_time_micros INTEGER NOT NULL DEFAULT 1000, eni_path TEXT NOT NULL DEFAULT '', ini_path TEXT NOT NULL DEFAULT '', created_at INTEGER, updated_at INTEGER)",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_bus_configs_name ON bus_configs (name)",
		"CREATE TABLE IF NOT EXISTS io_aliases (card_no INTEGER, kind TEXT, address INTEGER, alias TEXT, remarks TEXT, reverse NUMERIC, disabled NUMERIC, source_id TEXT, PRIMARY KEY (card_no, kind, address))",
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, statement := range statements {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Close() error {
	sqlDB, err := s.DB.DB()
	if err != nil {
		return err
	}
	closeErr := sqlDB.Close()
	if s.temporary != "" {
		closeErr = errors.Join(closeErr, os.RemoveAll(s.temporary))
	}
	return closeErr
}

func (s *Store) ListAxisConfigs() ([]AxisConfig, error) {
	var rows []AxisConfig
	err := s.DB.Order("card_no, axis").Find(&rows).Error
	return rows, err
}
func (s *Store) GetAxisConfig(card, axis uint16) (AxisConfig, error) {
	var row AxisConfig
	err := s.DB.Where("card_no = ? AND axis = ?", card, axis).First(&row).Error
	return row, err
}
func (s *Store) CreateAxisConfig(row *AxisConfig) error { return s.DB.Create(row).Error }
func (s *Store) SaveAxisConfig(row *AxisConfig) error   { return s.DB.Save(row).Error }
func (s *Store) DeleteAxisConfig(card, axis uint16) error {
	result := s.DB.Where("card_no = ? AND axis = ?", card, axis).Delete(&AxisConfig{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListBusConfigs() ([]BusConfig, error) {
	s.filesMu.RLock()
	defer s.filesMu.RUnlock()
	var rows []BusConfig
	err := s.DB.Order("id").Find(&rows).Error
	if err == nil {
		for i := range rows {
			s.setBusConfigFileStatus(&rows[i])
		}
	}
	return rows, err
}
func (s *Store) GetBusConfig(id uint) (BusConfig, error) {
	s.filesMu.RLock()
	defer s.filesMu.RUnlock()
	var row BusConfig
	err := s.DB.First(&row, id).Error
	if err == nil {
		s.setBusConfigFileStatus(&row)
	}
	return row, err
}
func (s *Store) CreateBusConfig(row *BusConfig) error { return s.DB.Create(row).Error }
func (s *Store) SaveBusConfig(row *BusConfig) error {
	return s.DB.Model(&BusConfig{}).Where("id = ?", row.ID).Select("name", "cycle_time_micros").Updates(row).Error
}
func (s *Store) DeleteBusConfig(id uint) error {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	var row BusConfig
	if err := s.DB.First(&row, id).Error; err != nil {
		return err
	}
	result := s.DB.Delete(&BusConfig{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	_ = s.removeBusFile(row.ENIPath)
	_ = s.removeBusFile(row.INIPath)
	return nil
}

func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
