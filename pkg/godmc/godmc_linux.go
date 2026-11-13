//go:build linux && amd64

// Package godmc 为 HardwareOrchestrator 使用的雷赛 LTDMC Linux API 提供 cgo 绑定。
package godmc

/*
#cgo linux,amd64 LDFLAGS: -L${SRCDIR} -Wl,-rpath,'$ORIGIN' -lLTDMC
#include <stdlib.h>
#include "ltdmc_abi.h"
*/
import "C"

import (
	"unsafe"
)

// SDK 函数的 short 返回值原样返回。成功码通常为 0；部分读取函数和 BoardInit
// 返回状态值或已初始化卡数，具体语义依对应的雷赛 API 而定。

func cStringPtr(value string) *C.char {
	return C.CString(value)
}

func bytePtr(value []byte) *C.char {
	if len(value) == 0 {
		return nil
	}
	return (*C.char)(unsafe.Pointer(unsafe.SliceData(value)))
}

func uint32Ptr(value []uint32) *C.uint32 {
	if len(value) == 0 {
		return nil
	}
	return (*C.uint32)(unsafe.Pointer(unsafe.SliceData(value)))
}

// SetDebugMode 设置 SDK 日志模式和日志文件名。
func SetDebugMode(mode uint16, fileName string) int16 {
	name := cStringPtr(fileName)
	defer C.free(unsafe.Pointer(name))
	return int16(C.dmc_set_debug_mode(C.WORD(mode), name))
}

// BoardInit 初始化控制卡，并返回 SDK 报告的卡数。
func BoardInit() int16 { return int16(C.dmc_board_init()) }

// BoardClose 关闭控制卡。
func BoardClose() int16 { return int16(C.dmc_board_close()) }

// SoftReset 执行控制卡软复位。
func SoftReset(cardNo uint16) int16 { return int16(C.dmc_soft_reset(C.WORD(cardNo))) }

// CoolReset 执行控制卡冷复位。
func CoolReset(cardNo uint16) int16 { return int16(C.dmc_cool_reset(C.WORD(cardNo))) }

// GetCardInfoList 读取卡信息；缓冲区容量按 Orchestrator 的 8 张卡上限固定。
func GetCardInfoList(cardCount *uint16, cardTypes *[8]uint32, cardIDs *[8]uint16) int16 {
	return int16(C.dmc_get_CardInfList(
		(*C.WORD)(unsafe.Pointer(cardCount)),
		(*C.uint32)(unsafe.Pointer(&cardTypes[0])),
		(*C.WORD)(unsafe.Pointer(&cardIDs[0])),
	))
}

// GetCardVersion 读取控制卡版本。
func GetCardVersion(cardNo uint16, cardVersion *uint32) int16 {
	return int16(C.dmc_get_card_version(C.WORD(cardNo), (*C.uint32)(unsafe.Pointer(cardVersion))))
}

// GetCardSoftVersion 读取卡上固件版本标识。
func GetCardSoftVersion(cardNo uint16, firmwareID, subFirmwareID *uint32) int16 {
	return int16(C.dmc_get_card_soft_version(C.WORD(cardNo), (*C.uint32)(unsafe.Pointer(firmwareID)), (*C.uint32)(unsafe.Pointer(subFirmwareID))))
}

// GetCardLibVersion 读取 LTDMC 动态库版本。
func GetCardLibVersion(libraryVersion *uint32) int16 {
	return int16(C.dmc_get_card_lib_version((*C.uint32)(unsafe.Pointer(libraryVersion))))
}

// GetReleaseVersion 读取控制卡发布版本字符串到调用方提供的缓冲区。
func GetReleaseVersion(connectNo uint16, releaseVersion []byte) int16 {
	return int16(C.dmc_get_release_version(C.WORD(connectNo), bytePtr(releaseVersion)))
}

// GetTotalAxes 读取控制卡轴数。
func GetTotalAxes(cardNo uint16, totalAxis *uint32) int16 {
	return int16(C.dmc_get_total_axes(C.WORD(cardNo), (*C.uint32)(unsafe.Pointer(totalAxis))))
}

// GetTotalIONum 读取控制卡本地 DI/DO 数量。
func GetTotalIONum(cardNo uint16, totalIn, totalOut *uint16) int16 {
	return int16(C.dmc_get_total_ionum(C.WORD(cardNo), (*C.WORD)(unsafe.Pointer(totalIn)), (*C.WORD)(unsafe.Pointer(totalOut))))
}

