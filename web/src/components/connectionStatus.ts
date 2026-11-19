// 连接状态的图标与配色只在这里定义一份：桌面侧栏与手机顶栏共用。
//
// 输入：ConnectionStatus
// 输出：图标组件、色调（ok / pending / bad）与是否旋转
// 预期：两端语义一致；新增状态只改这里，不复制三目链
import { AlertTriangle, CheckCircle2, Loader2, type LucideIcon } from 'lucide-react';
import type { ConnectionStatus } from '../types';

export type ConnectionTone = 'ok' | 'pending' | 'bad';

export interface ConnectionVisual {
  Icon: LucideIcon;
  tone: ConnectionTone;
  spinning: boolean;
}

// connectionVisual 把连接状态映射成展示用的图标与色调。
export function connectionVisual(status: ConnectionStatus): ConnectionVisual {
  switch (status) {
    case 'connected':
      return { Icon: CheckCircle2, tone: 'ok', spinning: false };
    case 'invalid_credentials':
      return { Icon: AlertTriangle, tone: 'bad', spinning: false };
    default:
      return { Icon: Loader2, tone: 'pending', spinning: true };
  }
}

// TONE_TEXT 是纯文字状态的颜色，桌面侧栏用它。
export const TONE_TEXT: Record<ConnectionTone, string> = {
  ok: 'text-emerald-600 dark:text-emerald-400',
  pending: 'text-amber-600 dark:text-amber-400',
  bad: 'text-red-600 dark:text-red-400',
};

// TONE_ICON 是图标本身的颜色。
export const TONE_ICON: Record<ConnectionTone, string> = {
  ok: 'text-emerald-500',
  pending: 'text-amber-500',
  bad: 'text-red-500',
};

// TONE_PILL 是带边框状态块的边框与底色，手机顶栏用它。
export const TONE_PILL: Record<ConnectionTone, string> = {
  ok: 'border-emerald-300/80 bg-emerald-50 text-emerald-700 dark:border-emerald-800/80 dark:bg-emerald-950/50 dark:text-emerald-300',
  pending:
    'border-amber-300/80 bg-amber-50 text-amber-700 dark:border-amber-800/80 dark:bg-amber-950/50 dark:text-amber-300',
  bad: 'border-red-300/80 bg-red-50 text-red-700 dark:border-red-800/80 dark:bg-red-950/50 dark:text-red-300',
};
