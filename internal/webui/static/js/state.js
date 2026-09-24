// 全局可变状态（Vanilla JS，小项目直接共享一个对象，避免过度设计）

export const S = {
  token: localStorage.getItem('mineagent.token') || '',
  me: localStorage.getItem('mineagent.name') || '',
  admin: localStorage.getItem('mineagent.admin') === '1',
  options: null,
  pending: [],
  // null = 新会话草稿（未落库）；"" = 旧版默认会话；"id" = 已存在会话。
  conv: localStorage.getItem('mineagent.conv'),
  conversations: [],
  unread: new Set(),
  waiting: false
};

export function saveAuth() {
  localStorage.setItem('mineagent.token', S.token);
  localStorage.setItem('mineagent.name', S.me);
  localStorage.setItem('mineagent.admin', S.admin ? '1' : '0');
}

export function clearAuth() {
  S.token = ''; S.me = ''; S.admin = false;
  localStorage.removeItem('mineagent.token');
  localStorage.removeItem('mineagent.name');
  localStorage.removeItem('mineagent.admin');
}

export function setConv(conv) {
  S.conv = conv;
  if (conv === null) localStorage.removeItem('mineagent.conv');
  else localStorage.setItem('mineagent.conv', conv);
}

// 请求参数：草稿不带 conv（服务端据此新建）
export function convParam() {
  return S.conv === null ? undefined : S.conv;
}

// 附件 URL 兜底拼 token（cookie 被拦时 <img>/下载也能用）
export function withTok(u) {
  if (!u || u.startsWith('blob:') || u.startsWith('data:')) return u;
  return u + (u.indexOf('?') >= 0 ? '&' : '?') + 'token=' + encodeURIComponent(S.token);
}
