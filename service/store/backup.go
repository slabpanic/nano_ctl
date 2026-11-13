package store

import (
	"gorm.io/gorm"
)

// BusConfigData contains bus configuration metadata. ENI and INI files are
// managed separately and intentionally stay out of full YAML backups.
type BusConfigData struct {
	ID              uint   `json:"id" yaml:"id"`
	Name            string `json:"name" yaml:"name"`
	CycleTimeMicros uint32 `json:"cycle_time_micros" yaml:"cycle_time_micros"`
	CreatedAt       int64  `json:"created_at" yaml:"created_at"`
	UpdatedAt       int64  `json:"updated_at" yaml:"updated_at"`
}

// ConfigData contains all user-managed configuration stored by nano_ctl.
type ConfigData struct {
	AxisConfigs []AxisConfig    `json:"axis_configs" yaml:"axis_configs"`
	BusConfigs  []BusConfigData `json:"bus_configs" yaml:"bus_configs"`
	IOAliases   []IOAlias       `json:"io_aliases" yaml:"io_aliases"`
}

func (s *Store) ExportConfigData() (ConfigData, error) {
	data := ConfigData{
		AxisConfigs: make([]AxisConfig, 0),
		BusConfigs:  make([]BusConfigData, 0),
		IOAliases:   make([]IOAlias, 0),
	}
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Order("card_no, axis").Find(&data.AxisConfigs).Error; err != nil {
			return err
		}
		var busRows []BusConfig
		if err := tx.Order("id").Find(&busRows).Error; err != nil {
			return err
		}
		for _, row := range busRows {
			data.BusConfigs = append(data.BusConfigs, BusConfigData{
				ID: row.ID, Name: row.Name, CycleTimeMicros: row.CycleTimeMicros,
				CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			})
		}
		return tx.Order("card_no, kind, address").Find(&data.IOAliases).Error
	})
	return data, err
}

// ReplaceConfigData atomically replaces all user-managed configuration rows.
// An empty alias list remains empty after the next process start.
func (s *Store) ReplaceConfigData(data ConfigData) error {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	var existingBusRows []BusConfig
	if err := s.DB.Order("id").Find(&existingBusRows).Error; err != nil {
		return err
	}
	existingByID := make(map[uint]BusConfig, len(existingBusRows))
	for _, row := range existingBusRows {
		existingByID[row.ID] = row
	}
	busRows := make([]BusConfig, 0, len(data.BusConfigs))
	keptBusIDs := make(map[uint]struct{}, len(data.BusConfigs))
	for _, row := range data.BusConfigs {
		stored := BusConfig{
			ID: row.ID, Name: row.Name, CycleTimeMicros: row.CycleTimeMicros,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
		if existing, ok := existingByID[row.ID]; ok {
			stored.ENIPath, stored.INIPath = existing.ENIPath, existing.INIPath
		}
		busRows = append(busRows, stored)
		keptBusIDs[row.ID] = struct{}{}
	}
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("1 = 1").Delete(&AxisConfig{}).Error; err != nil {
			return err
		}
		if err := tx.Where("1 = 1").Delete(&BusConfig{}).Error; err != nil {
			return err
		}
		if err := tx.Where("1 = 1").Delete(&IOAlias{}).Error; err != nil {
			return err
		}
		if len(data.AxisConfigs) > 0 {
			if err := tx.CreateInBatches(data.AxisConfigs, 100).Error; err != nil {
				return err
			}
		}
		if len(busRows) > 0 {
			if err := tx.CreateInBatches(busRows, 20).Error; err != nil {
				return err
			}
		}
		if len(data.IOAliases) > 0 {
			if err := tx.CreateInBatches(data.IOAliases, 100).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, row := range existingBusRows {
		if _, kept := keptBusIDs[row.ID]; !kept {
			_ = s.removeBusFile(row.ENIPath)
			_ = s.removeBusFile(row.INIPath)
		}
	}
	return err
}
