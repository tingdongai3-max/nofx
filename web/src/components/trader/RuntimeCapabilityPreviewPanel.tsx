import { useCallback, useEffect, useState } from 'react'
import { Shield, Lock, Unlock, Activity, AlertTriangle, ChevronDown, ChevronUp } from 'lucide-react'
import { api } from '../../lib/api'
import type { RuntimeCapabilityPreview } from '../../types'

interface RuntimeCapabilityPreviewPanelProps {
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

function parseJSON(text?: string): any {
  if (!text) return null
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

export function RuntimeCapabilityPreviewPanel({
  traderId,
  refreshInterval = 8000,
  selectedSymbol,
}: RuntimeCapabilityPreviewPanelProps) {
  const [preview, setPreview] = useState<RuntimeCapabilityPreview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState(true)

  const loadPreview = useCallback(async () => {
    try {
      const data = await api.getRuntimeCapabilityPreview(traderId, selectedSymbol, true)
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

  const profile = preview?.strategy_profile
  const aggregate = preview?.position_aggregate
  const allowed = preview?.allowed_actions ?? []
  const blocked = preview?.blocked_actions ?? []
  const protectionState = parseJSON(aggregate?.protection_state_json)
  const scalePlanState = parseJSON(aggregate?.scale_plan_state_json)
  const scaleInReasons = (preview?.scale_in_block_reasons ?? []).filter((reason) => reason.action === 'add_position')
  const protectionAdjustmentReasons = preview?.protection_adjustment_block_reasons ?? []
  const mismatchReasons = preview?.mismatch_reasons ?? []
  const protectionBlockReasons = preview?.protection_block_reasons ?? []

  if (loading) {
    return (
      <div className="p-3 text-center text-xs rounded" style={cardStyle}>
        Loading runtime capability preview...
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-3 text-center text-xs rounded" style={{ ...cardStyle, color: '#F6465D' }}>
        Runtime capability preview failed: {error}
      </div>
    )
  }

  if (!preview) {
    return (
      <div className="p-3 text-center text-xs rounded" style={cardStyle}>
        No runtime capability data available.
      </div>
    )
  }

  const badgeBase = 'px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider'

  return (
    <div className="rounded-lg overflow-hidden" style={cardStyle}>
      <div
        className="flex items-center justify-between p-3 cursor-pointer hover:bg-[#1E2329] transition-colors"
        onClick={() => setExpanded((v) => !v)}
      >
        <div className="flex items-center gap-2">
          <Shield className="w-4 h-4" style={{ color: '#F0B90B' }} />
          <span className="font-medium text-sm" style={{ color: '#EAECEF' }}>
            运行时能力预览
          </span>
          <span className={badgeBase} style={{ background: '#F0B90B22', color: '#F0B90B' }}>
            {translateTag(preview.exchange)}
          </span>
          <span className={badgeBase} style={{ background: '#0ECB8122', color: '#0ECB81' }}>
            {translateTag(preview.mode)}
          </span>
          <span className={badgeBase} style={{ background: '#848E9C22', color: '#848E9C' }}>
            {preview.execution_mode}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <span className={badgeBase} style={{ background: preview.allow_order_placement ? '#0ECB8122' : '#F6465D22', color: preview.allow_order_placement ? '#0ECB81' : '#F6465D' }}>
            {preview.allow_order_placement ? 'order on' : 'order off'}
          </span>
          {expanded ? (
            <ChevronUp className="w-4 h-4" style={{ color: '#848E9C' }} />
          ) : (
            <ChevronDown className="w-4 h-4" style={{ color: '#848E9C' }} />
          )}
        </div>
      </div>

      {expanded && (
        <div className="px-3 pb-3 space-y-3">
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Decision Style</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{profile?.decision_style ?? '-'}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Protection Mode</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{profile?.protection_mode ?? '-'}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Scale-in Cap</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{profile?.max_scale_in_count ?? 0}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Risk Budget</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{(profile?.max_position_risk_pct ?? 0).toFixed(1)}%</div>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Scale-In Status</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{preview.scale_in_status || '-'}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Scale-In Count</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{preview.scale_in_count ?? 0}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Scale-In Consistency</div>
              <div className="text-sm font-mono mt-1" style={{ color: preview.scale_in_consistency === 'mismatch' ? '#F6465D' : '#EAECEF' }}>
                {preview.scale_in_consistency || 'unknown'}
              </div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Risk Budget Left</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(preview.risk_budget_remaining)}</div>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Adjustment Status</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{preview.protection_adjustment_status || '-'}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Adj. Consistency</div>
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
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Cancel/Replace</div>
              <div className="text-sm font-mono mt-1" style={{ color: preview.has_pending_cancel_replace ? '#F6465D' : '#0ECB81' }}>
                {preview.has_pending_cancel_replace ? 'active' : 'clear'}
              </div>
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
              <span className="text-xs" style={{ color: '#848E9C' }}>Move Budget</span>
              <span className="font-mono text-[11px]" style={{ color: '#EAECEF' }}>
                {formatQty(preview.remaining_move_budget)}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Protection Rev.</span>
              <span className="font-mono text-[11px]" style={{ color: '#EAECEF' }}>
                {preview.protection_revision ?? 0}
              </span>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            {[
              ['Add Position', profile?.allow_add_position],
              ['Partial TP', profile?.allow_partial_take_profit],
              ['Move Stop', profile?.allow_move_stop_loss],
              ['Trailing Stop', profile?.allow_trailing_stop],
            ].map(([label, enabled]) => (
              <div key={label as string} className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
                <span className="text-xs" style={{ color: '#848E9C' }}>{label as string}</span>
                <span style={{ color: enabled ? '#0ECB81' : '#F6465D' }}>
                  {enabled ? <Unlock className="w-3 h-3" /> : <Lock className="w-3 h-3" />}
                </span>
              </div>
            ))}
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-2">
            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <AlertTriangle className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Add Position Gate</span>
              </div>
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div>
                  <div style={{ color: '#5E6673' }}>Allowed</div>
                  <div className="font-mono" style={{ color: allowed.includes('add_position') ? '#0ECB81' : '#F6465D' }}>
                    {allowed.includes('add_position') ? 'yes' : 'no'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Blocked</div>
                  <div className="font-mono" style={{ color: blocked.includes('add_position') ? '#F6465D' : '#0ECB81' }}>
                    {blocked.includes('add_position') ? 'yes' : 'no'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Pending Add Qty</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{formatQty(preview.pending_add_qty)}</div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Pending Reduce Qty</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{formatQty(preview.pending_reduce_qty)}</div>
                </div>
              </div>
            </div>

            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <AlertTriangle className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Add Position Reasons</span>
              </div>
              <div className="space-y-1 max-h-56 overflow-auto pr-1">
                {scaleInReasons.length > 0 ? scaleInReasons.map((reason, index) => (
                  <div key={`${reason.category}-${index}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                    <span className="font-mono" style={{ color: '#F0B90B' }}>{reason.category}</span>
                    <span style={{ color: '#848E9C' }}> · </span>
                    <span>{reason.reason}</span>
                  </div>
                )) : (
                  <div className="text-xs" style={{ color: '#848E9C' }}>No add-position block reasons.</div>
                )}
              </div>
            </div>
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-2">
            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <AlertTriangle className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Protection Adjustment Reasons</span>
              </div>
              <div className="space-y-1 max-h-56 overflow-auto pr-1">
                {protectionAdjustmentReasons.length > 0 ? protectionAdjustmentReasons.map((reason, index) => (
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
                <Shield className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Protection Adjustment Gate</span>
              </div>
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div>
                  <div style={{ color: '#5E6673' }}>Allowed</div>
                  <div className="font-mono" style={{ color: preview.can_move_stop_loss || preview.can_arm_break_even || preview.can_arm_trailing ? '#0ECB81' : '#F6465D' }}>
                    {preview.can_move_stop_loss || preview.can_arm_break_even || preview.can_arm_trailing ? 'yes' : 'no'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Pending Cancel/Replace</div>
                  <div className="font-mono" style={{ color: preview.has_pending_cancel_replace ? '#F6465D' : '#0ECB81' }}>
                    {preview.has_pending_cancel_replace ? 'yes' : 'no'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Break-even Ready</div>
                  <div className="font-mono" style={{ color: preview.can_arm_break_even ? '#0ECB81' : '#F6465D' }}>
                    {preview.can_arm_break_even ? 'yes' : 'no'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Trailing Ready</div>
                  <div className="font-mono" style={{ color: preview.can_arm_trailing ? '#0ECB81' : '#F6465D' }}>
                    {preview.can_arm_trailing ? 'yes' : 'no'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Move Stop Ready</div>
                  <div className="font-mono" style={{ color: preview.can_move_stop_loss ? '#0ECB81' : '#F6465D' }}>
                    {preview.can_move_stop_loss ? 'yes' : 'no'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Current Stop</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>
                    {formatPrice(preview.current_stop_loss_price)}
                  </div>
                </div>
              </div>
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
              <span className="text-xs" style={{ color: '#848E9C' }}>SL Armed</span>
              <span style={{ color: preview.stop_loss_armed ? '#0ECB81' : '#F6465D' }}>
                {preview.stop_loss_armed ? 'yes' : 'no'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>TP Armed</span>
              <span style={{ color: preview.take_profit_armed ? '#0ECB81' : '#F6465D' }}>
                {preview.take_profit_armed ? 'yes' : 'no'}
              </span>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            {[
              ['User Stream', preview.user_stream_ready],
              ['Working Orders', preview.has_working_orders],
              ['Cancel/Replace', preview.has_pending_cancel_replace],
              ['State Match', !preview.has_state_mismatch],
            ].map(([label, enabled]) => (
              <div key={label as string} className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
                <span className="text-xs" style={{ color: '#848E9C' }}>{label as string}</span>
                <span style={{ color: enabled ? '#0ECB81' : '#F6465D' }}>
                  {enabled ? <Unlock className="w-3 h-3" /> : <Lock className="w-3 h-3" />}
                </span>
              </div>
            ))}
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-2">
            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <Activity className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Selected Aggregate</span>
              </div>
              {aggregate ? (
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <div>
                    <div style={{ color: '#5E6673' }}>Symbol / Side</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{aggregate.symbol} / {aggregate.side}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Execution Eligible</div>
                    <div className="font-mono" style={{ color: aggregate.execution_eligible ? '#0ECB81' : '#F6465D' }}>
                      {aggregate.execution_eligible ? 'yes' : 'no'}
                    </div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Total / Available</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>
                      {formatQty(aggregate.total_qty)} / {formatQty(aggregate.available_qty)}
                    </div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Pending Add / Reduce</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>
                      {formatQty(aggregate.pending_add_qty)} / {formatQty(aggregate.pending_reduce_qty)}
                    </div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Avg Entry</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatPrice(aggregate.avg_entry_price)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Peak PnL</div>
                    <div className="font-mono" style={{ color: aggregate.peak_pnl_pct >= 0 ? '#0ECB81' : '#F6465D' }}>
                      {aggregate.peak_pnl_pct.toFixed(2)}%
                    </div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Realized / Unrealized</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>
                      {aggregate.realized_pnl.toFixed(2)} / {aggregate.unrealized_pnl.toFixed(2)}
                    </div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Selected Symbol</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.selected_symbol ?? '-'}</div>
                  </div>
                </div>
              ) : (
                <div className="text-xs" style={{ color: '#848E9C' }}>
                  No open aggregate selected.
                </div>
              )}
            </div>

            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <AlertTriangle className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Runtime Output</span>
              </div>
              <div className="space-y-2">
                <div>
                  <div className="text-[10px] uppercase tracking-wider mb-1" style={{ color: '#848E9C' }}>Allowed Actions</div>
                  <div className="flex flex-wrap gap-1">
                    {allowed.length > 0 ? allowed.map((action) => (
                      <span key={action} className="px-2 py-0.5 rounded text-[10px] font-mono" style={{ background: '#0ECB8122', color: '#0ECB81' }}>
                        {action}
                      </span>
                    )) : <span className="text-xs" style={{ color: '#848E9C' }}>None</span>}
                  </div>
                </div>
                <div className="p-2 rounded" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
                  <div className="text-[10px] uppercase tracking-wider mb-1" style={{ color: '#848E9C' }}>set_protection</div>
                  <div className="font-mono text-[11px]" style={{ color: allowed.includes('set_protection') ? '#0ECB81' : '#F6465D' }}>
                    {allowed.includes('set_protection') ? 'open' : 'blocked'}
                  </div>
                  {allowed.includes('set_protection') ? (
                    <div className="text-[11px] mt-1" style={{ color: '#848E9C' }}>
                      eligible to attach fixed protection
                    </div>
                  ) : (
                    <div className="text-[11px] mt-1" style={{ color: '#848E9C' }}>
                      use block reasons below to see why it is blocked
                    </div>
                  )}
                </div>
                <div>
                <div className="text-[10px] uppercase tracking-wider mb-1" style={{ color: '#848E9C' }}>Blocked Actions</div>
                <div className="flex flex-wrap gap-1">
                  {blocked.length > 0 ? blocked.map((action) => (
                    <span key={action} className="px-2 py-0.5 rounded text-[10px] font-mono" style={{ background: '#F6465D22', color: '#F6465D' }}>
                      {action}
                      </span>
                    )) : <span className="text-xs" style={{ color: '#848E9C' }}>None</span>}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-wider mb-1" style={{ color: '#848E9C' }}>Order-State Blocks</div>
                  <div className="grid grid-cols-2 gap-2 text-[11px]">
                    <div className="p-2 rounded" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
                      <div style={{ color: '#5E6673' }}>Pending Add</div>
                      <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.pending_add_qty.toFixed(6)}</div>
                    </div>
                    <div className="p-2 rounded" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
                      <div style={{ color: '#5E6673' }}>Pending Reduce</div>
                      <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.pending_reduce_qty.toFixed(6)}</div>
                    </div>
                  </div>
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-wider mb-1" style={{ color: '#848E9C' }}>Mismatch Reasons</div>
                  <div className="space-y-1 max-h-40 overflow-auto pr-1">
                    {mismatchReasons.length > 0 ? mismatchReasons.map((reason, index) => (
                      <div key={`${reason.category}-${reason.reason}-${index}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                        <span className="font-mono" style={{ color: '#F0B90B' }}>{reason.category}</span>
                        <span style={{ color: '#848E9C' }}> · </span>
                        <span>{reason.reason}</span>
                      </div>
                    )) : (
                      <div className="text-xs" style={{ color: '#848E9C' }}>No reconcile mismatches.</div>
                    )}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-wider mb-1" style={{ color: '#848E9C' }}>Block Reasons</div>
                  <div className="space-y-1 max-h-40 overflow-auto pr-1">
                    {preview.block_reasons.length > 0 ? preview.block_reasons.map((reason, index) => (
                      <div key={`${reason.action}-${index}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                        <span className="font-mono" style={{ color: '#F0B90B' }}>{reason.action}</span>
                        <span style={{ color: '#848E9C' }}> · </span>
                        <span style={{ color: '#848E9C' }}>{reason.category}</span>
                        <div style={{ color: '#EAECEF' }}>{reason.reason}</div>
                      </div>
                    )) : <span className="text-xs" style={{ color: '#848E9C' }}>No block reasons.</span>}
                  </div>
                </div>
              </div>
            </div>

            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <AlertTriangle className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Scale-Out Gate</span>
              </div>
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div>
                  <div style={{ color: '#5E6673' }}>reduce_position</div>
                  <div className="font-mono" style={{ color: allowed.includes('reduce_position') ? '#0ECB81' : '#F6465D' }}>
                    {allowed.includes('reduce_position') ? 'open' : 'blocked'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>arm_partial_take_profit</div>
                  <div className="font-mono" style={{ color: allowed.includes('arm_partial_take_profit') ? '#0ECB81' : '#F6465D' }}>
                    {allowed.includes('arm_partial_take_profit') ? 'open' : 'blocked'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Scale-Out Status</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.scale_out_status || '-'}</div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Scale-Out Consistency</div>
                  <div className="font-mono" style={{ color: preview.scale_out_consistency === 'mismatch' ? '#F6465D' : '#EAECEF' }}>{preview.scale_out_consistency || 'unknown'}</div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Has Scale-Out Plan</div>
                  <div className="font-mono" style={{ color: preview.has_scale_out_plan ? '#0ECB81' : '#F6465D' }}>
                    {preview.has_scale_out_plan ? 'yes' : 'no'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Protection Rebalanced</div>
                  <div className="font-mono" style={{ color: preview.protection_rebalanced ? '#0ECB81' : '#F6465D' }}>
                    {preview.protection_rebalanced ? 'yes' : 'no'}
                  </div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Pending Scale-Out</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{formatQty(preview.pending_scale_out_qty)}</div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Remaining / Executed</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>
                    {formatQty(preview.remaining_scale_out_qty)} / {formatQty(preview.executed_scale_out_qty)}
                  </div>
                </div>
              </div>
              <div className="mt-3">
                <div className="text-[10px] uppercase tracking-wider mb-1" style={{ color: '#848E9C' }}>Scale-Out Block Reasons</div>
                <div className="space-y-1 max-h-40 overflow-auto pr-1">
                  {preview.scale_out_block_reasons.length > 0 ? preview.scale_out_block_reasons.map((reason, index) => (
                    <div key={`${reason.action}-${index}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                      <span className="font-mono" style={{ color: '#F0B90B' }}>{reason.action}</span>
                      <span style={{ color: '#848E9C' }}> · </span>
                      <span style={{ color: '#848E9C' }}>{reason.category}</span>
                      <div style={{ color: '#EAECEF' }}>{reason.reason}</div>
                    </div>
                  )) : <span className="text-xs" style={{ color: '#848E9C' }}>No scale-out block reasons.</span>}
                </div>
              </div>
            </div>

            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <AlertTriangle className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Protection Block Reasons</span>
              </div>
              <div className="space-y-1 max-h-64 overflow-auto pr-1">
                {protectionBlockReasons.length > 0 ? protectionBlockReasons.map((reason, index) => (
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

          <details className="rounded" style={{ background: '#1E2329' }}>
            <summary className="cursor-pointer px-3 py-2 text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>
              Raw reconciliation blobs
            </summary>
            <div className="p-3 grid grid-cols-1 lg:grid-cols-2 gap-2 text-[11px]">
              <div>
                <div className="mb-1 uppercase tracking-wider" style={{ color: '#848E9C' }}>Protection JSON</div>
                <pre className="overflow-auto rounded p-2" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                  {JSON.stringify(protectionState, null, 2)}
                </pre>
              </div>
              <div>
                <div className="mb-1 uppercase tracking-wider" style={{ color: '#848E9C' }}>Scale Plan JSON</div>
                <pre className="overflow-auto rounded p-2" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                  {JSON.stringify(scalePlanState, null, 2)}
                </pre>
              </div>
            </div>
          </details>
        </div>
      )}
    </div>
  )
}
