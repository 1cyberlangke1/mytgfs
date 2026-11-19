import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  Check,
  CheckCircle2,
  Loader2,
  RefreshCw,
  RotateCcw,
  Save,
  ShieldCheck,
  UserRound,
} from 'lucide-react';
import { motion } from 'motion/react';
import {
  finishUserLogin,
  getAutostart,
  getCredentials,
  getIdentity,
  loginBot,
  saveCredentials,
  setAutostart,
  startUserLogin,
} from '../api';
import type { ApiCredentials, ApiIdentity } from '../api';
import type { AppSettingDef, AppSettings, Locale } from '../types';
import { errText } from '../utils/errorText';
import { formatNumber, interpolate, type Translations } from '../utils/i18n';
import {
  PANEL_HIDDEN,
  SECTION_ORDER,
  hintOf,
  labelOf,
  sectionOf,
  sectionTitle,
  shortcutLabel,
  validateOne,
} from '../utils/settingsFields';
import {
  DEFAULT_SHORTCUTS,
  SHORTCUT_ACTIONS,
  formatShortcut,
  shortcutConflict,
  shortcutFromEvent,
  type ShortcutAction,
  type ShortcutMap,
} from '../utils/shortcuts';
import { Button, IOS_SOFT_SPRING, Input, Switch } from './ui';

/** MASK 是已保存密钥的占位：输入框里显示它表示「不修改」 */
const ACCOUNT_MASK = '********';
type Busy = '' | 'save' | 'bot' | 'code' | 'finish' | 'check';

/** AccountController 是凭证/登录状态的契约（AccountCard 内部使用） */
type AccountController = ReturnType<typeof useAccount>;

