import React from 'react';
import { motion } from 'motion/react';
import { Globe, HardDrive, PanelLeftClose, PanelLeftOpen } from 'lucide-react';
import type { ConnectionStatus, Locale, NavTab } from '../types';
import { computeOverviewSummary, formatBytes, volumeDisplayName } from '../utils/fsLogic';
import { LOCALE_OPTIONS, Translations, formatNumber, formatPercent } from '../utils/i18n';
import { BrandEmblemSvg } from './VisualAssets';
import { CustomScrollArea } from './CustomScrollbar';
import { Button, cn, IOS_BOUNCE_SPRING, IOS_SOFT_SPRING, IOS_SPRING, Select } from './ui';
import { connectionVisual, TONE_ICON, TONE_TEXT } from './connectionStatus';

// OverviewSummary 直接取汇总函数的返回类型，避免两处手写同构结构。
type OverviewSummary = ReturnType<typeof computeOverviewSummary>;

export interface NavItem {
  id: NavTab;
  label: string;
  icon: React.ReactNode;
}

interface AppSidebarProps {
  t: Translations;
  locale: Locale;
  setLocale: (locale: Locale) => void;
  localeMeta: { label: string };
  cycleNextLocale: () => void;
  navItems: NavItem[];
  activeTab: NavTab;
  setActiveTab: (tab: NavTab) => void;
  sidebarCollapsed: boolean;
  setSidebarCollapsed: React.Dispatch<React.SetStateAction<boolean>>;
  summary: OverviewSummary;
  downloadsCount: number;
  activeVolumeId: string;
  selectVolume: (id: string) => void;
  setCurrentFolderId: (id: string | null) => void;
  connectionStatus: ConnectionStatus;
  themeNext: 'light' | 'dark' | 'system';
  themeLabel: string;
  themeIconClass: string;
  ThemeIcon: React.FC<{ className?: string }>;
  setTheme: (mode: 'light' | 'dark' | 'system') => void;
}