// GetTotalADCNum 读取本地 AD/DA 通道数。
func GetTotalADCNum(cardNo uint16, totalADIn, totalDAOut *uint16) int16 {
	return int16(C.dmc_get_total_adcnum(C.WORD(cardNo), (*C.WORD)(unsafe.Pointer(totalADIn)), (*C.WORD)(unsafe.Pointer(totalDAOut))))
}

// GetEquiv 读取轴脉冲当量。
func GetEquiv(cardNo, axis uint16, equiv *float64) int16 {
	return int16(C.dmc_get_equiv(C.WORD(cardNo), C.WORD(axis), (*C.double)(unsafe.Pointer(equiv))))
}

// SetEquiv 设置轴脉冲当量。
func SetEquiv(cardNo, axis uint16, equiv float64) int16 {
	return int16(C.dmc_set_equiv(C.WORD(cardNo), C.WORD(axis), C.double(equiv)))
}

// DownloadMemFile 将内存数据下载到控制卡。
func DownloadMemFile(cardNo uint16, data []byte, controlFileName []byte, fileType uint16) int16 {
	return int16(C.dmc_download_memfile(
		C.WORD(cardNo), bytePtr(data), C.uint32(len(data)), bytePtr(controlFileName), C.WORD(fileType),
	))
}

// SetDecStopTime 设置轴减速停止时间。
func SetDecStopTime(cardNo, axis uint16, stopTime float64) int16 {
	return int16(C.dmc_set_dec_stop_time(C.WORD(cardNo), C.WORD(axis), C.double(stopTime)))
}

// SetProfileUnit 设置单轴当量速度曲线。
func SetProfileUnit(cardNo, axis uint16, minVel, maxVel, tacc, tdec, stopVel float64) int16 {
	return int16(C.dmc_set_profile_unit(C.WORD(cardNo), C.WORD(axis), C.double(minVel), C.double(maxVel), C.double(tacc), C.double(tdec), C.double(stopVel)))
}

// SetSProfile 设置单轴平滑速度曲线参数。
func SetSProfile(cardNo, axis, mode uint16, parameter float64) int16 {
	return int16(C.dmc_set_s_profile(C.WORD(cardNo), C.WORD(axis), C.WORD(mode), C.double(parameter)))
}

// PmoveUnit 执行单轴当量定长运动。
func PmoveUnit(cardNo, axis uint16, distance float64, positionMode uint16) int16 {
	return int16(C.dmc_pmove_unit(C.WORD(cardNo), C.WORD(axis), C.double(distance), C.WORD(positionMode)))
}

// ResetTargetPosition 修改脉冲单位运动的目标位置。
func ResetTargetPosition(cardNo, axis uint16, distance int32, positionMode uint16) int16 {
	return int16(C.dmc_reset_target_position(C.WORD(cardNo), C.WORD(axis), C.int32(distance), C.WORD(positionMode)))
}

// ResetTargetPositionUnit 修改当量单位运动的目标位置。
func ResetTargetPositionUnit(cardNo, axis uint16, newPosition float64) int16 {
	return int16(C.dmc_reset_target_position_unit(C.WORD(cardNo), C.WORD(axis), C.double(newPosition)))
}

// ChangeSpeed 在线修改脉冲单位运动速度。
func ChangeSpeed(cardNo, axis uint16, currentVelocity, taccdec float64) int16 {
	return int16(C.dmc_change_speed(C.WORD(cardNo), C.WORD(axis), C.double(currentVelocity), C.double(taccdec)))
}

// ChangeSpeedUnit 在线修改当量单位运动速度。
func ChangeSpeedUnit(cardNo, axis uint16, newVelocity, taccdec float64) int16 {
	return int16(C.dmc_change_speed_unit(C.WORD(cardNo), C.WORD(axis), C.double(newVelocity), C.double(taccdec)))
}

// Vmove 执行单轴连续速度运动。
func Vmove(cardNo, axis, direction uint16) int16 {
	return int16(C.dmc_vmove(C.WORD(cardNo), C.WORD(axis), C.WORD(direction)))
}

// GetHomeResult 读取回零执行状态。
func GetHomeResult(cardNo, axis uint16, state *uint16) int16 {
	return int16(C.dmc_get_home_result(C.WORD(cardNo), C.WORD(axis), (*C.WORD)(unsafe.Pointer(state))))
}

