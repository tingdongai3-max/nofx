# -*- coding: utf-8 -*-
"""
NoFx CZSC 缠论数据中间件（Data Provider）
仅提供客观数据提取，不包含任何主观交易决策。
所有决策由下游 LLM Prompt 处理。

输出字段：
- bi: 笔序列
- zs: 笔中枢
- macd_signals: MACD 动力学数据

启动: uvicorn app:app --host 0.0.0.0 --port 8765 --workers 4
"""
from __future__ import annotations

from datetime import datetime, timedelta
from typing import Any

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

app = FastAPI(title="NoFx CZSC Data Provider", version="0.3.0-data-only")


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


def empty_labels(timeframe: str) -> dict[str, Any]:
    return {
        "bi": [],
        "zs": [],
        "macd_signals": {},
        "timeframe": timeframe,
    }


def _get_freq(timeframe: str):
    """将 timeframe 字符串映射为 czsc 的 Freq 枚举"""
    from czsc import Freq
    freq_map = {
        "1m": Freq.F1,
        "3m": Freq.F3,
        "5m": Freq.F5,
        "15m": Freq.F15,
        "30m": Freq.F30,
        "1h": Freq.F60,
        "2h": Freq.F120,
        "4h": Freq.F240,
        "1d": Freq.D,
    }
    return freq_map.get(timeframe.lower(), Freq.F60)


def run_czsc_analyze(symbol: str, timeframe: str, klines: list[dict]) -> dict[str, Any]:
    """使用 czsc 进行客观数据提取"""
    try:
        from czsc import CZSC, RawBar
    except ImportError:
        return empty_labels(timeframe)

    try:
        freq = _get_freq(timeframe)

        # 构建 K 线数据
        bars: list[RawBar] = []
        for i, k in enumerate(klines):
            rb = RawBar(
                symbol=symbol,
                id=i,
                dt=datetime.fromtimestamp(float(k["time"]) / 1000.0),
                open=float(k["open"]),
                high=float(k["high"]),
                low=float(k["low"]),
                close=float(k["close"]),
                vol=float(k.get("volume", 0) or 0.0),
                freq=freq,
                amount=float(k.get("volume", 0) or 0) * float(k["close"]),
            )
            bars.append(rb)

        if len(bars) < 30:
            return empty_labels(timeframe)

        # 初始化 CZSC
        c = CZSC(bars)

        out = empty_labels(timeframe)

        # ========== 1. 笔 (bi) ==========
        for bi in c.bi_list:
            out["bi"].append({
                "start_time": int(bi.sdt.timestamp() * 1000),
                "end_time": int(bi.edt.timestamp() * 1000),
                "high": float(bi.high),
                "low": float(bi.low),
                "direction": "up" if "向上" in str(bi.direction) else "down",
            })

        # ========== 2. 笔中枢 (zs) - 使用 czsc 库的 ZS 类 ==========
        try:
            from czsc import ZS
            zs_objs = []
            for i in range(len(c.bi_list) - 2):
                bis = c.bi_list[i:i+3]
                zs = ZS(bis)
                if zs.is_valid():
                    zs_objs.append(zs)

            for zs in zs_objs:
                out["zs"].append({
                    "start_time": int(zs.sdt.timestamp() * 1000),
                    "end_time": int(zs.edt.timestamp() * 1000),
                    "zg": float(zs.zg),
                    "zd": float(zs.zd),
                    "gg": float(zs.gg),
                    "dd": float(zs.dd),
                    "direction": "up" if "上" in str(zs.sdir) else "down",
                })
        except ImportError:
            # 如果导入失败，使用备用方法
            zs_list = _build_zones_from_bis(c.bi_list)
            for zs in zs_list:
                out["zs"].append({
                    "start_time": int(zs["start_time"]),
                    "end_time": int(zs["end_time"]),
                    "zg": float(zs["zg"]),
                    "zd": float(zs["zd"]),
                    "direction": zs["direction"],
                })

        # ========== 3. MACD 动力学数据 ==========
        out["macd_signals"] = _analyze_macd(bars)

        return out

    except Exception as e:
        print(f"CRITICAL ERROR: {str(e)}")
        import traceback
        traceback.print_exc()
        return empty_labels(timeframe)


