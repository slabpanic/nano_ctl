package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"nano_ctl/service/hardware"
	"nano_ctl/service/store"
	"nano_ctl/service/variables"
)

type ioVariableMetadataBody struct {
	Alias    string `json:"alias"`
	Remarks  string `json:"remarks"`
	Reverse  bool   `json:"reverse"`
	Disabled bool   `json:"disabled"`
}

type ioVariableWriteBody struct {
	On            *bool   `json:"on"`
	RawValue      *uint32 `json:"raw_value"`
	ReverseMillis uint32  `json:"reverse_time_millis"`
}

// listIOAliases godoc
// @Summary 列出 IO 变量配置
// @Tags io-variables
// @Produce json
// @Param card_no query int false "控制卡编号；省略时列出所有卡"
// @Success 200 {object} envelope
// @Router /io-variables [get]
func (s *Server) listIOAliases(c *gin.Context) {
	var card *uint16
	if c.Query("card_no") != "" {
		n, err := queryUint(c, "card_no", "0", 16)
		if err != nil {
			fail(c, err)
			return
		}
		value := uint16(n)
		card = &value
	}
	rows, err := s.db.ListIOAliases(card)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, rows)
}

func aliasRef(c *gin.Context) (store.IOAlias, error) {
	card, err := pathCard(c)
	if err != nil {
		return store.IOAlias{}, err
	}
	if _, err := parseIOKind(c.Param("kind")); err != nil {
		return store.IOAlias{}, err
	}
	address, err := parseUint(c, "address", 16)
	return store.IOAlias{CardNo: card, Kind: c.Param("kind"), Address: uint32(address)}, err
}

// getIOAlias godoc
// @Summary 读取 IO 变量配置
// @Tags io-variables
// @Produce json
// @Param card path int true "控制卡编号"
// @Param kind path string true "IO 类型" Enums(input-bit, output-bit, input-port, output-port, analog-input, analog-output)
// @Param address path int true "IO 地址"
// @Success 200 {object} envelope
// @Failure 404 {object} envelope
// @Router /io-variables/{card}/{kind}/{address} [get]
func (s *Server) getIOAlias(c *gin.Context) {
	ref, err := aliasRef(c)
	if err != nil {
		fail(c, err)
		return
	}
	row, err := s.db.GetIOAlias(ref.CardNo, ref.Kind, ref.Address)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, row)
}

// putIOAlias godoc
// @Summary 创建或更新 IO 变量配置
// @Tags io-variables
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param kind path string true "IO 类型" Enums(input-bit, output-bit, input-port, output-port, analog-input, analog-output)
// @Param address path int true "IO 地址"
// @Param body body ioVariableMetadataBody true "变量名称与读写属性"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Router /io-variables/{card}/{kind}/{address} [put]
func (s *Server) putIOAlias(c *gin.Context) {
	ref, err := aliasRef(c)
	if err != nil {
		fail(c, err)
		return
	}
	var body ioVariableMetadataBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	ref.Alias, ref.Remarks, ref.Reverse, ref.Disabled = body.Alias, body.Remarks, body.Reverse, body.Disabled
	if err := s.db.PutIOAlias(&ref); err != nil {
		fail(c, err)
		return
	}
	// Re-read to return retained source provenance on an update.
	row, err := s.db.GetIOAlias(ref.CardNo, ref.Kind, ref.Address)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, row)
}

// deleteIOAlias godoc
// @Summary 删除 IO 变量配置
// @Tags io-variables
// @Produce json
// @Param card path int true "控制卡编号"
// @Param kind path string true "IO 类型" Enums(input-bit, output-bit, input-port, output-port, analog-input, analog-output)
// @Param address path int true "IO 地址"
// @Success 204
// @Failure 404 {object} envelope
// @Router /io-variables/{card}/{kind}/{address} [delete]
func (s *Server) deleteIOAlias(c *gin.Context) {
	ref, err := aliasRef(c)
	if err != nil {
		fail(c, err)
		return
	}
	if err := s.db.DeleteIOAlias(ref.CardNo, ref.Kind, ref.Address); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// readIOAlias godoc
// @Summary 读取 IO 变量值
// @Tags io-variables
// @Produce json
// @Param card path int true "控制卡编号"
// @Param kind path string true "IO 类型" Enums(input-bit, output-bit, input-port, output-port, analog-input, analog-output)
// @Param address path int true "IO 地址"
// @Success 200 {object} envelope
// @Failure 409 {object} envelope
// @Failure 404 {object} envelope
// @Failure 503 {object} envelope
// @Router /io-variables/{card}/{kind}/{address}/value [get]
func (s *Server) readIOAlias(c *gin.Context) {
	ref, err := aliasRef(c)
	if err != nil {
		fail(c, err)
		return
	}
	row, err := s.db.GetIOAlias(ref.CardNo, ref.Kind, ref.Address)
	if err != nil {
		fail(c, err)
		return
	}
	value, err := variables.Read(c.Request.Context(), s.hw, row, hardware.PriorityControl)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, value)
}

// writeIOAlias godoc
// @Summary 写入 IO 变量逻辑值
// @Tags io-variables
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param kind path string true "IO 类型" Enums(output-bit, output-port)
// @Param address path int true "IO 地址"
// @Param body body ioVariableWriteBody true "逻辑开关或端口原始值"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 409 {object} envelope
// @Failure 404 {object} envelope
// @Failure 503 {object} envelope
// @Router /io-variables/{card}/{kind}/{address}/value [put]
func (s *Server) writeIOAlias(c *gin.Context) {
	ref, err := aliasRef(c)
	if err != nil {
		fail(c, err)
		return
	}
	var body ioVariableWriteBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	if ref.Kind == "output-bit" && body.On == nil && body.ReverseMillis == 0 || ref.Kind == "output-port" && body.RawValue == nil {
		fail(c, invalid("on or raw_value is required for output writes"))
		return
	}
	row, err := s.db.GetIOAlias(ref.CardNo, ref.Kind, ref.Address)
	if err != nil {
		fail(c, err)
		return
	}
	var raw uint32
	var on bool
	if body.RawValue != nil {
		raw = *body.RawValue
	}
	if body.On != nil {
		on = *body.On
	}
	if err := variables.Write(c.Request.Context(), s.hw, row, raw, 0, on, body.ReverseMillis, hardware.PriorityControl); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"written": true})
}
