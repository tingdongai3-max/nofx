import { useCallback, useEffect, useState } from 'react'
import { Activity, AlertTriangle, ChevronDown, ChevronUp, Shield } from 'lucide-react'
import { api } from '../../lib/api'
import type { ScaleOutReconcilePreview } from '../../types'

interface ScaleOutPreviewPanelProps {
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

export function ScaleOutPreviewPanel({
  traderId,
  refreshInterval = 8000,
  selectedSymbol,
}: ScaleOutPreviewPanelProps) {
  const [preview, setPreview] = useState<ScaleOutReconcilePreview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState(true)

  const loadPreview = useCallback(async () => {
    try {
      const data = await api.getRuntimeScaleOutPreview(traderId, selectedSymbol, true)
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
        Loading scale-out preview...
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-3 text-center text-xs rounded" style={{ ...cardStyle, color: '#F6465D' }}>
        Scale-out preview failed: {error}
      </div>
    )
  }

  if (!preview) {
    return (
      <div className="p-3 text-center text-xs rounded" style={cardStyle}>
        No scale-out data available.
      </div>
    )
  }

  const plan = preview.scale_out_plan
  const levels = preview.scale_out_levels ?? []
  const events = preview.scale_out_event_logs ?? []
  const orderSummary = preview.order_registry_summary
  const protectionSummary = preview.protection_summary
  const aggregate = preview.position_aggregate
  const mismatchReasons = preview.mismatch_reasons ?? []
  const blockReasons = preview.scale_out_block_reasons ?? []

  return (
    <div className="rounded-lg overflow-hidden" style={cardStyle}>
      <div
        className="flex items-center justify-between p-3 cursor-pointer hover:bg-[#1E2329] transition-colors"
        onClick={() => setExpanded((v) => !v)}
      >
        <div className="flex items-center gap-2">
          <Shield className="w-4 h-4" style={{ color: '#F0B90B' }} />
          <span className="font-medium text-sm" style={{ color: '#EAECEF' }}>
            分批止盈预览
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: '#F0B90B22', color: '#F0B90B' }}>
            {translateTag(preview.exchange)}
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: '#0ECB8122', color: '#0ECB81' }}>
            {translateTag(preview.mode)}
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: preview.scale_out_consistency === 'mismatch' ? '#F6465D22' : '#848E9C22', color: preview.scale_out_consistency === 'mismatch' ? '#F6465D' : '#EAECEF' }}>
            {translateTag(preview.scale_out_consistency)}
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
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Has Plan</span>
              <span style={{ color: preview.has_scale_out_plan ? '#0ECB81' : '#F6465D' }}>
                {preview.has_scale_out_plan ? 'yes' : 'no'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Working Orders</span>
              <span style={{ color: preview.has_working_orders ? '#0ECB81' : '#F6465D' }}>
                {preview.has_working_orders ? 'yes' : 'no'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>State Match</span>
              <span style={{ color: preview.has_state_mismatch ? '#F6465D' : '#0ECB81' }}>
                {preview.has_state_mismatch ? 'no' : 'yes'}
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
              <span className="text-xs" style={{ color: '#848E9C' }}>Add Position Conflict</span>
              <span style={{ color: preview.has_scale_out_plan && preview.scale_out_status !== 'completed' ? '#F6465D' : '#0ECB81' }}>
                {preview.has_scale_out_plan && preview.scale_out_status !== 'completed' ? 'yes' : 'no'}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Protected Qty</span>
              <span className="font-mono text-[11px]" style={{ color: '#EAECEF' }}>
                {formatQty(protectionSummary?.protected_quantity ?? 0)}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Pending Add</span>
              <span className="font-mono text-[11px]" style={{ color: '#EAECEF' }}>
                {formatQty(preview.pending_add_qty)}
              </span>
            </div>
            <div className="p-2 rounded flex items-center justify-between" style={{ background: '#1E2329' }}>
              <span className="text-xs" style={{ color: '#848E9C' }}>Add Block State</span>
              <span style={{ color: preview.has_scale_out_plan && preview.scale_out_status !== 'completed' ? '#F6465D' : '#0ECB81' }}>
                {preview.has_scale_out_plan && preview.scale_out_status !== 'completed' ? 'blocked' : 'clear'}
              </span>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Status</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{preview.scale_out_status || '-'}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Pending Qty</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(preview.pending_scale_out_qty)}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Remaining Qty</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(preview.remaining_scale_out_qty)}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Executed Qty</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{formatQty(preview.executed_scale_out_qty)}</div>
            </div>
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-2">
            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <Activity className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Plan Snapshot</span>
              </div>
              {plan ? (
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <div>
                    <div style={{ color: '#5E6673' }}>Plan ID</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{plan.scale_out_plan_id}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Plan Mode</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{plan.plan_mode}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Linked Position</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{plan.linked_position_key}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Last Updated</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatTime(plan.updated_at)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Total / Remaining</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatQty(plan.total_planned_qty)} / {formatQty(plan.remaining_planned_qty)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Executed</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatQty(plan.executed_qty)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Selected Symbol</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.selected_symbol ?? selectedSymbol ?? '-'}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>User Stream</div>
                    <div className="font-mono" style={{ color: preview.user_stream_ready ? '#0ECB81' : '#F6465D' }}>
                      {preview.user_stream_ready ? 'ready' : 'not ready'}
                    </div>
                  </div>
                </div>
              ) : (
                <div className="text-xs" style={{ color: '#848E9C' }}>No active plan row.</div>
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
                  <div className="text-xs" style={{ color: '#848E9C' }}>No scale-out mismatches.</div>
                )}
              </div>
            </div>
          </div>

          <div className="p-3 rounded" style={{ background: '#1E2329' }}>
            <div className="flex items-center gap-2 mb-2">
              <Activity className="w-3 h-3" style={{ color: '#F0B90B' }} />
              <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Level Snapshot</span>
            </div>
            <div className="overflow-x-auto max-h-80 overflow-y-auto custom-scrollbar">
              <table className="w-full text-xs">
                <thead className="text-left border-b border-white/5">
                  <tr>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap">Idx</th>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap">Target</th>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap text-right">Price</th>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap text-right">Planned</th>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap text-right">Exec</th>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap text-right">Remain</th>
                    <th className="px-1 pb-2 font-semibold text-nofx-text-muted whitespace-nowrap text-center">Status</th>
                  </tr>
                </thead>
                <tbody>
                  {levels.length > 0 ? levels.map((level) => (
                    <tr key={`${level.scale_out_plan_id}-${level.level_index}`} className="border-b border-white/5 last:border-0">
                      <td className="px-1 py-2 font-mono text-nofx-text-main whitespace-nowrap">{level.level_index}</td>
                      <td className="px-1 py-2 text-nofx-text-main whitespace-nowrap">{level.target_type}</td>
                      <td className="px-1 py-2 font-mono text-right text-nofx-text-main">{formatPrice(level.target_price)}</td>
                      <td className="px-1 py-2 font-mono text-right text-nofx-text-main">{formatQty(level.planned_qty)}</td>
                      <td className="px-1 py-2 font-mono text-right text-nofx-text-main">{formatQty(level.executed_qty)}</td>
                      <td className="px-1 py-2 font-mono text-right text-nofx-text-main">{formatQty(level.remaining_qty)}</td>
                      <td className="px-1 py-2 text-center">
                        <span className="font-mono" style={{ color: level.status === 'filled' ? '#0ECB81' : level.status === 'cancelled' || level.status === 'invalid' ? '#F6465D' : '#EAECEF' }}>
                          {level.status}
                        </span>
                      </td>
                    </tr>
                  )) : (
                    <tr>
                      <td className="px-1 py-4 text-center text-nofx-text-muted" colSpan={7}>No scale-out levels.</td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-3 gap-2">
            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <Shield className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Order Registry</span>
              </div>
              {orderSummary ? (
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <div>
                    <div style={{ color: '#5E6673' }}>Working / Pending</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{orderSummary.working_order_count} / {orderSummary.pending_order_ids.length}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Pending Reduce</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatQty(orderSummary.pending_reduce_qty)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Pending Add</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatQty(orderSummary.pending_add_qty)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Coverage</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatQty(orderSummary.protection_coverage_qty)}</div>
                  </div>
                </div>
              ) : (
                <div className="text-xs" style={{ color: '#848E9C' }}>No registry summary available.</div>
              )}
            </div>

            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <Shield className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Protection Summary</span>
              </div>
              {protectionSummary ? (
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <div>
                    <div style={{ color: '#5E6673' }}>Status</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{protectionSummary.protection_group_status}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Protected Qty</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatQty(protectionSummary.protected_quantity)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>SL / TP Armed</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>
                      {protectionSummary.stop_loss_armed ? 'SL' : '--'} / {protectionSummary.take_profit_armed ? 'TP' : '--'}
                    </div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Last Synced</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatTime(protectionSummary.last_synced_at)}</div>
                  </div>
                </div>
              ) : (
                <div className="text-xs" style={{ color: '#848E9C' }}>No protection summary available.</div>
              )}
            </div>

            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="flex items-center gap-2 mb-2">
                <Shield className="w-3 h-3" style={{ color: '#F0B90B' }} />
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Position Aggregate</span>
              </div>
              {aggregate ? (
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <div>
                    <div style={{ color: '#5E6673' }}>Available / Total</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatQty(aggregate.available_qty)} / {formatQty(aggregate.total_qty)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Pending Reduce</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatQty(aggregate.pending_reduce_qty)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Scale-Out Remaining</div>
                    <div className="font-mono" style={{ color: '#EAECEF' }}>{formatQty(aggregate.remaining_scale_out_qty)}</div>
                  </div>
                  <div>
                    <div style={{ color: '#5E6673' }}>Execution Eligible</div>
                    <div className="font-mono" style={{ color: aggregate.execution_eligible ? '#0ECB81' : '#F6465D' }}>
                      {aggregate.execution_eligible ? 'yes' : 'no'}
                    </div>
                  </div>
                </div>
              ) : (
                <div className="text-xs" style={{ color: '#848E9C' }}>No aggregate available.</div>
              )}
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
                      {event.scale_out_plan_id ? ` · ${event.scale_out_plan_id}` : ''}
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
                <span className="text-xs uppercase tracking-wider" style={{ color: '#848E9C' }}>Block Reasons</span>
              </div>
              <div className="space-y-1 max-h-64 overflow-auto pr-1">
                {blockReasons.length > 0 ? blockReasons.map((reason, index) => (
                  <div key={`${reason.action}-${index}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                    <span className="font-mono" style={{ color: '#F0B90B' }}>{reason.action}</span>
                    <span style={{ color: '#848E9C' }}> · </span>
                    <span style={{ color: '#848E9C' }}>{reason.category}</span>
                    <div style={{ color: '#EAECEF' }}>{reason.reason}</div>
                  </div>
                )) : (
                  <div className="text-xs" style={{ color: '#848E9C' }}>No scale-out block reasons.</div>
                )}
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
