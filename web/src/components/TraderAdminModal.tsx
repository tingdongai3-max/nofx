import { useEffect, useState } from 'react'
import { X, Bot, Brain, Clock, ListChecks } from 'lucide-react'
import { toast } from 'sonner'
import { httpClient } from '../lib/httpClient'
import {
  TraderAdmin,
  CreateTraderAdminRequest,
  AIModel,
  TraderListItem
} from '../types'
import { useLanguage } from '../contexts/LanguageContext'
import { t } from '../i18n/translations'

interface TraderAdminModalProps {
  isOpen: boolean
  onClose: () => void
  adminData?: TraderAdmin | null
  isEditMode?: boolean
  availableModels?: AIModel[]
  onSave?: (data: CreateTraderAdminRequest) => Promise<void>
}

export function TraderAdminModal({
  isOpen,
  onClose,
  adminData,
  isEditMode = false,
  availableModels = [],
  onSave
}: TraderAdminModalProps) {
  const { language } = useLanguage()
  const [saving, setSaving] = useState(false)
  const [formState, setFormState] = useState({
    name: '',
    ai_model_id: '',
    managed_trader_ids: [] as string[],
    scan_interval_mins: 60
  })
  const [availableTraders, setAvailableTraders] = useState<TraderListItem[]>([])
  const [loadingTraders, setLoadingTraders] = useState(false)

  // Fetch available traders on mount
  useEffect(() => {
    if (isOpen) {
      loadAvailableTraders()
    }
  }, [isOpen])

  // Populate form when editing
  useEffect(() => {
    if (adminData) {
      setFormState({
        name: adminData.name,
        ai_model_id: adminData.ai_model_id,
        managed_trader_ids: adminData.managed_trader_ids,
        scan_interval_mins: adminData.scan_interval_mins
      })
    } else {
      setFormState({
        name: '',
        ai_model_id: availableModels[0]?.id || '',
        managed_trader_ids: [],
        scan_interval_mins: 60
      })
    }
  }, [adminData, availableModels])

  const loadAvailableTraders = async () => {
    setLoadingTraders(true)
    try {
      const result = await httpClient.get<{ result: TraderListItem[] }>('/api/traders/all')
      if (result.data?.result) {
        setAvailableTraders(result.data.result)
      }
    } catch (error) {
      console.error('Failed to load traders:', error)
      toast.error(t('failedToLoadTraders', language) || 'Failed to load traders')
    } finally {
      setLoadingTraders(false)
    }
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()

    if (!formState.name.trim()) {
      toast.error(t('nameRequired', language) || 'Name is required')
      return
    }

    if (!formState.ai_model_id) {
      toast.error(t('modelRequired', language) || 'AI model is required')
      return
    }

    if (formState.managed_trader_ids.length === 0) {
      toast.error(t('selectTraders', language) || 'Please select at least one trader to manage')
      return
    }

    setSaving(true)
    try {
      if (onSave) {
        await onSave(formState)
      } else {
        // Default save behavior
        const endpoint = isEditMode && adminData
          ? `/api/trader-admins/${adminData.id}`
          : '/api/trader-admins'
        const method = isEditMode && adminData ? 'put' : 'post'

        await httpClient[method](endpoint, formState)
        toast.success(t('adminSaved', language) || 'Admin saved successfully')
      }
      onClose()
    } catch (error: any) {
      console.error('Failed to save admin:', error)
      toast.error(error.message || t('failedToSaveAdmin', language) || 'Failed to save admin')
    } finally {
      setSaving(false)
    }
  }

  const toggleTrader = (traderId: string) => {
    setFormState(prev => {
      const ids = prev.managed_trader_ids.includes(traderId)
        ? prev.managed_trader_ids.filter(id => id !== traderId)
        : [...prev.managed_trader_ids, traderId]
      return { ...prev, managed_trader_ids: ids }
    })
  }

  const selectAllTraders = () => {
    setFormState(prev => ({
      ...prev,
      managed_trader_ids: availableTraders.map(t => t.trader_id)
    }))
  }

  const deselectAllTraders = () => {
    setFormState(prev => ({
      ...prev,
      managed_trader_ids: []
    }))
  }

  if (!isOpen) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-black/60 backdrop-blur-sm"
        onClick={onClose}
      />

      {/* Modal */}
      <div className="relative w-full max-w-2xl mx-4 bg-[#1E2329] rounded-lg border border-white/10 shadow-2xl max-h-[90vh] overflow-hidden flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-white/10 bg-black/20">
          <div className="flex items-center gap-3">
            <Bot className="w-5 h-5 text-nofx-gold" />
            <h2 className="text-lg font-semibold text-white">
              {isEditMode ? t('editAdmin', language) || 'Edit Administrator' : t('createAdmin', language) || 'Create Administrator'}
            </h2>
          </div>
          <button
            onClick={onClose}
            className="p-1 rounded hover:bg-white/10 transition-colors"
          >
            <X className="w-5 h-5 text-zinc-400" />
          </button>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto p-6">
          <form onSubmit={handleSubmit} className="space-y-6">
            {/* Admin Name */}
            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">
                <Bot className="w-4 h-4 inline mr-1" />
                {t('adminName', language) || 'Administrator Name'}
              </label>
              <input
                type="text"
                value={formState.name}
                onChange={(e) => setFormState(prev => ({ ...prev, name: e.target.value }))}
                placeholder={t('enterAdminName', language) || 'Enter administrator name'}
                className="w-full px-4 py-2.5 bg-[#2A3038] border border-white/10 rounded text-white placeholder-zinc-500 focus:outline-none focus:border-nofx-gold/50"
              />
            </div>

            {/* AI Model Selection */}
            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">
                <Brain className="w-4 h-4 inline mr-1" />
                {t('aiModel', language) || 'AI Model'}
              </label>
              <select
                value={formState.ai_model_id}
                onChange={(e) => setFormState(prev => ({ ...prev, ai_model_id: e.target.value }))}
                className="w-full px-4 py-2.5 bg-[#2A3038] border border-white/10 rounded text-white focus:outline-none focus:border-nofx-gold/50"
              >
                <option value="">{t('selectModel', language) || 'Select AI Model'}</option>
                {availableModels.map(model => (
                  <option key={model.id} value={model.id}>
                    {model.name} ({model.provider})
                  </option>
                ))}
              </select>
            </div>

            {/* Scan Interval */}
            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">
                <Clock className="w-4 h-4 inline mr-1" />
                {t('scanInterval', language) || 'Scan Interval (minutes)'}
              </label>
              <select
                value={formState.scan_interval_mins}
                onChange={(e) => setFormState(prev => ({ ...prev, scan_interval_mins: parseInt(e.target.value) }))}
                className="w-full px-4 py-2.5 bg-[#2A3038] border border-white/10 rounded text-white focus:outline-none focus:border-nofx-gold/50"
              >
                <option value={15}>15 {t('minutes', language) || 'minutes'}</option>
                <option value={30}>30 {t('minutes', language) || 'minutes'}</option>
                <option value={60}>60 {t('minutes', language) || 'minutes'}</option>
                <option value={120}>120 {t('minutes', language) || 'minutes'}</option>
                <option value={240}>240 {t('minutes', language) || 'minutes'}</option>
              </select>
            </div>

            {/* Managed Traders */}
            <div>
              <div className="flex items-center justify-between mb-2">
                <label className="block text-sm font-medium text-zinc-300">
                  <ListChecks className="w-4 h-4 inline mr-1" />
                  {t('managedTraders', language) || 'Managed Traders'}
                </label>
                <div className="flex gap-2">
                  <button
                    type="button"
                    onClick={selectAllTraders}
                    className="text-xs text-nofx-gold hover:text-yellow-400"
                  >
                    {t('selectAll', language) || 'Select All'}
                  </button>
                  <span className="text-zinc-500">|</span>
                  <button
                    type="button"
                    onClick={deselectAllTraders}
                    className="text-xs text-zinc-400 hover:text-white"
                  >
                    {t('deselectAll', language) || 'Deselect All'}
                  </button>
                </div>
              </div>

              {loadingTraders ? (
                <div className="text-center py-4 text-zinc-500">
                  {t('loading', language) || 'Loading...'}
                </div>
              ) : availableTraders.length === 0 ? (
                <div className="text-center py-4 text-zinc-500">
                  {t('noTradersAvailable', language) || 'No traders available'}
                </div>
              ) : (
                <div className="grid grid-cols-1 md:grid-cols-2 gap-2 max-h-48 overflow-y-auto p-1">
                  {availableTraders.map(trader => (
                    <label
                      key={trader.trader_id}
                      className={`flex items-center gap-3 p-3 rounded border cursor-pointer transition-colors ${
                        formState.managed_trader_ids.includes(trader.trader_id)
                          ? 'bg-nofx-gold/10 border-nofx-gold/50'
                          : 'bg-[#2A3038] border-white/10 hover:border-white/20'
                      }`}
                    >
                      <input
                        type="checkbox"
                        checked={formState.managed_trader_ids.includes(trader.trader_id)}
                        onChange={() => toggleTrader(trader.trader_id)}
                        className="sr-only"
                      />
                      <div className="flex-1 min-w-0">
                        <div className="text-sm font-medium text-white truncate">
                          {trader.trader_name}
                        </div>
                        <div className="text-xs text-zinc-500 truncate">
                          {trader.trader_id}
                        </div>
                      </div>
                      {trader.is_running && (
                        <span className="px-2 py-0.5 text-xs rounded bg-green-500/20 text-green-400">
                          {t('running', language) || 'RUN'}
                        </span>
                      )}
                    </label>
                  ))}
                </div>
              )}
              <div className="mt-2 text-xs text-zinc-500">
                {t('selectedCount', language) || 'Selected'}: {formState.managed_trader_ids.length} / {availableTraders.length}
              </div>
            </div>
          </form>
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-3 px-6 py-4 border-t border-white/10 bg-black/20">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 text-sm font-medium text-zinc-300 hover:text-white transition-colors"
          >
            {t('cancel', language) || 'Cancel'}
          </button>
          <button
            onClick={handleSubmit}
            disabled={saving || loadingTraders}
            className="px-6 py-2 text-sm font-bold font-mono uppercase tracking-wider bg-nofx-gold text-black rounded hover:bg-yellow-400 disabled:opacity-50 disabled:cursor-not-allowed transition-all"
          >
            {saving ? t('saving', language) || 'Saving...' : t('save', language) || 'Save'}
          </button>
        </div>
      </div>
    </div>
  )
}
