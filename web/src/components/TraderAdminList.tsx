import { useState, useEffect } from 'react'
import { Bot, Play, Square, Trash2, Eye, RefreshCw, Brain, Clock, ListChecks } from 'lucide-react'
import { toast } from 'sonner'
import { httpClient } from '../lib/httpClient'
import { TraderAdmin, CreateTraderAdminRequest, AIModel } from '../types'
import { useLanguage } from '../contexts/LanguageContext'
import { t } from '../i18n/translations'
import { TraderAdminModal } from './TraderAdminModal'

interface TraderAdminListProps {
  availableModels?: AIModel[]
  onViewAnalysis?: (adminId: string) => void
}

export function TraderAdminList({ availableModels = [], onViewAnalysis }: TraderAdminListProps) {
  const { language } = useLanguage()
  const [admins, setAdmins] = useState<TraderAdmin[]>([])
  const [loading, setLoading] = useState(true)
  const [showModal, setShowModal] = useState(false)
  const [editingAdmin, setEditingAdmin] = useState<TraderAdmin | null>(null)
  const [actionLoading, setActionLoading] = useState<string | null>(null)

  useEffect(() => {
    loadAdmins()
  }, [])

  const loadAdmins = async () => {
    setLoading(true)
    try {
      const result = await httpClient.get<{ result: TraderAdmin[] }>('/api/trader-admins')
      if (result.data?.result) {
        setAdmins(result.data.result)
      }
    } catch (error) {
      console.error('Failed to load admins:', error)
      toast.error(t('failedToLoadAdmins', language) || 'Failed to load administrators')
    } finally {
      setLoading(false)
    }
  }

  const handleStart = async (adminId: string) => {
    setActionLoading(adminId + '-start')
    try {
      await httpClient.post(`/api/trader-admins/${adminId}/start`)
      toast.success(t('adminStarted', language) || 'Administrator started')
      loadAdmins()
    } catch (error: any) {
      console.error('Failed to start admin:', error)
      toast.error(error.message || t('failedToStartAdmin', language) || 'Failed to start administrator')
    } finally {
      setActionLoading(null)
    }
  }

  const handleStop = async (adminId: string) => {
    setActionLoading(adminId + '-stop')
    try {
      await httpClient.post(`/api/trader-admins/${adminId}/stop`)
      toast.success(t('adminStopped', language) || 'Administrator stopped')
      loadAdmins()
    } catch (error: any) {
      console.error('Failed to stop admin:', error)
      toast.error(error.message || t('failedToStopAdmin', language) || 'Failed to stop administrator')
    } finally {
      setActionLoading(null)
    }
  }

  const handleDelete = async (adminId: string) => {
    if (!confirm(t('confirmDeleteAdmin', language) || 'Are you sure you want to delete this administrator?')) {
      return
    }

    setActionLoading(adminId + '-delete')
    try {
      await httpClient.delete(`/api/trader-admins/${adminId}`)
      toast.success(t('adminDeleted', language) || 'Administrator deleted')
      loadAdmins()
    } catch (error: any) {
      console.error('Failed to delete admin:', error)
      toast.error(error.message || t('failedToDeleteAdmin', language) || 'Failed to delete administrator')
    } finally {
      setActionLoading(null)
    }
  }

  const handleScan = async (adminId: string) => {
    setActionLoading(adminId + '-scan')
    try {
      await httpClient.post<{ result: any }>(`/api/trader-admins/${adminId}/scan`)
      toast.success(t('scanCompleted', language) || 'Scan completed')
      if (onViewAnalysis) {
        onViewAnalysis(adminId)
      }
    } catch (error: any) {
      console.error('Failed to scan:', error)
      toast.error(error.message || t('failedToScan', language) || 'Failed to run scan')
    } finally {
      setActionLoading(null)
    }
  }

  const handleSave = async (data: CreateTraderAdminRequest) => {
    if (editingAdmin) {
      await httpClient.put(`/api/trader-admins/${editingAdmin.id}`, data)
      toast.success(t('adminUpdated', language) || 'Administrator updated')
    } else {
      await httpClient.post('/api/trader-admins', data)
      toast.success(t('adminCreated', language) || 'Administrator created')
    }
    loadAdmins()
  }

  const openEditModal = (admin: TraderAdmin) => {
    setEditingAdmin(admin)
    setShowModal(true)
  }

  const openCreateModal = () => {
    setEditingAdmin(null)
    setShowModal(true)
  }

  const closeModal = () => {
    setShowModal(false)
    setEditingAdmin(null)
  }

  const formatTime = (timeStr: string) => {
    if (!timeStr) return '-'
    const date = new Date(timeStr)
    return date.toLocaleString()
  }

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-semibold text-white flex items-center gap-2">
          <Bot className="w-5 h-5 text-nofx-gold" />
          {t('traderAdmins', language) || 'Trader Administrators'}
        </h3>
        <button
          onClick={openCreateModal}
          className="px-4 py-2 text-xs font-bold font-mono uppercase tracking-wider bg-nofx-gold text-black rounded hover:bg-yellow-400 transition-all flex items-center gap-2"
        >
          <Bot className="w-4 h-4" />
          {t('createAdmin', language) || 'Create Admin'}
        </button>
      </div>

      {/* List */}
      {loading ? (
        <div className="text-center py-8 text-zinc-500">
          {t('loading', language) || 'Loading...'}
        </div>
      ) : admins.length === 0 ? (
        <div className="text-center py-8 text-zinc-500">
          {t('noAdmins', language) || 'No administrators yet'}
        </div>
      ) : (
        <div className="grid gap-3">
          {admins.map(admin => (
            <div
              key={admin.id}
              className="bg-[#1E2329] rounded-lg border border-white/10 p-4 hover:border-white/20 transition-colors"
            >
              <div className="flex items-start justify-between">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 mb-2">
                    <h4 className="text-white font-medium">{admin.name}</h4>
                    {admin.is_running ? (
                      <span className="px-2 py-0.5 text-xs rounded bg-green-500/20 text-green-400 flex items-center gap-1">
                        <span className="w-1.5 h-1.5 rounded-full bg-green-400"></span>
                        {t('running', language) || 'RUN'}
                      </span>
                    ) : (
                      <span className="px-2 py-0.5 text-xs rounded bg-zinc-500/20 text-zinc-400">
                        {t('stopped', language) || 'STOPPED'}
                      </span>
                    )}
                  </div>

                  <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-sm">
                    <div className="flex items-center gap-1.5 text-zinc-400">
                      <Brain className="w-3.5 h-3.5" />
                      <span className="truncate">{admin.ai_model_id || '-'}</span>
                    </div>
                    <div className="flex items-center gap-1.5 text-zinc-400">
                      <ListChecks className="w-3.5 h-3.5" />
                      <span>{admin.managed_trader_ids?.length || 0} {t('traders', language) || 'traders'}</span>
                    </div>
                    <div className="flex items-center gap-1.5 text-zinc-400">
                      <Clock className="w-3.5 h-3.5" />
                      <span>{admin.scan_interval_mins} {t('min', language) || 'min'}</span>
                    </div>
                    <div className="flex items-center gap-1.5 text-zinc-400">
                      <RefreshCw className="w-3.5 h-3.5" />
                      <span>{formatTime(admin.last_scan_time)}</span>
                    </div>
                  </div>
                </div>

                {/* Actions */}
                <div className="flex items-center gap-1 ml-4">
                  {admin.is_running ? (
                    <button
                      onClick={() => handleStop(admin.id)}
                      disabled={actionLoading === admin.id + '-stop'}
                      className="p-2 rounded hover:bg-orange-500/20 text-orange-400 transition-colors disabled:opacity-50"
                      title={t('stop', language) || 'Stop'}
                    >
                      <Square className="w-4 h-4" />
                    </button>
                  ) : (
                    <button
                      onClick={() => handleStart(admin.id)}
                      disabled={actionLoading === admin.id + '-start'}
                      className="p-2 rounded hover:bg-green-500/20 text-green-400 transition-colors disabled:opacity-50"
                      title={t('start', language) || 'Start'}
                    >
                      <Play className="w-4 h-4" />
                    </button>
                  )}

                  <button
                    onClick={() => handleScan(admin.id)}
                    disabled={actionLoading === admin.id + '-scan'}
                    className="p-2 rounded hover:bg-blue-500/20 text-blue-400 transition-colors disabled:opacity-50"
                    title={t('scanNow', language) || 'Scan Now'}
                  >
                    <RefreshCw className={`w-4 h-4 ${actionLoading === admin.id + '-scan' ? 'animate-spin' : ''}`} />
                  </button>

                  {onViewAnalysis && (
                    <button
                      onClick={() => onViewAnalysis(admin.id)}
                      className="p-2 rounded hover:bg-purple-500/20 text-purple-400 transition-colors"
                      title={t('viewAnalysis', language) || 'View Analysis'}
                    >
                      <Eye className="w-4 h-4" />
                    </button>
                  )}

                  <button
                    onClick={() => openEditModal(admin)}
                    className="p-2 rounded hover:bg-white/10 text-zinc-400 transition-colors"
                    title={t('edit', language) || 'Edit'}
                  >
                    <Bot className="w-4 h-4" />
                  </button>

                  <button
                    onClick={() => handleDelete(admin.id)}
                    disabled={actionLoading === admin.id + '-delete'}
                    className="p-2 rounded hover:bg-red-500/20 text-red-400 transition-colors disabled:opacity-50"
                    title={t('delete', language) || 'Delete'}
                  >
                    <Trash2 className="w-4 h-4" />
                  </button>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Modal */}
      {showModal && (
        <TraderAdminModal
          isOpen={showModal}
          onClose={closeModal}
          adminData={editingAdmin}
          isEditMode={!!editingAdmin}
          availableModels={availableModels}
          onSave={handleSave}
        />
      )}
    </div>
  )
}
