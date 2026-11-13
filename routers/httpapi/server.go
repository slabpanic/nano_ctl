package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	_ "nano_ctl/routers/httpapi/docs"
	"nano_ctl/service/hardware"
	"nano_ctl/service/store"
	"nano_ctl/service/variables"
)

type Server struct {
	hw *hardware.Controller
	db *store.Store
}

func New(hw *hardware.Controller, db *store.Store) *Server { return &Server{hw: hw, db: db} }

func (s *Server) Handler() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), bodyLimit(128<<20))
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, envelope{Error: &apiError{Code: "not_found", Message: "route not found"}})
	})
	r.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, envelope{Error: &apiError{Code: "method_not_allowed", Message: "method not allowed"}})
	})
	v1 := r.Group("/api/v1")
	v1.GET("/health", s.health)
	v1.GET("/data/export", s.exportData)
	v1.PUT("/data/import", s.importData)
	v1.GET("/io-variables", s.listIOAliases)
	v1.GET("/io-variables/:card/:kind/:address", s.getIOAlias)
	v1.PUT("/io-variables/:card/:kind/:address", s.putIOAlias)
	v1.DELETE("/io-variables/:card/:kind/:address", s.deleteIOAlias)
	v1.GET("/io-variables/:card/:kind/:address/value", s.readIOAlias)
	v1.PUT("/io-variables/:card/:kind/:address/value", s.writeIOAlias)
	v1.GET("/cards", s.listCards)
	v1.GET("/cards/:card", s.getCard)
	v1.POST("/cards/:card/reset", s.resetCard)
	v1.POST("/cards/:card/soft-reset", s.softResetCard)
	v1.PUT("/cards/:card/cycle-time", s.setCycleTime)
	v1.GET("/cards/:card/io-info", s.getIOInfo)
	v1.GET("/cards/:card/axes", s.readAxes)
	v1.POST("/cards/:card/axes/read-batch", s.readAxesBody)
	v1.GET("/cards/:card/axes/:axis", s.readAxis)
	v1.POST("/cards/:card/axes/:axis/commands", s.axisCommand)
	v1.GET("/cards/:card/axis-stream", s.watchAxes)
	v1.GET("/cards/:card/io/:kind", s.readIO)
	v1.POST("/cards/:card/io/:kind/reads", s.readIOBody)
	v1.PUT("/cards/:card/io/:kind/:address", s.writeIO)
	v1.GET("/cards/:card/io-arrays/:direction/:port", s.readIOArray)
	v1.GET("/cards/:card/io-stream", s.watchIO)
	v1.POST("/cards/:card/io-stream", s.watchIO)
	v1.GET("/cards/:card/bus", s.readBus)
	v1.POST("/cards/:card/bus/commands", s.busCommand)
	v1.GET("/cards/:card/bus/od/:node/:index/:sub_index", s.readOD)
	v1.PUT("/cards/:card/bus/od/:node/:index/:sub_index", s.writeOD)
	v1.GET("/cards/:card/bus/pdo/rx/:address", s.readRxPDO)
	v1.PUT("/cards/:card/bus/pdo/rx/:address", s.writeRxPDO)
	v1.GET("/cards/:card/bus/pdo/tx/:address", s.readTxPDO)
	v1.GET("/cards/:card/hcmp/:channel", s.readHcmp)
	v1.POST("/cards/:card/hcmp/:channel/commands", s.hcmpCommand)
	v1.GET("/cards/:card/extra-encoders/:channel", s.readExtraEncoder)
	v1.PUT("/cards/:card/extra-encoders/:channel", s.extraEncoderCommand)
	v1.POST("/cards/:card/bus-configs/:id/apply", s.applyBusConfig)
	v1.GET("/cards/:card/axes/:axis/equivalence", s.getAxisEquivalence)
	v1.PUT("/cards/:card/axes/:axis/equivalence", s.setAxisEquivalence)

	v1.GET("/axis-configs", s.listAxisConfigs)
	v1.POST("/axis-configs", s.createAxisConfig)
	v1.GET("/axis-configs/:card/:axis", s.getAxisConfig)
	v1.PUT("/axis-configs/:card/:axis", s.putAxisConfig)
	v1.DELETE("/axis-configs/:card/:axis", s.deleteAxisConfig)

	v1.GET("/bus-configs", s.listBusConfigs)
	v1.POST("/bus-configs", s.createBusConfig)
	v1.GET("/bus-configs/:id", s.getBusConfig)
	v1.PUT("/bus-configs/:id", s.putBusConfig)
	v1.DELETE("/bus-configs/:id", s.deleteBusConfig)
	v1.GET("/bus-configs/:id/files/:kind", s.exportBusConfigFile)
	v1.PUT("/bus-configs/:id/files/:kind", s.importBusConfigFile)
	return r
}

