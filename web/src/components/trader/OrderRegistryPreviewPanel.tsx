import { useCallback, useEffect, useState } from 'react'
import { Activity, AlertTriangle, ChevronDown, ChevronUp, Shield } from 'lucide-react'
import { api } from '../../lib/api'
import type { OrderReconcilePreview } from '../../types'

interface OrderRegistryPreviewPanelProps {
  traderId: string
  refreshInterval?: number
  selectedSymbol?: string
}

function translateTag(value?: string): string {
  const v = (value || '').toLowerCase()
  switch (v) {
    case 'binance_usdm':
      return '币安 U 本位永续'
    case 'one_way':
      return '单向持仓'
    case 'pending':
      return '等待'
    case 'pending_user_stream':
      return '等待用户流'
    case 'mismatch':
      return '不一致'
    case 'unknown':
      return '未知'
    case 'consistent':
      return '一致'
    case 'readonly':
      return '只读'
    default:
      return value || '-'
  }
}

function formatQty(value: number): string {
  if (!value && value !== 0) return '-'
  return value.toFixed(6)
}

function formatTime(value?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toISOString()
}

export function OrderRegistryPreviewPanel({
  traderId,
  refreshInterval = 8000,
  selectedSymbol,
}: OrderRegistryPreviewPanelProps) {
  const [preview, setPreview] = useState<OrderReconcilePreview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState(true)

  const loadPreview = useCallback(async () => {
    try {
      const data = await api.getRuntimeOrderPreview(traderId, selectedSymbol, true)
      setPreview(data)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unknown error')
    } finally {
      setLoading(false)
    }
  }, [traderId, selectedSymbol])

  useEffect(() => {
    loadPreview()
    const timer = setInterval(loadPreview, refreshInterval)
    return () => clearInterval(timer)
  }, [loadPreview, refreshInterval])

  const cardStyle = {
    background: '#0B0E11',
    border: '1px solid #2B3139',
  }

  if (loading) {
    return (
      <div className="p-3 text-center text-xs rounded" style={cardStyle}>
        Loading order truth preview...
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-3 text-center text-xs rounded" style={{ ...cardStyle, color: '#F6465D' }}>
        Order truth preview failed: {error}
      </div>
    )
  }

  if (!preview) {
    return (
      <div className="p-3 text-center text-xs rounded" style={cardStyle}>
        No order truth data available.
      </div>
    )
  }

  const registry = preview.order_registry ?? []
  const events = preview.order_event_logs ?? []

  return (
    <div className="rounded-lg overflow-hidden" style={cardStyle}>
      <div
        className="flex items-center justify-between p-3 cursor-pointer hover:bg-[#1E2329] transition-colors"
        onClick={() => setExpanded((v) => !v)}
      >
        <div className="flex items-center gap-2">
          <Shield className="w-4 h-4" style={{ color: '#F0B90B' }} />
          <span className="font-medium text-sm" style={{ color: '#EAECEF' }}>
            订单真相预览
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: '#F0B90B22', color: '#F0B90B' }}>
            {translateTag(preview.exchange)}
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: '#0ECB8122', color: '#0ECB81' }}>
            {translateTag(preview.mode)}
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: preview.reconcile_status === 'mismatch' ? '#F6465D22' : '#848E9C22', color: preview.reconcile_status === 'mismatch' ? '#F6465D' : '#EAECEF' }}>
            {translateTag(preview.reconcile_status)}
          </span>
        </div>
        {expanded ? (
          <ChevronUp className="w-4 h-4" style={{ color: '#848E9C' }} />
        ) : (
          <ChevronDown className="w-4 h-4" style={{ color: '#848E9C' }} />
        )}
      </div>

      {expanded && (
        <div className="px-3 pb-3 space-y-3">
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            {[
              ['User Stream', preview.user_stream_ready],
              ['Working Orders', preview.has_working_orders],
              ['State Mismatch', !preview.has_state_mismatch],
              ['Cancel/Replace', !preview.has_pending_cancel_replace],
            ].map(([label, enabled]) => (
              <div key={label as string} className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
                <span className="text-xs" style={{ color: '#848E9C' }}>{label as string}</span>
                <span style={{ color: enabled ? '#0ECB81' : '#F6465D' }}>
                  {enabled ? <Shield className="w-3 h-3" /> : <AlertTriangle className="w-3 h-3" />}
                </span>
              </div>
            ))}
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Pending Add</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(preview.pending_add_qty)}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Pending Reduce</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(preview.pending_reduce_qty)}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Registry Rows</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{registry.length}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Event Logs</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{events.length}</div>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Has Scale-Out</span>
              <span style={{ color: preview.has_scale_out_plan ? '#0ECB81' : '#F6465D' }}>
                {preview.has_scale_out_plan ? 'yes' : 'no'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Scale-Out Status</span>
              <span className="font-mono text-[11px]" style={{ color: '#EAECEF' }}>
                {preview.scale_out_status || '-'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Scale-Out Consistency</span>
              <span className="font-mono text-[11px]" style={{ color: preview.scale_out_consistency === 'mismatch' ? '#F6465D' : '#EAECEF' }}>
                {preview.scale_out_consistency || 'unknown'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Protection Rebalanced</span>
              <span style={{ color: preview.protection_rebalanced ? '#0ECB81' : '#F6465D' }}>
                {preview.protection_rebalanced ? 'yes' : 'no'}
              </span>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Pending Scale-Out</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(preview.pending_scale_out_qty)}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Remaining Scale-Out</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(preview.remaining_scale_out_qty)}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Executed Scale-Out</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(preview.executed_scale_out_qty)}</div>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Scale-Out Mismatch</span>
              <span style={{ color: preview.has_scale_out_mismatch ? '#F6465D' : '#0ECB81' }}>
                {preview.has_scale_out_mismatch ? 'yes' : 'no'}
              </span>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Has Protection</span>
              <span style={{ color: preview.has_protection ? '#0ECB81' : '#F6465D' }}>
                {preview.has_protection ? 'yes' : 'no'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Protection Status</span>
              <span className="font-mono text-[11px]" style={{ color: '#EAECEF' }}>
                {preview.protection_group_status || '-'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Stop Loss Armed</span>
              <span style={{ color: preview.stop_loss_armed ? '#0ECB81' : '#F6465D' }}>
                {preview.stop_loss_armed ? 'yes' : 'no'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Take Profit Armed</span>
              <span style={{ color: preview.take_profit_armed ? '#0ECB81' : '#F6465D' }}>
                {preview.take_profit_armed ? 'yes' : 'no'}
              </span>
            </div>
          </div>

          <div className="p-3 rounded" style={{ background: '#1E2329' }}>
            <div className="flex items-center gap-2 mb-2">
              <Activity className="w-3 h-3" style={{ color: '#F0B90B' }} />
              <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Order Registry</span>
            </div>
            <div className="overflow-x-auto max-h-80 overflow-y-auto custom-scrollbar">
              <table className="w-full text-xs">
                <thead className="text-left border-b border-white/5">
                  <tr>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap">Symbol</th>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap">Role</th>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap">Status</th>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap text-right">Orig</th>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap text-right">Exec</th>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap text-right">Remain</th>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap text-center">Work</th>
                  </tr>
                </thead>
                <tbody>
                  {registry.length > 0 ? registry.map((row) => (
                    <tr key={`${row.id}-${row.exchange_order_id || row.local_intent_id}`} className="border-b border-white/5 last:border-0">
                      <td className="px-1 py-2 font-mono text-nofx-text-main whitespace-nowrap">{row.symbol}</td>
                      <td className="px-1 py-2 text-nofx-text-main whitespace-nowrap">{row.order_role}</td>
                      <td className="px-1 py-2 text-nofx-text-main whitespace-nowrap">{row.status}</td>
                      <td className="px-1 py-2 font-mono text-right text-nofx-text-main">{formatQty(row.orig_qty)}</td>
                      <td className="px-1 py-2 font-mono text-right text-nofx-text-main">{formatQty(row.executed_qty)}</td>
                      <td className="px-1 py-2 font-mono text-right text-nofx-text-main">{formatQty(row.remaining_qty)}</td>
                      <td className="px-1 py-2 text-center">
                        <span style={{ color: row.is_working ? '#0ECB81' : '#F6465D' }}>
                          {row.is_working ? 'yes' : 'no'}
                        </span>
                      </td>
                    </tr>
                  )) : (
                    <tr>
                      <td className="px-1 py-4 text-center text-nofx-text-muted" colSpan={7}>No registry rows.</td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-2">
            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <AlertTriangle className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Recent Events</span>
              </div>
              <div className="space-y-1 max-h-64 overflow-auto pr-1">
                {events.length > 0 ? events.map((event) => (
                  <div key={event.id} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-mono" style={{ color: '#F0B90B' }}>{event.event_type}</span>
                      <span className="font-mono" style={{ color: '#848E9C' }}>{formatTime(event.event_time)}</span>
                    </div>
                    <div className="text-nofx-text-muted mt-1">
                      {event.event_source}
                      {event.exchange_order_id ? ` · ${event.exchange_order_id}` : ''}
                    </div>
                  </div>
                )) : (
                  <div className="text-xs" style={{ color: '#848E9C' }}>No recent events.</div>
                )}
              </div>
            </div>

            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <AlertTriangle className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Mismatch Reasons</span>
              </div>
              <div className="space-y-1 max-h-64 overflow-auto pr-1">
                {preview.mismatch_reasons.length > 0 ? preview.mismatch_reasons.map((reason, index) => (
                  <div key={`${reason.category}-${index}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                    <span className="font-mono" style={{ color: '#F0B90B' }}>{reason.category}</span>
                    <span style={{ color: '#848E9C' }}> · </span>
                    <span>{reason.reason}</span>
                  </div>
                )) : (
                  <div className="text-xs" style={{ color: '#848E9C' }}>No mismatch reasons.</div>
                )}
              </div>
            </div>
          </div>

          <div className="p-3 rounded" style={{ background: '#1E2329' }}>
            <div className="flex items-center gap-2 mb-2">
              <AlertTriangle className="w-3 h-3" style={{ color: '#F0B90B' }} />
              <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Protection Block Reasons</span>
            </div>
            <div className="space-y-1 max-h-40 overflow-auto pr-1">
              {preview.protection_block_reasons.length > 0 ? preview.protection_block_reasons.map((reason, index) => (
                <div key={`${reason.category}-${index}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                  <span className="font-mono" style={{ color: '#F0B90B' }}>{reason.category}</span>
                  <span style={{ color: '#848E9C' }}> · </span>
                  <span>{reason.reason}</span>
                </div>
              )) : (
                <div className="text-xs" style={{ color: '#848E9C' }}>No protection block reasons.</div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
