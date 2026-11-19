import React from 'react';
import { LayoutGroup, motion } from 'motion/react';
import { Globe } from 'lucide-react';
import type { ConnectionStatus, Locale, NavTab } from '../types';
import { formatBytes } from '../utils/fsLogic';
import { formatNumber } from '../utils/i18n';
import { LOCALE_OPTIONS, Translations } from '../utils/i18n';
import { BrandEmblemSvg } from './VisualAssets';
import { Button, cn, IOS_BOUNCE_SPRING, IOS_SPRING, Select } from './ui';
import { connectionVisual, TONE_ICON, TONE_PILL } from './connectionStatus';
import { runViewTransition } from '../utils/viewTransition';
import type { NavItem } from './AppSidebar';

interface PhoneTopBarProps {
  t: Translations;
  locale: Locale;
  setLocale: (locale: Locale) => void;
  totalBytes: number;
  setActiveTab: (tab: NavTab) => void;
  /** Telegram 连接状态：手机上侧栏被隐藏，只能靠顶栏展示 */
  connectionStatus: ConnectionStatus;
  themeNext: 'light' | 'dark' | 'system';
  themeLabel: string;
  themeIconClass: string;
  ThemeIcon: React.FC<{ className?: string }>;
  setTheme: (mode: 'light' | 'dark' | 'system') => void;
}

// PhoneTopBar 是手机宽度的顶栏：品牌 + 连接状态 + 语言/主题。
//
// 输入：当前语言、主题、总占用与 TG 连接状态
// 输出：<header>（md 以上隐藏）
// 预期：只负责展示与回调，不持有状态
export const PhoneTopBar: React.FC<PhoneTopBarProps> = ({
  t,
  locale,
  setLocale,
  totalBytes,
  setActiveTab,
  connectionStatus,
  themeNext,
  themeLabel,
  themeIconClass,
  ThemeIcon,
  setTheme,
}) => {
  const { Icon, tone, spinning } = connectionVisual(connectionStatus);
  return (
  <header className="md:hidden sticky top-0 z-30 border-b border-zinc-200/80 dark:border-zinc-800/80 bg-white/85 dark:bg-[#0C0E14]/85 backdrop-blur-xl px-3.5 pb-2 pt-[max(0.5rem,env(safe-area-inset-top))]">
    <div className="flex items-center justify-between gap-2">
      <motion.a
        href="#overview"
        whileTap={{ scale: 0.92 }}
        transition={IOS_BOUNCE_SPRING}
        onClick={(e) => {
          e.preventDefault();
          setActiveTab('overview');
        }}
        className="flex items-center gap-2 min-w-0 shrink-0"
      >
        <BrandEmblemSvg className="w-7 h-7 shrink-0" />
        <div className="min-w-0">
          <div
            dir="ltr"
            className="text-sm font-bold tracking-tight text-zinc-950 dark:text-white leading-tight"
          >
            {t.appName}
          </div>
          <div className="text-[10px] font-mono text-emerald-600 dark:text-emerald-400 truncate leading-tight">
            {formatBytes(totalBytes, 1, locale)}
          </div>
        </div>
      </motion.a>

      {/* 连接状态紧贴品牌右侧：中间空白正好放它，异常时颜色与文案一起变 */}
      <div
        role="status"
        aria-live="polite"
        aria-label={`${t.connection.label}: ${t.connection[connectionStatus]}`}
        className={cn(
          'ms-2 flex shrink-0 items-center gap-1.5 rounded-full border px-2 py-[3px] text-[11px] leading-none',
          TONE_PILL[tone]
        )}
      >
        <Icon
          className={cn('w-3.5 h-3.5 shrink-0', TONE_ICON[tone], spinning && 'animate-spin')}
          aria-hidden="true"
        />
        <span className="truncate max-[359px]:hidden">
          {t.connection[connectionStatus]}
        </span>
      </div>

      <div className="flex-1" />

      <div className="flex items-center gap-1.5 min-w-0">
        <div className="relative flex items-center">
          <Globe className="w-3.5 h-3.5 text-zinc-500 dark:text-zinc-400 absolute start-2.5 pointer-events-none" />
          <Select
            aria-label={t.sidebar.languageLabel}
            value={locale}
            onChange={(e) => setLocale(e.target.value as Locale)}
            className="h-8 ps-7 pe-2 text-xs max-w-[116px] truncate"
          >
            {LOCALE_OPTIONS.map((opt) => (
              <option
                key={opt.id}
                value={opt.id}
                className="bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100"
              >
                {opt.label}
              </option>
            ))}
          </Select>
        </div>

        <Button
          type="button"
          variant="outline"
          size="icon"
          onClick={() => setTheme(themeNext)}
          aria-label={themeLabel}
        >
          <ThemeIcon className={`w-3.5 h-3.5 ${themeIconClass}`} />
        </Button>
      </div>
    </div>
  </header>
  );
};

