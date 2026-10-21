// Shared Passkey login/recovery UI. Secrets stay out of URLs, logs and persistent browser storage.
import { h, confirmDialog } from './ds.js';

function decode(value) {
  const raw = atob(value.replace(/-/g, '+').replace(/_/g, '/'));
  return Uint8Array.from(raw, (c) => c.charCodeAt(0));
}
function encode(value) {
  return btoa(String.fromCharCode(...new Uint8Array(value))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
function publicKeyOptions(data, registration) {
  const options = data.publicKey;
  options.challenge = decode(options.challenge);
  if (registration) options.user.id = decode(options.user.id);
  for (const item of options.allowCredentials || options.excludeCredentials || []) item.id = decode(item.id);
  return options;
}
function credentialJSON(credential) {
  const response = {};
  for (const key of ['clientDataJSON', 'attestationObject', 'authenticatorData', 'signature', 'userHandle']) {
    const value = credential.response[key]; if (value) response[key] = encode(value);
  }
  if (credential.response.getTransports) response.transports = credential.response.getTransports();
  return { id: credential.id, rawId: encode(credential.rawId), type: credential.type, response,
    clientExtensionResults: credential.getClientExtensionResults(), authenticatorAttachment: credential.authenticatorAttachment };
}
async function request(path, body = {}) {
  const response = await fetch('/api/auth/passkey/' + path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || '登录服务暂时不可用，请稍后重试');
  return data;
}

export function passkeyForm(onSuccess, initialMode = 'login') {
  const box = h('div', 'passkey-form');
  const error = h('p', 'field-err'); error.setAttribute('role', 'alert');
  let busy = false, unsaved = false;
  const guard = (event) => { if (unsaved) { event.preventDefault(); event.returnValue = ''; } };
  window.addEventListener('beforeunload', guard);
  const success = (data) => { window.removeEventListener('beforeunload', guard); onSuccess(data); };
  const run = async (action) => {
    if (busy) return;
    error.textContent = '';
    if (!window.isSecureContext || !window.PublicKeyCredential) { error.textContent = 'Passkey 需要支持它的浏览器与 HTTPS；也可使用另一台已绑定设备。'; return; }
    busy = true; box.setAttribute('aria-busy', 'true');
    box.querySelectorAll('button').forEach((button) => { button.disabled = true; });
    try { await action(); }
    catch (failure) { error.textContent = failure.name === 'NotAllowedError' ? '验证已取消或超时，可重新尝试；丢失 Passkey 时使用恢复码。' : failure.message; }
    finally { busy = false; box.setAttribute('aria-busy', 'false'); box.querySelectorAll('button').forEach((button) => { button.disabled = button.dataset.requiresSave === '1'; }); }
  };
  const login = () => run(async () => {
    const options = await request('login/begin');
    const credential = await navigator.credentials.get({ publicKey: publicKeyOptions(options, false) });
    if (!credential) throw new Error('未完成 Passkey 验证');
    success(await request('login/finish', credentialJSON(credential)));
  });
  const paint = (mode) => {
    box.replaceChildren();
    box.append(h('h1', null, mode === 'login' ? '欢迎回来。' : mode === 'recover' ? '找回你的账号' : '激活你的账号'));
    if (mode === 'login') {
      box.appendChild(h('p', 'login-sub', '使用设备解锁或安全密钥验证身份。昵称只用于显示，不再用作登录凭证。'));
      const enter = h('button', 'btn primary cta', '使用 Passkey 登录'); enter.onclick = login; box.appendChild(enter);
      const activate = h('button', 'btn ghost', '首次使用 · 激活账号'); activate.onclick = () => paint('activate');
      const recover = h('button', 'btn ghost', 'Passkey 丢失？恢复账号'); recover.onclick = () => paint('recover'); box.append(activate, recover);
    } else {
      box.appendChild(h('p', 'login-sub', mode === 'recover' ? '输入一条离线保存的恢复码。成功绑定新 Passkey 后，旧 Passkey、旧登录和旧恢复码将全部失效。没有恢复码时，请联系管理员核验账号归属。' : '请先联系管理员核验账号归属，再输入一次性激活码。原有对话和战绩会保留。'));
      const label = h('label', 'field-label', mode === 'recover' ? '恢复码' : '一次性激活码');
      const input = h('input', 'input'); input.id = 'passkey-code'; input.type = 'password'; input.autocomplete = 'off'; input.maxLength = 128; input.spellcheck = false; label.htmlFor = input.id;
      const activate = h('button', 'btn primary cta', '验证并创建 Passkey');
      activate.onclick = () => run(async () => {
        if (!input.value.trim()) throw new Error('请先输入完整的激活码或恢复码');
        const options = await request('register/begin', { code: input.value.trim() });
        const confirmed = await confirmDialog({ title: options.recovery ? '确认恢复账号？' : '确认激活账号？', body: '即将为「' + options.name + '」创建 Passkey。' + (options.recovery ? '验证成功后会撤销旧凭据与所有旧登录。' : '请确认这是你本人拥有的账号。'), confirmText: '创建 Passkey', danger: options.recovery });
        if (!confirmed) return;
        const credential = await navigator.credentials.create({ publicKey: publicKeyOptions(options, true) });
        if (!credential) throw new Error('未创建 Passkey');
        const data = await request('register/finish', credentialJSON(credential));
        input.value = ''; recoverySheet(data.recoveryCodes);
      });
      const back = h('button', 'btn ghost', '返回登录'); back.onclick = () => paint('login');
      box.append(label, input, activate, back);
      input.addEventListener('keydown', (event) => { if (event.key === 'Enter') activate.click(); });
    }
    error.textContent = ''; box.appendChild(error);
  };
  const recoverySheet = (codes) => {
    unsaved = true; box.replaceChildren();
    box.append(h('h1', null, '保存好你的恢复码'), h('p', null, '这些恢复码只在此显示一次。请离线保存，不要发到聊天里。任意一条都能用于绑定新 Passkey，并会撤销其余旧恢复码。'));
    const values = h('pre', 'recovery-codes', codes.join('\n')); box.appendChild(values);
    const download = h('button', 'btn', '下载恢复码文本');
    download.onclick = () => {
      const blob = new Blob(['zkun 账号恢复码（请离线保管）\n恢复一次后本列表全部失效。\n\n' + codes.join('\n')], { type: 'text/plain;charset=utf-8' });
      const url = URL.createObjectURL(blob); const a = h('a'); a.href = url; a.download = 'zkun-recovery-codes.txt'; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
    };
    const label = h('label', 'recovery-confirm'); const check = h('input'); check.type = 'checkbox'; label.append(check, document.createTextNode('我已离线保存恢复码'));
    const enter = h('button', 'btn primary', '继续 Passkey 登录'); enter.disabled = true; enter.dataset.requiresSave = '1';
    check.onchange = () => { enter.dataset.requiresSave = check.checked ? '0' : '1'; enter.disabled = !check.checked; };
    enter.onclick = () => { unsaved = false; login(); }; box.append(download, label, enter, error);
  };
  paint(initialMode); return box;
}
