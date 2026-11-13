import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Check, Loader2, Save } from 'lucide-react';
import { getAutostart, setAutostart } from '../api';
import { AppSettingDef, AppSettings, Locale } from '../types';
import { Translations, formatNumber } from '../utils/i18n';
import { Button, Input, IOS_SOFT_SPRING, Switch } from './ui';
import { motion } from 'motion/react';

interface SettingsViewProps {
  t: Translations;
  locale: Locale;
  settings: AppSettings;
  canAutostart: boolean;
  onSaveSettings: (values: Record<string, string>) => Promise<void>;
  onNotifyError: (msg: string) => void;
}

// SECTION_ORDER 控制分组顺序：与 config.toml 的段落顺序一致。
const SECTION_ORDER = ['webdav', 'mount', 'pwa', 'log'];

// PANEL_HIDDEN 是不在面板里出现的键：端口只能在启动参数或 config.toml 里改。
const PANEL_HIDDEN = new Set(['web.port']);

// sectionOf 取 "webdav.addr" 的 "webdav"。
function sectionOf(key: string): string {
  return key.includes('.') ? key.slice(0, key.indexOf('.')) : key;
}

// addrValid 与 Go 侧 checkHostPort 同规则：host:port，端口 1..65535。
function addrValid(value: string): boolean {
  const i = value.lastIndexOf(':');
  if (i <= 0 || i === value.length - 1) return false;
  const host = value.slice(0, i).trim();
  const port = Number(value.slice(i + 1));
  return host.length > 0 && Number.isInteger(port) && port >= 1 && port <= 65535;
}

// labelOf 取字段名，缺翻译时回落到键名。
function labelOf(t: Translations, key: string): string {
  const labels = t.settingLabels as Record<string, string | undefined>;
  return labels[key] ?? key;
}

// hintOf 取字段说明，没有时返回空串。
function hintOf(t: Translations, key: string): string {
  const hints = t.settingHints as Record<string, string | undefined>;
  return hints[key] ?? '';
}

// sectionTitle 取分组标题，缺翻译时回落到段名。
function sectionTitle(t: Translations, section: string): string {
  const titles = t.settingSections as Record<string, string | undefined>;
  return titles[section] ?? section;
}

// validateOne 返回该键的非法原因；合法返回空串。
function validateOne(def: AppSettingDef, value: string, t: Translations): string {
  const label = labelOf(t, def.key);
  if (def.kind === 'int') {
    const n = Number(value);
    if (value.trim() === '' || !Number.isFinite(n) || !Number.isInteger(n)) {
      return t.settings.invalidNumber.replace('{field}', label);
    }
    if (def.min !== undefined && def.max !== undefined && (n < def.min || n > def.max)) {
      return t.settings.invalidRange
        .replace('{field}', label)
        .replace('{min}', String(def.min))
        .replace('{max}', String(def.max));
    }
    return '';
  }
  if (def.options && def.options.length > 0) {
    return def.options.includes(value)
      ? ''
      : t.settings.invalidOption
          .replace('{field}', label)
          .replace('{options}', def.options.join(' / '));
  }
  if (def.key === 'webdav.addr' && value.trim() !== '' && !addrValid(value.trim())) {
    return t.settings.invalidAddr.replace('{field}', label);
  }
  if (value.length > 64) {
    return t.settings.invalidTooLong.replace('{field}', label);
  }
  return '';
}

