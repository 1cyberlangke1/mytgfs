import i18next from 'i18next';
import { initReactI18next } from 'react-i18next';
import { Locale } from '../types';
import zhPack from '../locales/zh.json';

export interface LocaleMeta {
  id: Locale;
  label: string;
  shortLabel: string;
  htmlLang: string;
  collatorLocale: string;
  dir: 'ltr' | 'rtl';
  byteUnits: [string, string, string, string, string];
}



/**
 * Negotiates best locale from BCP-47 language tags (e.g. navigator.languages).
 *
 * 输入：浏览器语言标签列表；fallback 兜底语言
 * 输出：'zh' 或 'en'
 * 预期：中日韩等其它语种统一回落到 en，繁中也归到 zh（界面只维护简中 + 英文）
 */
export function matchBcp47Locale(tags: readonly string[], fallback: Locale = 'zh'): Locale {
  for (const raw of tags) {
    const tag = raw.trim().toLowerCase();
    if (!tag) continue;
    if (tag.startsWith('zh')) return 'zh';
    if (tag.startsWith('en')) return 'en';
  }
  return fallback;
}

export interface PluralForms {
  one: string;
  few?: string;
  many?: string;
  two?: string;
  zero?: string;
  other: string;
}

/**
 * Formats a count + noun using native CLDR Intl.PluralRules for the given locale.
 */
export function formatPlural(count: number, forms: PluralForms, locale: Locale): string {
  const meta = getLocaleMeta(locale);
  const formattedCount = formatNumber(count, locale);
  try {
    const rules = new Intl.PluralRules(meta.htmlLang);
    const category = rules.select(count);
    const template = forms[category] ?? forms.other;
    return template.replace('{count}', formattedCount);
  } catch {
    const template = count === 1 ? forms.one : forms.other;
    return template.replace('{count}', formattedCount);
  }
}

/**
 * Formats integer/decimal numbers using locale-specific grouping & decimal separators.
 */
export function formatNumber(
  value: number,
  locale: Locale = 'zh',
  options?: Intl.NumberFormatOptions
): string {
  if (!Number.isFinite(value)) return '0';
  const meta = getLocaleMeta(locale);
  try {
    return new Intl.NumberFormat(meta.htmlLang, options).format(value);
  } catch {
    return String(value);
  }
}

/**
 * Formats percentage (0..100) using locale-specific conventions.
 */
export function formatPercent(pct: number, locale: Locale = 'zh', digits = 0): string {
  if (!Number.isFinite(pct)) return '0%';
  const meta = getLocaleMeta(locale);
  try {
    return new Intl.NumberFormat(meta.htmlLang, {
      style: 'percent',
      minimumFractionDigits: digits,
      maximumFractionDigits: digits,
    }).format(pct / 100);
  } catch {
    return `${pct.toFixed(digits)}%`;
  }
}

/**
 * Formats ISO date-time string into localized compact date & 24h time using Intl.DateTimeFormat.
 */
export function formatDateTime(isoString: string, locale: Locale = 'zh'): string {
  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) {
    return isoString.replace('T', ' ').slice(0, 16);
  }
  const meta = getLocaleMeta(locale);
  try {
    return new Intl.DateTimeFormat(meta.htmlLang, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    }).format(date);
  } catch {
    return isoString.replace('T', ' ').slice(0, 16);
  }
}

/**
 * Interpolates named placeholders ({name}, {count}, {limit}, etc.) with locale-aware numbers.
 */
export function interpolate(
  template: string,
  params: Record<string, string | number>,
  locale: Locale = 'zh'
): string {
  return template.replace(/\{(\w+)\}/g, (_, key: string) => {
    const val = params[key];
    if (val === undefined || val === null) return `{${key}}`;
    if (typeof val === 'number') {
      return formatNumber(val, locale);
    }
    return String(val);
  });
}

export type Translations = typeof zhPack;

const packs = import.meta.glob<{ default: Translations }>('../locales/*.json', {
  eager: true,
});

/** 全部已加载的语言包，键为文件名（zh、en…）。 */
export const DICTIONARY: Record<string, Translations> = Object.fromEntries(
  Object.entries(packs).map(([file, mod]) => [
    file.slice(file.lastIndexOf('/') + 1, file.lastIndexOf('.')),
    mod.default,
  ])
);

/** 语言选择器数据，来自每个语言包自带的 _meta。 */
export const LOCALE_OPTIONS: LocaleMeta[] = Object.entries(DICTIONARY)
  .map(([id, pack]) => ({ ...pack._meta, id }) as LocaleMeta)
  .sort((a, b) => (a.id === 'zh' ? -1 : b.id === 'zh' ? 1 : a.id.localeCompare(b.id)));

export function getLocaleMeta(locale: Locale): LocaleMeta {
  return LOCALE_OPTIONS.find((o) => o.id === locale) ?? LOCALE_OPTIONS[0]!;
}

const i18nResources = Object.fromEntries(
  Object.entries(DICTIONARY).map(([lang, translation]) => [lang, { translation }])
);

if (!i18next.isInitialized) {
  i18next.use(initReactI18next).init({
    resources: i18nResources,
    lng: 'zh',
    fallbackLng: 'zh',
    interpolation: {
      escapeValue: false,
    },
  });
}

export { i18next };