function useAccount({ t, onNotify }: { t: Translations; onNotify: (msg: string, kind: 'success' | 'error') => void }) {
  const a = t.account;
  const [creds, setCreds] = useState<ApiCredentials | null>(null);
  const [apiId, setApiId] = useState('');
  const [apiHash, setApiHash] = useState(ACCOUNT_MASK);
  const [botToken, setBotToken] = useState(ACCOUNT_MASK);
  const [busy, setBusy] = useState<Busy>('');
  const [botIdentity, setBotIdentity] = useState<ApiIdentity | null>(null);
  const [userIdentity, setUserIdentity] = useState<ApiIdentity | null>(null);
  const [phone, setPhone] = useState('');
  const [codeHash, setCodeHash] = useState('');
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
  const [needPassword, setNeedPassword] = useState(false);

  const applyCreds = (st: ApiCredentials) => {
    setCreds(st);
    setApiId(st.api_id > 0 ? ACCOUNT_MASK : '');
    setApiHash(st.api_hash_set ? ACCOUNT_MASK : '');
    setBotToken(st.bot_token_set ? ACCOUNT_MASK : '');
  };
  const refresh = () => getCredentials().then(applyCreds).catch(() => setCreds(null));
  useEffect(() => {
    void refresh();
  }, []);

  // 已保存的凭证够不够登录：后端只认盘上的 config.toml + 密钥文件
  const savedReady = Boolean(creds?.api_id && creds?.api_hash_set);
  // 输入框里刚填、还没保存的也算：点登录会先自动落盘再继续
  const fillingReady = apiId !== ACCOUNT_MASK && Number(apiId) > 0 && apiHash !== ACCOUNT_MASK && apiHash.trim() !== '';
  const loginReady = savedReady || fillingReady;

  const body = () => ({
    api_id: apiId === ACCOUNT_MASK ? 0 : Number(apiId) || 0,
    api_hash: apiHash === ACCOUNT_MASK ? '' : apiHash.trim(),
    bot_token: botToken === ACCOUNT_MASK ? '' : botToken.trim(),
  });
  // 登录前先把输入框里的凭证落盘：安卓全新安装没有 config.toml
  const ensureCreds = async () => {
    const b = body();
    if (!b.api_id && !b.api_hash && !b.bot_token) return;
    applyCreds((await saveCredentials(b)).status);
  };

  const saveCreds = () => {
    setBusy('save');
    saveCredentials(body())
      .then((res) => {
        applyCreds(res.status);
        onNotify(a.saved, 'success');
      })
      .catch((err) => onNotify(errText(err), 'error'))
      .finally(() => setBusy(''));
  };
  const doBotLogin = () => {
    setBusy('bot');
    const token = botToken === ACCOUNT_MASK ? '' : botToken.trim();
    ensureCreds()
      .then(() => loginBot(token))
      .then((res) => {
        setBotIdentity(res.user);
        setBotToken('');
        onNotify(interpolate(a.loggedIn, { name: res.user.username || res.user.name }), 'success');
        refresh();
      })
      .catch((err) => onNotify(errText(err), 'error'))
      .finally(() => setBusy(''));
  };
  const doSendCode = () => {
    setBusy('code');
    ensureCreds()
      .then(() => startUserLogin(phone.trim()))
      .then((res) => {
        setCodeHash(res.phone_code_hash);
        onNotify(a.codeSent, 'success');
      })
      .catch((err) => onNotify(errText(err), 'error'))
      .finally(() => setBusy(''));
  };
  const doFinishLogin = () => {
    setBusy('finish');
    ensureCreds()
      .then(() =>
        finishUserLogin({
          phone: phone.trim(),
          code: code.trim(),
          phone_code_hash: codeHash,
          password: needPassword ? password : undefined,
        })
      )
      .then((res) => {
        if (res.need_password) {
          setNeedPassword(true);
          onNotify(a.needPasswordHint, 'error');
          return;
        }
        setUserIdentity(res.user);
        setCode('');
        setPassword('');
        setCodeHash('');
        setNeedPassword(false);
        onNotify(interpolate(a.loggedIn, { name: res.user.username || res.user.name }), 'success');
        refresh();
      })
      .catch((err) => onNotify(errText(err), 'error'))
      .finally(() => setBusy(''));
  };
  // checkIdentity 只有点了才连 Telegram，避免每次进设置页都建连接
  const checkIdentity = (kind: 'bot' | 'user') => {
    setBusy('check');
    getIdentity(kind)
      .then((res) => (kind === 'bot' ? setBotIdentity(res.user) : setUserIdentity(res.user)))
      .catch((err) => onNotify(errText(err), 'error'))
      .finally(() => setBusy(''));
  };

  return {
    creds, apiId, setApiId, apiHash, setApiHash, botToken, setBotToken, busy,
    botIdentity, userIdentity, phone, setPhone, codeHash, code, setCode,
    password, setPassword, needPassword, loginReady,
    saveCreds, doBotLogin, doSendCode, doFinishLogin, checkIdentity,
  };
}

