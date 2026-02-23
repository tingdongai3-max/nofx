# 持久化验证步骤

## 1. 刷新当前终端（使 setx 生效）

**PowerShell（当前会话立即生效）：**
```powershell
$env:DATA_ENCRYPTION_KEY = "cwTXelFAjhYrAbnQRNmtOnJjiGrjG9FABzE82ICgK2k="
```

**或** 关闭并重新打开终端（setx 对新会话生效）。

## 2. 验证加密密钥缺失时会报错

临时重命名 `.env` 并清空 DATA_ENCRYPTION_KEY，运行应用应失败：

```powershell
# 备份并清空 DATA_ENCRYPTION_KEY
(Get-Content .env) -replace '^DATA_ENCRYPTION_KEY=.*','DATA_ENCRYPTION_KEY=' | Set-Content .env.bak
# 运行（应看到 "DATA_ENCRYPTION_KEY must be set" 错误）
go run main.go
# 恢复
Move-Item .env.bak .env -Force
```

## 3. 验证数据库持久化

1. 启动应用：`go run main.go`
2. 在 Web 界面添加一个 API Key 或创建交易员配置
3. 停止应用（Ctrl+C）
4. 再次启动：`go run main.go`
5. 确认之前添加的配置仍然可见、可读

## 4. 目录结构

```
nofx/
├── data/
│   ├── db/           # SQLite 数据库 (data.db)
│   └── config/       # 配置文件目录
├── .env              # DB_PATH=data/db/data.db, DATA_ENCRYPTION_KEY=...
└── ...
```