export const SettingsView: React.FC<SettingsViewProps> = ({
  t,
  locale,
  settings,
  canAutostart,
  onSaveSettings,
  onNotifyError,
}) => {
  const visibleSchema = useMemo(
    () => settings.schema.filter((def) => !PANEL_HIDDEN.has(def.key)),
    [settings.schema]
  );

  const [draft, setDraft] = useState<Record<string, string>>(settings.values);
  const [saved, setSaved] = useState<Record<string, string>>(settings.values);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [savingSection, setSavingSection] = useState('');
  const [savedSection, setSavedSection] = useState('');
  const [autoOn, setAutoOn] = useState<boolean | null>(null);
  const [autoBusy, setAutoBusy] = useState(false);

  // 自启状态读的是系统注册项，进页面时取一次
  useEffect(() => {
    if (!canAutostart) return;
    let cancelled = false;
    getAutostart()
      .then((res) => {
        if (!cancelled) setAutoOn(res.enabled);
      })
      .catch((err) => {
        if (!cancelled) onNotifyError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [canAutostart]);

  // 开关是离散动作，切换即提交；失败时回退显示
  const toggleAutostart = (on: boolean) => {
    setAutoBusy(true);
    setAutostart(on)
      .then((res) => setAutoOn(res.enabled))
      .catch((err) => onNotifyError(err instanceof Error ? err.message : String(err)))
      .finally(() => setAutoBusy(false));
  };

  const savedRef = useRef(saved);
  savedRef.current = saved;

  // 外部改了配置（SSE）时才同步；正在编辑的字段保留用户输入
  useEffect(() => {
    const incoming = settings.values;
    setDraft((prev) => {
      const next = { ...prev };
      for (const k of Object.keys(incoming)) {
        if (prev[k] === savedRef.current[k]) next[k] = incoming[k];
      }
      return next;
    });
    setSaved(incoming);
  }, [settings.values]);

  // 按段分组，空段不显示
  const groups = useMemo(() => {
    const map = new Map<string, AppSettingDef[]>();
    for (const def of visibleSchema) {
      const sec = sectionOf(def.key);
      const list = map.get(sec) ?? [];
      list.push(def);
      map.set(sec, list);
    }
    return [...map.entries()].sort(
      (a, b) => SECTION_ORDER.indexOf(a[0]) - SECTION_ORDER.indexOf(b[0])
    );
  }, [visibleSchema]);

  const isDirty = (key: string) => (draft[key] ?? '') !== (saved[key] ?? '');

  const sectionDirty = (defs: AppSettingDef[]) => defs.some((d) => isDirty(d.key));

  // saveSection 只提交这一张卡片里的键；校验不过就停在原地标红
  const saveSection = (section: string, defs: AppSettingDef[]) => {
    const nextErrors: Record<string, string> = {};
    const payload: Record<string, string> = {};
    for (const def of defs) {
      if (!isDirty(def.key)) continue;
      const raw = draft[def.key] ?? '';
      const err = validateOne(def, raw, t);
      if (err) nextErrors[def.key] = err;
      else payload[def.key] = raw.trim();
    }
    setErrors((prev) => ({ ...prev, ...nextErrors }));
    const firstError = Object.values(nextErrors).find(Boolean);
    if (firstError) {
      onNotifyError(firstError);
      return;
    }
    if (Object.keys(payload).length === 0) {
      setSavedSection(section);
      window.setTimeout(() => setSavedSection(''), 1600);
      return;
    }
    setSavingSection(section);
    onSaveSettings(payload)
      .then(() => {
        setSaved((prev) => ({ ...prev, ...payload }));
        setSavedSection(section);
        window.setTimeout(() => setSavedSection(''), 1600);
      })
      .catch((err) => onNotifyError(err instanceof Error ? err.message : String(err)))
      .finally(() => setSavingSection(''));
  };

  const setValue = (key: string, value: string) => {
    setDraft((prev) => ({ ...prev, [key]: value }));
    setErrors((prev) => (prev[key] ? { ...prev, [key]: '' } : prev));
  };

  const field = (def: AppSettingDef) => {
    const id = `setting-${def.key}`;
    const label = labelOf(t, def.key);
    const err = errors[def.key];
    const dirty = isDirty(def.key);
    const marked = Boolean(err) || dirty;
    const hint =
      def.kind === 'int' && def.min !== undefined && def.max !== undefined
        ? `${formatNumber(def.min, locale)} – ${formatNumber(def.max, locale)}`
        : def.options && def.options.length > 0
          ? def.options.join(' / ')
          : hintOf(t, def.key);

    const labelClass = marked
      ? 'text-red-600 dark:text-red-400'
      : 'text-zinc-700 dark:text-zinc-300';
    const controlClass = marked
      ? 'border-red-500/80 focus-visible:ring-red-500/30 dark:border-red-500/70'
      : '';

    return (
      <div className="space-y-1.5" key={def.key}>
        <label htmlFor={id} className={`block text-xs font-medium ${labelClass}`}>
          {label}
        </label>
        {def.kind === 'bool' ? (
          <div className="flex items-center gap-2 h-9">
            <Switch
              checked={draft[def.key] === 'true'}
              onCheckedChange={(on) => setValue(def.key, on ? 'true' : 'false')}
              aria-label={label}
            />
            <span
              className={`text-xs ${marked ? 'text-red-600 dark:text-red-400' : 'text-zinc-500 dark:text-zinc-400'}`}
            >
              {draft[def.key] === 'true' ? t.settings.boolOn : t.settings.boolOff}
            </span>
          </div>
        ) : def.options && def.options.length > 0 ? (
          <select
            id={id}
            value={draft[def.key] ?? ''}
            onChange={(e) => setValue(def.key, e.target.value)}
            className={`flex h-9 w-full rounded-xl border border-zinc-200/90 bg-white px-3 text-sm text-zinc-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#007AFF]/35 dark:border-zinc-800 dark:bg-zinc-900/70 dark:text-zinc-100 ${controlClass}`}
          >
            {def.options.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
        ) : (
          <Input
            id={id}
            dir="ltr"
            type={def.kind === 'int' ? 'number' : 'text'}
            min={def.min}
            max={def.max}
            step={1}
            value={draft[def.key] ?? ''}
            onChange={(e) => setValue(def.key, e.target.value)}
            aria-invalid={Boolean(err)}
            className={`font-mono tabular-nums text-start ${controlClass}`}
          />
        )}
        <p
          className={`text-[11px] ${err ? 'text-red-600 dark:text-red-400' : 'text-zinc-500 dark:text-zinc-400'}`}
        >
          {err || hint}
        </p>
      </div>
    );
  };

  return (
    <motion.div layout transition={IOS_SOFT_SPRING} className="space-y-6">
      <h1 className="text-xl sm:text-2xl font-semibold tracking-tight text-zinc-950 dark:text-white">
        {t.settings.title}
      </h1>

      {canAutostart && (
        <section className="surface-card rounded-2xl p-6 space-y-4">
          <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">
            {t.settings.autostartTitle}
          </h2>
          <div className="flex items-center justify-between gap-4">
            <div className="space-y-1">
              <p className="text-xs font-medium text-zinc-700 dark:text-zinc-300">
                {t.settings.autostartLabel}
              </p>
              <p className="text-[11px] text-zinc-500 dark:text-zinc-400">{t.settings.autostartHint}</p>
            </div>
            <div className="flex items-center gap-2">
              <Switch
                checked={autoOn === true}
                disabled={autoOn === null || autoBusy}
                onCheckedChange={toggleAutostart}
                aria-label={t.settings.autostartLabel}
              />
              <span className="text-xs text-zinc-500 dark:text-zinc-400">
                {autoOn === true ? t.settings.boolOn : t.settings.boolOff}
              </span>
            </div>
          </div>
        </section>
      )}

      {groups.map(([section, defs]) => {
        const dirty = sectionDirty(defs);
        const saving = savingSection === section;
        const done = savedSection === section;
        const blocked = defs.some((d) => Boolean(errors[d.key]));
        return (
          <section key={section} className="surface-card rounded-2xl p-6 space-y-4">
            <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">
              {sectionTitle(t, section)}
            </h2>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-5">{defs.map(field)}</div>
            <div className="flex items-center justify-end gap-3">
              {done && (
                <span className="flex items-center gap-1 text-[11px] text-emerald-600 dark:text-emerald-400">
                  <Check className="w-3.5 h-3.5" />
                  {t.settings.savedCard}
                </span>
              )}
              <Button
                type="button"
                variant="accent"
                disabled={!dirty || saving || blocked}
                onClick={() => saveSection(section, defs)}
              >
                {saving ? (
                  <Loader2 className="w-3.5 h-3.5 animate-spin" />
                ) : (
                  <Save className="w-3.5 h-3.5" />
                )}
                <span>{saving ? t.settings.savingBtn : t.settings.saveCard}</span>
              </Button>
            </div>
          </section>
        );
      })}
    </motion.div>
  );
};
