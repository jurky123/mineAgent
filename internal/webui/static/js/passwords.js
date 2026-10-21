// Shared login: claim an unprotected account once, then require its password.
import { h } from './ds.js';
import { passkeyForm } from './passkeys.js';

export function passwordForm(onSuccess) {
  const root = h('div', 'passkey-form');
  let isNewAccount = false;
  let name = (localStorage.getItem('mineagent.lastName') || '').trim(), busy = false;
  const request = async (action, body) => {
    const response = await fetch('/api/auth/password/' + action, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.error || '登录暂时不可用，请稍后重试');
    return data;
  };
  const paint = (mode = 'name') => {
    root.replaceChildren();
    root.append(h('h1', null, mode === 'setup' ? (isNewAccount ? '创建账号' : '为账号设置密码') : '登录 zkun'));
    if (mode === 'name') root.appendChild(h('p', 'login-sub', '输入账号名称继续。首次使用可创建账号；已有账号请使用原名称。'));
    const error = h('p', 'field-err'); error.setAttribute('role', 'alert');
    const form = h('form', 'password-fields');
    const label = h('label', 'field-label', '账号名称');
    const input = h('input', 'input'); input.id = 'login-name'; input.name = 'username'; input.autocomplete = 'username'; input.maxLength = 24; input.value = name; input.readOnly = mode !== 'name'; label.htmlFor = input.id;
    form.append(label, input);
    let password, confirm;
    if (mode !== 'name') {
      root.appendChild(h('p', 'login-sub', mode === 'setup' ? (isNewAccount ? '设置密码后创建账号，可在其他设备使用同一账号登录。' : '这个账号尚未设置密码。创建后，其他设备也需使用此密码登录；原有对话和战绩会保留。') : '请输入这个账号的密码。'));
      const passLabel = h('label', 'field-label', mode === 'setup' ? '创建密码' : '密码');
      password = h('input', 'input'); password.id = 'login-password'; password.name = 'password'; password.type = 'password'; password.autocomplete = mode === 'setup' ? 'new-password' : 'current-password'; password.minLength = 12; password.maxLength = 128; password.required = true; passLabel.htmlFor = password.id;
      form.append(passLabel, password);
      if (mode === 'setup') {
        const hint = h('p', 'login-sub', '使用 12–128 个字符，可以使用较长、容易记住的短语。'); form.appendChild(hint);
        const confirmLabel = h('label', 'field-label', '再次输入密码');
        confirm = h('input', 'input'); confirm.id = 'confirm-password'; confirm.name = 'confirm'; confirm.type = 'password'; confirm.autocomplete = 'new-password'; confirm.required = true; confirm.minLength = 12; confirm.maxLength = 128; confirmLabel.htmlFor = confirm.id;
        form.append(confirmLabel, confirm);
      }
    }
    const submit = h('button', 'btn primary cta', mode === 'name' ? '继续' : mode === 'setup' ? '设置密码并进入' : '登录'); submit.type = 'submit';
    form.append(submit, error); root.appendChild(form);
    const run = async (action) => {
      if (busy) return; busy = true; error.textContent = ''; root.setAttribute('aria-busy', 'true');
      root.querySelectorAll('button').forEach((button) => { button.disabled = true; });
      try { await action(); }
      catch (failure) { error.textContent = failure.message; }
      finally { busy = false; root.setAttribute('aria-busy', 'false'); root.querySelectorAll('button').forEach((button) => { button.disabled = false; }); }
    };
    form.onsubmit = (event) => {
      event.preventDefault();
      run(async () => {
        if (!window.isSecureContext) throw new Error('请使用本站 HTTPS 页面设置密码或登录');
        name = input.value.trim(); if (!name) throw new Error('请先输入账号名称');
        if (mode === 'name') {
          const status = await request('status', { name });
          if (status.passkeyOnly) throw new Error('这个账号已绑定 Passkey，请使用下方 Passkey 登录');
          isNewAccount = !!status.newAccount; paint(status.needsSetup ? 'setup' : 'login'); return;
        }
        if (confirm && password.value !== confirm.value) throw new Error('两次输入的密码不一致');
        const data = await request(mode === 'setup' ? 'setup' : 'login', { name, password: password.value, confirm: confirm?.value });
        password.value = ''; if (confirm) confirm.value = ''; onSuccess(data);
      });
    };
    if (mode !== 'name') {
      const back = h('button', 'btn ghost', '换个账号'); back.onclick = () => paint('name'); root.appendChild(back);
      root.appendChild(h('p', 'login-sub', '忘记密码请联系管理员核验后重置。'));
    }
    const alternative = h('button', 'btn ghost', '使用 Passkey 登录'); alternative.onclick = () => {
      root.replaceChildren(passkeyForm(onSuccess));
      const back = h('button', 'btn ghost', '返回密码登录'); back.onclick = () => paint('name'); root.appendChild(back);
    }; root.appendChild(alternative);
    const recovery = h('details', 'login-recovery'); recovery.appendChild(h('summary', null, '无法登录？'));
    recovery.append(h('p', 'login-sub', '忘记密码：联系管理员核验后重置。Passkey 丢失：使用离线恢复码重新绑定。'), h('a', 'login-help-link', '查看 Passkey 恢复流程'));
    recovery.querySelector('a').href = '/account?recover=1'; root.appendChild(recovery);
    if (matchMedia('(hover: hover) and (pointer: fine)').matches) requestAnimationFrame(() => (password || input).focus());
  };
  if (new URLSearchParams(location.search).has('recover')) root.replaceChildren(passkeyForm(onSuccess, 'recover')); else paint(); return root;
}