type envelope struct {
	Data  any       `json:"data,omitempty"`
	Error *apiError `json:"error,omitempty"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	SDKCode *int16 `json:"sdk_code,omitempty"`
}

type RequestError struct{ Message string }

func (e *RequestError) Error() string { return e.Message }
func invalid(message string) error    { return &RequestError{Message: message} }

func respond(c *gin.Context, code int, data any) { c.JSON(code, envelope{Data: data}) }
func fail(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	out := &apiError{Code: "internal", Message: err.Error()}
	switch {
	case errors.Is(err, store.ErrInvalidIOAlias), errors.Is(err, variables.ErrReadOnly):
		status = http.StatusBadRequest
		out.Code = "invalid_argument"
	case errors.Is(err, variables.ErrDisabled):
		status = http.StatusConflict
		out.Code = "variable_disabled"
	case errors.Is(err, hardware.ErrCardClosed):
		status = http.StatusServiceUnavailable
		out.Code = "card_closed"
	case errors.Is(err, hardware.ErrTransition):
		status = http.StatusServiceUnavailable
		out.Code = "card_transition"
	case errors.Is(err, hardware.ErrBusy):
		status = http.StatusTooManyRequests
		out.Code = "hardware_queue_full"
	case errors.Is(err, hardware.ErrCardUnknown), store.IsNotFound(err), errors.Is(err, store.ErrBusConfigFileMissing):
		status = http.StatusNotFound
		out.Code = "not_found"
	case errors.As(err, new(*strconv.NumError)):
		status = http.StatusBadRequest
		out.Code = "invalid_argument"
	case errors.As(err, new(*RequestError)):
		status = http.StatusBadRequest
		out.Code = "invalid_argument"
	case errors.As(err, new(*hardware.SDKError)):
		status = http.StatusBadGateway
		out.Code = "sdk_error"
		var sdkErr *hardware.SDKError
		if errors.As(err, &sdkErr) {
			code := sdkErr.Code
			out.SDKCode = &code
		}
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		out.Code = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		status = 499
		out.Code = "canceled"
	case errors.As(err, new(*http.MaxBytesError)):
		status = http.StatusRequestEntityTooLarge
		out.Code = "body_too_large"
	case strings.Contains(err.Error(), "UNIQUE constraint failed"):
		status = http.StatusConflict
		out.Code = "conflict"
	}
	c.JSON(status, envelope{Error: out})
}

func bodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

func bindJSON(c *gin.Context, dst any) error {
	if err := c.ShouldBindJSON(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return err
		}
		return invalid(err.Error())
	}
	return nil
}

func parseUint(c *gin.Context, key string, bits int) (uint64, error) {
	v, err := strconv.ParseUint(c.Param(key), 10, bits)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return v, nil
}
func pathCard(c *gin.Context) (uint16, error) {
	v, err := parseUint(c, "card", 16)
	return uint16(v), err
}

func pathU16(c *gin.Context, key string) (uint16, error) {
	v, err := parseUint(c, key, 16)
	return uint16(v), err
}

func queryUint(c *gin.Context, key, fallback string, bits int) (uint64, error) {
	raw := c.Query(key)
	if raw == "" {
		raw = fallback
	}
	v, err := strconv.ParseUint(raw, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return v, nil
}

func parseUintList(raw, name string, bits, maxItems int) ([]uint32, error) {
	if raw == "" {
		return nil, invalid(name + " is required")
	}
	parts := strings.Split(raw, ",")
	if len(parts) == 0 || len(parts) > maxItems {
		return nil, invalid(fmt.Sprintf("%s must contain between 1 and %d values", name, maxItems))
	}
	values := make([]uint32, len(parts))
	for i, part := range parts {
		v, err := strconv.ParseUint(strings.TrimSpace(part), 10, bits)
		if err != nil {
			return nil, fmt.Errorf("invalid %s item %q: %w", name, part, err)
		}
		values[i] = uint32(v)
	}
	return values, nil
}

func readInterval(c *gin.Context) (time.Duration, error) {
	micros, err := queryUint(c, "interval_micros", "10000", 32)
	if err != nil {
		return 0, err
	}
	if micros < 1000 || micros > 1000000 {
		return 0, invalid("interval_micros must be between 1000 and 1000000")
	}
	return time.Duration(micros) * time.Microsecond, nil
}

func writeStreamEvent(c *gin.Context, event string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Writer.Write([]byte("event: " + event + "\ndata: " + string(data) + "\n\n")); err != nil {
		return err
	}
	c.Writer.Flush()
	return nil
}

// health godoc
// @Summary 服务健康状态
// @Tags system
// @Produce json
// @Success 200 {object} envelope
// @Router /health [get]
func (s *Server) health(c *gin.Context) {
	open, transitioning, cards := s.hw.Status()
	respond(c, http.StatusOK, gin.H{"status": "ok", "card_open": open, "transitioning": transitioning, "cards": cards})
}

// listCards godoc
// @Summary 列出已打开的控制卡
// @Tags cards
// @Produce json
// @Success 200 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards [get]
func (s *Server) listCards(c *gin.Context) {
	open, _, ids := s.hw.Status()
	if !open {
		fail(c, hardware.ErrCardClosed)
		return
	}
	items := make([]hardware.CardInfo, 0, len(ids))
	for _, id := range ids {
		info, err := s.hw.CardInfo(c.Request.Context(), id, 2)
		if err != nil {
			fail(c, err)
			return
		}
		items = append(items, info)
	}
	respond(c, http.StatusOK, items)
}

// getCard godoc
// @Summary 读取控制卡信息
// @Tags cards
// @Produce json
// @Param card path int true "控制卡编号"
// @Success 200 {object} envelope
// @Failure 404 {object} envelope
// @Router /cards/{card} [get]
func (s *Server) getCard(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	info, err := s.hw.CardInfo(c.Request.Context(), card, 2)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, info)
}

// resetCard godoc
// @Summary 复位控制卡
// @Tags cards
// @Produce json
// @Param card path int true "控制卡编号"
// @Success 200 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/reset [post]
func (s *Server) resetCard(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	if err = s.hw.Reset(c.Request.Context(), card); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"card_no": card, "reset": true})
}

// softResetCard godoc
// @Summary 软复位控制卡
// @Tags cards
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param body body map[string]int64 false "软复位选项，例如 timeout_ms"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/soft-reset [post]
func (s *Server) softResetCard(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	var body struct {
		TimeoutMS int64 `json:"timeout_ms"`
	}
	if c.Request.ContentLength != 0 {
		if err := bindJSON(c, &body); err != nil {
			fail(c, err)
			return
		}
	}
	if body.TimeoutMS <= 0 {
		body.TimeoutMS = 5000
	}
	if body.TimeoutMS > 60000 {
		fail(c, invalid("timeout_ms must not exceed 60000"))
		return
	}
	if err = s.hw.SoftReset(c.Request.Context(), card, time.Duration(body.TimeoutMS)*time.Millisecond); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"card_no": card, "reset": true})
}

// setCycleTime godoc
// @Summary 设置 EtherCAT 周期
// @Tags cards
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param body body map[string]uint32 true "总线端口与周期"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/cycle-time [put]
func (s *Server) setCycleTime(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	var body struct {
		PortNum   uint16 `json:"port_num"`
		CycleTime uint32 `json:"cycle_time_micros"`
	}
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	if body.PortNum == 0 {
		body.PortNum = 2
	}
	if body.PortNum != 2 {
		fail(c, invalid("port_num must be 2"))
		return
	}
	if body.CycleTime != 250 && body.CycleTime != 500 && body.CycleTime != 1000 && body.CycleTime != 2000 {
		fail(c, invalid("cycle_time_micros must be 250, 500, 1000 or 2000"))
		return
	}
	if err := s.hw.SetCycleTime(c.Request.Context(), card, body.PortNum, body.CycleTime); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"card_no": card, "port_num": body.PortNum, "cycle_time_micros": body.CycleTime})
}

// getIOInfo godoc
// @Summary 读取本地与 EtherCAT IO 数量
// @Tags cards
// @Produce json
// @Param card path int true "控制卡编号"
// @Success 200 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/io-info [get]
func (s *Server) getIOInfo(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	info, err := s.hw.CardInfo(c.Request.Context(), card, 2)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"card_no": card, "local": gin.H{"inputs": info.LocalInputCount, "outputs": info.LocalOutputCount}, "ethercat": gin.H{"inputs": info.BusInputCount, "outputs": info.BusOutputCount}})
}

// getAxisEquivalence godoc
// @Summary 读取轴当量
// @Tags axes
// @Produce json
// @Param card path int true "控制卡编号"
// @Param axis path int true "轴编号"
// @Success 200 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/axes/{axis}/equivalence [get]
func (s *Server) getAxisEquivalence(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	axis64, err := parseUint(c, "axis", 16)
	if err != nil {
		fail(c, err)
		return
	}
	axis := uint16(axis64)
	equivalence, err := s.hw.ReadAxisEquivalence(c.Request.Context(), card, axis)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"card_no": card, "axis": axis, "equivalence": equivalence})
}

// setAxisEquivalence godoc
// @Summary 设置轴当量
// @Tags axes
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param axis path int true "轴编号"
// @Param body body map[string]float64 true "轴当量"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/axes/{axis}/equivalence [put]
func (s *Server) setAxisEquivalence(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	axis64, err := parseUint(c, "axis", 16)
	if err != nil {
		fail(c, err)
		return
	}
	var body struct {
		Equivalence float64 `json:"equivalence"`
	}
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	if body.Equivalence <= 0 || math.IsNaN(body.Equivalence) || math.IsInf(body.Equivalence, 0) {
		fail(c, invalid("equivalence must be finite and greater than zero"))
		return
	}
	if err := s.hw.SetAxisEquivalence(c.Request.Context(), card, uint16(axis64), body.Equivalence); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"card_no": card, "axis": axis64, "equivalence": body.Equivalence})
}

func axisReadMask(c *gin.Context) (hardware.AxisReadMask, error) {
	v, err := queryUint(c, "fields_mask", "0", 32)
	return hardware.AxisReadMask(v), err
}

// readAxis godoc
// @Summary 读取单轴状态
// @Tags axes
// @Produce json
// @Param card path int true "控制卡编号"
// @Param axis path int true "轴编号"
// @Param fields_mask query int false "状态字段掩码"
// @Success 200 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/axes/{axis} [get]
func (s *Server) readAxis(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	axis, err := pathU16(c, "axis")
	if err != nil {
		fail(c, err)
		return
	}
	mask, err := axisReadMask(c)
	if err != nil {
		fail(c, err)
		return
	}
	value, err := s.hw.ReadAxis(c.Request.Context(), card, axis, mask, hardware.PriorityControl)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, value)
}

// readAxes godoc
// @Summary 批量读取轴状态
// @Tags axes
// @Produce json
// @Param card path int true "控制卡编号"
// @Param axes query string true "逗号分隔的轴编号，最多 64 个"
// @Param fields_mask query int false "状态字段掩码"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/axes [get]
func (s *Server) readAxes(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	rawAxes, err := parseUintList(c.Query("axes"), "axes", 16, 64)
	if err != nil {
		fail(c, err)
		return
	}
	axes := make([]uint16, len(rawAxes))
	for i, axis := range rawAxes {
		axes[i] = uint16(axis)
	}
	mask, err := axisReadMask(c)
	if err != nil {
		fail(c, err)
		return
	}
	values, err := s.hw.ReadAxes(c.Request.Context(), card, axes, mask, hardware.PriorityControl)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"sampled_at_unix_nano": time.Now().UnixNano(), "axes": values})
}

type axesReadBody struct {
	Axes       []uint16 `json:"axes"`
	FieldsMask uint32   `json:"fields_mask"`
}

// readAxesBody godoc
// @Summary 通过请求体批量读取轴状态
// @Tags axes
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param body body axesReadBody true "轴编号列表与状态字段掩码"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/axes/read-batch [post]
func (s *Server) readAxesBody(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	var body axesReadBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	if len(body.Axes) == 0 || len(body.Axes) > 64 {
		fail(c, invalid("axes must contain between 1 and 64 values"))
		return
	}
	values, err := s.hw.ReadAxes(c.Request.Context(), card, body.Axes, hardware.AxisReadMask(body.FieldsMask), hardware.PriorityControl)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"sampled_at_unix_nano": time.Now().UnixNano(), "axes": values})
}

type axisCommandBody struct {
	Action         string  `json:"action"`
	Unit           string  `json:"unit"`
	Position       float64 `json:"position"`
	Speed          float64 `json:"speed"`
	Acceleration   float64 `json:"acceleration"`
	Deceleration   float64 `json:"deceleration"`
	Direction      uint16  `json:"direction"`
	PositionMode   uint16  `json:"position_mode"`
	StopMode       uint16  `json:"stop_mode"`
	HomeMethod     uint16  `json:"home_method"`
	SecondarySpeed float64 `json:"secondary_speed"`
	Offset         float64 `json:"offset"`
	ProbeFunction  uint16  `json:"probe_function"`
	Equivalence    float64 `json:"equivalence"`
}

func parseAxisAction(value string) (hardware.AxisAction, error) {
	switch strings.ToLower(value) {
	case "enable":
		return hardware.ActionEnable, nil
	case "disable":
		return hardware.ActionDisable, nil
	case "stop":
		return hardware.ActionStop, nil
	case "emergency_stop":
		return hardware.ActionEmergencyStop, nil
	case "clear_fault":
		return hardware.ActionClearFault, nil
	case "clear_stop_reason":
		return hardware.ActionClearStopReason, nil
	case "zero_position":
		return hardware.ActionZeroPosition, nil
	case "jog":
		return hardware.ActionJog, nil
	case "move":
		return hardware.ActionMove, nil
	case "home":
		return hardware.ActionHome, nil
	case "home_stop":
		return hardware.ActionHomeStop, nil
	case "change_speed":
		return hardware.ActionChangeSpeed, nil
	case "change_target":
		return hardware.ActionChangeTarget, nil
	case "set_equivalence":
		return hardware.ActionSetEquivalence, nil
	case "set_offset":
		return hardware.ActionSetOffset, nil
	default:
		return 0, invalid("unsupported axis action")
	}
}

// axisCommand godoc
// @Summary 执行轴命令
// @Tags axes
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param axis path int true "轴编号"
// @Param body body axisCommandBody true "轴动作与动作参数"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/axes/{axis}/commands [post]
func (s *Server) axisCommand(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	axis, err := pathU16(c, "axis")
	if err != nil {
		fail(c, err)
		return
	}
	var body axisCommandBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	action, err := parseAxisAction(body.Action)
	if err != nil {
		fail(c, err)
		return
	}
	unit := strings.ToLower(body.Unit)
	if unit == "" {
		unit = "position"
	}
	if unit != "position" && unit != "angle" {
		fail(c, invalid("unit must be position or angle"))
		return
	}
	cmd := hardware.AxisCommand{CardNo: card, Axis: axis, Action: action, Position: body.Position, Speed: body.Speed, Acceleration: body.Acceleration, Deceleration: body.Deceleration, Direction: body.Direction, PositionMode: body.PositionMode, StopMode: body.StopMode, HomeMethod: body.HomeMethod, SecondarySpeed: body.SecondarySpeed, Offset: body.Offset, ProbeFunction: body.ProbeFunction, Equivalence: body.Equivalence}
	if unit == "angle" {
		cfg, err := s.db.GetAxisConfig(card, axis)
		if err != nil {
			fail(c, err)
			return
		}
		if cfg.ReductionRatio <= 0 || cfg.Resolution < 1 || cfg.Resolution > 40 {
			fail(c, invalid("axis angle configuration is invalid"))
			return
		}
		degreePerPosition := 360.0 / (float64(cfg.ReductionRatio) * math.Pow(2, float64(cfg.Resolution)))
		switch action {
		case hardware.ActionJog, hardware.ActionMove, hardware.ActionHome, hardware.ActionChangeSpeed:
			cmd.Speed /= degreePerPosition
			cmd.Acceleration /= degreePerPosition
			cmd.Deceleration /= degreePerPosition
			cmd.SecondarySpeed /= degreePerPosition
		}
		switch action {
		case hardware.ActionMove, hardware.ActionChangeTarget:
			cmd.Position = math.RoundToEven(cmd.Position / degreePerPosition)
		case hardware.ActionHome:
			cmd.Offset = math.RoundToEven(cmd.Offset / degreePerPosition)
		}
	}
	if err := s.hw.AxisCommand(c.Request.Context(), cmd, hardware.PriorityControl); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"sdk_code": 0})
}

// watchAxes godoc
// @Summary 通过 Server-Sent Events 订阅轴状态
// @Tags axes
// @Produce text/event-stream
// @Param card path int true "控制卡编号"
// @Param axes query string true "逗号分隔的轴编号，最多 64 个"
// @Param fields_mask query int false "状态字段掩码"
// @Param interval_micros query int false "采样间隔（微秒），范围 1000 到 1000000"
// @Success 200 {string} string
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/axis-stream [get]
func (s *Server) watchAxes(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	rawAxes, err := parseUintList(c.Query("axes"), "axes", 16, 64)
	if err != nil {
		fail(c, err)
		return
	}
	axes := make([]uint16, len(rawAxes))
	for i, axis := range rawAxes {
		axes[i] = uint16(axis)
	}
	mask, err := axisReadMask(c)
	if err != nil {
		fail(c, err)
		return
	}
	interval, err := readInterval(c)
	if err != nil {
		fail(c, err)
		return
	}
	s.stream(c, "axes", interval, func() (any, error) {
		values, err := s.hw.ReadAxes(c.Request.Context(), card, axes, mask, hardware.PriorityControl)
		if err != nil {
			return nil, err
		}
		return gin.H{"sampled_at_unix_nano": time.Now().UnixNano(), "axes": values}, nil
	})
}

func (s *Server) stream(c *gin.Context, event string, interval time.Duration, read func() (any, error)) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		value, err := read()
		if err != nil {
			_ = writeStreamEvent(c, "error", gin.H{"message": err.Error()})
			return
		}
		if err := writeStreamEvent(c, event, value); err != nil {
			return
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func parseIOKind(value string) (hardware.IOKind, error) {
	switch strings.ToLower(value) {
	case "input-bit":
		return hardware.IOInputBit, nil
	case "output-bit":
		return hardware.IOOutputBit, nil
	case "input-port":
		return hardware.IOInputPort, nil
	case "output-port":
		return hardware.IOOutputPort, nil
	case "analog-input":
		return hardware.IOAnalogInput, nil
	case "analog-output":
		return hardware.IOAnalogOutput, nil
	default:
		return 0, invalid("unsupported IO kind")
	}
}

// readIO godoc
// @Summary 按地址读取 IO 物理值
// @Tags io
// @Produce json
// @Param card path int true "控制卡编号"
// @Param kind path string true "IO 类型" Enums(input-bit, output-bit, input-port, output-port, analog-input, analog-output)
// @Param addresses query string true "逗号分隔的地址，最多 4096 个"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/io/{kind} [get]
func (s *Server) readIO(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	kind, err := parseIOKind(c.Param("kind"))
	if err != nil {
		fail(c, err)
		return
	}
	addresses, err := parseUintList(c.Query("addresses"), "addresses", 16, 4096)
	if err != nil {
		fail(c, err)
		return
	}
	values, err := s.hw.ReadIO(c.Request.Context(), card, kind, addresses, hardware.PriorityControl)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, values)
}

type ioReadBody struct {
	Addresses []uint32 `json:"addresses"`
}

type ioWatchBody struct {
	Kind           string   `json:"kind"`
	Addresses      []uint32 `json:"addresses"`
	IntervalMicros uint32   `json:"interval_micros"`
}

// readIOBody godoc
// @Summary 通过请求体按地址批量读取 IO 物理值
// @Tags io
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param kind path string true "IO 类型" Enums(input-bit, output-bit, input-port, output-port, analog-input, analog-output)
// @Param body body ioReadBody true "地址列表"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/io/{kind}/reads [post]
func (s *Server) readIOBody(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	kind, err := parseIOKind(c.Param("kind"))
	if err != nil {
		fail(c, err)
		return
	}
	var body ioReadBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	if len(body.Addresses) == 0 || len(body.Addresses) > 4096 {
		fail(c, invalid("addresses must contain between 1 and 4096 values"))
		return
	}
	for _, address := range body.Addresses {
		if address > math.MaxUint16 {
			fail(c, invalid("IO address exceeds SDK range"))
			return
		}
	}
	values, err := s.hw.ReadIO(c.Request.Context(), card, kind, body.Addresses, hardware.PriorityControl)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, values)
}

type ioWriteBody struct {
	RawValue          uint32  `json:"raw_value"`
	AnalogValue       float64 `json:"analog_value"`
	On                bool    `json:"on"`
	ReverseTimeMillis uint32  `json:"reverse_time_millis"`
}

// writeIO godoc
// @Summary 写入 IO 物理值
// @Tags io
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param kind path string true "IO 类型" Enums(input-bit, output-bit, input-port, output-port, analog-input, analog-output)
// @Param address path int true "IO 地址"
// @Param body body ioWriteBody true "要写入的逻辑位值、物理原始值或模拟值"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/io/{kind}/{address} [put]
func (s *Server) writeIO(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	address, err := pathU16(c, "address")
	if err != nil {
		fail(c, err)
		return
	}
	kind, err := parseIOKind(c.Param("kind"))
	if err != nil {
		fail(c, err)
		return
	}
	var body ioWriteBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	if err := s.hw.WriteIO(c.Request.Context(), card, kind, uint32(address), body.RawValue, body.AnalogValue, body.On, body.ReverseTimeMillis, hardware.PriorityControl); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"sdk_code": 0})
}

// readIOArray godoc
// @Summary 读取连续 IO 数组
// @Tags io
// @Produce json
// @Param card path int true "控制卡编号"
// @Param direction path string true "方向" Enums(input, output)
// @Param port path int true "端口编号"
// @Param length query int false "读取长度，范围 1 到 4096"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/io-arrays/{direction}/{port} [get]
func (s *Server) readIOArray(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	port, err := pathU16(c, "port")
	if err != nil {
		fail(c, err)
		return
	}
	direction := strings.ToLower(c.Param("direction"))
	if direction != "input" && direction != "output" {
		fail(c, invalid("direction must be input or output"))
		return
	}
	length, err := queryUint(c, "length", "1", 32)
	if err != nil {
		fail(c, err)
		return
	}
	if length == 0 || length > 4096 {
		fail(c, invalid("length must be between 1 and 4096"))
		return
	}
	values, err := s.hw.ReadIOArray(c.Request.Context(), card, port, uint32(length), direction == "output", hardware.PriorityControl)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"card_no": card, "port_num": port, "direction": direction, "values": values})
}

// watchIO godoc
// @Summary 通过 Server-Sent Events 订阅 IO 物理值
// @Tags io
// @Accept json
// @Produce text/event-stream
// @Param card path int true "控制卡编号"
// @Param kind query string false "IO 类型；POST 时放在请求体中" Enums(input-bit, output-bit, input-port, output-port, analog-input, analog-output)
// @Param addresses query string false "GET 使用逗号分隔的地址列表"
// @Param interval_micros query int false "GET 采样间隔（微秒），范围 1000 到 1000000"
// @Param body body ioWatchBody false "POST 使用的 IO 类型、地址列表和采样间隔"
// @Success 200 {string} string
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/io-stream [get]
// @Router /cards/{card}/io-stream [post]
func (s *Server) watchIO(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	kindName := c.Query("kind")
	addressesRaw := c.Query("addresses")
	if c.Request.Method == http.MethodPost {
		var body ioWatchBody
		if err := bindJSON(c, &body); err != nil {
			fail(c, err)
			return
		}
		kindName = body.Kind
		if len(body.Addresses) == 0 || len(body.Addresses) > 4096 {
			fail(c, invalid("addresses must contain between 1 and 4096 values"))
			return
		}
		for _, address := range body.Addresses {
			if address > math.MaxUint16 {
				fail(c, invalid("IO address exceeds SDK range"))
				return
			}
		}
		addresses := append([]uint32(nil), body.Addresses...)
		kind, err := parseIOKind(kindName)
		if err != nil {
			fail(c, err)
			return
		}
		interval := time.Duration(body.IntervalMicros) * time.Microsecond
		if body.IntervalMicros == 0 {
			interval = 10 * time.Millisecond
		}
		if interval < time.Millisecond || interval > time.Second {
			fail(c, invalid("interval_micros must be between 1000 and 1000000"))
			return
		}
		s.stream(c, "io", interval, func() (any, error) {
			values, err := s.hw.ReadIO(c.Request.Context(), card, kind, addresses, hardware.PriorityControl)
			return gin.H{"sampled_at_unix_nano": time.Now().UnixNano(), "values": values}, err
		})
		return
	}
	kind, err := parseIOKind(kindName)
	if err != nil {
		fail(c, err)
		return
	}
	addresses, err := parseUintList(addressesRaw, "addresses", 16, 4096)
	if err != nil {
		fail(c, err)
		return
	}
	interval, err := readInterval(c)
	if err != nil {
		fail(c, err)
		return
	}
	s.stream(c, "io", interval, func() (any, error) {
		values, err := s.hw.ReadIO(c.Request.Context(), card, kind, addresses, hardware.PriorityControl)
		return gin.H{"sampled_at_unix_nano": time.Now().UnixNano(), "values": values}, err
	})
}

func busPort(c *gin.Context) (uint16, error) {
	v, err := queryUint(c, "port_num", "2", 16)
	return uint16(v), err
}

// readBus godoc
// @Summary 读取 EtherCAT 总线状态
// @Tags bus
// @Produce json
// @Param card path int true "控制卡编号"
// @Param port_num query int false "总线端口编号，默认 2"
// @Success 200 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/bus [get]
func (s *Server) readBus(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	port, err := busPort(c)
	if err != nil {
		fail(c, err)
		return
	}
	value, err := s.hw.ReadBus(c.Request.Context(), card, port, hardware.PriorityControl)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, value)
}

type busCommandBody struct {
	Action  string `json:"action"`
	PortNum uint16 `json:"port_num"`
}

// busCommand godoc
// @Summary 执行 EtherCAT 总线命令
// @Tags bus
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param body body busCommandBody true "stop 或 clear_error 命令"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/bus/commands [post]
func (s *Server) busCommand(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	var body busCommandBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	port := body.PortNum
	if port == 0 {
		port = 2
	}
	switch strings.ToLower(body.Action) {
	case "stop":
		state, err := s.hw.StopBus(c.Request.Context(), card, hardware.PriorityControl)
		if err != nil {
			fail(c, err)
			return
		}
		respond(c, http.StatusOK, gin.H{"sdk_code": 0, "value": state})
	case "clear_error":
		if err := s.hw.ClearBusError(c.Request.Context(), card, port, hardware.PriorityControl); err != nil {
			fail(c, err)
			return
		}
		respond(c, http.StatusOK, gin.H{"sdk_code": 0})
	default:
		fail(c, invalid("action must be stop or clear_error"))
	}
}

func odPath(c *gin.Context) (card, port, node, index, sub, length uint16, err error) {
	card, err = pathCard(c)
	if err != nil {
		return
	}
	port, err = busPort(c)
	if err != nil {
		return
	}
	node, err = pathU16(c, "node")
	if err != nil {
		return
	}
	index, err = pathU16(c, "index")
	if err != nil {
		return
	}
	sub, err = pathU16(c, "sub_index")
	if err != nil {
		return
	}
	var raw uint64
	raw, err = queryUint(c, "value_length", "4", 16)
	length = uint16(raw)
	return
}

// readOD godoc
// @Summary 读取 EtherCAT 对象字典
// @Tags bus
// @Produce json
// @Param card path int true "控制卡编号"
// @Param node path int true "从站节点"
// @Param index path int true "对象索引"
// @Param sub_index path int true "子索引"
// @Param port_num query int false "总线端口编号，默认 2"
// @Param value_length query int false "读取字节数，默认 4"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/bus/od/{node}/{index}/{sub_index} [get]
func (s *Server) readOD(c *gin.Context) {
	card, port, node, index, sub, length, err := odPath(c)
	if err != nil {
		fail(c, err)
		return
	}
	value, err := s.hw.ReadOD(c.Request.Context(), card, port, node, index, sub, length, hardware.PriorityControl)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"value": value})
}

type odWriteBody struct {
	Value       int32  `json:"value"`
	ValueLength uint16 `json:"value_length"`
}

// writeOD godoc
// @Summary 写入 EtherCAT 对象字典
// @Tags bus
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param node path int true "从站节点"
// @Param index path int true "对象索引"
// @Param sub_index path int true "子索引"
// @Param port_num query int false "总线端口编号，默认 2"
// @Param value_length query int false "写入字节数，默认 4"
// @Param body body odWriteBody true "对象值"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/bus/od/{node}/{index}/{sub_index} [put]
func (s *Server) writeOD(c *gin.Context) {
	card, port, node, index, sub, length, err := odPath(c)
	if err != nil {
		fail(c, err)
		return
	}
	var body odWriteBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	if body.ValueLength != 0 {
		length = body.ValueLength
	}
	if err := s.hw.WriteOD(c.Request.Context(), card, port, node, index, sub, length, body.Value, hardware.PriorityControl); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"sdk_code": 0})
}

func pdoPath(c *gin.Context) (card, port, address, length uint16, err error) {
	card, err = pathCard(c)
	if err != nil {
		return
	}
	port, err = busPort(c)
	if err != nil {
		return
	}
	address, err = pathU16(c, "address")
	if err != nil {
		return
	}
	var raw uint64
	raw, err = queryUint(c, "data_length", "4", 16)
	length = uint16(raw)
	return
}

// readRxPDO godoc
// @Summary 读取 EtherCAT RxPDO 映射值
// @Tags bus
// @Produce json
// @Param card path int true "控制卡编号"
// @Param address path int true "PDO 地址"
// @Param port_num query int false "总线端口编号，默认 2"
// @Param data_length query int false "数据长度，默认 4"
// @Success 200 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/bus/pdo/rx/{address} [get]
func (s *Server) readRxPDO(c *gin.Context) { s.readPDO(c, true) }

// readTxPDO godoc
// @Summary 读取 EtherCAT TxPDO 映射值
// @Tags bus
// @Produce json
// @Param card path int true "控制卡编号"
// @Param address path int true "PDO 地址"
// @Param port_num query int false "总线端口编号，默认 2"
// @Param data_length query int false "数据长度，默认 4"
// @Success 200 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/bus/pdo/tx/{address} [get]
func (s *Server) readTxPDO(c *gin.Context) { s.readPDO(c, false) }
func (s *Server) readPDO(c *gin.Context, rx bool) {
	card, port, address, length, err := pdoPath(c)
	if err != nil {
		fail(c, err)
		return
	}
	value, err := s.hw.ReadPDO(c.Request.Context(), card, port, address, length, rx, hardware.PriorityControl)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"value": value})
}

type pdoWriteBody struct {
	Value      int32  `json:"value"`
	DataLength uint16 `json:"data_length"`
}

// writeRxPDO godoc
// @Summary 写入 EtherCAT RxPDO 映射值
// @Tags bus
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param address path int true "PDO 地址"
// @Param port_num query int false "总线端口编号，默认 2"
// @Param data_length query int false "数据长度，默认 4"
// @Param body body pdoWriteBody true "PDO 值"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/bus/pdo/rx/{address} [put]
func (s *Server) writeRxPDO(c *gin.Context) {
	card, port, address, length, err := pdoPath(c)
	if err != nil {
		fail(c, err)
		return
	}
	var body pdoWriteBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	if body.DataLength != 0 {
		length = body.DataLength
	}
	if err := s.hw.WritePDO(c.Request.Context(), card, port, address, length, body.Value, hardware.PriorityControl); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"sdk_code": 0})
}

func channelPath(c *gin.Context) (uint16, uint16, error) {
	card, err := pathCard(c)
	if err != nil {
		return 0, 0, err
	}
	channel, err := pathU16(c, "channel")
	return card, channel, err
}

func hcmpData(value hardware.HcmpSnapshot) gin.H {
	return gin.H{"card_no": value.CardNo, "channel": value.Channel, "mode": value.Mode, "axis": value.Axis, "source": value.Source, "logic": value.Logic, "time": value.Time, "increment": value.Increment, "count": value.Count, "remaining_points": value.RemainingPoints, "current_point": value.CurrentPoint, "run_points": value.RunPoints}
}

// readHcmp godoc
// @Summary 读取高速比较器状态
// @Tags hcmp
// @Produce json
// @Param card path int true "控制卡编号"
// @Param channel path int true "比较器通道"
// @Success 200 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/hcmp/{channel} [get]
func (s *Server) readHcmp(c *gin.Context) {
	card, channel, err := channelPath(c)
	if err != nil {
		fail(c, err)
		return
	}
	value, err := s.hw.Hcmp(c.Request.Context(), hardware.HcmpCommand{CardNo: card, Channel: channel}, hardware.PriorityControl)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, hcmpData(value))
}

type hcmpCommandBody struct {
	Action    string `json:"action"`
	Mode      uint16 `json:"mode"`
	Axis      uint16 `json:"axis"`
	Source    uint16 `json:"source"`
	Logic     uint16 `json:"logic"`
	Time      int32  `json:"time"`
	Increment int32  `json:"increment"`
	Count     int32  `json:"count"`
	Position  int32  `json:"position"`
}

// hcmpCommand godoc
// @Summary 配置高速比较器
// @Tags hcmp
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param channel path int true "比较器通道"
// @Param body body hcmpCommandBody true "比较器命令参数"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/hcmp/{channel}/commands [post]
func (s *Server) hcmpCommand(c *gin.Context) {
	card, channel, err := channelPath(c)
	if err != nil {
		fail(c, err)
		return
	}
	var body hcmpCommandBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	actions := map[string]uint8{"set_config": 1, "set_linear": 2, "add_point": 3, "clear_points": 4}
	action, ok := actions[strings.ToLower(body.Action)]
	if !ok {
		fail(c, invalid("unsupported hcmp action"))
		return
	}
	cmd := hardware.HcmpCommand{CardNo: card, Channel: channel, Action: action, Mode: body.Mode, Axis: body.Axis, Source: body.Source, Logic: body.Logic, Time: body.Time, Increment: body.Increment, Count: body.Count, Position: body.Position}
	if _, err := s.hw.Hcmp(c.Request.Context(), cmd, hardware.PriorityControl); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"sdk_code": 0})
}

// readExtraEncoder godoc
// @Summary 读取外接编码器状态
// @Tags extra-encoders
// @Produce json
// @Param card path int true "控制卡编号"
// @Param channel path int true "编码器通道"
// @Success 200 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/extra-encoders/{channel} [get]
func (s *Server) readExtraEncoder(c *gin.Context) {
	card, channel, err := channelPath(c)
	if err != nil {
		fail(c, err)
		return
	}
	value, err := s.hw.ExtraEncoder(c.Request.Context(), hardware.ExtraEncoderCommand{CardNo: card, Channel: channel}, hardware.PriorityControl)
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"card_no": value.CardNo, "channel": value.Channel, "input_mode": value.InputMode, "multiplier": value.Multiplier, "position": value.Position})
}

type extraEncoderCommandBody struct {
	Action     string `json:"action"`
	InputMode  uint16 `json:"input_mode"`
	Multiplier uint16 `json:"multiplier"`
	Position   int32  `json:"position"`
}

// extraEncoderCommand godoc
// @Summary 配置外接编码器或设置位置
// @Tags extra-encoders
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param channel path int true "编码器通道"
// @Param body body extraEncoderCommandBody true "编码器命令参数"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/extra-encoders/{channel} [put]
func (s *Server) extraEncoderCommand(c *gin.Context) {
	card, channel, err := channelPath(c)
	if err != nil {
		fail(c, err)
		return
	}
	var body extraEncoderCommandBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	actions := map[string]uint8{"set_mode": 1, "set_position": 2}
	action, ok := actions[strings.ToLower(body.Action)]
	if !ok {
		fail(c, invalid("action must be set_mode or set_position"))
		return
	}
	cmd := hardware.ExtraEncoderCommand{CardNo: card, Channel: channel, Action: action, InputMode: body.InputMode, Multiplier: body.Multiplier, Position: body.Position}
	if _, err := s.hw.ExtraEncoder(c.Request.Context(), cmd, hardware.PriorityControl); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"sdk_code": 0})
}

func validAxisConfig(row *store.AxisConfig) error {
	if row.Resolution < 1 || row.Resolution > 40 {
		return invalid("resolution must be between 1 and 40")
	}
	if row.ReductionRatio <= 0 {
		return invalid("reduction_ratio must be positive")
	}
	if row.MoveLock != 0 && row.MoveLock != 1 {
		return invalid("move_lock must be 0 or 1")
	}
	if row.ErrBand < 0 || row.HoldTime < 0 {
		return invalid("err_band and hold_time cannot be negative")
	}
	return nil
}

// listAxisConfigs godoc
// @Summary 列出轴配置
// @Tags axis-configs
// @Produce json
// @Success 200 {object} envelope
// @Router /axis-configs [get]
func (s *Server) listAxisConfigs(c *gin.Context) {
	rows, err := s.db.ListAxisConfigs()
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, rows)
}

// createAxisConfig godoc
// @Summary 新建轴配置
// @Tags axis-configs
// @Accept json
// @Produce json
// @Param body body store.AxisConfig true "轴配置"
// @Success 201 {object} envelope
// @Failure 400 {object} envelope
// @Router /axis-configs [post]
func (s *Server) createAxisConfig(c *gin.Context) {
	var row store.AxisConfig
	if err := bindJSON(c, &row); err != nil {
		fail(c, err)
		return
	}
	if err := validAxisConfig(&row); err != nil {
		fail(c, err)
		return
	}
	row.ID = 0
	if err := s.db.CreateAxisConfig(&row); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusCreated, row)
}

// getAxisConfig godoc
// @Summary 读取轴配置
// @Tags axis-configs
// @Produce json
// @Param card path int true "控制卡编号"
// @Param axis path int true "轴编号"
// @Success 200 {object} envelope
// @Failure 404 {object} envelope
// @Router /axis-configs/{card}/{axis} [get]
func (s *Server) getAxisConfig(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	axis, err := parseUint(c, "axis", 16)
	if err != nil {
		fail(c, err)
		return
	}
	row, err := s.db.GetAxisConfig(card, uint16(axis))
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, row)
}

// putAxisConfig godoc
// @Summary 新建或更新轴配置
// @Tags axis-configs
// @Accept json
// @Produce json
// @Param card path int true "控制卡编号"
// @Param axis path int true "轴编号"
// @Param body body store.AxisConfig true "轴配置"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Router /axis-configs/{card}/{axis} [put]
func (s *Server) putAxisConfig(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	axis, err := parseUint(c, "axis", 16)
	if err != nil {
		fail(c, err)
		return
	}
	row, err := s.db.GetAxisConfig(card, uint16(axis))
	if err != nil && !store.IsNotFound(err) {
		fail(c, err)
		return
	}
	if store.IsNotFound(err) {
		row.CardNo, row.Axis = card, uint16(axis)
	}
	rowID := row.ID
	if err := bindJSON(c, &row); err != nil {
		fail(c, err)
		return
	}
	row.ID = rowID
	row.CardNo, row.Axis = card, uint16(axis)
	if err := validAxisConfig(&row); err != nil {
		fail(c, err)
		return
	}
	if err := s.db.SaveAxisConfig(&row); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, row)
}

// deleteAxisConfig godoc
// @Summary 删除轴配置
// @Tags axis-configs
// @Produce json
// @Param card path int true "控制卡编号"
// @Param axis path int true "轴编号"
// @Success 204
// @Failure 404 {object} envelope
// @Router /axis-configs/{card}/{axis} [delete]
func (s *Server) deleteAxisConfig(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	axis, err := parseUint(c, "axis", 16)
	if err != nil {
		fail(c, err)
		return
	}
	if err := s.db.DeleteAxisConfig(card, uint16(axis)); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type busConfigBody struct {
	Name            string `json:"name"`
	CycleTimeMicros uint32 `json:"cycle_time_micros"`
}

// listBusConfigs godoc
// @Summary 列出总线配置
// @Tags bus-configs
// @Produce json
// @Success 200 {object} envelope
// @Router /bus-configs [get]
func (s *Server) listBusConfigs(c *gin.Context) {
	rows, err := s.db.ListBusConfigs()
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, rows)
}

// createBusConfig godoc
// @Summary 新建总线配置元数据
// @Tags bus-configs
// @Accept json
// @Produce json
// @Param body body busConfigBody true "总线配置名称和周期"
// @Success 201 {object} envelope
// @Failure 400 {object} envelope
// @Router /bus-configs [post]
func (s *Server) createBusConfig(c *gin.Context) {
	var body busConfigBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	row, err := makeBusConfig(body)
	if err != nil {
		fail(c, err)
		return
	}
	if err := s.db.CreateBusConfig(&row); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusCreated, row)
}
func makeBusConfig(body busConfigBody) (store.BusConfig, error) {
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len([]rune(body.Name)) > 128 {
		return store.BusConfig{}, invalid("name must contain 1 to 128 characters")
	}
	if body.CycleTimeMicros == 0 {
		body.CycleTimeMicros = 1000
	}
	if body.CycleTimeMicros != 250 && body.CycleTimeMicros != 500 && body.CycleTimeMicros != 1000 && body.CycleTimeMicros != 2000 {
		return store.BusConfig{}, invalid("cycle_time_micros must be 250, 500, 1000 or 2000")
	}
	return store.BusConfig{Name: body.Name, CycleTimeMicros: body.CycleTimeMicros}, nil
}

// getBusConfig godoc
// @Summary 读取总线配置
// @Tags bus-configs
// @Produce json
// @Param id path int true "总线配置 ID"
// @Success 200 {object} envelope
// @Failure 404 {object} envelope
// @Router /bus-configs/{id} [get]
func (s *Server) getBusConfig(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, fmt.Errorf("invalid id: %w", err))
		return
	}
	row, err := s.db.GetBusConfig(uint(id))
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, row)
}

// putBusConfig godoc
// @Summary 更新总线配置元数据
// @Tags bus-configs
// @Accept json
// @Produce json
// @Param id path int true "总线配置 ID"
// @Param body body busConfigBody true "总线配置名称和周期"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Router /bus-configs/{id} [put]
func (s *Server) putBusConfig(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, fmt.Errorf("invalid id: %w", err))
		return
	}
	var row store.BusConfig
	if row, err = s.db.GetBusConfig(uint(id)); err != nil {
		fail(c, err)
		return
	}
	var body busConfigBody
	if err := bindJSON(c, &body); err != nil {
		fail(c, err)
		return
	}
	updated, err := makeBusConfig(body)
	if err != nil {
		fail(c, err)
		return
	}
	row.Name, row.CycleTimeMicros = updated.Name, updated.CycleTimeMicros
	if err := s.db.SaveBusConfig(&row); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, row)
}

// exportBusConfigFile godoc
// @Summary 下载总线配置文件
// @Tags bus-configs
// @Produce application/octet-stream, application/xml, text/plain
// @Param id path int true "总线配置 ID"
// @Param kind path string true "文件类型" Enums(eni, ini)
// @Success 200 {file} file
// @Failure 404 {object} envelope
// @Router /bus-configs/{id}/files/{kind} [get]
func (s *Server) exportBusConfigFile(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, fmt.Errorf("invalid id: %w", err))
		return
	}
	kind := strings.ToLower(c.Param("kind"))
	if kind != "eni" && kind != "ini" {
		fail(c, invalid("file kind must be eni or ini"))
		return
	}
	content, err := s.db.ReadBusConfigFile(uint(id), kind)
	if err != nil {
		fail(c, err)
		return
	}
	contentType := "application/octet-stream"
	if kind == "eni" {
		contentType = "application/xml; charset=utf-8"
	} else {
		contentType = "text/plain; charset=utf-8"
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="bus-config-%d.%s"`, id, kind))
	c.Data(http.StatusOK, contentType, content)
}