// HcmpSetMode 设置高速比较模式。
func HcmpSetMode(cardNo, channel, mode uint16) int16 {
	return int16(C.dmc_hcmp_set_mode(C.WORD(cardNo), C.WORD(channel), C.WORD(mode)))
}

// HcmpGetMode 读取高速比较模式。
func HcmpGetMode(cardNo, channel uint16, mode *uint16) int16 {
	return int16(C.dmc_hcmp_get_mode(C.WORD(cardNo), C.WORD(channel), (*C.WORD)(unsafe.Pointer(mode))))
}

// HcmpSetConfig 设置高速比较参数。
func HcmpSetConfig(cardNo, channel, axis, source, logic uint16, time int32) int16 {
	return int16(C.dmc_hcmp_set_config(C.WORD(cardNo), C.WORD(channel), C.WORD(axis), C.WORD(source), C.WORD(logic), C.int32(time)))
}

// HcmpGetConfig 读取高速比较参数。
func HcmpGetConfig(cardNo, channel uint16, axis, source, logic *uint16, time *int32) int16 {
	return int16(C.dmc_hcmp_get_config(C.WORD(cardNo), C.WORD(channel), (*C.WORD)(unsafe.Pointer(axis)), (*C.WORD)(unsafe.Pointer(source)), (*C.WORD)(unsafe.Pointer(logic)), (*C.int32)(unsafe.Pointer(time))))
}

// HcmpAddPoint 向高速比较队列添加位置。
func HcmpAddPoint(cardNo, channel uint16, position int32) int16 {
	return int16(C.dmc_hcmp_add_point(C.WORD(cardNo), C.WORD(channel), C.int32(position)))
}

// HcmpSetLiner 设置高速比较线性模式参数。
func HcmpSetLiner(cardNo, channel uint16, increment, count int32) int16 {
	return int16(C.dmc_hcmp_set_liner(C.WORD(cardNo), C.WORD(channel), C.int32(increment), C.int32(count)))
}

// HcmpGetLiner 读取高速比较线性模式参数。
func HcmpGetLiner(cardNo, channel uint16, increment, count *int32) int16 {
	return int16(C.dmc_hcmp_get_liner(C.WORD(cardNo), C.WORD(channel), (*C.int32)(unsafe.Pointer(increment)), (*C.int32)(unsafe.Pointer(count))))
}

// HcmpGetCurrentState 读取高速比较队列状态。
func HcmpGetCurrentState(cardNo, channel uint16, remainingPoints, currentPoint, runPoints *int32) int16 {
	return int16(C.dmc_hcmp_get_current_state(C.WORD(cardNo), C.WORD(channel), (*C.int32)(unsafe.Pointer(remainingPoints)), (*C.int32)(unsafe.Pointer(currentPoint)), (*C.int32)(unsafe.Pointer(runPoints))))
}

// HcmpClearPoints 清空高速比较位置队列。
func HcmpClearPoints(cardNo, channel uint16) int16 {
	return int16(C.dmc_hcmp_clear_points(C.WORD(cardNo), C.WORD(channel)))
}

// ReadInBit 读取单个输入位；返回 SDK 原始状态值。
func ReadInBit(cardNo, bitNo uint16) int16 {
	return int16(C.dmc_read_inbit(C.WORD(cardNo), C.WORD(bitNo)))
}

// WriteOutBit 设置单个输出位。
func WriteOutBit(cardNo, bitNo, onOff uint16) int16 {
	return int16(C.dmc_write_outbit(C.WORD(cardNo), C.WORD(bitNo), C.WORD(onOff)))
}

// ReadOutBit 读取单个输出位；返回 SDK 原始状态值。
func ReadOutBit(cardNo, bitNo uint16) int16 {
	return int16(C.dmc_read_outbit(C.WORD(cardNo), C.WORD(bitNo)))
}

// ReadInPort 读取输入端口值。
func ReadInPort(cardNo, portNo uint16) uint32 {
	return uint32(C.dmc_read_inport(C.WORD(cardNo), C.WORD(portNo)))
}

// ReadOutPort 读取输出端口值。
func ReadOutPort(cardNo, portNo uint16) uint32 {
	return uint32(C.dmc_read_outport(C.WORD(cardNo), C.WORD(portNo)))
}

// WriteOutPort 设置输出端口值。
func WriteOutPort(cardNo, portNo uint16, value uint32) int16 {
	return int16(C.dmc_write_outport(C.WORD(cardNo), C.WORD(portNo), C.uint32(value)))
}

