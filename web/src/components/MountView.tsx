import React, { useState } from 'react';
import { Check, Copy, HardDrive, Terminal } from 'lucide-react';
import { motion } from 'motion/react';
import { AppSettings } from '../types';
import { Translations } from '../utils/i18n';
import {
  Button,
  Input,
  SegmentedTabs,
  Switch,
  useInterruptibleViewSpring,
} from './ui';

interface MountViewProps {
  t: Translations;
  settings: AppSettings;
  onToggleWebdav: (enabled: boolean) => void;
  onCopyAddress: (url: string) => void;
}

export const MountView: React.FC<MountViewProps> = ({
  t,
  settings,
  onToggleWebdav,
  onCopyAddress,
}) => {
  const [osTab, setOsTab] = useState<'windows' | 'macos' | 'linux'>('windows');
  const [copied, setCopied] = useState(false);

  const osTabSpring = useInterruptibleViewSpring(osTab);

  // WebDAV 地址来自服务端实际监听结果，不用面板端口拼
  const webdavUrl = settings.webdavPath.trim() ? `http://${settings.webdavPath.trim()}` : '';
  const hasAddr = webdavUrl !== '';

  const handleCopy = () => {
    if (!hasAddr) return;
    onCopyAddress(webdavUrl);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="space-y-6">
      <h1 className="text-xl sm:text-2xl font-semibold tracking-tight text-zinc-950 dark:text-white">
        {t.mount.title}
      </h1>

      {/* Card 1: WebDAV Service Switch & Listen URL */}
      <section className="surface-card rounded-2xl p-5 sm:p-6 space-y-5">
        <div className="flex items-center justify-between gap-4">
          <div className="flex items-center gap-3.5 min-w-0">
            <div className="w-10 h-10 rounded-xl bg-[#007AFF]/10 text-[#007AFF] dark:text-[#0A84FF] flex items-center justify-center shrink-0">
              <HardDrive className="w-5 h-5" />
            </div>
            <div className="flex items-center gap-2.5 flex-wrap min-w-0">
              <h2 className="text-base font-semibold text-zinc-950 dark:text-white">
                {t.mount.webdavCardTitle}
              </h2>
              <span
                className={`inline-flex items-center gap-1.5 text-xs font-medium ${
                  settings.webdavEnabled
                    ? 'text-emerald-600 dark:text-emerald-400'
                    : 'text-zinc-500 dark:text-zinc-400'
                }`}
              >
                <span
                  className={`w-1.5 h-1.5 rounded-full ${
                    settings.webdavEnabled ? 'bg-emerald-500' : 'bg-zinc-400'
                  }`}
                />
                {settings.webdavEnabled ? t.mount.webdavStatusOn : t.mount.webdavStatusOff}
              </span>
            </div>
          </div>

          <Switch
            checked={settings.webdavEnabled}
            onCheckedChange={onToggleWebdav}
            ariaLabel={t.mount.webdavCardTitle}
          />
        </div>

        {/* Loopback Listen Endpoint & Copy */}
        <div className="pt-4 border-t border-zinc-200/80 dark:border-zinc-800/80 space-y-2">
          <label
            htmlFor="webdav-listen-url"
            className="block text-xs font-medium text-zinc-700 dark:text-zinc-300"
          >
            {t.mount.endpointLabel}
          </label>
          <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-2 min-w-0">
            <Input
              id="webdav-listen-url"
              dir="ltr"
              type="text"
              readOnly
              value={hasAddr ? webdavUrl : t.mount.enableFirst}
              className="min-w-0 flex-1 font-mono text-xs select-all text-start"
            />
            <Button
              type="button"
              variant="outline"
              onClick={handleCopy}
              disabled={!hasAddr}
              className="sm:w-auto w-full"
            >
              {copied ? (
                <Check className="w-3.5 h-3.5 text-emerald-500" />
              ) : (
                <Copy className="w-3.5 h-3.5" />
              )}
              <span>{t.mount.copyBtn}</span>
            </Button>
          </div>
        </div>
      </section>

      {/* Card 2: OS Drive Mapping Instructions with iOS Segmented Control */}
      <section className="surface-card rounded-2xl p-5 sm:p-6 space-y-4">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 pb-4 border-b border-zinc-200/80 dark:border-zinc-800/80">
          <h2 className="text-base font-semibold text-zinc-950 dark:text-white">
            {t.mount.guideTitle}
          </h2>
          <SegmentedTabs
            value={osTab}
            onValueChange={setOsTab}
            layoutGroupId="mount-os-tabs"
            ariaLabel={t.mount.guideTitle}
            items={[
              { id: 'windows', label: t.mount.tabWindows },
              { id: 'macos', label: t.mount.tabMacos },
              { id: 'linux', label: t.mount.tabLinux },
            ]}
          />
        </div>

        <motion.div
          style={{
            y: osTabSpring.y,
            scale: osTabSpring.scale,
            opacity: osTabSpring.opacity,
          }}
          className="space-y-3 text-sm text-zinc-700 dark:text-zinc-300 leading-relaxed"
        >
          {osTab === 'windows' && (
            <>
              <p>{t.mount.winStep1}</p>
              <p>{t.mount.winStep2}</p>
              {hasAddr ? (
                <pre
                  dir="ltr"
                  className="p-3.5 rounded-xl bg-zinc-950 text-zinc-100 border border-zinc-800 font-mono text-xs overflow-x-auto text-left"
                >
                  {`net use Z: ${webdavUrl} /persistent:yes`}
                </pre>
              ) : (
                <p className="text-sm text-zinc-500 dark:text-zinc-400">{t.mount.enableFirst}</p>
              )}
              <p>{t.mount.winStep3}</p>
            </>
          )}

          {osTab === 'macos' && (
            <>
              <p>{t.mount.macStep1}</p>
              <p>{t.mount.macStep2}</p>
              {hasAddr ? (
                <pre
                  dir="ltr"
                  className="p-3.5 rounded-xl bg-zinc-950 text-zinc-100 border border-zinc-800 font-mono text-xs overflow-x-auto text-left"
                >
                  {webdavUrl}
                </pre>
              ) : (
                <p className="text-sm text-zinc-500 dark:text-zinc-400">{t.mount.enableFirst}</p>
              )}
              <p>{t.mount.macStep3}</p>
            </>
          )}

          {osTab === 'linux' && (
            <>
              <p>{t.mount.linuxStep1}</p>
              <p>{t.mount.linuxStep2}</p>
              {hasAddr ? (
                <pre
                  dir="ltr"
                  className="p-3.5 rounded-xl bg-zinc-950 text-zinc-100 border border-zinc-800 font-mono text-xs overflow-x-auto flex items-center gap-2 text-left"
                >
                  <Terminal className="w-3.5 h-3.5 text-blue-400 shrink-0" />
                  <code>{`gio mount dav://${settings.webdavPath.trim()}`}</code>
                </pre>
              ) : (
                <p className="text-sm text-zinc-500 dark:text-zinc-400">{t.mount.enableFirst}</p>
              )}
              <p>{t.mount.linuxStep3}</p>
            </>
          )}
        </motion.div>
      </section>
    </div>
  );
};
