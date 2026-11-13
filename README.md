# nano_ctl

## 目录

- [简介](#简介)
- [安装与运行](#安装与运行)
- [使用](#使用)
- [API 参考](#api-参考)
- [参与开发](#参与开发)
- [许可证](#许可证)

## 简介

为雷赛 PCIe/EtherCAT 控制卡提供 REST/gRPC 后端服务。

项目面向运动控制应用开发者和设备集成人员。

`nano_ctl` 使用 Go 和 cgo 调用雷赛 LTDMC SDK，提供控制卡状态、轴、IO、EtherCAT、PDO/OD、HCMP 等操作接口。REST API 基于 Gin，gRPC API 使用 Protocol Buffers。配套前端可通过 REST API 与本服务交互。

项目代码按以下职责组织：

| 目录 | 内容 |
|---|---|
| `pkg/godmc` | LTDMC cgo 绑定、Go 值类型接口和配套 `libLTDMC.so` |
| `pkg/proto/godmc/v1` | gRPC 的 `.proto` 定义及生成代码 |
| `service` | 控制卡调度、SQLite 存储、IO 变量和服务生命周期 |
| `routers/httpapi` | REST 路由、处理器和 Swagger 文档 |
| `routers/grpcapi` | gRPC 服务实现 |
| `scripts` | 构建、启动及 systemd 配置 |
| `main.go` | 进程入口和依赖装配 |

SQLite 保存轴配置、总线配置元数据和 IO 别名。ENI/INI 内容作为独立文件保存在 SQLite 数据库文件旁的 `bus-configs/` 目录，数据库仅保存相对路径。应用总线配置时，服务将文件读入内存并传给 SDK。

> [!IMPORTANT]
> 服务启动时会初始化 SDK 并尝试打开控制卡。没有可用控制卡或 SDK 初始化失败时，服务不会进入正常监听状态。硬件访问需要兼容的 Linux 环境、随项目提供的匹配 LTDMC 动态库，以及访问 PCIe BAR 所需的权限。

## 安装与运行

### 构建要求

| 项目 | 要求 |
|---|---|
| Go | Go 1.27.3，版本见 `go.mod` |
| cgo | GCC 及可用的 cgo 工具链 |
| 运行环境 | Linux、雷赛 PCIe 控制卡及项目配套的 `libLTDMC.so` |
| 启动脚本 | `scripts/start.sh` 使用 `setpriv` 隔离构建子进程的 capability |

### 手动构建

在项目根目录执行：

```bash
./scripts/build.sh
```

构建脚本生成 Swagger 文档，编译启用 cgo 的程序，并将 `libLTDMC.so` 复制到 `dist/`。构建完成后可运行：

```bash
./dist/nano_ctl
```

也可以使用启动脚本一次完成构建和运行：

```bash
./scripts/start.sh
```

`start.sh` 每次启动都会先重新构建。手动运行时，启动用户必须拥有控制卡所需的硬件访问权限。

### systemd 部署

随仓库提供的 `scripts/nano_ctl@.service` 以指定的普通用户运行服务，并授予服务进程 `CAP_SYS_RAWIO`。启动脚本会从构建子进程中移除该 capability，仅让最终服务进程保留它。

<details>
<summary>安装 systemd 服务模板</summary>

```bash
sudo install -m 0644 scripts/nano_ctl@.service /etc/systemd/system/nano_ctl@.service
sudo install -m 0644 scripts/nano_ctl.env.example /etc/default/nano_ctl
sudoedit /etc/default/nano_ctl
```

将 `NANO_CTL_ROOT` 设置为本机项目目录的绝对路径，例如 `<NANO_CTL_PROJECT_DIR>`。把下面的 `<SERVICE_USER>` 替换为运行服务的 Linux 账号，再启用服务：

```bash
SERVICE_USER="<SERVICE_USER>"
sudo systemctl daemon-reload
sudo systemctl enable --now "nano_ctl@${SERVICE_USER}.service"
```

systemd 模板当前固定使用 `CPUAffinity=2`；部署机器的 CPU 布局不同时，应按实际环境调整模板。

</details>

### 运行配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `NANO_CTL_DB` | `./data/nano_ctl.db` | SQLite 数据库文件路径 |
| `NANO_CTL_HTTP_ADDR` | `:5000` | REST 监听地址 |
| `NANO_CTL_GRPC_ADDR` | `:50051` | gRPC 监听地址 |

> [!WARNING]
> `:5000` 和 `:50051` 默认监听所有网络接口。仅需本机访问时，将地址设为回环接口地址；对外提供服务时，应按部署网络配置访问控制。

## 使用

本项目配套前端用于日常配置和操作。REST 接口的交互文档由 Swagger 提供，默认地址为 `http://127.0.0.1:5000/swagger/index.html`；Swagger JSON 定义位于 `/swagger/doc.json`。

服务启动后会自动打开控制卡。ENI/INI 文件需要单独上传；上传后仍需通过总线配置的 `apply` 接口显式应用，不会因服务启动或导入全量 YAML 而自动下发。

## API 参考

REST API 前缀为 `/api/v1`，成功响应使用 `data` 字段，错误响应使用 `error` 对象。完整请求字段和响应定义请查看 Swagger 文档。

| 资源 | 主要路径 | 能力 |
|---|---|---|
| 健康状态 | `GET /health` | 查询服务与硬件会话状态 |
| 控制卡 | `/cards`、`/cards/{card}` | 卡信息、复位、EtherCAT 周期和 IO 信息 |
| 轴 | `/cards/{card}/axes/...` | 轴状态、批量读取、命令、SSE 状态流和当量 |
| IO | `/cards/{card}/io/...`、`/cards/{card}/io-stream` | 数字量/模拟量读写、数组读取和 SSE 状态流 |
| EtherCAT | `/cards/{card}/bus/...` | 总线状态、命令、对象字典及 RxPDO/TxPDO 操作 |
| HCMP 与外接编码器 | `/cards/{card}/hcmp/...`、`/cards/{card}/extra-encoders/...` | 高速比较和外接编码器状态/配置 |
| 轴配置 | `/axis-configs` | 轴配置的创建、查询、更新和删除 |
| 总线配置 | `/bus-configs` | 总线配置元数据的创建、查询、更新和删除 |
| IO 变量 | `/io-variables` | IO 别名、备注、逻辑反转、禁用状态及变量值 |

### ENI/INI 文件

| 方法 | 路径 | 用途 |
|---|---|---|
| `PUT` | `/api/v1/bus-configs/{id}/files/{kind}` | 上传 ENI 或 INI；`kind` 为 `eni` 或 `ini`，multipart 字段名为 `file` |
| `GET` | `/api/v1/bus-configs/{id}/files/{kind}` | 单独下载 ENI 或 INI 文件 |
| `POST` | `/api/v1/cards/{card}/bus-configs/{id}/apply` | 将本地 ENI/INI 内容下发到控制卡并应用总线配置 |

ENI 文件最大 16 MiB，接受 `.eni` 或 `.xml` 扩展名；INI 文件最大 4 MiB，使用 `.ini` 扩展名。上传的文件保存在数据库旁的本地目录，而非 SQLite blob。

### 全量 YAML 数据

| 方法 | 路径 | 用途 |
|---|---|---|
| `GET` | `/api/v1/data/export` | 导出轴配置、总线配置元数据和 IO 别名 |
| `PUT` | `/api/v1/data/import` | 校验并整体替换上述 SQLite 数据 |

导出文档包含 `exported_at`、`axis_configs`、`bus_configs` 和 `io_aliases`。导入必须提供三个列表；无数据时传空列表。导入采用整体替换，ENI/INI 文件不包含在 YAML 中，也不通过 YAML 上传。

gRPC 服务定义见 [`motion.proto`](pkg/proto/godmc/v1/motion.proto)，提供轴与 IO 读取/流式订阅、硬件命令、总线访问、IO 变量、HCMP 和外接编码器等 RPC。

## 参与开发

- `main.go` 负责进程入口；业务实现分别放在 `pkg/`、`service/` 和 `routers/`。
- 修改 gRPC 接口时编辑 `.proto` 定义并重新生成 Go 代码，不要直接修改生成的 `*.pb.go` 文件。
- REST handler 的 Swagger 注释是接口文档来源；`scripts/build.sh` 会重新生成 `routers/httpapi/docs/` 下的文档文件。

## 许可证

本项目采用 [MIT License](LICENSE)。
