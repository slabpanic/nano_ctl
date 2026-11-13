package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"
	"nano_ctl/service/store"
)

const maxImportBytes = 128 << 20

type dataExportDocument struct {
	ExportedAt  string                `yaml:"exported_at"`
	AxisConfigs []store.AxisConfig    `yaml:"axis_configs"`
	BusConfigs  []store.BusConfigData `yaml:"bus_configs"`
	IOAliases   []store.IOAlias       `yaml:"io_aliases"`
}

type dataImportDocument struct {
	ExportedAt  string                 `yaml:"exported_at,omitempty"`
	AxisConfigs *[]store.AxisConfig    `yaml:"axis_configs"`
	BusConfigs  *[]store.BusConfigData `yaml:"bus_configs"`
	IOAliases   *[]store.IOAlias       `yaml:"io_aliases"`
}

// exportData godoc
// @Summary 导出全部配置数据
// @Tags data
// @Produce application/yaml
// @Success 200 {file} file
// @Failure 500 {object} envelope
// @Router /data/export [get]
func (s *Server) exportData(c *gin.Context) {
	data, err := s.db.ExportConfigData()
	if err != nil {
		fail(c, err)
		return
	}
	output := dataExportDocument{
		ExportedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		AxisConfigs: data.AxisConfigs, BusConfigs: data.BusConfigs, IOAliases: data.IOAliases,
	}
	encoded, err := yaml.Marshal(output)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="nano_ctl-data.yaml"`)
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", encoded)
}

// importData godoc
// @Summary 导入并替换全部配置数据
// @Tags data
// @Accept application/yaml
// @Produce json
// @Param body body string true "包含 axis_configs、bus_configs 和 io_aliases 的 YAML 文档"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Router /data/import [put]
func (s *Server) importData(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxImportBytes+1))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(c, err)
			return
		}
		fail(c, invalid("unable to read YAML import body"))
		return
	}
	if len(body) == 0 {
		fail(c, invalid("YAML import body is empty"))
		return
	}
	if len(body) > maxImportBytes {
		fail(c, invalid("YAML import exceeds 128 MiB"))
		return
	}

	decoder := yaml.NewDecoder(bytes.NewReader(body), yaml.Strict())
	var doc dataImportDocument
	if err := decoder.Decode(&doc); err != nil {
		fail(c, invalid("invalid YAML: "+err.Error()))
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			err = invalid("only one YAML document is supported")
		}
		fail(c, invalid("invalid YAML document sequence: "+err.Error()))
		return
	}
	if doc.AxisConfigs == nil || doc.BusConfigs == nil || doc.IOAliases == nil {
		fail(c, invalid("axis_configs, bus_configs, and io_aliases are all required; use empty lists when there is no data"))
		return
	}
	data := store.ConfigData{
		AxisConfigs: *doc.AxisConfigs,
		BusConfigs:  *doc.BusConfigs,
		IOAliases:   *doc.IOAliases,
	}
	if err := validateImportedData(&data); err != nil {
		fail(c, err)
		return
	}
	if err := s.db.ReplaceConfigData(data); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{
		"replaced":          true,
		"axis_config_count": len(data.AxisConfigs),
		"bus_config_count":  len(data.BusConfigs),
		"io_alias_count":    len(data.IOAliases),
	})
}

func validateImportedData(data *store.ConfigData) error {
	axisIDs := make(map[uint]struct{}, len(data.AxisConfigs))
	type axisKey struct{ card, axis uint16 }
	axisKeys := make(map[axisKey]struct{}, len(data.AxisConfigs))
	for i := range data.AxisConfigs {
		row := &data.AxisConfigs[i]
		if row.ID == 0 {
			return invalid("axis_configs entries require a positive id")
		}
		if _, exists := axisIDs[row.ID]; exists {
			return invalid("axis_configs contains duplicate ids")
		}
		axisIDs[row.ID] = struct{}{}
		key := axisKey{card: row.CardNo, axis: row.Axis}
		if _, exists := axisKeys[key]; exists {
			return invalid("axis_configs contains duplicate card_no/axis pairs")
		}
		axisKeys[key] = struct{}{}
		if err := validAxisConfig(row); err != nil {
			return invalid("invalid axis_config: " + err.Error())
		}
	}

	busIDs := make(map[uint]struct{}, len(data.BusConfigs))
	busNames := make(map[string]struct{}, len(data.BusConfigs))
	for i := range data.BusConfigs {
		row := &data.BusConfigs[i]
		row.Name = strings.TrimSpace(row.Name)
		if row.ID == 0 {
			return invalid("bus_configs entries require a positive id")
		}
		if _, exists := busIDs[row.ID]; exists {
			return invalid("bus_configs contains duplicate ids")
		}
		busIDs[row.ID] = struct{}{}
		if row.Name == "" || utf8.RuneCountInString(row.Name) > 128 {
			return invalid("bus_config name must contain 1 to 128 characters")
		}
		if _, exists := busNames[row.Name]; exists {
			return invalid("bus_configs contains duplicate names")
		}
		busNames[row.Name] = struct{}{}
		if row.CycleTimeMicros != 250 && row.CycleTimeMicros != 500 && row.CycleTimeMicros != 1000 && row.CycleTimeMicros != 2000 {
			return invalid("bus_config cycle_time_micros must be 250, 500, 1000 or 2000")
		}
	}

	type aliasKey struct {
		card    uint16
		kind    string
		address uint32
	}
	aliasKeys := make(map[aliasKey]struct{}, len(data.IOAliases))
	for i := range data.IOAliases {
		row := &data.IOAliases[i]
		if err := row.Normalize(); err != nil {
			return invalid("invalid io_alias: " + err.Error())
		}
		key := aliasKey{card: row.CardNo, kind: row.Kind, address: row.Address}
		if _, exists := aliasKeys[key]; exists {
			return invalid("io_aliases contains duplicate card_no/kind/address entries")
		}
		aliasKeys[key] = struct{}{}
	}
	return nil
}
