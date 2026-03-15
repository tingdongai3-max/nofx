import { useState, useEffect } from 'react'
import { X, Brain, AlertTriangle, CheckCircle, TrendingUp, RefreshCw } from 'lucide-react'
import { toast } from 'sonner'
import { httpClient } from '../lib/httpClient'
import { TraderAdmin, TraderAdminAnalysis } from '../types'
import { useLanguage } from '../contexts/LanguageContext'
import { t } from '../i18n/translations'

interface TraderAdminAnalysisProps {
  adminId: string
  onClose: () => void
}

export function TraderAdminAnalysisView({ adminId, onClose }: TraderAdminAnalysisProps) {
  const { language } = useLanguage()
  const [loading, setLoading] = useState(true)
  const [admin, setAdmin] = useState<TraderAdmin | null>(null)
  const [analyses, setAnalyses] = useState<TraderAdminAnalysis[]>([])
  const [selectedAnalysis, setSelectedAnalysis] = useState<TraderAdminAnalysis | null>(null)

  useEffect(() => {
    loadAnalysis()
  }, [adminId])

  const loadAnalysis = async () => {
    setLoading(true)
    try {
      const result = await httpClient.get<{ result: TraderAdminAnalysis[], admin: TraderAdmin }>(
        `/api/trader-admins/${adminId}/analysis?limit=50`
      )
      if (result.data?.result) {
        setAnalyses(result.data.result)
        setAdmin(result.data.admin)
        if (result.data.result.length > 0) {
          setSelectedAnalysis(result.data.result[0])
        }
      }
    } catch (error) {
      console.error('Failed to load analysis:', error)
      toast.error(t('failedToLoadAnalysis', language) || 'Failed to load analysis results')
    } finally {
      setLoading(false)
    }
  }

  const getSeverityColor = (severity?: string) => {
    switch (severity) {
      case 'high': return 'text-red-400'
      case 'medium': return 'text-orange-400'
      case 'low': return 'text-yellow-400'
      default: return 'text-zinc-400'
    }
  }

  const getSeverityBg = (severity?: string) => {
    switch (severity) {
      case 'high': return 'bg-red-500/20'
      case 'medium': return 'bg-orange-500/20'
      case 'low': return 'bg-yellow-500/20'
      default: return 'bg-zinc-500/20'
    }
  }

  const formatTime = (timeStr: string) => {
    if (!timeStr) return '-'
    const date = new Date(timeStr)
    return date.toLocaleString()
  }

  if (!adminId) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />

      {/* Modal */}
      <div className="relative w-full max-w-4xl mx-4 bg-[#1E2329] rounded-lg border border-white/10 shadow-2xl max-h-[90vh] overflow-hidden flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-white/10 bg-black/20">
          <div className="flex items-center gap-3">
            <Brain className="w-5 h-5 text-nofx-gold" />
            <div>
              <h2 className="text-lg font-semibold text-white">
                {t('analysisResults', language) || 'Analysis Results'}
              </h2>
              {admin && (
                <p className="text-sm text-zinc-400">
                  {admin.name} - {analyses.length} {t('scans', language) || 'scans'}
                </p>
              )}
            </div>
          </div>
          <button onClick={onClose} className="p-1 rounded hover:bg-white/10 transition-colors">
            <X className="w-5 h-5 text-zinc-400" />
          </button>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-hidden flex">
          {loading ? (
            <div className="flex-1 flex items-center justify-center text-zinc-500">
              {t('loading', language) || 'Loading...'}
            </div>
          ) : analyses.length === 0 ? (
            <div className="flex-1 flex items-center justify-center text-zinc-500">
              {t('noAnalysisData', language) || 'No analysis data yet. Run a scan first.'}
            </div>
          ) : (
            <>
              {/* Analysis List Sidebar */}
              <div className="w-48 border-r border-white/10 overflow-y-auto">
                {analyses.map((analysis, idx) => (
                  <button
                    key={analysis.id}
                    onClick={() => setSelectedAnalysis(analysis)}
                    className={`w-full text-left px-4 py-3 border-b border-white/5 hover:bg-white/5 transition-colors ${
                      selectedAnalysis?.id === analysis.id ? 'bg-nofx-gold/10 border-l-2 border-l-nofx-gold' : ''
                    }`}
                  >
                    <div className="text-sm text-white truncate">{formatTime(analysis.scan_time)}</div>
                    <div className="text-xs text-zinc-500">{t('scan', language) || 'Scan'} #{idx + 1}</div>
                  </button>
                ))}
              </div>

              {/* Analysis Content */}
              <div className="flex-1 overflow-y-auto p-6">
                {selectedAnalysis && (
                  <div className="space-y-6">
                    {/* Summary */}
                    <div className="bg-[#2A3038] rounded-lg p-4">
                      <h3 className="text-sm font-medium text-zinc-300 mb-3">
                        {t('scanSummary', language) || 'Scan Summary'}
                      </h3>
                      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                        <div>
                          <div className="text-xs text-zinc-500">{t('scanTime', language) || 'Scan Time'}</div>
                          <div className="text-white">{formatTime(selectedAnalysis.scan_time)}</div>
                        </div>
                        <div>
                          <div className="text-xs text-zinc-500">{t('tradersAnalyzed', language) || 'Traders Analyzed'}</div>
                          <div className="text-white">{selectedAnalysis.trader_id ? 1 : 0}</div>
                        </div>
                      </div>
                    </div>

                    {/* Hallucination Report */}
                    {selectedAnalysis.hallucination_data && (
                      <div className="bg-[#2A3038] rounded-lg p-4">
                        <h3 className="text-sm font-medium text-zinc-300 mb-3 flex items-center gap-2">
                          <AlertTriangle className="w-4 h-4 text-yellow-500" />
                          {t('hallucinationReport', language) || 'Hallucination Report'}
                        </h3>
                        <div className="space-y-2">
                          {(() => {
                            const report = selectedAnalysis.hallucination_data as any
                            return (
                              <>
                                <div className="flex items-center gap-2">
                                  <span className="text-zinc-400">{t('status', language) || 'Status'}:</span>
                                  {report.has_hallucination ? (
                                    <span className="flex items-center gap-1 text-red-400">
                                      <AlertTriangle className="w-4 h-4" />
                                      {t('hallucinationsDetected', language) || 'Hallucinations Detected'}
                                    </span>
                                  ) : (
                                    <span className="flex items-center gap-1 text-green-400">
                                      <CheckCircle className="w-4 h-4" />
                                      {t('noIssues', language) || 'No Issues'}
                                    </span>
                                  )}
                                </div>
                                {report.severity && (
                                  <div className={`inline-flex items-center gap-1 px-2 py-1 rounded ${getSeverityBg(report.severity)}`}>
                                    <span className={`text-xs font-medium ${getSeverityColor(report.severity)}`}>
                                      {report.severity.toUpperCase()} {t('severity', language) || 'SEVERITY'}
                                    </span>
                                  </div>
                                )}
                                {report.total_issues !== undefined && (
                                  <div className="text-zinc-400 text-sm">
                                    {t('totalIssues', language) || 'Total Issues'}: {report.total_issues}
                                  </div>
                                )}
                              </>
                            )
                          })()}
                        </div>
                      </div>
                    )}

                    {/* Optimizations */}
                    {selectedAnalysis.optimizations && (
                      <div className="bg-[#2A3038] rounded-lg p-4">
                        <h3 className="text-sm font-medium text-zinc-300 mb-3 flex items-center gap-2">
                          <TrendingUp className="w-4 h-4 text-green-400" />
                          {t('optimizationSuggestions', language) || 'Optimization Suggestions'}
                        </h3>
                        <div className="space-y-2">
                          {(() => {
                            const opt = selectedAnalysis.optimizations as any
                            return (
                              <>
                                {opt.general && opt.general.length > 0 && (
                                  <div className="mb-3">
                                    <div className="text-xs text-zinc-500 mb-1">{t('general', language) || 'General'}</div>
                                    <ul className="space-y-1">
                                      {opt.general.map((item: string, idx: number) => (
                                        <li key={idx} className="text-sm text-zinc-300 flex items-start gap-2">
                                          <span className="text-nofx-gold">•</span>
                                          {item}
                                        </li>
                                      ))}
                                    </ul>
                                  </div>
                                )}
                                {opt.specific && opt.specific.length > 0 && (
                                  <div>
                                    <div className="text-xs text-zinc-500 mb-1">{t('specific', language) || 'Specific'}</div>
                                    <ul className="space-y-1">
                                      {opt.specific.map((item: string, idx: number) => (
                                        <li key={idx} className="text-sm text-zinc-300 flex items-start gap-2">
                                          <span className="text-blue-400">•</span>
                                          {item}
                                        </li>
                                      ))}
                                    </ul>
                                  </div>
                                )}
                              </>
                            )
                          })()}
                        </div>
                      </div>
                    )}

                    {/* Raw Data */}
                    {selectedAnalysis.analysis_data && (
                      <div className="bg-[#2A3038] rounded-lg p-4">
                        <h3 className="text-sm font-medium text-zinc-300 mb-3">
                          {t('detailedAnalysis', language) || 'Detailed Analysis'}
                        </h3>
                        <pre className="text-xs text-zinc-400 overflow-x-auto max-h-64">
                          {JSON.stringify(selectedAnalysis.analysis_data, null, 2)}
                        </pre>
                      </div>
                    )}
                  </div>
                )}
              </div>
            </>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between px-6 py-4 border-t border-white/10 bg-black/20">
          <div className="text-sm text-zinc-400">
            {analyses.length} {t('totalScans', language) || 'total scans'}
          </div>
          <button
            onClick={loadAnalysis}
            className="px-4 py-2 text-sm font-medium text-zinc-300 hover:text-white transition-colors flex items-center gap-2"
          >
            <RefreshCw className="w-4 h-4" />
            {t('refresh', language) || 'Refresh'}
          </button>
        </div>
      </div>
    </div>
  )
}
