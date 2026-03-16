# OpenClaw Integration

`web/src/components/OpenClawWidget.tsx` is the frontend control widget.

Set these Vite env vars before building the frontend:

- `VITE_OPENCLAW_WEBHOOK_URL`: OpenClaw chat or orchestration endpoint.
- `VITE_OPENCLAW_WEBHOOK_TOKEN`: optional bearer token for the webhook.

Widget request payload:

```json
{
  "message": "帮我创建一个新的 Binance 模拟盘交易员",
  "conversation": [{"role": "user", "content": "..." }],
  "tools": [{ "name": "create_trader", "path": "/api/traders", "method": "POST" }],
  "context": { "app": "nofx", "pathname": "/traders", "timestamp": "2026-03-16T00:00:00Z" }
}
```

Expected OpenClaw response payload:

```json
{
  "reply": "我会先创建交易员，再热重载配置。",
  "tool_calls": [
    {
      "tool": "create_trader",
      "arguments": {
        "name": "DeepSeek-Paper-01",
        "ai_model_id": "deepseek",
        "exchange_id": "exchange-uuid",
        "is_dry_run": true,
        "virtual_equity": 10000
      }
    }
  ]
}
```

The widget executes `tool_calls` locally through the authenticated browser session, so OpenClaw does not need direct network access to the NOFX backend if the webhook only returns plans and tool calls.
