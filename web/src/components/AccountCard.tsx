import React, { useEffect, useState } from 'react';
import { CheckCircle2, Loader2, RefreshCw, ShieldCheck, UserRound } from 'lucide-react';
import {
  ApiCredentials,
  ApiIdentity,
  finishUserLogin,
  getCredentials,
  getIdentity,
  loginBot,
  saveCredentials,
  startUserLogin,
} from '../api';
import { Translations, interpolate } from '../utils/i18n';
import { Button, Input } from './ui';

// AccountCard 是「凭证 + 登录」卡片。
//
// 输入：t 文案；onNotify 顶部提示回调
// 输出：卡片 UI
// 预期：
//   - 密钥输入框永远不回显已保存内容，只提示「已保存」
//   - user 登录分两步：发验证码 → 提交验证码（需要 2FA 时再填密码）
//   - 改完凭证提示重启，运行中的 bot 连接才会用新值

interface AccountCardProps {
  t: Translations;
  onNotify: (msg: string, kind: 'success' | 'error') => void;
}

type Busy = '' | 'save' | 'bot' | 'code' | 'finish' | 'check';

const MASK = '********';

export const AccountCard: React.FC<AccountCardProps> = ({ t, onNotify }) => {
  const a = t.account;
  const [creds, setCreds] = useState<ApiCredentials | null>(null);
  const [apiId, setApiId] = useState('');
  const [apiHash, setApiHash] = useState(MASK);
  const [botToken, setBotToken] = useState(MASK);
  const [busy, setBusy] = useState<Busy>('');

  const [botIdentity, setBotIdentity] = useState<ApiIdentity | null>(null);
  const [userIdentity, setUserIdentity] = useState<ApiIdentity | null>(null);

  const [phone, setPhone] = useState('');
  const [codeHash, setCodeHash] = useState('');
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
  const [needPassword, setNeedPassword] = useState(false);

  // 读取凭证状态；已保存的密钥显示成占位点，不在输入框里回显真实值
  const refresh = () => {
    getCredentials()
      .then((st) => {
        setCreds(st);
        setApiId(st.api_id > 0 ? MASK : '');
        setApiHash(st.api_hash_set ? MASK : '');
        setBotToken(st.bot_token_set ? MASK : '');
      })
      .catch(() => setCreds(null));
  };

  useEffect(refresh, []);

  const errText = (err: unknown) => (err instanceof Error ? err.message : String(err));

  // saveCreds 保存凭证；值为哨兵点时表示不修改对应密钥
  const saveCreds = () => {
    setBusy('save');
    saveCredentials({
      api_id: apiId === MASK ? 0 : Number(apiId) || 0,
      api_hash: apiHash === MASK ? '' : apiHash.trim(),
      bot_token: botToken === MASK ? '' : botToken.trim(),
    })
      .then((res) => {
        setCreds(res.status);
        setApiId(res.status.api_id > 0 ? MASK : '');
        setApiHash(res.status.api_hash_set ? MASK : '');
        setBotToken(res.status.bot_token_set ? MASK : '');
        onNotify(a.saved, 'success');
      })
      .catch((err) => onNotify(errText(err), 'error'))
      .finally(() => setBusy(''));
  };

  const doBotLogin = () => {
    setBusy('bot');
    loginBot(botToken === MASK ? '' : botToken.trim())
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
    startUserLogin(phone.trim())
      .then((res) => {
        setCodeHash(res.phone_code_hash);
        onNotify(a.codeSent, 'success');
      })
      .catch((err) => onNotify(errText(err), 'error'))
      .finally(() => setBusy(''));
  };

  const doFinishLogin = () => {
    setBusy('finish');
    finishUserLogin({
      phone: phone.trim(),
      code: code.trim(),
      phone_code_hash: codeHash,
      password: needPassword ? password : undefined,
    })
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
      .then((res) => {
        if (kind === 'bot') setBotIdentity(res.user);
        else setUserIdentity(res.user);
      })
      .catch((err) => onNotify(errText(err), 'error'))
      .finally(() => setBusy(''));
  };

  const identityText = (idn: ApiIdentity | null, hasSession: boolean | undefined) => {
    if (idn) return `@${idn.username || '—'} · ${idn.name || ''}`.trim();
    return hasSession ? a.checkHint : a.notLoggedIn;
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <h2 className="text-lg font-semibold tracking-tight text-zinc-950 dark:text-white">
          {a.title}
        </h2>
      </div>

      <section className="surface-card rounded-2xl p-6 space-y-4">
        <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">{a.credentials}</h2>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
          <div className="space-y-1.5">
            <label htmlFor="acc-api-id" className="block text-xs font-medium">
              {a.apiId}
            </label>
            <Input
              id="acc-api-id"
              type="password"
              dir="ltr"
              inputMode="numeric"
              autoComplete="off"
              value={apiId}
              onFocus={(e) => e.currentTarget.select()}
              onChange={(e) => setApiId(e.target.value)}
            />
            <p className="text-[11px] text-zinc-500 dark:text-zinc-400">{a.apiIdHint}</p>
          </div>
          <div className="space-y-1.5">
            <label htmlFor="acc-api-hash" className="block text-xs font-medium">
              {a.apiHash}
            </label>
            <Input
              id="acc-api-hash"
              type="password"
              dir="ltr"
              autoComplete="off"
              placeholder={a.apiHashEmpty}
              value={apiHash}
              onFocus={(e) => e.currentTarget.select()}
              onChange={(e) => setApiHash(e.target.value)}
            />
          </div>
          <div className="space-y-1.5 md:col-span-2">
            <label htmlFor="acc-bot-token" className="block text-xs font-medium">
              {a.botToken}
            </label>
            <Input
              id="acc-bot-token"
              type="password"
              dir="ltr"
              autoComplete="off"
              placeholder={a.botTokenEmpty}
              value={botToken}
              onChange={(e) => setBotToken(e.target.value)}
            />
            <p className="text-[11px] text-zinc-500 dark:text-zinc-400">{a.botTokenHint}</p>
          </div>
        </div>
        <div className="flex items-center justify-end">
          <Button type="button" variant="accent" onClick={saveCreds} disabled={busy !== ''}>
            {busy === 'save' ? (
              <Loader2 className="w-3.5 h-3.5 animate-spin" />
            ) : (
              <ShieldCheck className="w-3.5 h-3.5" />
            )}
            <span>{a.save}</span>
          </Button>
        </div>
      </section>

      <section className="surface-card rounded-2xl p-6 space-y-5">
        <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">{a.login}</h2>

        <div className="space-y-2">
          <div className="flex items-center justify-between gap-3">
            <span className="text-xs font-medium">{a.botStatus}</span>
            <Button
              type="button"
              variant="ghost"
              onClick={() => checkIdentity('bot')}
              disabled={busy !== ''}
            >
              <RefreshCw className={`w-3.5 h-3.5 ${busy === 'check' ? 'animate-spin' : ''}`} />
              <span>{a.check}</span>
            </Button>
          </div>
          <p className="text-xs text-zinc-500 dark:text-zinc-400 break-all">
            {identityText(botIdentity, creds?.bot_session)}
          </p>
          {creds?.bot_session && !botIdentity && (
            <p className="text-[11px] text-zinc-500 dark:text-zinc-400">{a.botLoginHint}</p>
          )}
          <div className="flex items-center gap-2">
            <Button type="button" variant="outline" onClick={doBotLogin} disabled={busy !== ''}>
              {busy === 'bot' ? (
                <Loader2 className="w-3.5 h-3.5 animate-spin" />
              ) : (
                <UserRound className="w-3.5 h-3.5" />
              )}
              <span>{a.botLogin}</span>
            </Button>
          </div>
        </div>

        <div className="border-t border-zinc-200/80 dark:border-zinc-800/80 pt-5 space-y-3">
          <div className="flex items-center justify-between gap-3">
            <span className="text-xs font-medium">{a.userStatus}</span>
            <Button
              type="button"
              variant="ghost"
              onClick={() => checkIdentity('user')}
              disabled={busy !== ''}
            >
              <RefreshCw className={`w-3.5 h-3.5 ${busy === 'check' ? 'animate-spin' : ''}`} />
              <span>{a.check}</span>
            </Button>
          </div>
          <p className="text-xs text-zinc-500 dark:text-zinc-400 break-all">
            {identityText(userIdentity, creds?.user_session)}
          </p>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label htmlFor="acc-phone" className="block text-xs font-medium">
                {a.phone}
              </label>
              <Input
                id="acc-phone"
                dir="ltr"
                inputMode="tel"
                placeholder={a.phonePlaceholder}
                value={phone}
                onChange={(e) => setPhone(e.target.value)}
              />
            </div>
            <div className="flex items-end">
              <Button
                type="button"
                variant="outline"
                onClick={doSendCode}
                disabled={busy !== '' || !phone.trim()}
              >
                {busy === 'code' ? (
                  <Loader2 className="w-3.5 h-3.5 animate-spin" />
                ) : (
                  <ShieldCheck className="w-3.5 h-3.5" />
                )}
                <span>{a.sendCode}</span>
              </Button>
            </div>
          </div>

          {codeHash !== '' && (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <label htmlFor="acc-code" className="block text-xs font-medium">
                  {a.code}
                </label>
                <Input
                  id="acc-code"
                  dir="ltr"
                  inputMode="numeric"
                  placeholder={a.codePlaceholder}
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                />
              </div>
              {needPassword && (
                <div className="space-y-1.5">
                  <label htmlFor="acc-2fa" className="block text-xs font-medium">
                    {a.twofa}
                  </label>
                  <Input
                    id="acc-2fa"
                    type="password"
                    dir="ltr"
                    autoComplete="off"
                    placeholder={a.twofaPlaceholder}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                  />
                </div>
              )}
              <div className="md:col-span-2 flex items-center gap-2">
                <Button
                  type="button"
                  variant="accent"
                  onClick={doFinishLogin}
                  disabled={busy !== '' || !code.trim()}
                >
                  {busy === 'finish' ? (
                    <Loader2 className="w-3.5 h-3.5 animate-spin" />
                  ) : (
                    <CheckCircle2 className="w-3.5 h-3.5" />
                  )}
                  <span>{a.submitLogin}</span>
                </Button>
                <span className="text-[11px] text-zinc-500 dark:text-zinc-400">{a.userLoginHint}</span>
              </div>
            </div>
          )}
        </div>
      </section>
    </div>
  );
};
