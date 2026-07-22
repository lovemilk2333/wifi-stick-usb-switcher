# 项目命名规范

## 基础规则

| 类别 | 规范 | 示例 |
|------|------|------|
| **导出类型/函数** | PascalCase | `NewDaemon`, `UsbGadgetController` |
| **未导出函数** | camelCase | `isValidConfigFs`, `loadLedInterpreters` |
| **私有字段** | snake_case | `input_device`, `current_mode` |
| **方法接收器** | `this` | `func (this *Daemon) Tick()` |
| **常量** | UPPER_SNAKE_CASE | `DEVICE_STATUS_NORMAL`, `PROJECT_IDENT` |
| **包名** | 全小写，单单词 | `usb`, `led`, `input` |

## 详细说明

### 命名风格

- **导出（公开）成员**：首字母大写的驼峰命名 `PascalCase`，如 `NewDevice`、`ClearFunctions`
- **未导出（私有）成员**：首字母小写的驼峰命名 `camelCase`，如 `init()`、`loadLedInterpreters()`
- **结构体字段**：无论是否导出，**私有字段**统一使用下划线命名 `snake_case`
  - 正确：`input_device`、`current_mode`、`tick_rate`
  - 错误：`inputDevice`、`currentMode`、`tickRate`
  - 导出字段仍使用 PascalCase：`DeviceName`、`ConfigPath`
- **方法接收器**统一命名为 `this`
  - 正确：`func (this *Daemon) Tick()`
  - 错误：`func (d *Daemon) Tick()`
- **常量**：全大写加下划线，如 `USB_GADGET_FUNCTION_CODE_RNDIS`

### 包设计

- 每个包职责单一，避免循环依赖
- `core/` 为业务逻辑核心，子包（`usb`、`led`、`input`、`base`）提供具体能力
- `cmd/` 仅做参数解析和入口调用，不包含业务逻辑
