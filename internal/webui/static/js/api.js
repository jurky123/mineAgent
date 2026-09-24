// 后端 API 封装：统一带 token、401 交给 app 处理

import { S, clearAuth } from './state.js';
import { bus } from './ui.js';

let unauthorizedHandler = null;
export function setUnauthorizedHandler(fn) { unauthorizedHandler = fn; }

export async function api(path, opts) {
  opts = opts || {};
  opts.headers = Object.assign({}, opts.headers, { 'Authorization': 'Bearer ' + S.token });
  const res = await fetch(path, opts);
  let body = null;
  try { body = await res.json(); } catch (e) { /* 空响应 */ }
  if (res.status === 401) {
    clearAuth();
    if (unauthorizedHandler) unauthorizedHandler();
    throw new Error('登录已失效，请重新进入');
  }
  if (!res.ok) throw new Error((body && body.error) || ('HTTP ' + res.status));
  return body;
}

export const apiGet = (path) => api(path);
export const apiPost = (path, payload) => api(path, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(payload || {})
});

export function logoutRequest() {
  return fetch('/api/logout', { method: 'POST', headers: { 'Authorization': 'Bearer ' + S.token } }).catch(() => {});
}

export { bus };
