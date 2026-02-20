# 图表截图/多模态视觉与动态指标对齐

## 目的

当实现「K 线图截图或服务端绘图 → 发给多模态 AI 做视觉分析」时，图片上的指标必须与文本数据中的 **DynamicIndicators** 完全一致，避免 AI 看到的是默认 EMA20/50 图，而文本里是用户配置的 EMA200。

## 约定

1. **数据源**：截图/绘图模块必须接收当前交易员的 **策略指标配置**（即 `market.IndicatorParams` 或 `store.IndicatorConfig.Indicators`），与 `market.Get` / `market.GetWithTimeframes` 使用的参数一致。
2. **绘制内容**：在 K 线图上绘制的均线、RSI、MACD 等，必须按该配置生成（例如用户配置了 EMA200、RSI14，则图中只画这些，且周期一致）。
3. **与文本一致**：文本 prompt 中的 `current_ema200`、`current_rsi14` 等来自 `Data.DynamicIndicators`，图中对应曲线/副图应与这些数值对应同一计算逻辑（同一周期、同一数据源）。

## 实现参考

- 后端：`market.IndicatorParams`、`market.Get(symbol, opts)` / `market.GetWithTimeframes(..., opts)`，以及 `kernel.IndicatorParamsFromConfig(store.IndicatorConfig)`。
- 前端：图表组件已支持动态均线与 MACD/RSI 副图，可复用同一套配置（或通过 API 下发策略的指标配置）用于服务端绘图。

## 当前状态

- 文本侧：已通过 DynamicIndicators + IndicatorParams 打通，LLM 收到的为用户自定义指标。
- 截图/绘图模块：仓库内暂无实现；后续新增时请按本文约定读取动态指标配置并绘制。
