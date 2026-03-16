import { useEffect, useMemo, useRef, useState } from 'react'
import { Bot, ChevronDown, Loader2, Send, Sparkles, TerminalSquare, Wrench } from 'lucide-react'
import { toast } from 'sonner'
import {
  executeOpenClawToolCall,
  OPENCLAW_TOOLS,
  type OpenClawToolCall,
  type OpenClawWebhookResponse,
} from '../lib/openclaw'

type Role = 'assistant' | 'user' | 'tool'

interface ChatMessage {
  id: string
  role: Role
  content: string
}

const SUGGESTIONS = [
  '帮我创建一个使用 DeepSeek、绑定 Binance 主账户、5 分钟扫描的模拟盘交易员',
  '列出当前所有交易员和交易所配置，告诉我哪些配置缺失',
  '把指定交易员热重载，并把自定义 prompt 更新成更保守的风控策略',
]

export function OpenClawWidget() {
  const webhookUrl = import.meta.env.VITE_OPENCLAW_WEBHOOK_URL as string | undefined
  const webhookToken = import.meta.env.VITE_OPENCLAW_WEBHOOK_TOKEN as string | undefined
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState(false)
  const [input, setInput] = useState('')
  const [messages, setMessages] = useState<ChatMessage[]>([
    {
      id: 'intro',
      role: 'assistant',
      content: webhookUrl
        ? 'OpenClaw 已就绪。输入自然语言需求后，我会先请求 OpenClaw 规划，再按返回的 tool calls 调用当前 NOFX 后端接口。'
        : '尚未配置 OpenClaw WebHook。设置 VITE_OPENCLAW_WEBHOOK_URL 后，这个控件会把对话和工具清单发送给 OpenClaw。',
    },
  ])
  const scrollRef = useRef<HTMLDivElement | null>(null)

  const toolSummary = useMemo(
    () => OPENCLAW_TOOLS.map((tool) => `${tool.name} ${tool.method} ${tool.path}`).join('\n'),
    []
  )

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' })
  }, [messages, open])

  useEffect(() => {
    const onOpen = () => setOpen(true)
    window.addEventListener('open-openclaw-widget', onOpen)
    return () => window.removeEventListener('open-openclaw-widget', onOpen)
  }, [])

  const appendMessage = (message: ChatMessage) => {
    setMessages((prev) => [...prev, message])
  }

  const executeToolCalls = async (toolCalls: OpenClawToolCall[]) => {
    for (const call of toolCalls) {
      appendMessage({
        id: `${Date.now()}-${call.tool}-pending`,
        role: 'tool',
        content: `执行 ${call.tool} ${JSON.stringify(call.arguments ?? {})}`,
      })

      try {
        const result = await executeOpenClawToolCall(call)
        appendMessage({
          id: `${Date.now()}-${call.tool}-result`,
          role: 'tool',
          content: `${call.tool} 完成: ${JSON.stringify(result.data ?? result.message ?? {}, null, 2)}`,
        })
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Tool execution failed'
        appendMessage({
          id: `${Date.now()}-${call.tool}-error`,
          role: 'tool',
          content: `${call.tool} 失败: ${message}`,
        })
        throw error
      }
    }
  }

  const handleSend = async (nextInput?: string) => {
    const content = (nextInput ?? input).trim()
    if (!content || pending) return

    const nextConversation = [...messages, { id: `${Date.now()}-user`, role: 'user' as const, content }]
    setMessages(nextConversation)
    setInput('')

    if (!webhookUrl) {
      appendMessage({
        id: `${Date.now()}-no-webhook`,
        role: 'assistant',
        content: `缺少 OpenClaw WebHook。请配置 VITE_OPENCLAW_WEBHOOK_URL。\n\n当前已暴露工具:\n${toolSummary}`,
      })
      return
    }

    setPending(true)

    try {
      const response = await fetch(webhookUrl, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          ...(webhookToken ? { Authorization: `Bearer ${webhookToken}` } : {}),
        },
        body: JSON.stringify({
          message: content,
          conversation: nextConversation.map(({ role, content: itemContent }) => ({ role, content: itemContent })),
          tools: OPENCLAW_TOOLS,
          context: {
            app: 'nofx',
            pathname: window.location.pathname,
            timestamp: new Date().toISOString(),
          },
        }),
      })

      if (!response.ok) {
        throw new Error(`OpenClaw webhook request failed: ${response.status}`)
      }

      const payload = (await response.json()) as OpenClawWebhookResponse

      if (payload.reply) {
        appendMessage({
          id: `${Date.now()}-assistant`,
          role: 'assistant',
          content: payload.reply,
        })
      }

      if (payload.tool_calls?.length) {
        await executeToolCalls(payload.tool_calls)
      }

      if (!payload.reply && !payload.tool_calls?.length) {
        appendMessage({
          id: `${Date.now()}-empty`,
          role: 'assistant',
          content: 'OpenClaw 已响应，但没有返回 reply 或 tool_calls。',
        })
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error'
      toast.error(message)
      appendMessage({
        id: `${Date.now()}-assistant-error`,
        role: 'assistant',
        content: `请求失败: ${message}`,
      })
    } finally {
      setPending(false)
    }
  }

  return (
    <div className="fixed bottom-5 right-5 z-50">
      {open ? (
        <div className="w-[360px] max-w-[calc(100vw-24px)] overflow-hidden rounded-3xl border border-[#3b4a59] bg-[#09111a]/95 shadow-[0_24px_80px_rgba(0,0,0,0.45)] backdrop-blur-xl">
          <div className="border-b border-white/10 bg-[radial-gradient(circle_at_top_left,_rgba(104,211,145,0.16),_transparent_55%),linear-gradient(135deg,_rgba(15,23,42,0.98),_rgba(7,12,18,0.96))] px-4 py-4">
            <div className="flex items-start justify-between gap-3">
              <div className="flex items-center gap-3">
                <div className="flex h-11 w-11 items-center justify-center rounded-2xl bg-[#d0ff71] text-[#05110d]">
                  <Bot className="h-5 w-5" />
                </div>
                <div>
                  <div className="flex items-center gap-2 text-sm font-semibold text-white">
                    OpenClaw
                    <span className="rounded-full border border-[#d0ff71]/30 bg-[#d0ff71]/10 px-2 py-0.5 text-[10px] uppercase tracking-[0.24em] text-[#d0ff71]">
                      Control
                    </span>
                  </div>
                  <p className="mt-1 text-xs text-zinc-400">
                    {webhookUrl ? 'Webhook connected' : 'Webhook missing'}
                  </p>
                </div>
              </div>
              <button
                onClick={() => setOpen(false)}
                className="rounded-full border border-white/10 p-2 text-zinc-400 transition hover:border-white/20 hover:text-white"
                aria-label="Collapse OpenClaw widget"
              >
                <ChevronDown className="h-4 w-4" />
              </button>
            </div>

            <div className="mt-4 grid grid-cols-2 gap-2 text-[11px] text-zinc-300">
              <div className="rounded-2xl border border-white/10 bg-white/5 px-3 py-2">
                <div className="flex items-center gap-2 text-zinc-400">
                  <Wrench className="h-3.5 w-3.5" />
                  Tools
                </div>
                <div className="mt-1 font-mono text-white">{OPENCLAW_TOOLS.length}</div>
              </div>
              <div className="rounded-2xl border border-white/10 bg-white/5 px-3 py-2">
                <div className="flex items-center gap-2 text-zinc-400">
                  <TerminalSquare className="h-3.5 w-3.5" />
                  Mode
                </div>
                <div className="mt-1 font-mono text-white">{webhookUrl ? 'nl->tools' : 'manifest only'}</div>
              </div>
            </div>
          </div>

          <div ref={scrollRef} className="max-h-[360px] space-y-3 overflow-y-auto px-4 py-4">
            {messages.map((message) => (
              <div
                key={message.id}
                className={`rounded-2xl px-3 py-2 text-sm leading-6 ${
                  message.role === 'user'
                    ? 'ml-8 bg-[#d0ff71] text-[#06110d]'
                    : message.role === 'tool'
                      ? 'border border-cyan-400/20 bg-cyan-400/10 font-mono text-cyan-100'
                      : 'mr-8 border border-white/10 bg-white/5 text-zinc-100'
                }`}
              >
                <div className="mb-1 text-[10px] uppercase tracking-[0.24em] opacity-60">
                  {message.role}
                </div>
                <div className="whitespace-pre-wrap break-words">{message.content}</div>
              </div>
            ))}
            {pending && (
              <div className="mr-8 rounded-2xl border border-white/10 bg-white/5 px-3 py-2 text-sm text-zinc-200">
                <div className="flex items-center gap-2">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  OpenClaw 正在规划并调度工具
                </div>
              </div>
            )}
          </div>

          <div className="border-t border-white/10 px-4 py-4">
            <div className="mb-3 flex flex-wrap gap-2">
              {SUGGESTIONS.map((suggestion) => (
                <button
                  key={suggestion}
                  onClick={() => handleSend(suggestion)}
                  className="rounded-full border border-white/10 bg-white/5 px-3 py-1.5 text-left text-[11px] text-zinc-300 transition hover:border-[#d0ff71]/40 hover:text-white"
                >
                  <span className="flex items-center gap-1.5">
                    <Sparkles className="h-3 w-3" />
                    {suggestion}
                  </span>
                </button>
              ))}
            </div>

            <div className="flex items-end gap-2">
              <textarea
                value={input}
                onChange={(event) => setInput(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && !event.shiftKey) {
                    event.preventDefault()
                    void handleSend()
                  }
                }}
                placeholder="让 OpenClaw 帮你改配置、创建交易员、热重载..."
                className="min-h-[84px] flex-1 resize-none rounded-2xl border border-white/10 bg-[#050b12] px-3 py-3 text-sm text-white outline-none transition focus:border-[#d0ff71]/50"
              />
              <button
                onClick={() => void handleSend()}
                disabled={pending || input.trim().length === 0}
                className="flex h-12 w-12 items-center justify-center rounded-2xl bg-[#d0ff71] text-[#06110d] transition hover:bg-[#bbf25f] disabled:cursor-not-allowed disabled:opacity-40"
                aria-label="Send message to OpenClaw"
              >
                {pending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
              </button>
            </div>
          </div>
        </div>
      ) : (
        <button
          onClick={() => setOpen(true)}
          className="group flex items-center gap-3 rounded-full border border-[#d0ff71]/30 bg-[#09111a]/92 px-4 py-3 text-left text-white shadow-[0_18px_48px_rgba(0,0,0,0.42)] backdrop-blur-xl transition hover:border-[#d0ff71]/60 hover:translate-y-[-1px]"
        >
          <span className="flex h-11 w-11 items-center justify-center rounded-full bg-[#d0ff71] text-[#06110d]">
            <Bot className="h-5 w-5" />
          </span>
          <span>
            <span className="block text-sm font-semibold">OpenClaw</span>
            <span className="block text-xs text-zinc-400">Natural language control plane</span>
          </span>
        </button>
      )}
    </div>
  )
}
