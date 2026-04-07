import { useCallback, useEffect, useState } from 'react'
import { Activity, AlertTriangle, ChevronDown, ChevronUp, Shield } from 'lucide-react'
import { api } from '../../lib/api'
import type { ProtectionAdjustmentPreview } from '../../types'

interface ProtectionAdjustmentPreviewPanelProps {
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

export function ProtectionAdjustmentPreviewPanel({
  traderId,
  refreshInterval = 8000,
  selectedSymbol,
}: ProtectionAdjustmentPreviewPanelProps) {
  const [preview, setPreview] = useState<ProtectionAdjustmentPreview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState(true)

  const loadPreview = useCallback(async () => {
    try {
      const data = await api.getRuntimeProtectionAdjustmentPreview(traderId, selectedSymbol, true)
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
        Loading protection adjustment preview...
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-3 text-center text-xs rounded" style={{ ...cardStyle, color: '#F6465D' }}>
        Protection adjustment preview failed: {error}
      </div>
    )
  }

  if (!preview) {
    return (
      <div className="p-3 text-center text-xs rounded" style={cardStyle}>
        No protection adjustment data available.
      </div>
    )
  }

  const group = preview.protection_group
  const trailingRule = preview.trailing_rule
  const events = preview.protection_event_logs ?? []
  const guard = preview.guard_assessment
  const blockReasons = preview.protection_adjustment_block_reasons ?? []

  return (
    <div className="rounded-lg overflow-hidden" style={cardStyle}>
      <div
        className="flex items-center justify-between p-3 cursor-pointer hover:bg-[#1E2329] transition-colors"
        onClick={() => setExpanded((v) => !v)}
      >
        <div className="flex items-center gap-2">
          <Shield className="w-4 h-4" style={{ color: '#F0B90B' }} />
          <span className="font-medium text-sm" style={{ color: '#EAECEF' }}>
            保护调整预览
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: '#F0B90B22', color: '#F0B90B' }}>
            {translateTag(preview.exchange)}
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: '#0ECB8122', color: '#0ECB81' }}>
            {translateTag(preview.mode)}
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: preview.protection_adjustment_consistency === 'mismatch' ? '#F6465D22' : '#848E9C22', color: preview.protection_adjustment_consistency === 'mismatch' ? '#F6465D' : '#EAECEF' }}>
            {translateTag(preview.protection_adjustment_consistency || 'unknown')}
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
              ['Break-even', preview.break_even_armed],
              ['Trailing', preview.trailing_armed],
              ['Cancel/Replace', !preview.has_pending_cancel_replace],
              ['Guard Allowed', guard?.allowed ?? false],
            ].map(([label, enabled]) => (
              <div key={label as string} className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
                <span className="text-xs" style={{ color: '#848E9C' }}>{label as string}</span>
                <span style={{ color: enabled ? '#0ECB81' : '#F6465D' }}>
                  {enabled ? 'yes' : 'no'}
                </span>
              </div>
            ))}
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Protection Revision</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{preview.protection_revision ?? 0}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Stop Loss</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>
                {formatPrice(preview.initial_stop_loss_price)} / {formatPrice(preview.current_stop_loss_price)}
              </div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Move Budget</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(preview.remaining_move_budget)}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Cancel/Replace</div>
              <div className="text-sm font-mono mt-1" style={{ color: preview.has_pending_cancel_replace ? '#F6465D' : '#0ECB81' }}>
                {preview.has_pending_cancel_replace ? 'active' : 'clear'}
              </div>
            </div>
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-2">
            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <Activity className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Trailing Rule</span>
              </div>
              {trailingRule ? (
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <div>
                    <div style={{ color: '#5E6673' }}>Rule ID</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{trailingRule.trailing_rule_id}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Mode</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{trailingRule.rule_mode}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Activation</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{trailingRule.activation_type} {trailingRule.activation_value}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Step Move</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{trailingRule.step_move_type} {trailingRule.step_move_value}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Step Trigger</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{trailingRule.step_trigger_type} {trailingRule.step_trigger_value}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Max Moves</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{trailingRule.max_move_count}</div>
                  </div>
                </div>
              ) : (
                <div className="text-xs" style={{ color: '#848E9C' }}>No trailing rule row.</div>
              )}
            </div>

            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <AlertTriangle className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Guard / Reasons</span>
              </div>
              <div className="space-y-1 max-h-64 overflow-auto pr-1">
                {guard?.blocked_reasons?.length ? guard.blocked_reasons.map((reason, index) => (
                  <div key={`${reason.action}-${reason.category}-${index}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                    <span className="font-mono" style={{ color: '#F0B90B' }}>{reason.action}</span>
                    <span style={{ color: '#848E9C' }}> · </span>
                    <span className="font-mono" style={{ color: '#0ECB81' }}>{reason.category}</span>
                    <span style={{ color: '#848E9C' }}> · </span>
                    <span>{reason.reason}</span>
                  </div>
                )) : blockReasons.length > 0 ? blockReasons.map((reason, index) => (
                  <div key={`${reason.action}-${reason.category}-${index}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                    <span className="font-mono" style={{ color: '#F0B90B' }}>{reason.action}</span>
                    <span style={{ color: '#848E9C' }}> · </span>
                    <span className="font-mono" style={{ color: '#0ECB81' }}>{reason.category}</span>
                    <span style={{ color: '#848E9C' }}> · </span>
                    <span>{reason.reason}</span>
                  </div>
                )) : (
                  <div className="text-xs" style={{ color: '#848E9C' }}>No block reasons.</div>
                )}
              </div>
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
                    <div style={{ color: '#5E6673' }}>Status</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{group.status}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Initial / Current SL</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatPrice(group.stop_loss_initial_trigger_price)} / {formatPrice(group.stop_loss_current_trigger_price)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Initial / Current TP</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatPrice(group.take_profit_initial_trigger_price)} / {formatPrice(group.take_profit_current_trigger_price)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Revision</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{group.protection_revision}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Last Action</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{group.last_protection_action || '-'}</div>
                  </div>
                </div>
              ) : (
                <div className="text-xs" style={{ color: '#848E9C' }}>No protection group row.</div>
              )}
            </div>

            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <Activity className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Recent Events</span>
              </div>
              <div className="space-y-1 max-h-64 overflow-auto pr-1">
                {events.length > 0 ? events.map((event) => (
                  <div key={`${event.id}-${event.event_time}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-mono" style={{ color: '#F0B90B' }}>{event.event_type}</span>
                      <span className="font-mono" style={{ color: '#848E9C' }}>{formatTime(event.event_time)}</span>
                    </div>
                    <div className="text-[10px]" style={{ color: '#848E9C' }}>{event.event_source}</div>
                  </div>
                )) : (
                  <div className="text-xs" style={{ color: '#848E9C' }}>No recent protection adjustment events.</div>
                )}
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
