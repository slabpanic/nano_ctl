# godmc

`godmc` 是 Linux x86-64 的雷赛 LTDMC Go SDK。建议使用值对象 API：

```go
session, err := godmc.Open()
if err != nil { return err }
defer session.Close()

cards := session.Cards()
if len(cards) == 0 { return errors.New("no motion card") }
axis := cards[0].Axis(0)
state, err := axis.Snapshot()
```

`Session` 负责卡生命周期；`Card`、`Axis`、`Bus`、`IO`、`Comparator` 和 `ExtraEncoder` 返回 Go 值与 `*godmc.SDKError`，不要求业务代码传 SDK 输出指针。原始 cgo 函数仍可用于未封装的调用。

SDK 使用进程级状态且没有明确的线程安全保证。直接调用 `godmc` 的程序应自行同步；`nano_ctl` 后端对只读调用使用有界并行，对写入和生命周期操作使用独占锁。

需要 Linux x86-64、Go、GCC 和匹配的 `libLTDMC.so`。库文件放在本目录；服务构建脚本会把它复制到二进制旁边，运行时由 `$ORIGIN` 找到：

```sh
cd nano_ctl
./scripts/build.sh
```

当前库 SHA-256 为 `439707ba074f396a364a5e9a29ef25869a50b636ac64ae86eecac346a627e67b`。不要换成工控机 `/opt/product/DMC3K5K_linux_20230731/LIB` 下的旧库，该版本缺少 `dmc_read_inport_array` 和 `dmc_read_outport_array`。

实际 PCIe BAR 访问链路和系统恢复清单见 [`PCIe_DEPLOYMENT.md`](../../docs/PCIe_DEPLOYMENT.md)。后端的 REST/gRPC 契约、实时队列和数据库设计见 [`NANO_CTL_BACKEND.md`](../../docs/NANO_CTL_BACKEND.md)。