// ReverseOutBit 设置输出位反转时间。
func ReverseOutBit(cardNo, bitNo uint16, reverseTime float64) int16 {
	return int16(C.dmc_reverse_outbit(C.WORD(cardNo), C.WORD(bitNo), C.double(reverseTime)))
}

// GetEncoderUnit 读取当量单位编码器位置。
func GetEncoderUnit(cardNo, axis uint16, position *float64) int16 {
	return int16(C.dmc_get_encoder_unit(C.WORD(cardNo), C.WORD(axis), (*C.double)(unsafe.Pointer(position))))
}

// GetADInput 读取本地模拟输入通道电压。
func GetADInput(cardNo, channel uint16, voltage *float64) int16 {
	return int16(C.dmc_get_ad_input(C.WORD(cardNo), C.WORD(channel), (*C.double)(unsafe.Pointer(voltage))))
}

// GetDAOutput 读取本地模拟输出通道值。
func GetDAOutput(cardNo, channel uint16, voltage *float64) int16 {
	return int16(C.dmc_get_da_output(C.WORD(cardNo), C.WORD(channel), (*C.double)(unsafe.Pointer(voltage))))
}

// SetExtraEncoderMode 设置额外编码器模式。
func SetExtraEncoderMode(cardNo, channel, inputMode, multiplier uint16) int16 {
	return int16(C.dmc_set_extra_encoder_mode(C.WORD(cardNo), C.WORD(channel), C.WORD(inputMode), C.WORD(multiplier)))
}

// GetExtraEncoderMode 读取额外编码器模式。
func GetExtraEncoderMode(cardNo, channel uint16, inputMode, multiplier *uint16) int16 {
	return int16(C.dmc_get_extra_encoder_mode(C.WORD(cardNo), C.WORD(channel), (*C.WORD)(unsafe.Pointer(inputMode)), (*C.WORD)(unsafe.Pointer(multiplier))))
}

// SetExtraEncoder 设置额外编码器位置。
func SetExtraEncoder(cardNo, channel uint16, position int32) int16 {
	return int16(C.dmc_set_extra_encoder(C.WORD(cardNo), C.WORD(channel), C.int32(position)))
}

// GetExtraEncoder 读取额外编码器位置。
func GetExtraEncoder(cardNo, channel uint16, position *int32) int16 {
	return int16(C.dmc_get_extra_encoder(C.WORD(cardNo), C.WORD(channel), (*C.int32)(unsafe.Pointer(position))))
}

// SetPositionUnit 设置当量单位的轴位置。
func SetPositionUnit(cardNo, axis uint16, position float64) int16 {
	return int16(C.dmc_set_position_unit(C.WORD(cardNo), C.WORD(axis), C.double(position)))
}

// ReadCurrentSpeedUnit 读取当量单位的当前速度。
func ReadCurrentSpeedUnit(cardNo, axis uint16, currentSpeed *float64) int16 {
	return int16(C.dmc_read_current_speed_unit(C.WORD(cardNo), C.WORD(axis), (*C.double)(unsafe.Pointer(currentSpeed))))
}

// GetTargetPosition 读取轴目标位置。
func GetTargetPosition(cardNo, axis uint16) int32 {
	return int32(C.dmc_get_target_position(C.WORD(cardNo), C.WORD(axis)))
}

// CheckDone 读取轴运动完成状态；返回 SDK 原始状态值。
func CheckDone(cardNo, axis uint16) int16 {
	return int16(C.dmc_check_done(C.WORD(cardNo), C.WORD(axis)))
}

// AxisIOStatus 读取轴相关 IO 状态位。
func AxisIOStatus(cardNo, axis uint16) uint32 {
	return uint32(C.dmc_axis_io_status(C.WORD(cardNo), C.WORD(axis)))
}

// Stop 停止单轴运动。
func Stop(cardNo, axis, stopMode uint16) int16 {
	return int16(C.dmc_stop(C.WORD(cardNo), C.WORD(axis), C.WORD(stopMode)))
}

// GetStopReason 读取单轴停止原因。
func GetStopReason(cardNo, axis uint16, stopReason *int32) int16 {
	return int16(C.dmc_get_stop_reason(C.WORD(cardNo), C.WORD(axis), (*C.int32)(unsafe.Pointer(stopReason))))
}

// ClearStopReason 清除单轴停止原因。
func ClearStopReason(cardNo, axis uint16) int16 {
	return int16(C.dmc_clear_stop_reason(C.WORD(cardNo), C.WORD(axis)))
}

