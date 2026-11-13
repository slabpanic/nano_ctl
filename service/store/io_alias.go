package store

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidIOAlias = errors.New("invalid IO alias")

// IOAlias identifies one physical IO address. Names are labels, not keys:
// the legacy database intentionally repeats names like 保留.
type IOAlias struct {
	CardNo   uint16 `json:"card_no" yaml:"card_no" gorm:"primaryKey;autoIncrement:false"`
	Kind     string `json:"kind" yaml:"kind" gorm:"primaryKey;size:32"`
	Address  uint32 `json:"address" yaml:"address" gorm:"primaryKey;autoIncrement:false"`
	Alias    string `json:"alias" yaml:"alias"`
	Remarks  string `json:"remarks" yaml:"remarks"`
	Reverse  bool   `json:"reverse" yaml:"reverse"`
	Disabled bool   `json:"disabled" yaml:"disabled"`
	SourceID string `json:"source_id" yaml:"source_id"`
}

func (a *IOAlias) Normalize() error {
	a.Alias = strings.TrimSpace(a.Alias)
	if a.Alias == "" || utf8.RuneCountInString(a.Alias) > 255 || utf8.RuneCountInString(a.Remarks) > 4096 || len(a.SourceID) > 255 || a.Address > 65535 {
		return fmt.Errorf("%w: nonempty alias <= 255, remarks <= 4096, address <= 65535 required", ErrInvalidIOAlias)
	}
	switch a.Kind {
	case "input-bit", "output-bit":
	case "input-port", "output-port", "analog-input", "analog-output":
		if a.Reverse {
			return fmt.Errorf("%w: reverse is only supported for digital bits", ErrInvalidIOAlias)
		}
	default:
		return fmt.Errorf("%w: unsupported IO kind", ErrInvalidIOAlias)
	}
	return nil
}

func (s *Store) ListIOAliases(card *uint16) ([]IOAlias, error) {
	rows := make([]IOAlias, 0)
	query := s.DB.Order("card_no, kind, address")
	if card != nil {
		query = query.Where("card_no = ?", *card)
	}
	err := query.Find(&rows).Error
	return rows, err
}

func aliasQuery(db *gorm.DB, card uint16, kind string, address uint32) *gorm.DB {
	return db.Where("card_no = ? AND kind = ? AND address = ?", card, kind, address)
}

func (s *Store) GetIOAlias(card uint16, kind string, address uint32) (IOAlias, error) {
	var row IOAlias
	err := aliasQuery(s.DB, card, kind, address).First(&row).Error
	return row, err
}

func (s *Store) PutIOAlias(row *IOAlias) error {
	if err := row.Normalize(); err != nil {
		return err
	}
	return s.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "card_no"}, {Name: "kind"}, {Name: "address"}},
		DoUpdates: clause.AssignmentColumns([]string{"alias", "remarks", "reverse", "disabled"}),
	}).Create(row).Error
}

func (s *Store) DeleteIOAlias(card uint16, kind string, address uint32) error {
	result := aliasQuery(s.DB, card, kind, address).Delete(&IOAlias{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
