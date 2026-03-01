# OpenClaw 本地部署说明（Windows）

## 环境要求

- **Node.js ≥ 22**（你当前为 v24.13.1，已满足）
- Windows 10 (1903+) 或 Windows 11
- 至少一个 AI 提供商 API Key：Anthropic / OpenAI / Google，或本地 Ollama

---

## 方式一：npm 全局安装（你已执行）

若已在终端执行过：

```powershell
npm install -g openclaw@latest
```

安装可能需 3–5 分钟。完成后**新开一个 PowerShell**，执行：

```powershell
openclaw --version
```

若提示「无法将 openclaw 识别为 cmdlet」，请把 npm 全局目录加入 PATH：

```powershell
npm config get prefix
# 将输出路径下的 \bin 加入系统/用户 PATH，例如：%AppData%\npm
```

---

## 方式二：官方 PowerShell 一键安装（推荐首次安装）

**以管理员身份**打开 PowerShell，执行：

```powershell
iwr -useb https://openclaw.ai/install.ps1 | iex
```

脚本会检查/安装 Node.js 22+、安装 openclaw，并引导你完成向导。

> 说明：PowerShell 方式适合快速体验；若需长期稳定使用或完整工具链，建议用 WSL2（见方式三）。

---

## 方式三：WSL2 安装（最稳定，适合重度使用）

你已有 WSL2 + Ubuntu，且已启用 systemd，可直接在 WSL 内安装。

### 推荐：在 WSL 终端里手动执行（可输入 sudo 密码）

1. **打开 WSL**：在 Windows 中打开「Ubuntu」或任意 WSL 发行版终端。

2. **一键执行本仓库提供的安装脚本**（会提示输入 sudo 密码）：
   ```bash
   sed -i 's/\r$//' /mnt/c/Users/23268/nofx/nofx/scripts/openclaw-install-wsl.sh
   bash /mnt/c/Users/23268/nofx/nofx/scripts/openclaw-install-wsl.sh
   ```

3. **或按下面步骤手动安装**（若脚本失败可逐条执行）：

   ```bash
   # 安装 Node.js 22（NodeSource）
   sudo apt-get update
   sudo apt-get install -y ca-certificates curl gnupg
   curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
   sudo apt-get install -y nodejs

   # 验证
   node -v   # 应为 v22.x
   npm -v

   # 全局安装 OpenClaw
   npm install -g openclaw@latest

   # 配置向导 + 安装 daemon
   openclaw onboard --install-daemon
   ```

4. **启动网关**（二选一）：
   ```bash
   # 前台运行（调试用）
   openclaw gateway --port 18789 --verbose

   # 或安装为 daemon 后后台运行
   openclaw daemon start
   ```

### 若尚未配置 WSL2

1. **安装 WSL2**（管理员 PowerShell）：
   ```powershell
   wsl --install
   ```
   按提示重启。

2. **启用 systemd**（在 WSL 的 Ubuntu 终端中）：
   ```bash
   sudo nano /etc/wsl.conf
   ```
   写入：
   ```ini
   [boot]
   systemd=true
   ```
   保存后，在 PowerShell 执行：`wsl --shutdown`，再重新打开 WSL。

3. 然后按上面「推荐」中的步骤在 WSL 内安装 OpenClaw。

---

## 安装完成后的步骤

### 1. 安装并配置 Daemon（保持网关常驻）

```powershell
openclaw onboard --install-daemon
```

按向导配置：

- AI 模型提供商与 API Key
- 聊天渠道（如 WhatsApp / Telegram / Discord 等）
- 是否将 Gateway 安装为后台服务

### 2. 启动 Gateway

若未装 daemon，可手动前台启动：

```powershell
openclaw gateway --port 18789 --verbose
```

若已装 daemon，可用：

```powershell
openclaw daemon start
# 查看状态
openclaw daemon status
```

### 3. 验证与常用命令

```powershell
openclaw --version
openclaw doctor
openclaw dashboard
```

### 4. 发送第一条消息（示例）

```powershell
openclaw message send --to +1234567890 --message "Hello from OpenClaw"
```

### 5. 与助手对话（可对接 WhatsApp/Telegram 等）

```powershell
openclaw agent --message "Ship checklist" --thinking high
```

---

## 配置文件位置

- 主配置：`~/.openclaw/openclaw.json`
- 工作区：`~/.openclaw/workspace`（可配置 `agents.defaults.workspace`）

最小配置示例（仅指定模型）：

```json
{
  "agent": {
    "model": "anthropic/claude-opus-4-6"
  }
}
```

---

## 安装太慢 / 国内加速

脚本已内置加速（在 WSL 里运行时会自动）：

- **apt**：替换为阿里云镜像（`mirrors.aliyun.com`），`apt update` / `apt install` 会快很多。
- **npm**：使用 npmmirror（`registry.npmmirror.com`），`npm install -g openclaw` 会明显加速。

若不需要换镜像（例如不在国内），可在执行脚本前加上：

```bash
SKIP_MIRROR=1 bash /mnt/c/Users/23268/nofx/nofx/scripts/openclaw-install-wsl.sh
```

手动加速（在 WSL 里执行一次即可）：

```bash
# apt 换阿里云
sudo sed -i 's|http://archive.ubuntu.com|https://mirrors.aliyun.com|g' /etc/apt/sources.list
# npm 换 npmmirror
npm config set registry https://registry.npmmirror.com
```

---

## 故障排查

| 现象 | 处理 |
|------|------|
| `openclaw` 无法识别 | 将 `npm config get prefix` 所得路径下的 `\bin` 加入 PATH，或使用 `%AppData%\npm` |
| 安装被 Windows Defender 拦截 | 在「病毒和威胁防护」→「排除项」中排除 `%AppData%\npm\node_modules\openclaw` |
| Node 版本过低 | 使用 winget：`winget install OpenJS.NodeJS.LTS` |

更多见：[OpenClaw Windows 安装文档](https://open-claw.bot/docs/install/windows/)、[官方文档](https://docs.openclaw.ai/)。
