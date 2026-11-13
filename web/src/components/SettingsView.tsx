import React, { useEffect, useState } from 'react';
import { Loader2, RotateCcw, Save } from 'lucide-react';
import { AppSettings, Locale } from '../types';
import { Translations, formatNumber } from '../utils/i18n';
import { Button, Input, IOS_SOFT_SPRING } from './ui';
import { motion } from 'motion/react';

interface SettingsViewProps {
  t: Translations;
  locale: Locale;
  settings: AppSettings;
  onSaveSettings: (nextSettings: AppSettings) => void;
  onResetDefaults: () => void;
  onNotifyError: (msg: string) => void;
}

// 每个字段的可填范围，与 Go 侧 config 的夹取范围保持一致。
const LIMITS = {
  panelPort: { min: 1024, max: 65535 },
  autoSyncIntervalSec: { min: 0, max: 86400 },
  downloadConnections: { min: 1, max: 8 },
  uploadConcurrency: { min: 1, max: 16 },
  readChunkSizeMb: { min: 1, max: 64 },
  pwaOfflineMaxFileMb: { min: 0, max: 4096 },
} as const;

type NumericKey = keyof typeof LIMITS;

export const SettingsView: React.FC<SettingsViewProps> = ({
  t,
  locale,
  settings,
  onSaveSettings,
  onResetDefaults,
  onNotifyError,
}) => {
  const [formState, setFormState] = useState<AppSettings>(settings);
  const [isSaving, setIsSaving] = useState(false);

  useEffect(() => {
    setFormState(settings);
  }, [settings]);

  const setNumber = (key: NumericKey, raw: string) => {
    const value = raw === '' ? 0 : Number(raw);
    setFormState((prev) => ({ ...prev, [key]: Number.isFinite(value) ? value : 0 }));
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    for (const key of Object.keys(LIMITS) as NumericKey[]) {
      const { min, max } = LIMITS[key];
      const value = formState[key];
      if (value < min || value > max) {
        onNotifyError(`${t.settings.saveErrorPrefix}${key}: ${min}–${max}`);
        return;
      }
    }
    setIsSaving(true);
    onSaveSettings(formState);
    window.setTimeout(() => setIsSaving(false), 320);
  };

  // 数字输入行：标签 + 范围提示 + 可填输入框
  const numberField = (
    key: NumericKey,
    label: string,
    hint?: string,
    step = 1
  ) => (
    <div className="space-y-1.5">
      <label
        htmlFor={`setting-${key}`}
        className="block text-xs font-medium text-zinc-700 dark:text-zinc-300"
      >
        {label}
      </label>
      <Input
        id={`setting-${key}`}
        dir="ltr"
        type="number"
        min={LIMITS[key].min}
        max={LIMITS[key].max}
        step={step}
        required
        value={String(formState[key])}
        onChange={(e) => setNumber(key, e.target.value)}
        className="font-mono tabular-nums text-start"
      />
      <p className="text-[11px] text-zinc-500 dark:text-zinc-400">
        {hint ?? `${formatNumber(LIMITS[key].min, locale)} – ${formatNumber(LIMITS[key].max, locale)}`}
      </p>
    </div>
  );

  return (
    <motion.form
      layout
      transition={IOS_SOFT_SPRING}
      onSubmit={handleSubmit}
      className="space-y-6"
    >
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <h1 className="text-xl sm:text-2xl font-semibold tracking-tight text-zinc-950 dark:text-white">
          {t.settings.title}
        </h1>

        <div className="flex items-center gap-2 shrink-0">
          <Button type="button" variant="outline" onClick={onResetDefaults}>
            <RotateCcw className="w-3.5 h-3.5" />
            <span>{t.settings.resetDefaultsBtn}</span>
          </Button>
          <Button type="submit" variant="accent" disabled={isSaving}>
            {isSaving ? (
              <Loader2 className="w-3.5 h-3.5 animate-spin" />
            ) : (
              <Save className="w-3.5 h-3.5" />
            )}
            <span>{isSaving ? t.settings.savingBtn : t.settings.saveSettingsBtn}</span>
          </Button>
        </div>
      </div>

      <section className="surface-card rounded-2xl p-6">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
          {numberField('panelPort', t.settings.panelPortLabel)}
          {numberField('autoSyncIntervalSec', t.settings.syncIntervalLabel)}
          {numberField('downloadConnections', t.settings.downloadConnLabel)}
          {numberField('uploadConcurrency', t.settings.uploadConcLabel)}
          {numberField('readChunkSizeMb', t.settings.readChunkLabel)}
          {numberField('pwaOfflineMaxFileMb', t.settings.pwaLimitLabel)}
        </div>
      </section>
    </motion.form>
  );
};
