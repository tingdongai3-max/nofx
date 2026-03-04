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
        dt = datetime.utcfromtimestamp(k["time"] / 1000.0)
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
    """使用 czsc 库进行缠论分析；连续 K 线时优先 update 模式（热加载），否则全量 init。"""
    try:
        from czsc.objects import RawBar
        from czsc.analyze import CZSC
        from czsc.enum import Freq
    except ImportError:
        return empty_labels(timeframe)

    if len(klines) < 20:
        return empty_labels(timeframe)

    try:
        key = (symbol, timeframe)
        with _czsc_cache_lock:
            entry = _czsc_cache.get(key)
            # 连续扩展：新 K 线前 n 条与缓存一致，仅追加新 bar 并 update
            if entry is not None:
                n_bars = entry["n_bars"]
                last_time = entry["last_bar_time"]
                if len(klines) >= n_bars and klines[n_bars - 1]["time"] == last_time and len(klines) > n_bars:
                    c = entry["czsc"]
                    from czsc.objects import RawBar
                    from czsc.enum import Freq
                    freq_map = {"1m": Freq.F1, "3m": Freq.F3, "5m": Freq.F5, "15m": Freq.F15, "30m": Freq.F30,
                                "1h": Freq.F60, "2h": Freq.F120, "4h": Freq.F240, "1d": Freq.D}
                    freq = freq_map.get(timeframe.lower(), Freq.F5)
                    for j, k in enumerate(klines[n_bars:]):
                        dt = datetime.utcfromtimestamp(k["time"] / 1000.0)
                        bar = RawBar(
                            symbol=symbol,
                            id=n_bars + j,
                            freq=freq,
                            dt=dt,
                            open=float(k["open"]),
                            close=float(k["close"]),
                            high=float(k["high"]),
                            low=float(k["low"]),
                            vol=float(k.get("volume", 0)),
                            amount=0,
                        )
                        c.update(bar)
                    entry["n_bars"] = len(klines)
                    entry["last_bar_time"] = klines[-1]["time"]
                    return _czsc_to_output(c, timeframe)
                # 非连续或变短，下面全量重建并更新缓存
            bars, freq = _klines_to_bars(symbol, timeframe, klines)
            c = CZSC(bars)
            _czsc_cache[key] = {"czsc": c, "n_bars": len(klines), "last_bar_time": klines[-1]["time"]}
        return _czsc_to_output(c, timeframe)
    except Exception:
        with _czsc_cache_lock:
            _czsc_cache.pop((symbol, timeframe), None)
        return empty_labels(timeframe)


@app.post("/analyze")
def analyze(req: AnalyzeRequest) -> dict[str, Any]:
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
