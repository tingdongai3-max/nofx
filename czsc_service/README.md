# NoFx CZSC 缠论中间件

为 NoFx 后端提供缠论（笔、线段、中枢、买卖点）预处理，将标签以 JSON 形式注入 AI Prompt，实现「浪浪交易法」依标签决策，减轻 AI 对原始 K 线的幻觉。

## 接口

- `POST /analyze`  
  Body: `{ "symbol": "BTCUSDT", "timeframe": "5m", "klines": [ { "time": 毫秒时间戳, "open", "high", "low", "close", "volume" }, ... ] }`  
  返回: `{ "bi": [...], "xd": [...], "zs": [...], "buy_sell_points": [...], "timeframe": "5m" }`

- `GET /health`  
  健康检查。

## 运行

```bash
pip install -r requirements.txt
# 可选：安装 czsc 以启用笔识别
# pip install czsc -U
# 多 worker 支撑 Go 端并发请求，降低总延迟
uvicorn app:app --host 0.0.0.0 --port 8765 --workers 4
```

- 启动时会对 CZSC 做一次预热，避免首请求加载配置过慢。
- 同 symbol+timeframe 的连续 K 线会走 **update 模式**（热加载），只追加新 bar，不重复全量 init。

NoFx 策略中勾选「缠论 CZSC」并保持默认 `CZSCServiceURL: http://127.0.0.1:8765` 即可使用。

## 扩展

- 未安装 `czsc` 时，接口仍可调用，但返回空的 `bi/xd/zs/buy_sell_points`。
- 安装 `czsc` 后会自动识别**笔**并填入 `bi`。
- **线段、中枢、1/2/3 类买卖点**需在 `run_czsc_analyze` 中按 czsc 文档补充（如使用 `CzscTrader` 或对应 signals）。
