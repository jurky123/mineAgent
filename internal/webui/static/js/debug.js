// 渲染调试模式：URL 加 ?ui=1 打开（不连后端、不登录，纯前端假数据）
//
//   /?ui=1                      聊天主界面（含示例消息/图片/文件/表格/代码块）
//   /?ui=1&panel=intel          打开 Intelligence 弹层
//   /?ui=1&panel=appearance     打开外观弹窗
//   /?ui=1&sidebar=0            收起侧栏
//   /?ui=1&theme=dark           强制深色（light / system 同理）
//   /?ui=1&empty=1              空会话状态（输入框居中）
//
// 用于给无头浏览器截图、快速对比视觉，不影响正常登录流程。

import { $, askConfirm, askInput } from './ui.js';
import { S } from './state.js';
import { renderMsg, setTyping, layout, toBottom } from './chat.js';
import { renderToolbar, openIntelPopover, openModelPopover } from './composer.js';
import { renderList } from './conversations.js';
import { openAppearance, applyTheme } from './theme.js';

const TINY_IMG = 'data:image/svg+xml;utf8,' + encodeURIComponent(
  '<svg xmlns="http://www.w3.org/2000/svg" width="320" height="200">' +
  '<rect width="320" height="200" fill="#eef2ff"/>' +
  '<circle cx="110" cy="96" r="52" fill="#2f6fed"/>' +
  '<rect x="180" y="70" width="110" height="52" rx="8" fill="#a5b4fc"/>' +
  '<text x="16" y="186" font-size="14" fill="#5d5d5d">demo image</text></svg>');

export function startDebug() {
  const q = new URLSearchParams(location.search);
  const theme = q.get('theme');
  if (theme) {
    localStorage.setItem('mineagent.theme', theme);
    applyTheme();
  }

  S.token = 'debug';
  S.me = 'Jiang';
  S.admin = true;
  S.conv = 'demo';
  S.options = {
    skills: [
      { name: '查服务器', desc: '在线玩家 / 负载 / 时间天气', prompt: '看看服务器现在的情况' },
      { name: '画张图', desc: '用 PIL 画好直接发给我', prompt: '用 PIL 画一张图并发给我，内容：' },
      { name: '写代码跑一下', desc: 'workspace 里写脚本并执行（管理员）', prompt: '在 workspace 里写个脚本跑一下：' }
    ],
    models: ['deepseek-v4.1-flash', 'deepseek-v4-pro', 'glm-5.3', 'grok-4.7', 'kimi-k3', 'qwen3.8-max'],
    defaultModel: 'deepseek-v4.1-flash',
    model: '',
    effort: 'medium',
    admin: true
  };
  const eff = q.get('effort');
  if (eff !== null) S.options.effort = eff;   // '' | low | medium | high
  S.conversations = [
    { conv: 'demo', title: 'CUDA kernel 优化', updatedAt: Date.now() },
    { conv: 'b', title: 'MineAudio 架构设计', updatedAt: Date.now() - 3600e3 * 5 },
    { conv: 'c', title: '视频生成稀疏注意力', updatedAt: Date.now() - 86400e3 },
    { conv: 'd', title: '服务器状态周报', updatedAt: Date.now() - 86400e3 * 4 }
  ];

  $('login').style.display = 'none';
  $('app').style.display = 'block';
  $('myname').textContent = S.me;
  $('myavatar').textContent = S.me[0];
  $('myrole').textContent = '管理员';
  $('myrole').className = 'badge admin';
  $('menu-ws').hidden = false;
  if (q.get('sidebar') === '0') document.body.classList.add('collapsed');
  renderList();
  renderToolbar();
  if (q.get('empty') === '1') {
    $('conv-search').value = '';
    layout(true);
    return;
  }
  const now = Date.now();
  const msgs = [
    { id: 1, role: 'user', text: '帮我看下站点配置，端口好像冲突了', at: now - 300000 },
    {
      id: 2, role: 'assistant', at: now - 240000,
      text: '## 结论\n\n有两处需要改：\n\n1. **端口冲突**：`8100` 已经被 BlueMap 占用\n2. ~~超时太短~~ 反代少了 `X-Forwarded-For`\n\n```yaml\nserver:\n  listen: 8123\n  proxy: true\n```\n\n光线追踪的渲染方程是 $L_o(x,\\omega_o) = L_e(x,\\omega_o) + \\int_{\\Omega} f_r(x,\\omega_i,\\omega_o) L_i(x,\\omega_i) (\\omega_i \\cdot n) d\\omega_i$，实时里一般近似为：\n\n$$L_o \\approx \\sum_{k=1}^{N} f_r \\cdot L_i \\cdot \\cos\\theta_k \\cdot \\Delta\\omega_k$$\n\n```python\ndef shade(normal, light, albedo, intensity=1.0):\n    cos_theta = max(0.0, normal.dot(light))\n    return albedo * intensity * cos_theta\n```\n\n待办：\n- [x] 检查端口占用\n- [ ] 补 X-Forwarded-For\n- [ ] 重载配置\n\n> 改完记得重载配置，不要直接重启\n\n| 项 | 现状 | 建议 |\n|---|---|---|\n| 端口 | 8100 | 8123 |\n| 超时 | 30s | 60s |'
    },
    { id: 3, role: 'user', text: '顺便看下这张拓扑图，中间那层是不是多余的？', at: now - 120000, files: [{ name: 'network-topo.png', url: TINY_IMG, image: true }] },
    {
      id: 4, role: 'assistant', at: now - 60000,
      text: '中间那层确实可以省掉：它只做转发、没有状态，合并后延迟能降 3~5ms。整理成 PDF 给你：',
      files: [{ name: '网络拓扑建议.pdf', url: '/static/css/tokens.css', image: false, size: 182470 }]
    },
    { id: 5, role: 'user', text: '好，按这个改', at: now - 20000 }
  ];
  msgs.forEach((m) => renderMsg(m));
  setTyping(true);
  layout(true);
  toBottom();

  const panel = q.get('panel');
  if (panel === 'intel') openIntelPopover();
  else if (panel === 'models') openModelPopover(true);
  else if (panel === 'appearance') openAppearance();
  else if (panel === 'confirm') {
    askConfirm({ title: '删除这个聊天？', text: '聊天记录和记忆都会被清除，不能恢复。', okLabel: '删除' });
  } else if (panel === 'rename') {
    askInput({ title: '重命名聊天', value: 'CUDA kernel 优化', okLabel: '保存' });
  }
}
