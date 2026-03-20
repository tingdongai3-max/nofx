interface TrailingTakeProfitEditorProps {
  activationPct?: number
  retracePct?: number
  onActivationChange: (value: number) => void
  onRetraceChange: (value: number) => void
  disabled?: boolean
  language: string
}

export function TrailingTakeProfitEditor({
  activationPct = 0,
  retracePct = 0,
  onActivationChange,
  onRetraceChange,
  disabled,
  language,
}: TrailingTakeProfitEditorProps) {
  const isZh = language === 'zh'

  return (
    <div className="mt-4">
      <div className="flex items-center gap-2 mb-4">
        <span className="text-sm font-medium" style={{ color: '#F0B90B' }}>
          {isZh ? '追踪止盈' : 'Trailing Take Profit'}
        </span>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div
          className="p-4 rounded-lg"
          style={{ background: '#0B0E11', border: '1px solid #2B3139' }}
        >
          <label className="block text-sm mb-1" style={{ color: '#EAECEF' }}>
            {isZh
              ? '全局追踪激活进度 (%)'
              : 'Global Trailing Activation Target Progress (%)'}
          </label>
          <p className="text-xs mb-2" style={{ color: '#848E9C' }}>
            {isZh
              ? '按当前仓位的止盈目标进度激活追踪止盈。例：AI 止盈目标为 +10%，这里填 80，则价格到 +8% 时激活。设为 0 则仍由 AI 动态决定追踪参数。'
              : 'Activate trailing after the position reaches this percentage of its take-profit target. Example: if AI sets TP at +10% and this is 80, trailing activates at +8%. Set 0 to let AI decide dynamically.'}
          </p>
          <div className="flex items-center gap-2">
            <input
              type="number"
              value={activationPct}
              onChange={(e) =>
                onActivationChange(Math.max(0, parseFloat(e.target.value) || 0))
              }
              disabled={disabled}
              min={0}
              step={0.1}
              className="w-28 px-3 py-2 rounded"
              style={{
                background: '#1E2329',
                border: '1px solid #2B3139',
                color: '#EAECEF',
              }}
            />
            <span style={{ color: '#848E9C' }}>%</span>
          </div>
        </div>

        <div
          className="p-4 rounded-lg"
          style={{ background: '#0B0E11', border: '1px solid #2B3139' }}
        >
          <label className="block text-sm mb-1" style={{ color: '#EAECEF' }}>
            {isZh
              ? '全局追踪利润回吐率 (%)'
              : 'Global Trailing Profit Giveback Ratio (%)'}
          </label>
          <p className="text-xs mb-2" style={{ color: '#848E9C' }}>
            {isZh
              ? '激活后，允许从利润极值回吐此比例时平仓。例：已获利润达到 10%，这里填 20，则利润回吐到 8% 时触发。这里是对已获利润的占比，不是价格绝对波动百分比。设为 0 则由 AI 动态决定。'
              : 'After activation, close when profit gives back by this ratio from the post-activation extreme. Example: if open profit reaches 10% and this is 20, the position closes after profit falls to 8%. This is a share of earned profit, not an absolute price move. Set 0 to let AI decide dynamically.'}
          </p>
          <div className="flex items-center gap-2">
            <input
              type="number"
              value={retracePct}
              onChange={(e) =>
                onRetraceChange(Math.max(0, parseFloat(e.target.value) || 0))
              }
              disabled={disabled}
              min={0}
              step={0.1}
              className="w-28 px-3 py-2 rounded"
              style={{
                background: '#1E2329',
                border: '1px solid #2B3139',
                color: '#EAECEF',
              }}
            />
            <span style={{ color: '#848E9C' }}>%</span>
          </div>
        </div>
      </div>
    </div>
  )
}
