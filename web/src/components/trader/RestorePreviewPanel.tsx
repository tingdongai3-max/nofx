import { useCallback, useEffect, useState } from 'react'
import { ChevronDown, ChevronUp, DatabaseZap, AlertTriangle } from 'lucide-react'
import { api } from '../../lib/api'
import type { RestorePreview } from '../../types'

interface RestorePreviewPanelProps {
  traderId: string
  selectedSymbol?: string
  refreshInterval?: number
}

export function RestorePreviewPanel({
  traderId,
  selectedSymbol,
  refreshInterval = 15000,
}: RestorePreviewPanelProps) {
  const [preview, setPreview] = useState<RestorePreview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState(true)

  const loadPreview = useCallback(async () => {
    try {
      const data = await api.getRuntimeRestorePreview(traderId, selectedSymbol, true)
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
        Loading restore preview...
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-3 text-center text-xs rounded" style={{ ...cardStyle, color: '#F6465D' }}>
        Restore preview failed: {error}
      </div>
    )
  }

  if (!preview) {
    return (
      <div className="p-3 text-center text-xs rounded" style={cardStyle}>
        No restore preview data available.
      </div>
    )
  }

  return (
    <div className="rounded-lg overflow-hidden" style={cardStyle}>
      <div
        className="flex items-center justify-between p-3 cursor-pointer hover:bg-[#1E2329] transition-colors"
        onClick={() => setExpanded((v) => !v)}
      >
        <div className="flex items-center gap-2">
          <DatabaseZap className="w-4 h-4" style={{ color: '#F0B90B' }} />
          <span className="font-medium text-sm" style={{ color: '#EAECEF' }}>
            Restore Preview
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: '#F0B90B22', color: '#F0B90B' }}>
            {preview.restore_scope}
          </span>
          <span className="px-2 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wider" style={{ background: preview.has_mismatch ? '#F6465D22' : '#0ECB8122', color: preview.has_mismatch ? '#F6465D' : '#0ECB81' }}>
            {preview.has_mismatch ? 'mismatch' : 'clean'}
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
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Snapshot</div>
              <div className="text-xs font-mono mt-1 break-all" style={{ color: '#EAECEF' }}>{preview.truth_snapshot_version}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Before Qty</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{preview.before_snapshot?.position_aggregate?.total_qty ?? 0}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>After Qty</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{preview.after_snapshot?.position_aggregate?.total_qty ?? 0}</div>
            </div>
            <div className="p-2 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Symbol</div>
              <div className="text-sm font-mono mt-1" style={{ color: '#EAECEF' }}>{preview.selected_symbol || '-'}</div>
            </div>
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-2">
            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider mb-2" style={{ color: '#848E9C' }}>Before Snapshot</div>
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div>
                  <div style={{ color: '#5E6673' }}>Avg Entry</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.before_snapshot?.position_aggregate?.avg_entry_price ?? 0}</div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Protected Qty</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.before_snapshot?.protection_summary?.protected_quantity ?? 0}</div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Pending Reduce</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.before_snapshot?.order_summary?.pending_reduce_qty ?? 0}</div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Pending Add</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.before_snapshot?.order_summary?.pending_add_qty ?? 0}</div>
                </div>
              </div>
            </div>

            <div className="p-3 rounded" style={{ background: '#1E2329' }}>
              <div className="text-[10px] uppercase tracking-wider mb-2" style={{ color: '#848E9C' }}>After Snapshot</div>
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div>
                  <div style={{ color: '#5E6673' }}>Avg Entry</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.after_snapshot?.position_aggregate?.avg_entry_price ?? 0}</div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Protected Qty</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.after_snapshot?.protection_summary?.protected_quantity ?? 0}</div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Pending Reduce</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.after_snapshot?.order_summary?.pending_reduce_qty ?? 0}</div>
                </div>
                <div>
                  <div style={{ color: '#5E6673' }}>Pending Add</div>
                  <div className="font-mono" style={{ color: '#EAECEF' }}>{preview.after_snapshot?.order_summary?.pending_add_qty ?? 0}</div>
                </div>
              </div>
            </div>
          </div>

          <div className="p-3 rounded" style={{ background: '#1E2329' }}>
            <div className="text-[10px] uppercase tracking-wider mb-2" style={{ color: '#848E9C' }}>Capability Result</div>
            <div className="text-xs" style={{ color: '#EAECEF' }}>
              Allowed: {(preview.capability_preview?.allowed_actions ?? []).join(', ') || '-'}
            </div>
            <div className="text-xs mt-1" style={{ color: '#848E9C' }}>
              Blocked: {(preview.capability_preview?.blocked_actions ?? []).join(', ') || '-'}
            </div>
          </div>

          <div className="p-3 rounded" style={{ background: '#1E2329' }}>
            <div className="flex items-center gap-2 mb-2">
              <AlertTriangle className="w-3 h-3" style={{ color: preview.has_mismatch ? '#F6465D' : '#0ECB81' }} />
              <span className="text-[10px] uppercase tracking-wider" style={{ color: '#848E9C' }}>Diff Reasons</span>
            </div>
            <div className="space-y-1 max-h-48 overflow-auto pr-1">
              {preview.mismatch_reasons.length ? preview.mismatch_reasons.map((reason, index) => (
                <div key={`${reason.category}-${reason.field}-${index}`} className="rounded px-2 py-1 text-[11px]" style={{ background: '#0B0E11', color: '#EAECEF', border: '1px solid #2B3139' }}>
                  <span className="font-mono" style={{ color: '#F0B90B' }}>{reason.field || reason.category}</span>
                  <span style={{ color: '#848E9C' }}> · </span>
                  <span>{reason.reason}</span>
                </div>
              )) : (
                <div className="text-xs" style={{ color: '#0ECB81' }}>No restore mismatches detected.</div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