// AppSidebar 是桌面端的左侧栏：品牌、页签导航、卷占用、连接状态与语言/主题。
//
// 输入：当前页签、卷汇总、折叠状态与各回调
// 输出：可折叠的 motion.aside
// 预期：点击页签/卷都走回调，不在组件内改全局状态
export const AppSidebar: React.FC<AppSidebarProps> = ({
  t,
  locale,
  setLocale,
  localeMeta,
  cycleNextLocale,
  navItems,
  activeTab,
  setActiveTab,
  sidebarCollapsed,
  setSidebarCollapsed,
  summary,
  downloadsCount,
  activeVolumeId,
  selectVolume,
  setCurrentFolderId,
  connectionStatus,
  themeNext,
  themeLabel,
  themeIconClass,
  ThemeIcon,
  setTheme,
}) => {
  const { Icon: ConnectionIcon, tone: connectionTone, spinning: connectionSpinning } =
    connectionVisual(connectionStatus);
  return (
  <motion.aside
    initial={false}
    animate={{ width: sidebarCollapsed ? 76 : 256 }}
    transition={IOS_SPRING}
    className="hidden md:flex shrink-0 flex-col border-e border-zinc-200/80 dark:border-zinc-800/80 bg-white/80 dark:bg-[#0C0E14]/90 backdrop-blur-xl sticky top-0 h-screen overflow-hidden"
  >
    <CustomScrollArea className="flex-1 h-full" viewportClassName="p-3.5">
      <div className="space-y-5">
        {/* Brand Identity + Collapse Toggle */}
        <div className="flex items-center justify-between gap-2 px-1">
          {!sidebarCollapsed && (
            <motion.a
              href="#overview"
              whileTap={{ scale: 0.93 }}
              whileHover={{ scale: 1.015 }}
              transition={IOS_BOUNCE_SPRING}
              onClick={(e) => {
                e.preventDefault();
                setActiveTab('overview');
              }}
              className="flex items-center gap-2.5 min-w-0"
            >
              <BrandEmblemSvg className="w-8 h-8 shrink-0" />
              <div className="min-w-0">
                <div
                  dir="ltr"
                  className="text-sm font-bold text-zinc-950 dark:text-white tracking-tight truncate"
                >
                  {t.appName}
                </div>
              </div>
            </motion.a>
          )}

          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={() => setSidebarCollapsed((c) => !c)}
            aria-label={sidebarCollapsed ? t.sidebar.expandSidebar : t.sidebar.collapseSidebar}
            title={sidebarCollapsed ? t.sidebar.expandSidebar : t.sidebar.collapseSidebar}
            className={sidebarCollapsed ? 'mx-auto' : 'ms-auto'}
          >
            {sidebarCollapsed ? (
              <PanelLeftOpen className="w-4 h-4" />
            ) : (
              <PanelLeftClose className="w-4 h-4" />
            )}
          </Button>
        </div>

        {/* Navigation Section with iOS Liquid Spring Pill & Tactile Press */}
        <nav aria-label={t.a11y.primaryNav} className="space-y-1">
          {navItems.map((item) => {
            const isActive = activeTab === item.id;
            return (
              <motion.a
                key={item.id}
                href={`#${item.id}`}
                whileTap={{ scale: 0.92 }}
                whileHover={{ scale: 1.015 }}
                transition={IOS_BOUNCE_SPRING}
                onClick={(e) => {
                  e.preventDefault();
                  // 页签切换直接改状态：整页 View Transition 交叉淡化会把内容冲成白一瞬，
                  // 且快照会盖住页面自身的进入动画。
                  setActiveTab(item.id);
                }}
                title={sidebarCollapsed ? item.label : undefined}
                aria-current={isActive ? 'page' : undefined}
                className={`relative w-full h-10 ${
                  sidebarCollapsed ? 'justify-center px-0' : 'justify-between px-3'
                } rounded-xl text-xs font-medium flex items-center transition-colors select-none z-10 ${
                  isActive
                    ? 'text-white font-semibold'
                    : 'text-zinc-600 dark:text-zinc-400 hover:text-zinc-950 dark:hover:text-white hover:bg-zinc-200/50 dark:hover:bg-white/[0.04]'
                }`}
              >
                {isActive && (
                  <motion.span
                    layoutId="sidebar-active-pill"
                    transition={IOS_SPRING}
                    className="absolute inset-0 rounded-xl bg-[#007AFF] dark:bg-[#0A84FF] shadow-xs -z-10"
                  />
                )}
                <span className="flex items-center gap-2.5 min-w-0">
                  {item.icon}
                  {!sidebarCollapsed && <span className="truncate">{item.label}</span>}
                </span>
                {!sidebarCollapsed && item.id === 'files' && (
                  <span
                    className={`font-mono text-[11px] tabular-nums shrink-0 ${
                      isActive ? 'text-white/90' : 'text-zinc-400 dark:text-zinc-500'
                    }`}
                  >
                    {formatNumber(summary.totalFiles, locale)}
                  </span>
                )}
                {!sidebarCollapsed && item.id === 'downloads' && (
                  <span
                    className={`font-mono text-[11px] tabular-nums shrink-0 ${
                      isActive ? 'text-white/90' : 'text-zinc-400 dark:text-zinc-500'
                    }`}
                  >
                    {formatNumber(downloadsCount, locale)}
                  </span>
                )}
              </motion.a>
            );
          })}
        </nav>

        {/* Total Used Header + Per-Volume Usage (xxG xx%) */}
        {!sidebarCollapsed && (
          <div className="space-y-2 pt-4 border-t border-zinc-200/80 dark:border-zinc-800/80">
            <div className="px-2.5 py-1 flex items-center justify-between gap-2 text-xs font-medium text-zinc-500 dark:text-zinc-400">
              <span className="truncate">{t.sidebar.totalUsed}</span>
              <span
                dir="ltr"
                className="font-mono font-semibold tabular-nums text-zinc-900 dark:text-zinc-100 shrink-0"
              >
                {formatBytes(summary.totalBytes, 1, locale)}
              </span>
            </div>
            {summary.perVolume.map(({ volume, totalBytes, sharePct = 0 }) => {
              const isCurrentVol = activeVolumeId === volume.id && activeTab === 'files';
              const visualWidth = Math.min(100, Math.max(6, sharePct));
              return (
                <motion.button
                  key={volume.id}
                  type="button"
                  data-volume-id={volume.id}
                  whileTap={{ scale: 0.93 }}
                  whileHover={{ scale: 1.015 }}
                  transition={IOS_BOUNCE_SPRING}
                  onClick={() => {
                    selectVolume(volume.id);
                    setCurrentFolderId(null);
                    setActiveTab('files');
                  }}
                  className={`w-full px-2.5 py-2.5 rounded-xl text-start transition-colors space-y-1.5 cursor-pointer ${
                    isCurrentVol
                      ? 'bg-[#007AFF]/10 text-[#007AFF] dark:text-[#0A84FF] ring-1 ring-[#007AFF]/30'
                      : 'text-zinc-700 dark:text-zinc-300 hover:bg-zinc-200/50 dark:hover:bg-white/[0.04]'
                  }`}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="flex items-center gap-2 min-w-0">
                      <HardDrive className="w-3.5 h-3.5 shrink-0 opacity-75" />
                      <span dir="ltr" className="text-xs font-mono font-semibold truncate">
                        {volumeDisplayName(volume.name, volume.title)}
                      </span>
                    </span>
                    <span
                      dir="ltr"
                      className="text-[11px] font-mono tabular-nums text-zinc-500 dark:text-zinc-400 shrink-0"
                    >
                      {formatBytes(totalBytes, 1, locale)} {formatPercent(sharePct, locale)}
                    </span>
                  </div>
                  <div className="w-full h-1.5 rounded-full bg-zinc-200/80 dark:bg-zinc-800 overflow-hidden">
                    <motion.div
                      initial={false}
                      animate={{ width: `${visualWidth}%` }}
                      transition={IOS_SOFT_SPRING}
                      className="h-full bg-[#007AFF] dark:bg-[#0A84FF] rounded-full"
                    />
                  </div>
                </motion.button>
              );
            })}
          </div>
        )}
      </div>

      {/* Bottom Sidebar Loopback Telemetry + Multi-Language Selector & Theme Controls */}
      <div className="space-y-3 pt-4 border-t border-zinc-200/80 dark:border-zinc-800/80">
        {!sidebarCollapsed && (
          <div className="p-3 rounded-xl bg-zinc-100/80 dark:bg-zinc-900/70 border border-zinc-200/60 dark:border-zinc-800/80">
            <div className="flex items-center gap-2 text-xs">
              <ConnectionIcon
                className={cn(
                  'w-4 h-4 shrink-0',
                  TONE_ICON[connectionTone],
                  connectionSpinning && 'animate-spin'
                )}
              />
              <span className="text-zinc-500 dark:text-zinc-400 truncate">
                {t.connection.label}:
              </span>
              <span className={cn('font-medium truncate', TONE_TEXT[connectionTone])}>
                {t.connection[connectionStatus]}
              </span>
            </div>
          </div>
        )}

        {sidebarCollapsed ? (
          <div className="flex flex-col gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={cycleNextLocale}
              aria-label={t.sidebar.languageLabel}
              title={`${t.sidebar.languageLabel}: ${localeMeta.label}`}
              className="px-0 justify-center"
            >
              <Globe className="w-3.5 h-3.5 shrink-0" />
            </Button>

            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setTheme(themeNext)}
              aria-label={themeLabel}
              className="px-0 justify-center"
            >
              <ThemeIcon className={`w-3.5 h-3.5 ${themeIconClass}`} />
            </Button>
          </div>
        ) : (
          <div className="flex items-center gap-2">
            <div className="relative flex items-center flex-1 min-w-0">
              <Globe className="w-3.5 h-3.5 text-zinc-500 dark:text-zinc-400 absolute start-2.5 pointer-events-none" />
              <Select
                aria-label={t.sidebar.languageLabel}
                value={locale}
                onChange={(e) => setLocale(e.target.value as Locale)}
                className="h-8 w-full ps-7 pe-2 text-xs rounded-lg truncate"
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
              size="sm"
              onClick={() => setTheme(themeNext)}
              aria-label={themeLabel}
              title={themeLabel}
              className="px-2.5 shrink-0"
            >
              <ThemeIcon className={`w-3.5 h-3.5 ${themeIconClass}`} />
              <span className="truncate max-w-[72px]">{themeLabel}</span>
            </Button>
          </div>
        )}
      </div>
    </CustomScrollArea>
  </motion.aside>
  );
};