// NmcGetTotalAxes 读取总线卡轴数。
func NmcGetTotalAxes(cardNo uint16, totalAxis *uint32) int16 {
	return int16(C.nmc_get_total_axes(C.WORD(cardNo), (*C.uint32)(unsafe.Pointer(totalAxis))))
}

// NmcGetTotalIONum 读取总线远程 IO 数量。
func NmcGetTotalIONum(cardNo uint16, totalIn, totalOut *uint16) int16 {
	return int16(C.nmc_get_total_ionum(C.WORD(cardNo), (*C.WORD)(unsafe.Pointer(totalIn)), (*C.WORD)(unsafe.Pointer(totalOut))))
}

// NmcSetCycleTime 设置现场总线周期时间。
func NmcSetCycleTime(cardNo, portNum uint16, cycleTime uint32) int16 {
	return int16(C.nmc_set_cycletime(C.WORD(cardNo), C.WORD(portNum), C.uint32(cycleTime)))
}

// NmcGetCycleTime 读取现场总线周期时间，单位为微秒。
func NmcGetCycleTime(cardNo, portNum uint16, cycleTime *int32) int16 {
	return int16(C.nmc_get_cycletime(C.WORD(cardNo), C.WORD(portNum), (*C.int32)(unsafe.Pointer(cycleTime))))
}

// NmcGetConsumeTimeFieldbus 读取总线平均/最大周期耗时和累计周期数。
func NmcGetConsumeTimeFieldbus(cardNo, portNum uint16, average, maximum *uint32, cycles *uint64) int16 {
	return int16(C.nmc_get_consume_time_fieldbus(C.WORD(cardNo), C.WORD(portNum),
		(*C.uint32)(unsafe.Pointer(average)), (*C.uint32)(unsafe.Pointer(maximum)),
		(*C.uint64_t)(unsafe.Pointer(cycles))))
}

// NmcGetNodeOD 读取从站对象字典值。
func NmcGetNodeOD(cardNo, portNum, nodeNum, index, subIndex, valueLength uint16, value *int32) int16 {
	return int16(C.nmc_get_node_od(C.WORD(cardNo), C.WORD(portNum), C.WORD(nodeNum), C.WORD(index), C.WORD(subIndex), C.WORD(valueLength), (*C.int32)(unsafe.Pointer(value))))
}

// NmcSetNodeOD 写入从站对象字典值。
func NmcSetNodeOD(cardNo, portNum, nodeNum, index, subIndex, valueLength uint16, value int32) int16 {
	return int16(C.nmc_set_node_od(C.WORD(cardNo), C.WORD(portNum), C.WORD(nodeNum), C.WORD(index), C.WORD(subIndex), C.WORD(valueLength), C.int32(value)))
}

// NmcGetAxisStateMachine 读取总线轴状态机状态。
func NmcGetAxisStateMachine(cardNo, axis uint16, stateMachine *uint16) int16 {
	return int16(C.nmc_get_axis_state_machine(C.WORD(cardNo), C.WORD(axis), (*C.WORD)(unsafe.Pointer(stateMachine))))
}

// NmcSetAxisEnable 使能总线轴。
func NmcSetAxisEnable(cardNo, axis uint16) int16 {
	return int16(C.nmc_set_axis_enable(C.WORD(cardNo), C.WORD(axis)))
}

// NmcSetAxisDisable 禁用总线轴。
func NmcSetAxisDisable(cardNo, axis uint16) int16 {
	return int16(C.nmc_set_axis_disable(C.WORD(cardNo), C.WORD(axis)))
}

// NmcGetTotalSlaves 读取 EtherCAT 从站数。
func NmcGetTotalSlaves(cardNo, portNum uint16, totalSlaves *uint16) int16 {
	return int16(C.nmc_get_total_slaves(C.WORD(cardNo), C.WORD(portNum), (*C.WORD)(unsafe.Pointer(totalSlaves))))
}

// NmcSetHomeProfile 设置总线轴回零参数。
func NmcSetHomeProfile(cardNo, axis, homeMode uint16, lowVel, highVel, tacc, tdec, offsetPos float64) int16 {
	return int16(C.nmc_set_home_profile(C.WORD(cardNo), C.WORD(axis), C.WORD(homeMode), C.double(lowVel), C.double(highVel), C.double(tacc), C.double(tdec), C.double(offsetPos)))
}