def _build_zones_from_bis(bi_list: list) -> list:
    """从笔序列构建笔中枢

    笔中枢：连续三笔有重叠区域
    """
    if not bi_list or len(bi_list) < 3:
        return []

    def has_overlap(b1, b2, b3) -> bool:
        high_min = min(b1.high, b2.high, b3.high)
        low_max = max(b1.low, b2.low, b3.low)
        return high_min > low_max

    zones = []
    n = len(bi_list)

    for i in range(n - 2):
        b1, b2, b3 = bi_list[i], bi_list[i+1], bi_list[i+2]

        if has_overlap(b1, b2, b3):
            high_min = min(b1.high, b2.high, b3.high)
            low_max = max(b1.low, b2.low, b3.low)

            direction = "up" if "向上" in str(b1.direction) else "down"

            zones.append({
                "start_time": int(b1.sdt.timestamp() * 1000),
                "end_time": int(b3.edt.timestamp() * 1000),
                "high": high_min,
                "low": low_max,
                "zg": high_min,
                "zd": low_max,
                "direction": direction,
            })

    # 合并重叠的中枢
    if not zones:
        return []

    merged = [zones[0]]
    for z in zones[1:]:
        last = merged[-1]
        if z["low"] <= last["high"] and z["high"] >= last["low"]:
            merged[-1] = {
                "start_time": min(last["start_time"], z["start_time"]),
                "end_time": max(last["end_time"], z["end_time"]),
                "high": max(last["high"], z["high"]),
                "low": min(last["low"], z["low"]),
                "zg": max(last["zg"], z["zg"]),
                "zd": min(last["zd"], z["zd"]),
                "direction": last["direction"],
            }
        else:
            merged.append(z)

    return merged


def _analyze_macd(bars: list, di: int = 1, n: int = 20) -> dict:
    """MACD 动力学数据提取

    直接计算 MACD 指标值，不依赖 czsc 信号生成 API（兼容新版本）。
    返回客观物理数值，不包含任何主观判断。
    """
    try:
        import pandas as pd

        if len(bars) < 30:
            return {"bc_signals": [], "error": f"数据不足({len(bars)}根)"}

        # 提取收盘价
        closes = [bar.close for bar in bars]
        if not closes:
            return {"bc_signals": [], "error": "无收盘价数据"}

        # 计算 EMA
        series = pd.Series(closes)
        ema_fast = series.ewm(span=12, adjust=False).mean()
        ema_slow = series.ewm(span=26, adjust=False).mean()
        diff = ema_fast - ema_slow
        dea = diff.ewm(span=9, adjust=False).mean()
        histogram = (diff - dea) * 2

        result = {
            "bc_signals": [],
            "histogram": float(histogram.iloc[-1]) if len(histogram) > 0 else 0.0,
            "diff": float(diff.iloc[-1]) if len(diff) > 0 else 0.0,
            "dea": float(dea.iloc[-1]) if len(dea) > 0 else 0.0,
            "zero_cross": None,
        }

        # 零轴位置（客观物理状态）
        last_diff = result["diff"]
        if abs(last_diff) < 0.5:
            result["zero_cross"] = "near_zero"
        elif last_diff > 0:
            result["zero_cross"] = "above_zero"
        else:
            result["zero_cross"] = "below_zero"

        # 检测背驰：比较最近价格创新高/新低与 MACD 创新高/新低
        if len(closes) >= 20 and len(diff) >= 20:
            recent_closes = closes[-20:]
            recent_diffs = diff.values[-20:]

            # 检查最近价格是否创新高但 MACD 没有创新高（顶背驰）
            price_high = max(recent_closes)
            diff_high = max(recent_diffs)

            # 检查最近价格是否创新低但 MACD 没有创新低（底背驰）
            price_low = min(recent_closes)
            diff_low = min(recent_diffs)

            # 简单背驰判断
            if recent_closes[-1] >= price_high * 0.98 and recent_diffs[-1] < diff_high * 0.8:
                result["bc_signals"].append({
                    "signal": "顶背驰",
                    "time": int(bars[-1].dt.timestamp() * 1000),
                })
            elif recent_closes[-1] <= price_low * 1.02 and recent_diffs[-1] > diff_low * 1.2:
                result["bc_signals"].append({
                    "signal": "底背驰",
                    "time": int(bars[-1].dt.timestamp() * 1000),
                })

        print(f"DEBUG MACD: diff={result['diff']:.6f}, dea={result['dea']:.6f}, histogram={result['histogram']:.6f}")
        return result

    except Exception as e:
        import traceback
        traceback.print_exc()
        return {"bc_signals": [], "error": str(e)}


@app.post("/analyze")
def analyze(req: AnalyzeRequest) -> dict[str, Any]:
    print(f"DEBUG: {req.symbol} {req.timeframe} {len(req.klines)} bars")

    if len(req.klines) < 20:
        raise HTTPException(status_code=400, detail="Need at least 20 klines")

    klist = [k.model_dump() for k in req.klines]
    try:
        return run_czsc_analyze(req.symbol, req.timeframe, klist)
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.on_event("startup")
def warmup_czsc() -> None:
    try:
        from czsc import CZSC, RawBar, Freq
        base = datetime.utcnow().replace(second=0, microsecond=0)
        bars = []
        for i in range(50):
            dt = base + timedelta(minutes=i * 5)
            bars.append(RawBar(
                symbol="WARMUP", id=i, freq=Freq.F5, dt=dt,
                open=100.0, close=100.0, high=100.0, low=100.0,
                vol=0.0, amount=0
            ))
        CZSC(bars)
    except Exception:
        pass


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": "nofx-czsc-v0.3-data-only"}
