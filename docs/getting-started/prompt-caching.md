# Prompt Caching (AI Token Optimization)

NOFX supports **prompt caching** to reduce API token consumption for 24/7 trading bots.

## How it works

- **Static system prompt** (strategy rules, risk limits structure, output format, custom prompt) is built once per strategy config and **can be cached** by the provider.
- **Dynamic content** (current equity, position limits this period, and the full user prompt with market data and positions) is **never cached** and is sent fresh every request.

This way:
- Cached tokens are charged at much lower rates (e.g. ~90% cheaper on Claude).
- Every trading decision still uses up-to-date equity and market data; no stale prompts.

## Safety

- Only the **static** part of the system prompt is eligible for caching.
- **Equity, position limits, and all user prompt content** (account, positions, market data, K-lines, etc.) are sent in the non-cached path every time.
- Cache is maintained by the provider (e.g. Anthropic); we only mark which block is cacheable. If the provider invalidates or misses the cache, the request still succeeds with full prompt.

## Providers

| Provider | Behavior |
|----------|----------|
| **Claude** | Explicit caching: static system block is sent with `cache_control: { type: "ephemeral" }`. Dynamic block and user message are not cached. Logs cache read/creation in response. |
| **OpenAI** | Automatic caching when prompt is long enough (e.g. ≥1024 tokens). We send full system (static+dynamic) as one; prefix matches across requests when only user message changes. |
| **Gemini** | Implicit caching (enabled by default on most Gemini models). We send full system (static+dynamic) via the OpenAI-compatible endpoint; same prefix each time so implicit cache can hit. For explicit caching you would need the native Gemini API (cachedContents + generateContent); not implemented here. |

## Monitoring

For Claude, when cache is used the log will show something like:

```
📦 [Claude] Prompt cache: read=12000 created=0 input_after_breakpoint=3500
```

- `read`: tokens served from cache (cheaper).
- `created`: tokens written to cache this request.
- `input_after_breakpoint`: non-cached input (dynamic + user message).

## References

- [Anthropic: Prompt caching](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching)
- OpenAI and Gemini: caching is automatic; see provider docs for thresholds and pricing.