// importBusConfigFile godoc
// @Summary 上传总线配置文件
// @Tags bus-configs
// @Accept multipart/form-data
// @Produce json
// @Param id path int true "总线配置 ID"
// @Param kind path string true "文件类型" Enums(eni, ini)
// @Param file formData file true "要上传的 ENI 或 INI 文件"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 404 {object} envelope
// @Router /bus-configs/{id}/files/{kind} [put]
func (s *Server) importBusConfigFile(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, fmt.Errorf("invalid id: %w", err))
		return
	}
	kind := strings.ToLower(c.Param("kind"))
	if kind != "eni" && kind != "ini" {
		fail(c, invalid("file kind must be eni or ini"))
		return
	}
	content, err := readBusConfigUpload(c, kind)
	if err != nil {
		fail(c, err)
		return
	}
	if err := s.db.UploadBusConfigFile(uint(id), kind, content); err != nil {
		fail(c, err)
		return
	}
	row, err := s.db.GetBusConfig(uint(id))
	if err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, row)
}

// deleteBusConfig godoc
// @Summary 删除总线配置及其本地文件
// @Tags bus-configs
// @Produce json
// @Param id path int true "总线配置 ID"
// @Success 204
// @Failure 404 {object} envelope
// @Router /bus-configs/{id} [delete]
func (s *Server) deleteBusConfig(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, fmt.Errorf("invalid id: %w", err))
		return
	}
	if err := s.db.DeleteBusConfig(uint(id)); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// applyBusConfig godoc
