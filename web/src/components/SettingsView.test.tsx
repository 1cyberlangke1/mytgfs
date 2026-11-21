// @vitest-environment jsdom
//
// SettingsView 的组件测试：默认值、按卡保存、超范围拒绝、快捷键录制与冲突。
//
// 输入：假的 schema/values 与 vi.fn() 回调
// 输出：断言渲染值、提交的 payload（只含改动键）、错误提示与快捷键映射
// 预期：不碰网络；不合法的值不提交；冲突键位拒绝且不覆盖旧动作
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';

import { SettingsView } from './SettingsView';
import { DICTIONARY } from '../utils/i18n';
import type { Translations } from '../utils/i18n';
import { DEFAULT_SHORTCUTS } from '../utils/shortcuts';
import type { AppSettingDef, AppSettings } from '../types';

const t = DICTIONARY.zh as Translations;

afterEach(cleanup);

const schema: AppSettingDef[] = [
  { key: 'mount.sync_seconds', kind: 'int', min: 0, max: 86400 },
  { key: 'mount.download_parallel', kind: 'int', min: 1, max: 16 },
  { key: 'downloads.dir', kind: 'string' },
  { key: 'log.level', kind: 'string', options: ['error', 'info', 'debug'] },
];

const settings: AppSettings = {
  schema,
  values: {
    'mount.sync_seconds': '15',
    'mount.download_parallel': '4',
    'downloads.dir': 'data/downloads',
    'log.level': 'info',
  },
};

const renderView = (overrides: Partial<Parameters<typeof SettingsView>[0]> = {}) => {
  const handlers = {
    onChangeShortcuts: vi.fn(),
    onSaveSettings: vi.fn(() => Promise.resolve()),
    onNotifyError: vi.fn(),
  };
  render(
    <SettingsView
      t={t}
      locale="zh"
      settings={settings}
      canAutostart={false}
      canUseShortcuts
      canSetDownloadsDir
      shortcuts={{ ...DEFAULT_SHORTCUTS }}
      onChangeShortcuts={handlers.onChangeShortcuts}
      onSaveSettings={handlers.onSaveSettings}
      onNotifyError={handlers.onNotifyError}
      {...overrides}
    />
  );
  return handlers;
};

// saveButtons 按卡片顺序返回保存按钮（SECTION_ORDER=mount/downloads/log）
const saveButtons = () => screen.getAllByRole('button', { name: t.settings.saveCard });

describe('SettingsView', () => {
  it('渲染真实生效值，且没有改动时保存按钮全部禁用', () => {
    renderView();

    expect((document.getElementById('setting-mount.sync_seconds') as HTMLInputElement).value).toBe(
      '15'
    );
    expect((document.getElementById('setting-downloads.dir') as HTMLInputElement).value).toBe(
      'data/downloads'
    );
    expect((document.getElementById('setting-log.level') as HTMLSelectElement).value).toBe('info');

    for (const btn of saveButtons()) expect(btn.hasAttribute('disabled')).toBe(true);
  });

  it('改动只提交所在卡片的那一张，提交成功后重新禁用', async () => {
    const handlers = renderView();

    fireEvent.change(document.getElementById('setting-mount.sync_seconds')!, {
      target: { value: '17' },
    });

    // 只有 mount 那张卡变可保存
    expect(saveButtons()[0]!.hasAttribute('disabled')).toBe(false);
    expect(saveButtons()[1]!.hasAttribute('disabled')).toBe(true);
    expect(saveButtons()[2]!.hasAttribute('disabled')).toBe(true);

    fireEvent.click(saveButtons()[0]!);
    await waitFor(() => expect(handlers.onSaveSettings).toHaveBeenCalledTimes(1));
    expect(handlers.onSaveSettings).toHaveBeenCalledWith({ 'mount.sync_seconds': '17' });
    expect(handlers.onNotifyError).not.toHaveBeenCalled();

    await waitFor(() => expect(saveButtons()[0]!.hasAttribute('disabled')).toBe(true));
  });

  it('超范围的值标红并拒绝提交', async () => {
    const handlers = renderView();

    fireEvent.change(document.getElementById('setting-mount.sync_seconds')!, {
      target: { value: '999999' },
    });
    fireEvent.click(saveButtons()[0]!);

    await waitFor(() => expect(handlers.onNotifyError).toHaveBeenCalledTimes(1));
    expect(handlers.onSaveSettings).not.toHaveBeenCalled();

    const input = document.getElementById('setting-mount.sync_seconds')!;
    expect(input.getAttribute('aria-invalid')).toBe('true');
    // 保存按钮被阻塞，避免反复点
    expect(saveButtons()[0]!.hasAttribute('disabled')).toBe(true);
  });

  // pressKey 先等录制态真正渲染出来，再灌按键。
  //
  // 输入：录制所在的按钮；要按下的键
  // 输出：无
  // 预期：只在 UI 已进入「请按键…」后派发，避免在 effect 挂监听之前丢按键
  const pressKey = async (chip: HTMLElement, key: string) => {
    await waitFor(() => expect(chip.textContent).toBe(t.settings.shortcutListening));
    window.dispatchEvent(new KeyboardEvent('keydown', { key, cancelable: true }));
  };

  it('录制键位时冲突会被拒绝并指出占用的动作', async () => {
    const handlers = renderView();

    const upLabel = t.files.shortcutMoveUp;
    const row = screen.getByText(upLabel).parentElement as HTMLElement;
    const chip = within(row).getAllByRole('button')[0]!;

    fireEvent.click(chip);
    // ArrowDown 已经被「下移」占用
    await pressKey(chip, 'ArrowDown');

    expect(handlers.onChangeShortcuts).not.toHaveBeenCalled();
    const alert = screen.getByRole('alert');
    expect(alert.textContent).toContain(t.files.shortcutMoveDown);
  });

  it('录制不冲突的键会写回映射，Esc 取消则不写', async () => {
    const handlers = renderView();

    const upLabel = t.files.shortcutMoveUp;
    const row = screen.getByText(upLabel).parentElement as HTMLElement;
    const chip = within(row).getAllByRole('button')[0]!;

    fireEvent.click(chip);
    await pressKey(chip, 'Escape');
    expect(handlers.onChangeShortcuts).not.toHaveBeenCalled();

    fireEvent.click(chip);
    await pressKey(chip, 'j');
    expect(handlers.onChangeShortcuts).toHaveBeenCalledWith({
      ...DEFAULT_SHORTCUTS,
      moveUp: 'J',
    });
  });

  it('不允许改下载目录时整个字段不渲染（安卓沙盒固定路径）', () => {
    renderView({ canSetDownloadsDir: false });

    expect(document.getElementById('setting-downloads.dir')).toBeNull();
    // 其它字段照常出现
    expect(document.getElementById('setting-log.level')).not.toBeNull();
  });

  it('恢复全部键位写回默认映射', () => {
    const handlers = renderView({
      shortcuts: { ...DEFAULT_SHORTCUTS, moveUp: 'J' },
    });

    fireEvent.click(screen.getByRole('button', { name: t.settings.shortcutResetAll }));
    expect(handlers.onChangeShortcuts).toHaveBeenCalledWith(DEFAULT_SHORTCUTS);
  });
});
