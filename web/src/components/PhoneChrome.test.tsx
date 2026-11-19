// @vitest-environment jsdom
//
// 手机顶栏连接状态回归测试。
//
// 输入：四种连接状态（已连接 / 连接中 / 重连中 / 凭据失效）
// 输出：状态块的 role=status、aria-label 与可见文案
// 预期：手机端没有侧栏，连接状态必须在顶栏常驻且能被读屏读到
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { DICTIONARY } from '../utils/i18n';
import type { ConnectionStatus } from '../types';
import { PhoneTopBar } from './PhoneChrome';

const t = DICTIONARY.zh!;
const ThemeIcon = () => <svg data-testid="theme-icon" />;

// barOf 按给定状态渲染顶栏，其余 props 用固定桩。
function barOf(connectionStatus: ConnectionStatus) {
  return (
    <PhoneTopBar
      t={t}
      locale="zh"
      setLocale={() => {}}
      totalBytes={1024}
      setActiveTab={() => {}}
      connectionStatus={connectionStatus}
      themeNext="dark"
      themeLabel="主题"
      themeIconClass=""
      ThemeIcon={ThemeIcon}
      setTheme={() => {}}
    />
  );
}

describe('PhoneTopBar 连接状态', () => {
  afterEach(() => cleanup());

  it.each<ConnectionStatus>([
    'connected',
    'connecting',
    'reconnecting',
    'invalid_credentials',
  ])('%s 状态有可读文案', (status) => {
    render(barOf(status));
    const box = screen.getByRole('status');
    expect(box.getAttribute('aria-label')).toBe(`${t.connection.label}: ${t.connection[status]}`);
    expect(box.textContent).toContain(t.connection[status]);
  });

  it('状态块在品牌之后、语言下拉之前', () => {
    render(barOf('connected'));
    const header = screen.getByRole('banner');
    const status = screen.getByRole('status');
    const select = screen.getByRole('combobox', { name: t.sidebar.languageLabel });
    // Node.compareDocumentPosition：4 表示 status 在 select 之前
    expect(status.compareDocumentPosition(select) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(header.contains(status)).toBe(true);
  });
});
