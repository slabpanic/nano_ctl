#ifndef GODMC_LTDMC_ABI_H
#define GODMC_LTDMC_ABI_H

#include <stdint.h>

/* 雷赛 Linux SDK 使用 C 调用约定；类型宽度按 Linux SDK 头文件定义。 */
typedef unsigned short WORD;
typedef uint32_t uint32;
typedef int32_t int32;

#ifdef __cplusplus
extern "C" {
#endif

short dmc_set_debug_mode(WORD mode, const char *file_name);
short dmc_board_init(void);
short dmc_board_close(void);
short dmc_soft_reset(WORD card_no);
short dmc_cool_reset(WORD card_no);
short dmc_get_CardInfList(WORD *card_count, uint32 *card_types, WORD *card_ids);
short dmc_get_card_version(WORD card_no, uint32 *card_version);
short dmc_get_card_soft_version(WORD card_no, uint32 *firmware_id, uint32 *sub_firmware_id);
short dmc_get_card_lib_version(uint32 *library_version);
short dmc_get_release_version(WORD connect_no, char *release_version);
short dmc_get_total_axes(WORD card_no, uint32 *total_axis);
short dmc_get_total_ionum(WORD card_no, WORD *total_in, WORD *total_out);
short dmc_get_total_adcnum(WORD card_no, WORD *total_adc_in, WORD *total_dac_out);
short dmc_get_equiv(WORD card_no, WORD axis, double *equiv);
short dmc_set_equiv(WORD card_no, WORD axis, double equiv);
short dmc_download_memfile(WORD card_no, const char *buffer, uint32 buffer_size,
                           const char *file_name_in_control, WORD file_type);
short dmc_set_dec_stop_time(WORD card_no, WORD axis, double stop_time);
short dmc_set_profile_unit(WORD card_no, WORD axis, double min_vel, double max_vel,
                           double tacc, double tdec, double stop_vel);
short dmc_set_s_profile(WORD card_no, WORD axis, WORD s_mode, double s_para);
short dmc_pmove_unit(WORD card_no, WORD axis, double distance, WORD position_mode);
short dmc_reset_target_position(WORD card_no, WORD axis, int32 distance, WORD position_mode);
short dmc_reset_target_position_unit(WORD card_no, WORD axis, double new_position);
short dmc_change_speed(WORD card_no, WORD axis, double current_velocity, double taccdec);
short dmc_change_speed_unit(WORD card_no, WORD axis, double new_velocity, double taccdec);
short dmc_vmove(WORD card_no, WORD axis, WORD direction);
short dmc_get_home_result(WORD card_no, WORD axis, WORD *state);
short dmc_hcmp_set_mode(WORD card_no, WORD hcmp, WORD mode);
short dmc_hcmp_get_mode(WORD card_no, WORD hcmp, WORD *mode);
short dmc_hcmp_set_config(WORD card_no, WORD hcmp, WORD axis, WORD source,
                          WORD logic, int32 time);
short dmc_hcmp_get_config(WORD card_no, WORD hcmp, WORD *axis, WORD *source,
                          WORD *logic, int32 *time);
short dmc_hcmp_add_point(WORD card_no, WORD hcmp, int32 position);
short dmc_hcmp_set_liner(WORD card_no, WORD hcmp, int32 increment, int32 count);
short dmc_hcmp_get_liner(WORD card_no, WORD hcmp, int32 *increment, int32 *count);
short dmc_hcmp_get_current_state(WORD card_no, WORD hcmp, int32 *remaining_points,
                                 int32 *current_point, int32 *run_points);
short dmc_hcmp_clear_points(WORD card_no, WORD hcmp);
short dmc_read_inbit(WORD card_no, WORD bit_no);
short dmc_write_outbit(WORD card_no, WORD bit_no, WORD on_off);
short dmc_read_outbit(WORD card_no, WORD bit_no);
uint32 dmc_read_inport(WORD card_no, WORD port_no);
uint32 dmc_read_outport(WORD card_no, WORD port_no);
short dmc_write_outport(WORD card_no, WORD port_no, uint32 value);
short dmc_reverse_outbit(WORD card_no, WORD bit_no, double reverse_time);
short dmc_get_encoder_unit(WORD card_no, WORD axis, double *position);
short dmc_get_ad_input(WORD card_no, WORD channel, double *voltage);
short dmc_get_da_output(WORD card_no, WORD channel, double *voltage);
short dmc_set_extra_encoder_mode(WORD card_no, WORD channel, WORD input_mode, WORD multiplier);
short dmc_get_extra_encoder_mode(WORD card_no, WORD channel, WORD *input_mode, WORD *multiplier);
short dmc_set_extra_encoder(WORD card_no, WORD channel, int32 position);
short dmc_get_extra_encoder(WORD card_no, WORD channel, int32 *position);
short dmc_set_position_unit(WORD card_no, WORD axis, double position);
short dmc_read_current_speed_unit(WORD card_no, WORD axis, double *current_speed);
int32 dmc_get_target_position(WORD card_no, WORD axis);
short dmc_check_done(WORD card_no, WORD axis);
uint32 dmc_axis_io_status(WORD card_no, WORD axis);
short dmc_stop(WORD card_no, WORD axis, WORD stop_mode);
short dmc_get_stop_reason(WORD card_no, WORD axis, int32 *stop_reason);
short dmc_clear_stop_reason(WORD card_no, WORD axis);
short nmc_get_total_axes(WORD card_no, uint32 *total_axis);
short nmc_get_total_ionum(WORD card_no, WORD *total_in, WORD *total_out);
short nmc_set_cycletime(WORD card_no, WORD port_num, uint32 cycle_time);
short nmc_get_cycletime(WORD card_no, WORD port_num, int32 *cycle_time);
short nmc_get_consume_time_fieldbus(WORD card_no, WORD port_num, uint32 *average_time,
                                    uint32 *max_time, uint64_t *cycles);
short nmc_get_node_od(WORD card_no, WORD port_num, WORD node_num, WORD index,
                      WORD sub_index, WORD value_length, int32 *value);
short nmc_set_node_od(WORD card_no, WORD port_num, WORD node_num, WORD index,
                      WORD sub_index, WORD value_length, int32 value);
short nmc_get_axis_state_machine(WORD card_no, WORD axis, WORD *state_machine);
short nmc_set_axis_enable(WORD card_no, WORD axis);
short nmc_set_axis_disable(WORD card_no, WORD axis);
short nmc_get_total_slaves(WORD card_no, WORD port_num, WORD *total_slaves);
short nmc_set_home_profile(WORD card_no, WORD axis, WORD home_mode, double low_vel,
                           double high_vel, double tacc, double tdec, double offset_pos);
short nmc_home_move(WORD card_no, WORD axis);
short nmc_stop_etc(WORD card_no, WORD *ethercat_state);
short nmc_get_errcode(WORD card_no, WORD channel, WORD *error_code);
short nmc_clear_errcode(WORD card_no, WORD channel);
short nmc_get_axis_errcode(WORD card_no, WORD axis, WORD *error_code);
short nmc_clear_axis_errcode(WORD card_no, WORD axis);
short nmc_write_rxpdo_extra(WORD card_no, WORD port_num, WORD address, WORD data_len,
                            int32 value);
short nmc_read_rxpdo_extra(WORD card_no, WORD port_num, WORD address, WORD data_len,
                           int32 *value);
short nmc_read_txpdo_extra(WORD card_no, WORD port_num, WORD address, WORD data_len,
                           int32 *value);
short nmc_set_offset_pos(WORD card_no, WORD axis, double offset_pos);
short nmc_get_offset_pos(WORD card_no, WORD axis, double *offset_pos);
short nmc_get_torque(WORD card_no, WORD axis, int32 *torque);

/* 这两个导出由 Orchestrator 随附的 libLTDMC.so 提供，旧版 SDK 头文件未声明。 */
short dmc_read_outport_array(WORD card_no, WORD port_num, uint32 *status);
short dmc_read_inport_array(WORD card_no, WORD port_num, uint32 *status);

#ifdef __cplusplus
}
#endif

#endif