// @Summary 应用总线配置到控制卡
// @Tags bus-configs
// @Produce json
// @Param card path int true "控制卡编号"
// @Param id path int true "总线配置 ID"
// @Success 200 {object} envelope
// @Failure 400 {object} envelope
// @Failure 404 {object} envelope
// @Failure 503 {object} envelope
// @Router /cards/{card}/bus-configs/{id}/apply [post]
func (s *Server) applyBusConfig(c *gin.Context) {
	card, err := pathCard(c)
	if err != nil {
		fail(c, err)
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, fmt.Errorf("invalid id: %w", err))
		return
	}
	row, err := s.db.GetBusConfig(uint(id))
	if err != nil {
		fail(c, err)
		return
	}
	if !row.ENIUploaded || !row.INIUploaded {
		fail(c, invalid("upload both ENI and INI files before applying this configuration"))
		return
	}
	eni, ini, err := s.db.ReadBusConfigFiles(row.ID)
	if err != nil {
		fail(c, err)
		return
	}
	if err := s.hw.ApplyBusConfig(c.Request.Context(), card, eni, ini, row.CycleTimeMicros); err != nil {
		fail(c, err)
		return
	}
	respond(c, http.StatusOK, gin.H{"card_no": card, "bus_config_id": row.ID, "applied": true})
}