// NmcHomeMove 启动总线轴回零。
func NmcHomeMove(cardNo, axis uint16) int16 {
	return int16(C.nmc_home_move(C.WORD(cardNo), C.WORD(axis)))
}

// NmcStopEtc 停止 EtherCAT 通讯，并读取停止状态。
func NmcStopEtc(cardNo uint16, ethercatState *uint16) int16 {
	return int16(C.nmc_stop_etc(C.WORD(cardNo), (*C.WORD)(unsafe.Pointer(ethercatState))))
}

// NmcGetErrCode 读取 EtherCAT 通讯错误码。
func NmcGetErrCode(cardNo, channel uint16, errorCode *uint16) int16 {
	return int16(C.nmc_get_errcode(C.WORD(cardNo), C.WORD(channel), (*C.WORD)(unsafe.Pointer(errorCode))))
}

// NmcClearErrCode 清除 EtherCAT 通讯错误码。
func NmcClearErrCode(cardNo, channel uint16) int16 {
	return int16(C.nmc_clear_errcode(C.WORD(cardNo), C.WORD(channel)))
}

// NmcGetAxisErrCode 读取总线轴错误码。
func NmcGetAxisErrCode(cardNo, axis uint16, errorCode *uint16) int16 {
	return int16(C.nmc_get_axis_errcode(C.WORD(cardNo), C.WORD(axis), (*C.WORD)(unsafe.Pointer(errorCode))))
}

// NmcClearAxisErrCode 清除总线轴错误码。
func NmcClearAxisErrCode(cardNo, axis uint16) int16 {
	return int16(C.nmc_clear_axis_errcode(C.WORD(cardNo), C.WORD(axis)))
}

// NmcWriteRxPDOExtra 写入从站 RxPDO 扩展数据。
func NmcWriteRxPDOExtra(cardNo, portNum, address, dataLen uint16, value int32) int16 {
	return int16(C.nmc_write_rxpdo_extra(C.WORD(cardNo), C.WORD(portNum), C.WORD(address), C.WORD(dataLen), C.int32(value)))
}

// NmcReadRxPDOExtra 读取从站 RxPDO 扩展数据。
func NmcReadRxPDOExtra(cardNo, portNum, address, dataLen uint16, value *int32) int16 {
	return int16(C.nmc_read_rxpdo_extra(C.WORD(cardNo), C.WORD(portNum), C.WORD(address), C.WORD(dataLen), (*C.int32)(unsafe.Pointer(value))))
}

// NmcReadTxPDOExtra 读取从站 TxPDO 扩展数据。
func NmcReadTxPDOExtra(cardNo, portNum, address, dataLen uint16, value *int32) int16 {
	return int16(C.nmc_read_txpdo_extra(C.WORD(cardNo), C.WORD(portNum), C.WORD(address), C.WORD(dataLen), (*C.int32)(unsafe.Pointer(value))))
}

// NmcSetOffsetPos 设置总线轴位置偏置。
func NmcSetOffsetPos(cardNo, axis uint16, offsetPos float64) int16 {
	return int16(C.nmc_set_offset_pos(C.WORD(cardNo), C.WORD(axis), C.double(offsetPos)))
}

// NmcGetOffsetPos 读取总线轴位置偏置。
func NmcGetOffsetPos(cardNo, axis uint16, offsetPos *float64) int16 {
	return int16(C.nmc_get_offset_pos(C.WORD(cardNo), C.WORD(axis), (*C.double)(unsafe.Pointer(offsetPos))))
}

// NmcGetTorque 读取总线轴转矩。
func NmcGetTorque(cardNo, axis uint16, torque *int32) int16 {
	return int16(C.nmc_get_torque(C.WORD(cardNo), C.WORD(axis), (*C.int32)(unsafe.Pointer(torque))))
}

// ReadInPortArray 读取输入端口状态数组；status 的容量应与 Orchestrator 请求的长度一致。
func ReadInPortArray(cardNo, portNum uint16, status []uint32) int16 {
	return int16(C.dmc_read_inport_array(C.WORD(cardNo), C.WORD(portNum), uint32Ptr(status)))
}

// ReadOutPortArray 读取输出端口状态数组；status 的容量应与 Orchestrator 请求的长度一致。
func ReadOutPortArray(cardNo, portNum uint16, status []uint32) int16 {
	return int16(C.dmc_read_outport_array(C.WORD(cardNo), C.WORD(portNum), uint32Ptr(status)))
}