function useSettingsForm(p: {
  settings: AppSettings;
  t: Translations;
  onSaveSettings: (values: Record<string, string>) => Promise<void>;
  onNotifyError: (msg: string) => void;
}) {
  const { settings, t, onSaveSettings, onNotifyError } = p;
  const visibleSchema = useMemo(() => settings.schema.filter((def) => !PANEL_HIDDEN.has(def.key)), [settings.schema]);
  const [draft, setDraft] = useState<Record<string, string>>(settings.values);
  const [saved, setSaved] = useState<Record<string, string>>(settings.values);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [savingSection, setSavingSection] = useState('');
  const [savedSection, setSavedSection] = useState('');
  const savedRef = useRef(saved);
  savedRef.current = saved;

  // 外部改了配置（推送）时才同步；正在编辑的字段保留用户输入
  useEffect(() => {
    const incoming = settings.values;
    setDraft((prev) => {
      const next = { ...prev };
      for (const k of Object.keys(incoming)) {
        const value = incoming[k];
        if (prev[k] === savedRef.current[k] && value !== undefined) next[k] = value;
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
      map.set(sec, [...(map.get(sec) ?? []), def]);
    }
    return [...map.entries()].sort((a, b) => SECTION_ORDER.indexOf(a[0]) - SECTION_ORDER.indexOf(b[0]));
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
    if (firstError) return onNotifyError(firstError);
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
      .catch((err) => onNotifyError(errText(err)))
      .finally(() => setSavingSection(''));
  };
  const setValue = (key: string, value: string) => {
    setDraft((prev) => ({ ...prev, [key]: value }));
    setErrors((prev) => (prev[key] ? { ...prev, [key]: '' } : prev));
  };

  return { groups, draft, setValue, errors, isDirty, sectionDirty, saveSection, savingSection, savedSection };
}

function SettingField(p: {
  t: Translations;
  locale: Locale;
  def: AppSettingDef;
  value: string;
  error: string;
  dirty: boolean;
  onChange: (value: string) => void;
}) {
  const { t, locale, def, value, error, dirty, onChange } = p;
  const id = `setting-${def.key}`;
  const label = labelOf(t, def.key);
  const marked = Boolean(error) || dirty;
  const hint =
    def.kind === 'int' && def.min !== undefined && def.max !== undefined
      ? `${formatNumber(def.min, locale)} – ${formatNumber(def.max, locale)}`
      : def.options && def.options.length > 0
        ? def.options.join(' / ')
        : hintOf(t, def.key);
  const labelClass = marked ? 'text-red-600 dark:text-red-400' : 'text-zinc-700 dark:text-zinc-300';
  const controlClass = marked ? 'border-red-500/80 focus-visible:ring-red-500/30 dark:border-red-500/70' : '';
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className={`block text-xs font-medium ${labelClass}`}>
        {label}
      </label>
      {def.kind === 'bool' ? (
        <div className="flex items-center gap-2 h-9">
          <Switch checked={value === 'true'} onCheckedChange={(on) => onChange(on ? 'true' : 'false')} aria-label={label} />
          <span className={`text-xs ${marked ? 'text-red-600 dark:text-red-400' : 'text-zinc-500 dark:text-zinc-400'}`}>
            {value === 'true' ? t.settings.boolOn : t.settings.boolOff}
          </span>
        </div>
      ) : def.options && def.options.length > 0 ? (
        <select
          id={id}
          value={value}
          onChange={(e) => onChange(e.target.value)}
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
          value={value}
          onChange={(e) => onChange(e.target.value)}
          aria-invalid={Boolean(error)}
          className={`font-mono tabular-nums text-start ${controlClass}`}
        />
      )}
      <p className={`text-[11px] ${error ? 'text-red-600 dark:text-red-400' : 'text-zinc-500 dark:text-zinc-400'}`}>
        {error || hint}
      </p>
    </div>
  );
}

function ShortcutsCard(p: { t: Translations; locale: Locale; shortcuts: ShortcutMap; onChangeShortcuts: (next: ShortcutMap) => void }) {
  const { t, locale, shortcuts, onChangeShortcuts } = p;
  const [listening, setListening] = useState<ShortcutAction | null>(null);
  const [shortcutError, setShortcutError] = useState('');

  useEffect(() => {
    if (!listening) return;
    const onKey = (e: KeyboardEvent) => {
      e.preventDefault();
      e.stopPropagation();
      if (e.key === 'Escape') {
        setListening(null);
        setShortcutError('');
        return;
      }
      const spec = shortcutFromEvent(e);
      if (!spec) return;
      const conflict = shortcutConflict(shortcuts, listening, spec);
      if (conflict) return setShortcutError(interpolate(t.settings.shortcutConflict, { key: spec, action: shortcutLabel(t, conflict) }, locale));
      setShortcutError('');
      onChangeShortcuts({ ...shortcuts, [listening]: spec });
      setListening(null);
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [listening, shortcuts, t, locale, onChangeShortcuts]);

  const reset = (action?: ShortcutAction) => {
    onChangeShortcuts(action ? { ...shortcuts, [action]: DEFAULT_SHORTCUTS[action] } : { ...DEFAULT_SHORTCUTS });
    setShortcutError('');
    setListening(null);
  };

  return (
    <section className="surface-card rounded-2xl p-6 space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1">
          <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">{t.settings.shortcutsTitle}</h2>
          <p className="text-[11px] text-zinc-500 dark:text-zinc-400">{t.settings.shortcutsHint}</p>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={() => reset()}>
          {t.settings.shortcutResetAll}
        </Button>
      </div>
      <div className="stagger space-y-2">
        {SHORTCUT_ACTIONS.map((action) => {
          const active = listening === action;
          return (
            <div key={action} className="flex items-center justify-between gap-3">
              <span className="text-xs text-zinc-700 dark:text-zinc-300">{shortcutLabel(t, action)}</span>
              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  onClick={() => {
                    setListening(active ? null : action);
                    setShortcutError('');
                  }}
                  aria-label={`${shortcutLabel(t, action)}: ${active ? t.settings.shortcutListening : formatShortcut(shortcuts[action])}`}
                  className={`min-w-[96px] px-2.5 py-1 rounded-lg border font-mono text-[11px] text-center transition-colors cursor-pointer ${
                    active
                      ? 'border-[#007AFF] text-[#007AFF] bg-[#007AFF]/10 animate-pulse'
                      : 'border-zinc-300 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-900 text-zinc-800 dark:text-zinc-200 hover:border-zinc-400 dark:hover:border-zinc-600'
                  }`}
                >
                  {active ? t.settings.shortcutListening : formatShortcut(shortcuts[action])}
                </button>
                <Button type="button" variant="ghost" size="icon" onClick={() => reset(action)} aria-label={t.settings.shortcutReset} title={t.settings.shortcutReset}>
                  <RotateCcw className="w-3.5 h-3.5" />
                </Button>
              </div>
            </div>
          );
        })}
      </div>
      {shortcutError && (
        <p role="alert" className="text-[11px] text-red-600 dark:text-red-400">
          {shortcutError}
        </p>
      )}
    </section>
  );
}

function AutostartCard({ t, onNotifyError }: { t: Translations; onNotifyError: (msg: string) => void }) {
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let cancelled = false;
    getAutostart()
      .then((res) => {
        if (!cancelled) setEnabled(res.enabled);
      })
      .catch((err) => {
        if (!cancelled) onNotifyError(errText(err));
      });
    return () => {
      cancelled = true;
    };
  }, []);
  const toggle = (on: boolean) => {
    setBusy(true);
    setAutostart(on)
      .then((res) => setEnabled(res.enabled))
      .catch((err) => onNotifyError(errText(err)))
      .finally(() => setBusy(false));
  };
  return (
    <section className="surface-card rounded-2xl p-6 space-y-4">
      <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">{t.settings.autostartTitle}</h2>
      <div className="flex items-center justify-between gap-4">
        <div className="space-y-1">
          <p className="text-xs font-medium text-zinc-700 dark:text-zinc-300">{t.settings.autostartLabel}</p>
          <p className="text-[11px] text-zinc-500 dark:text-zinc-400">{t.settings.autostartHint}</p>
        </div>
        <div className="flex items-center gap-2">
          <Switch checked={enabled === true} disabled={busy} onCheckedChange={toggle} aria-label={t.settings.autostartLabel} />
          <span className="text-xs text-zinc-500 dark:text-zinc-400">{enabled === true ? t.settings.boolOn : t.settings.boolOff}</span>
        </div>
      </div>
    </section>
  );
}

function CredentialsCard({ t, account }: { t: Translations; account: AccountController }) {
  const a = t.account;
  const field = (
    id: string,
    label: string,
    value: string,
    onChange: (v: string) => void,
    extra: { type?: string; placeholder?: string; inputMode?: 'numeric' | 'tel'; hint?: string; span?: boolean }
  ) => (
    <div className={`space-y-1.5 ${extra.span ? 'md:col-span-2' : ''}`}>
      <label htmlFor={id} className="block text-xs font-medium">
        {label}
      </label>
      <Input
        id={id}
        type={extra.type ?? 'password'}
        dir="ltr"
        inputMode={extra.inputMode}
        autoComplete="off"
        placeholder={extra.placeholder}
        value={value}
        onFocus={(e) => e.currentTarget.select()}
        onChange={(e) => onChange(e.target.value)}
      />
      {extra.hint && <p className="text-[11px] text-zinc-500 dark:text-zinc-400">{extra.hint}</p>}
    </div>
  );
  return (
    <section className="surface-card rounded-2xl p-6 space-y-4">
      <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">{a.credentials}</h2>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
        {field('acc-api-id', a.apiId, account.apiId, account.setApiId, { inputMode: 'numeric', hint: a.apiIdHint })}
        {field('acc-api-hash', a.apiHash, account.apiHash, account.setApiHash, { placeholder: a.apiHashEmpty })}
        {field('acc-bot-token', a.botToken, account.botToken, account.setBotToken, { placeholder: a.botTokenEmpty, hint: a.botTokenHint, span: true })}
      </div>
      <div className="flex items-center justify-end">
        <Button type="button" variant="accent" onClick={account.saveCreds} disabled={account.busy !== ''}>
          {account.busy === 'save' ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <ShieldCheck className="w-3.5 h-3.5" />}
          <span>{a.save}</span>
        </Button>
      </div>
    </section>
  );
}

function LoginCard({ t, account }: { t: Translations; account: AccountController }) {
  const a = t.account;
  const identityText = (idn: ApiIdentity | null, hasSession: boolean | undefined) =>
    idn ? `@${idn.username || '—'} · ${idn.name || ''}`.trim() : hasSession ? a.checkHint : a.notLoggedIn;

  return (
    <section className="surface-card rounded-2xl p-6 space-y-5">
      <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">{a.login}</h2>
      {!account.loginReady && <p className="text-[11px] text-amber-600 dark:text-amber-400">{a.credentialsRequired}</p>}

      <div className="space-y-2">
        <div className="flex items-center justify-between gap-3">
          <span className="text-xs font-medium">{a.botStatus}</span>
          <Button type="button" variant="ghost" onClick={() => account.checkIdentity('bot')} disabled={account.busy !== ''}>
            <RefreshCw className={`w-3.5 h-3.5 ${account.busy === 'check' ? 'animate-spin' : ''}`} />
            <span>{a.check}</span>
          </Button>
        </div>
        <p className="text-xs text-zinc-500 dark:text-zinc-400 break-all">{identityText(account.botIdentity, account.creds?.bot_session)}</p>
        {account.creds?.bot_session && !account.botIdentity && <p className="text-[11px] text-zinc-500 dark:text-zinc-400">{a.botLoginHint}</p>}
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" onClick={account.doBotLogin} disabled={account.busy !== '' || !account.loginReady}>
            {account.busy === 'bot' ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <UserRound className="w-3.5 h-3.5" />}
            <span>{a.botLogin}</span>
          </Button>
        </div>
      </div>

      <div className="border-t border-zinc-200/80 dark:border-zinc-800/80 pt-5 space-y-3">
        <div className="flex items-center justify-between gap-3">
          <span className="text-xs font-medium">{a.userStatus}</span>
          <Button type="button" variant="ghost" onClick={() => account.checkIdentity('user')} disabled={account.busy !== ''}>
            <RefreshCw className={`w-3.5 h-3.5 ${account.busy === 'check' ? 'animate-spin' : ''}`} />
            <span>{a.check}</span>
          </Button>
        </div>
        <p className="text-xs text-zinc-500 dark:text-zinc-400 break-all">{identityText(account.userIdentity, account.creds?.user_session)}</p>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <label htmlFor="acc-phone" className="block text-xs font-medium">
              {a.phone}
            </label>
            <Input id="acc-phone" dir="ltr" inputMode="tel" placeholder={a.phonePlaceholder} value={account.phone} onChange={(e) => account.setPhone(e.target.value)} />
          </div>
          <div className="flex items-end">
            <Button type="button" variant="outline" onClick={account.doSendCode} disabled={account.busy !== '' || !account.phone.trim() || !account.loginReady}>
              {account.busy === 'code' ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <ShieldCheck className="w-3.5 h-3.5" />}
              <span>{a.sendCode}</span>
            </Button>
          </div>
        </div>

        {account.codeHash !== '' && (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label htmlFor="acc-code" className="block text-xs font-medium">
                {a.code}
              </label>
              <Input id="acc-code" dir="ltr" inputMode="numeric" placeholder={a.codePlaceholder} value={account.code} onChange={(e) => account.setCode(e.target.value)} />
            </div>
            {account.needPassword && (
              <div className="space-y-1.5">
                <label htmlFor="acc-2fa" className="block text-xs font-medium">
                  {a.twofa}
                </label>
                <Input id="acc-2fa" type="password" dir="ltr" autoComplete="off" placeholder={a.twofaPlaceholder} value={account.password} onChange={(e) => account.setPassword(e.target.value)} />
              </div>
            )}
            <div className="md:col-span-2 flex items-center gap-2">
              <Button type="button" variant="accent" onClick={account.doFinishLogin} disabled={account.busy !== '' || !account.code.trim() || !account.loginReady}>
                {account.busy === 'finish' ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <CheckCircle2 className="w-3.5 h-3.5" />}
                <span>{a.submitLogin}</span>
              </Button>
              <span className="text-[11px] text-zinc-500 dark:text-zinc-400">{a.userLoginHint}</span>
            </div>
          </div>
        )}
      </div>
    </section>
  );
}

export const AccountCard: React.FC<{ t: Translations; onNotify: (msg: string, kind: 'success' | 'error') => void }> = ({ t, onNotify }) => {
  const account = useAccount({ t, onNotify });
  return (
    <div className="stagger space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <h2 className="text-lg font-semibold tracking-tight text-zinc-950 dark:text-white">{t.account.title}</h2>
      </div>
      <CredentialsCard t={t} account={account} />
      <LoginCard t={t} account={account} />
    </div>
  );
};

interface SettingsViewProps {
  t: Translations;
  locale: Locale;
  settings: AppSettings;
  canAutostart: boolean;
  /** 设备有硬件键盘：false 时整块快捷键设置不渲染 */
  canUseShortcuts: boolean;
  shortcuts: ShortcutMap;
  onChangeShortcuts: (next: ShortcutMap) => void;
  onSaveSettings: (values: Record<string, string>) => Promise<void>;
  onNotifyError: (msg: string) => void;
}

export const SettingsView: React.FC<SettingsViewProps> = (p) => {
  const { t, locale, settings, canAutostart, canUseShortcuts, shortcuts, onChangeShortcuts, onSaveSettings, onNotifyError } = p;
  const { groups, draft, setValue, errors, isDirty, sectionDirty, saveSection, savingSection, savedSection } = useSettingsForm({
    settings,
    t,
    onSaveSettings,
    onNotifyError,
  });

  return (
    <motion.div layout transition={IOS_SOFT_SPRING} className="stagger space-y-6">
      <h1 className="text-xl sm:text-2xl font-semibold tracking-tight text-zinc-950 dark:text-white">{t.settings.title}</h1>

      {canAutostart && <AutostartCard t={t} onNotifyError={onNotifyError} />}

      {groups.map(([section, defs]) => {
        const dirty = sectionDirty(defs);
        const saving = savingSection === section;
        const done = savedSection === section;
        const blocked = defs.some((d) => Boolean(errors[d.key]));
        return (
          <section key={section} className="surface-card rounded-2xl p-6 space-y-4">
            <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">{sectionTitle(t, section)}</h2>
            <div className="stagger grid grid-cols-1 md:grid-cols-2 gap-5">
              {defs.map((def) => (
                <SettingField
                  key={def.key}
                  t={t}
                  locale={locale}
                  def={def}
                  value={draft[def.key] ?? ''}
                  error={errors[def.key] ?? ''}
                  dirty={isDirty(def.key)}
                  onChange={(value) => setValue(def.key, value)}
                />
              ))}
            </div>
            <div className="flex items-center justify-end gap-3">
              {done && (
                <span className="flex items-center gap-1 text-[11px] text-emerald-600 dark:text-emerald-400">
                  <Check className="w-3.5 h-3.5" />
                  {t.settings.savedCard}
                </span>
              )}
              <Button type="button" variant="accent" disabled={!dirty || saving || blocked} onClick={() => saveSection(section, defs)}>
                {saving ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Save className="w-3.5 h-3.5" />}
                <span>{saving ? t.settings.savingBtn : t.settings.saveCard}</span>
              </Button>
            </div>
          </section>
        );
      })}

      {canUseShortcuts && <ShortcutsCard t={t} locale={locale} shortcuts={shortcuts} onChangeShortcuts={onChangeShortcuts} />}
    </motion.div>
  );
};
