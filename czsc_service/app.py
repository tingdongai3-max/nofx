# -*- coding: utf-8 -*-
"""
NoFx CZSC 缠论中间件：接收 K 线 JSON，返回笔/线段/中枢/买卖点标签，供 NoFx 注入 AI Prompt。
支持热加载：同 symbol+timeframe 连续 K 线用 update 模式，减少重复 init。
启动: uvicorn app:app --host 0.0.0.0 --port 8765 --workers 4
"""
from __future__ import annotations

import threading
from datetime import datetime, timedelta
from typing import Any

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

app = FastAPI(title="NoFx CZSC", version="0.1.0")

# 热加载：按 (symbol, timeframe) 缓存 CZSC 实例与最后一条 K 线信息，连续请求用 update 而非全量 init
_czsc_cache: dict[tuple[str, str], dict[str, Any]] = {}
_czsc_cache_lock = threading.Lock()


class KlineBar(BaseModel):
    time: int
    open: float
    high: float
    low: float
    close: float
    volume: float


class AnalyzeRequest(BaseModel):
    symbol: str
    timeframe: str
    klines: list[KlineBar]


# 与 NoFx kernel.CZSCLabels 对齐的响应结构
def empty_labels(timeframe: str) -> dict[str, Any]:
    return {
        "bi": [],
        "xd": [],
        "zs": [],
        "buy_sell_points": [],
        "timeframe": timeframe,
    }


def _klines_to_bars(symbol: str, timeframe: str, klines: list[dict]) -> tuple[list, Any]:
    from czsc.objects import RawBar
    from czsc.analyze import CZSC
    from czsc.enum import Freq

    freq_map = {"1m": Freq.F1, "3m": Freq.F3, "5m": Freq.F5, "15m": Freq.F15, "30m": Freq.F30,
                "1h": Freq.F60, "2h": Freq.F120, "4h": Freq.F240, "1d": Freq.D}
    freq = freq_map.get(timeframe.lower(), Freq.F5)
    bars: list[RawBar] = []
    for i, k in enumerate(klines):
        # Go 侧传入的是毫秒时间戳，这里按本地时区转换为 datetime，避免时区导致的序列错位
        dt = datetime.fromtimestamp(k["time"] / 1000.0)
        bar = RawBar(
            symbol=symbol,
            id=i,
            freq=freq,
            dt=dt,
            open=float(k["open"]),
            close=float(k["close"]),
            high=float(k["high"]),
            low=float(k["low"]),
            vol=float(k.get("volume", 0)),
            amount=0,
        )
        bars.append(bar)
    return bars, freq


def _czsc_to_output(c, timeframe: str) -> dict[str, Any]:
    out = empty_labels(timeframe)
    try:
        # 调试：看 CZSC 实际识别出了多少笔
        print(f"DEBUG: CZSC bi_list length = {len(getattr(c, 'bi_list', []))}")
    except Exception:
        pass
    for bi in c.bi_list:
        start_dt = bi.fx_a.elements[0].dt
        end_dt = bi.fx_b.elements[-1].dt
        direction = "up" if str(bi.direction).lower().startswith("up") else "down"
        out["bi"].append({
            "start_time": int(start_dt.timestamp() * 1000),
            "end_time": int(end_dt.timestamp() * 1000),
            "high": bi.high,
            "low": bi.low,
            "direction": direction,
        })
    return out


def run_czsc_analyze(symbol: str, timeframe: str, klines: list[dict]) -> dict[str, Any]:
    """使用 czsc 库进行缠论分析；解析 K 线时做强制类型转换并显式打印调试信息。"""
    try:
        from czsc.objects import RawBar
        from czsc.analyze import CZSC
        from czsc.enum import Freq
    except ImportError:
        return empty_labels(timeframe)

    try:
        # 强制类型转换，确保所有数值字段为 float，时间为 datetime，freq 为有效周期，id 唯一递增
        bars: list[RawBar] = []
        for i, k in enumerate(klines):
            open_price = float(k["open"])
            high_price = float(k["high"])
            low_price = float(k["low"])
            close_price = float(k["close"])
            vol = float(k.get("volume", 0) or 0.0)
            rb = RawBar(
                symbol=symbol,
                id=i,  # 唯一自增 ID
                dt=datetime.fromtimestamp(float(k["time"]) / 1000.0),
                open=open_price,
                high=high_price,
                low=low_price,
                close=close_price,
                vol=vol,
                freq=Freq.F60,  # 当前约定按 1h 级别分析，必须提供有效 freq
                amount=vol * close_price,  # 简单成交额估算：vol * close
            )
            bars.append(rb)

        if len(bars) < 50:
            print(f"DEBUG: {symbol} bars too few ({len(bars)}), skipping analysis.")
            return empty_labels(timeframe)

        # 初始化分析器
        c = CZSC(bars)

        # 显式打印笔的数量，方便确认 CZSC 是否正常工作
        bi_len = len(c.bi_list) if getattr(c, "bi_list", None) else 0
        print(f"DEBUG: {symbol} analysis done. bi_list length = {bi_len}")

        # 复用统一输出结构，将笔写入 JSON
        out = empty_labels(timeframe)
        for bi in getattr(c, "bi_list", []):
            start_dt = bi.fx_a.elements[0].dt
            end_dt = bi.fx_b.elements[-1].dt
            direction = "up" if str(bi.direction).lower().startswith("up") else "down"
            out["bi"].append({
                "start_time": int(start_dt.timestamp() * 1000),
                "end_time": int(end_dt.timestamp() * 1000),
                "high": bi.high,
                "low": bi.low,
                "direction": direction,
            })
        return out
    except Exception as e:
        # 任何错误都打印出来，避免“静默失败 + 空结果”
        print(f"CRITICAL ERROR during analysis for {symbol}: {str(e)}")
        try:
            import traceback
            traceback.print_exc()
        except Exception:
            pass
        return empty_labels(timeframe)


@app.post("/analyze")
def analyze(req: AnalyzeRequest) -> dict[str, Any]:
    # Debug: 检查 Go 侧发送的 K 线数量与字段形态
    try:
        print(f"DEBUG: Symbol {req.symbol} sent {len(req.klines)} bars.")
        if req.klines:
            first = req.klines[0]
            first_dict = first.model_dump()
            print(f"DEBUG: First bar keys: {list(first_dict.keys())}")
            print(f"DEBUG: First bar time: {first_dict.get('time')}")
    except Exception as debug_err:
        print(f"DEBUG: analyze inspect error: {debug_err}")

    if len(req.klines) < 20:
        raise HTTPException(status_code=400, detail="Need at least 20 klines")
    klist = [k.model_dump() for k in req.klines]
    try:
        return run_czsc_analyze(req.symbol, req.timeframe, klist)
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.on_event("startup")
def warmup_czsc() -> None:
    """预热：首次初始化 CZSC 会加载配置/策略，用 dummy 数据跑一遍避免首请求慢."""
    try:
        from czsc.analyze import CZSC
        from czsc.objects import RawBar
        from czsc.enum import Freq
        base = datetime.utcnow().replace(second=0, microsecond=0)
        bars = []
        for i in range(30):
            dt = base + timedelta(minutes=i * 5)
            bars.append(RawBar(symbol="WARMUP", id=i, freq=Freq.F5, dt=dt, open=100.0, close=100.0, high=100.0, low=100.0, vol=0.0, amount=0))
        CZSC(bars)
    except Exception:
        pass


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": "nofx-czsc"}