interface PhoneTabBarProps {
  t: Translations;
  locale: Locale;
  navItems: NavItem[];
  activeTab: NavTab;
  setActiveTab: (tab: NavTab) => void;
  totalFiles: number;
  downloadsCount: number;
}

// PhoneTabBar 是手机宽度的底部页签栏。
//
// 输入：页签列表、当前页签与两个徽标数字
// 输出：固定底部的 <nav>（md 以上隐藏）
// 预期：点击走视图过渡，徽标为 0 时不渲染
export const PhoneTabBar: React.FC<PhoneTabBarProps> = ({
  t,
  locale,
  navItems,
  activeTab,
  setActiveTab,
  totalFiles,
  downloadsCount,
}) => (
  <LayoutGroup id="phone-bottom-dock">
    <nav
      aria-label={t.a11y.primaryNav}
      className="md:hidden fixed bottom-0 inset-x-0 z-40 border-t border-zinc-200/80 dark:border-zinc-800/80 bg-white/88 dark:bg-[#0C0E14]/92 backdrop-blur-2xl px-2 py-1.5 pb-[max(0.375rem,env(safe-area-inset-bottom))]"
    >
      <div className="flex gap-1 max-w-md mx-auto">
        {navItems.map((item) => {
          const isActive = activeTab === item.id;
          const badgeCount =
            item.id === 'files' ? totalFiles : item.id === 'downloads' ? downloadsCount : null;
          return (
            <motion.a
              key={item.id}
              href={`#${item.id}`}
              whileTap={{ scale: 0.86 }}
              transition={IOS_BOUNCE_SPRING}
              onClick={(e) => {
                e.preventDefault();
                runViewTransition(() => setActiveTab(item.id));
              }}
              aria-current={isActive ? 'page' : undefined}
              className={`relative flex flex-1 min-w-0 flex-col items-center justify-center py-1.5 px-1 rounded-xl text-[11px] font-medium select-none transition-colors z-10 ${
                isActive
                  ? 'text-[#007AFF] dark:text-[#0A84FF] font-semibold'
                  : 'text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200'
              }`}
            >
              {isActive && (
                <motion.span
                  layoutId="phone-bottom-tab-pill"
                  transition={IOS_SPRING}
                  className="absolute inset-0 rounded-xl bg-[#007AFF]/12 dark:bg-[#0A84FF]/18 -z-10"
                />
              )}
              <div className="relative flex items-center justify-center">
                {item.icon}
                {badgeCount !== null && badgeCount > 0 && (
                  <span className="absolute -top-1.5 -end-3 min-w-[15px] h-[15px] px-1 rounded-full bg-[#007AFF] text-white text-[9px] font-mono font-bold inline-flex items-center justify-center leading-none">
                    {formatNumber(badgeCount, locale)}
                  </span>
                )}
              </div>
              <span className="mt-1 truncate max-w-full leading-none">{item.label}</span>
            </motion.a>
          );
        })}
      </div>
    </nav>
  </LayoutGroup>
);
