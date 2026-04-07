import { useCallback, useEffect, useState } from 'react'
import { Activity, AlertTriangle, ChevronDown, ChevronUp, Shield } from 'lucide-react'
import { api } from '../../lib/api'
import type { ProtectionReconcilePreview } from '../../types'

interface ProtectionPreviewPanelProps {
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

function formatPrice(value: number): string {
  if (!value && value !== 0) return '-'
  if (value === 0) return '-'
  if (value >= 1000) return value.toFixed(2)
  if (value >= 1) return value.toFixed(4)
  return value.toFixed(6)
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

export function ProtectionPreviewPanel({
  traderId,
  refreshInterval = 8000,
  selectedSymbol,
}: ProtectionPreviewPanelProps) {
  const [preview, setPreview] = useState<ProtectionReconcilePreview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState(true)

  const loadPreview = useCallback(async () => {
    try {
      const data = await api.getRuntimeProtectionPreview(traderId, selectedSymbol, true)
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
        Loading protection preview...
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-3 text-center text-xs rounded" style={{ ...cardStyle, color: '#F6465D' }}>
        Protection preview failed: {error}
      </div>
    )
  }

  if (!preview) {
    return (
      <div className="p-3 text-center text-xs rounded" style={cardStyle}>
        No protection data available.
      </div>
    )
  }

  const group = preview.protection_group
  const events = preview.protection_event_logs ?? []
  const summary = preview.order_registry_summary
  const mismatchReasons = preview.mismatch_reasons ?? []
  const adjustmentReasons = preview.protection_adjustment_block_reasons ?? []

  return (
    <div className="rounded-lg overflow-hidden" style={cardStyle}>
      <div
        className="flex items-center justify-between p-3 cursor-pointer hover:bg-[#1E2329] transition-colors"
        onClick={() => setExpanded((v) => !v)}
      >
        <div className="flex items-center gap-2">
          <Shield className="w-4 h-4" style={{ color: '#F0B90B' }} />
          <span className="font-medium text-sm" style={{ color: '#EAECEF' }}>
            保护真相预览
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: '#F0B90B22', color: '#F0B90B' }}>
            {translateTag(preview.exchange)}
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: '#0ECB8122', color: '#0ECB81' }}>
            {translateTag(preview.mode)}
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: preview.protection_consistency === 'mismatch' ? '#F6465D22' : '#848E9C22', color: preview.protection_consistency === 'mismatch' ? '#F6465D' : '#EAECEF' }}>
            {translateTag(preview.protection_consistency || 'unknown')}
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
              ['Has Protection', preview.has_protection],
              ['Stop Loss Armed', preview.stop_loss_armed],
              ['Take Profit Armed', preview.take_profit_armed],
              ['State Match', !preview.has_state_mismatch],
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
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Protection Mode</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{preview.protection_mode || '-'}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Protected Qty</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(group?.protected_quantity ?? 0)}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>SL / TP Trigger</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>
                {formatPrice(group?.stop_loss_trigger_price ?? 0)} / {formatPrice(group?.take_profit_trigger_price ?? 0)}
              </div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Group Status</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{preview.protection_group_status || '-'}</div>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Adjustment Status</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{preview.protection_adjustment_status || '-'}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Adjustment Consistency</div>
              <div className="text-sm font-mono mt-1" style={{ color: preview.protection_adjustment_consistency === 'mismatch' ? '#F6465D' : '#EAECEF' }}>
                {preview.protection_adjustment_consistency || 'unknown'}
              </div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Stop Loss</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>
                {formatPrice(preview.initial_stop_loss_price)} / {formatPrice(preview.current_stop_loss_price)}
              </div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Revision</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{preview.protection_revision ?? 0}</div>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Break Even</span>
              <span style={{ color: preview.break_even_armed ? '#0ECB81' : '#F6465D' }}>
                {preview.break_even_armed ? 'armed' : 'off'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Trailing</span>
              <span style={{ color: preview.trailing_armed ? '#0ECB81' : '#F6465D' }}>
                {preview.trailing_armed ? 'armed' : 'off'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Cancel/Replace</span>
              <span style={{ color: preview.has_pending_cancel_replace ? '#F6465D' : '#0ECB81' }}>
                {preview.has_pending_cancel_replace ? 'active' : 'clear'}
              </span>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Move Budget</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(preview.remaining_move_budget)}</div>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Working Orders</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{summary?.working_order_count ?? 0}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Pending Reduce</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(summary?.pending_reduce_qty ?? preview.pending_reduce_qty)}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Pending Add</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(summary?.pending_add_qty ?? preview.pending_add_qty)}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Events</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{events.length}</div>
            </div>
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-2">
            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <AlertTriangle className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Adjustment Reasons</span>
              </div>
              <div className="space-y-1 max-h-56 overflow-auto pr-1">
                {adjustmentReasons.length > 0 ? adjustmentReasons.map((reason, index) => (
                  <div key={`${reason.action}-${reason.category}-${index}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                    <span className="font-mono" style={{ color: '#F0B90B' }}>{reason.action}</span>
                    <span style={{ color: '#848E9C' }}> · </span>
                    <span className="font-mono" style={{ color: '#0ECB81' }}>{reason.category}</span>
                    <span style={{ color: '#848E9C' }}> · </span>
                    <span>{reason.reason}</span>
                  </div>
                )) : (
                  <div className="text-xs" style={{ color: '#848E9C' }}>No dynamic protection block reasons.</div>
                )}
              </div>
            </div>
            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <Activity className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Recent Adjustment Events</span>
              </div>
              <div className="space-y-1 max-h-56 overflow-auto pr-1">
                {events.length > 0 ? events.map((event) => (
                  <div key={`${event.id}-${event.event_time}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-mono" style={{ color: '#F0B90B' }}>{event.event_type}</span>
                      <span className="font-mono" style={{ color: '#848E9C' }}>{formatTime(event.event_time)}</span>
                    </div>
                    <div className="text-[10px]" style={{ color: '#848E9C' }}>{event.event_source}</div>
                  </div>
                )) : (
                  <div className="text-xs" style={{ color: '#848E9C' }}>No recent adjustment events.</div>
                )}
              </div>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Protection Coverage</span>
              <span className="font-mono text-[11px]" style={{ color: '#EAECEF' }}>
                {formatQty(summary?.protection_coverage_qty ?? group?.protected_quantity ?? 0)}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Add Position Pressure</span>
              <span className="font-mono text-[11px]" style={{ color: (summary?.pending_add_qty ?? preview.pending_add_qty) > 0 ? '#F6465D' : '#0ECB81' }}>
                {(summary?.pending_add_qty ?? preview.pending_add_qty) > 0 ? 'yes' : 'no'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Protection Consistency</span>
              <span className="font-mono text-[11px]" style={{ color: preview.protection_consistency === 'mismatch' ? '#F6465D' : '#EAECEF' }}>
                {preview.protection_consistency || 'unknown'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Add Position Ready</span>
              <span className="font-mono text-[11px]" style={{ color: preview.has_state_mismatch || preview.has_pending_cancel_replace ? '#F6465D' : '#0ECB81' }}>
                {preview.has_state_mismatch || preview.has_pending_cancel_replace ? 'no' : 'yes'}
              </span>
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

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-2">
            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <Activity className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Protection Group</span>
              </div>
              {group ? (
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <div>
                    <div style={{ color: '#5E6673' }}>Group ID</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{group.protection_group_id}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Linked Position</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{group.linked_position_key}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Stop Loss Intent</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{group.stop_loss_order_intent_id || '-'}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Take Profit Intent</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{group.take_profit_order_intent_id || '-'}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Last Synced</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatTime(group.last_synced_at)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Source</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{group.source || '-'}</div>
                  </div>
                </div>
              ) : (
                <div className="text-xs" style={{ color: '#848E9C' }}>
                  No current protection group row.
                </div>
              )}
            </div>

            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <AlertTriangle className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Consistency</span>
              </div>
              <div className="space-y-1 max-h-64 overflow-auto pr-1">
                {mismatchReasons.length > 0 ? mismatchReasons.map((reason, index) => (
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

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-2">
            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <Activity className="w-3 h-3" style={{ color: '#F0B90B' }} />
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
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Protection State</span>
              </div>
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div>
                  <div style={{ color: '#5E6673' }}>Selected Symbol</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.selected_symbol ?? '-'}</div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>User Stream</div>
                  <div className="font-mono" style={{ color: preview.user_stream_ready ? '#0ECB81' : '#F6465D' }}>
                    {preview.user_stream_ready ? 'ready' : 'not ready'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Has State Mismatch</div>
                  <div className="font-mono" style={{ color: preview.has_state_mismatch ? '#F6465D' : '#0ECB81' }}>
                    {preview.has_state_mismatch ? 'yes' : 'no'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Protection Group Status</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.protection_group_status || '-'}</div>
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
