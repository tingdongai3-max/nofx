# ATR 移动止盈止损（分批止盈）

本文档说明「ATR 止盈止损」功能的规则与实现要点，便于后续维护与排查。

## 功能概述

策略可开启 **ATR 移动止盈止损**（`EnableATRTrailing`）。开仓后不挂交易所固定 TP/SL，由机器狗根据实时价格与 ATR 倍数监控，到价后执行止损或**分批止盈**。

- **止损**：`entry ± atr_sl_mult × ATR`，触发则平掉全部剩余仓位。
- **分批止盈**：最多 3 档，每档为「达到 atr_mult × ATR 时平掉开仓总仓位的 close_pct%」。

## 分批止盈规则（重要）

以下规则由代码强制保证，避免误用或 AI 改参数导致重复触发。

### 1. 每档只触发一次

- 每个百分比档位（如第一档 30%、第二档 30%、第三档 40%）**只触发一次**。
- 状态中用 `TriggeredStage[0..2]` 记录各档是否已触发；触发后置为 true，后续不再判断该档。
- **更新策略时**（例如 AI 把第一目标从 1.5× 改成 1.2×）**不会重置** `TriggeredStage`，因此已触发的档位不会因为倍数调低而再次触发。

### 2. 平仓量按「开仓总仓位」的百分比

- 每档平仓量 = **开仓总仓位（OriginalQty）× (close_pct / 100)**，不是「当前剩余仓位」的百分比。
- 示例：总仓位 1.0 BTC，三档 30% / 30% / 40%。第一档平 0.3 BTC，第二档仍平 **0.3 BTC**（总仓位的 30%），不是剩余 0.7 的 30%。

实现上：

- 开仓或新建 ATR 状态时写入 `OriginalQty = 开仓数量`。
- 更新已有状态时**不覆盖** `OriginalQty`。
- 机器狗计算：`closeQty = OriginalQty * (closePct/100)`，再与 `CurrentQty` 取 min，避免超平。

### 3. 倍数必须严格递增

- 三档的 `atr_mult` 必须**严格递增**：第二目标 > 第一目标，第三目标 > 第二目标。
- 代码中对 `ATRTrailingTpStages` 做 **normalizeATRStages**：按 `AtrMult` 升序排序，丢弃不满足严格递增的档位，最多保留 3 档。

## 相关代码位置

| 说明 | 文件与要点 |
|------|------------|
| 状态结构（含 OriginalQty、TriggeredStage） | `trader/watchdog.go`：`ATRTrailingState` |
| 每档平仓量按 OriginalQty 比例、TriggeredStage 判断 | `trader/watchdog.go`：`runATRTrailingCycle` |
| 阶段排序与严格递增 | `trader/watchdog.go`：`normalizeATRStages` |
| 开仓时写入 OriginalQty、TpStages 用 normalizeATRStages | `trader/auto_trader.go`：开多/开空 |
| 更新策略时保留 OriginalQty、TriggeredStage，只更新倍数与 stages | `trader/auto_trader.go`：`updateTpSl` |
| 策略配置与 AI 输出结构 | `store/strategy.go`：`EnableATRTrailing`；`kernel/engine.go`：`ATRTrailingSlMult`、`ATRTrailingTpMult`、`ATRTrailingTpStages` |

## 示例配置（AI 输出）

```json
{
  "atr_sl_mult": 1.0,
  "atr_tp_mult": 3.0,
  "atr_tp_stages": [
    { "atr_mult": 1.5, "close_pct": 30 },
    { "atr_mult": 2.2, "close_pct": 30 },
    { "atr_mult": 3.0, "close_pct": 40 }
  ]
}
```

含义：

- 第一目标（1.5× ATR）：平仓 30%（总仓位），锁定利润，止损可移至开仓价。
- 第二目标（2.2× ATR）：再平 30%（总仓位），止损可移至第一目标价。
- 第三目标（3.0× ATR）：平剩余 40%，让利润奔跑。

若 AI 返回的 `atr_mult` 未严格递增，`normalizeATRStages` 会排序并过滤，保证执行时只使用合法三档。
