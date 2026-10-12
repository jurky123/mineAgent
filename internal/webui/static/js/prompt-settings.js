// Agent 页的人设选择、渠道规则与共享模板编辑；保存前保留本地草稿。
import { apiGet, apiPost } from './api.js';
import { h, icon, openDialog, confirmDialog, toast } from './ds.js';

let activeDialog = null;
const count = (text) => Array.from(text || '').length;
const personaIcons = { default: 'sparkle', 'mc-veteran': 'pickaxe', concise: 'check', tech: 'server', snarky: 'skull', translator: 'users', teacher: 'info' };

function button(label, fn, cls = 'btn') {
  const el = h('button', cls, label);
  el.type = 'button';
  el.onclick = fn;
  return el;
}

function updateLabel(data) {
  const id = data.myPersona || data.webPersona;
  const p = data.personas.find((item) => item.id === id);
  const label = document.getElementById('agent-persona-label');
  if (label) label.textContent = p ? p.name : '人设与提示词';
}

export async function refreshPromptLabel() {
  updateLabel(await apiGet('/api/prompts'));
}

export function initPromptSettings(previewData = null) {
  if (previewData) updateLabel(previewData);
  for (const id of ['agent-settings', 'menu-prompts', 'header-prompts']) {
    const el = document.getElementById(id);
    if (el) el.onclick = () => {
      document.getElementById('accountmenu')?.classList.remove('on');
      openPromptSettings(previewData);
    };
  }
}

export async function openPromptSettings(previewData = null) {
  if (activeDialog) { activeDialog.box.focus(); return; }
  let data, view = 'mine', serviceId = 'web', templateId, draft, baseline = '', saving = false;
  const getData = () => previewData ? Promise.resolve(structuredClone(previewData)) : apiGet('/api/prompts');
  const post = (payload) => {
    if (previewData) return Promise.reject(new Error('预览模式不保存更改'));
    return apiPost('/api/prompts', payload);
  };
  const root = h('div', 'prompt-shell');
  const tabs = h('nav', 'prompt-tabs');
  tabs.setAttribute('role', 'tablist');
  tabs.setAttribute('aria-label', 'Agent 设置');
  const content = h('div', 'prompt-content');
  content.id = 'prompt-content';
  content.setAttribute('role', 'tabpanel');
  const footer = h('div', 'prompt-footer');
  const status = h('div', 'prompt-status');
  status.setAttribute('aria-live', 'polite');
  const error = h('div', 'prompt-error');
  error.setAttribute('role', 'alert');
  const save = button('保存更改', saveChanges, 'btn primary');
  save.hidden = !!previewData;
  const note = h('div');
  note.append(status, error);
  footer.append(note, save);
  root.append(tabs, content, footer);

  const dirty = () => draft && JSON.stringify(draft) !== baseline;
  const discard = async () => !!previewData || !dirty() || await confirmDialog({
    title: '放弃未保存的更改？', body: '这些更改还没有应用。你可以返回继续编辑，或放弃草稿。', confirmText: '放弃更改', danger: true,
  });
  activeDialog = openDialog({ title: 'Agent 设置', body: root, width: 1060, className: 'agent-prompts',
    beforeClose: () => saving ? false : discard(), onClose: () => { activeDialog = null; },
  });

  function validation() {
    if (!draft) return '';
    if (view === 'services') {
      if (draft.enabled && !draft.text.trim()) return '自定义系统提示词不能为空';
      if (count(draft.text) > data.limits.capability) return '系统提示词超过字数上限';
    }
    if (view === 'templates') {
      if (!draft.name.trim() || !draft.text.trim()) return '填写名称和正文后即可保存';
      if (count(draft.name) > data.limits.name || count(draft.desc) > data.limits.desc || count(draft.text) > data.limits.persona) return '模板超过字数上限';
    }
    return '';
  }

  function syncFooter() {
    save.disabled = saving || !dirty() || !!validation();
    save.textContent = saving ? '正在保存…' : view === 'mine' ? '应用人设' : '保存更改';
    status.textContent = previewData ? '预览模式 · 更改不会保存' : saving ? '正在保存，请稍候' : dirty() ? (validation() || '有未保存的更改') : '更改从下一条消息开始生效';
    status.classList.toggle('dirty', !!dirty());
  }

  function changed() { error.textContent = ''; syncFooter(); }

  async function switchTo(update) {
    if (saving || !await discard()) return;
    update(); render();
  }

  function heading(title, text) {
    const box = h('div', 'prompt-heading');
    box.append(h('h3', null, title), h('p', null, text));
    return box;
  }

  function field(label, el, limit) {
    const wrap = h('div', 'prompt-field');
    const head = h('div', 'prompt-field-head');
    const lab = h('label', null, label);
    lab.htmlFor = el.id;
    head.append(lab);
    if (limit) {
      const counter = h('span', 'prompt-counter');
      const sync = () => {
        counter.textContent = count(el.value) + ' / ' + limit;
        counter.classList.toggle('over', count(el.value) > limit);
      };
      el.addEventListener('input', sync); sync();
      head.append(counter);
      el.setAttribute('aria-describedby', el.id + '-count');
      counter.id = el.id + '-count';
    }
    wrap.append(head, el);
    return wrap;
  }

  function renderMine() {
    draft = { persona: data.myPersona || '' };
    content.append(heading('让 Agent 用你习惯的方式交流', '选择适合你的表达方式。只影响你的网页对话，不会改动其他用户或渠道。'));
    const layout = h('div', 'prompt-personas-layout');
    const grid = h('div', 'prompt-personas');
    grid.setAttribute('role', 'radiogroup');
    grid.setAttribute('aria-label', '我的网页人设');
    const preview = h('details', 'prompt-preview');
    preview.open = !window.matchMedia('(max-width: 720px)').matches;
    const channelPersona = data.personas.find((p) => p.id === data.webPersona);
    const cards = [{ id: '', name: '跟随渠道默认', desc: '当前：' + (channelPersona?.name || '渠道默认人设') }, ...data.personas];
    const sync = () => {
      grid.querySelectorAll('[role=radio]').forEach((el) => {
        const selected = el.dataset.persona === draft.persona;
        el.classList.toggle('selected', selected);
        el.setAttribute('aria-checked', String(selected));
        el.tabIndex = selected ? 0 : -1;
      });
      const p = data.personas.find((item) => item.id === (draft.persona || data.webPersona));
      const summary = h('summary');
      const title = h('div');
      title.append(h('span', 'prompt-eyebrow', '人设预览'), h('h4', null, p?.name || '渠道默认'));
      summary.append(title, icon('right', 'prompt-preview-arrow'));
      preview.replaceChildren(summary, h('p', 'prompt-preview-desc', p?.desc || ''), h('div', 'prompt-preview-text', p?.text || '渠道默认人设暂不可用。'));
      preview.append(h('div', 'prompt-preview-note', '人设决定语气与交流方式；能力和操作规则仍由渠道设置决定。'));
      changed();
    };
    for (const p of cards) {
      const card = button('', () => { draft.persona = p.id; sync(); }, 'prompt-choice');
      card.dataset.persona = p.id;
      card.setAttribute('role', 'radio');
      const mark = h('span', 'prompt-persona-icon');
      mark.append(icon(p.id ? (personaIcons[p.id] || 'user') : 'refresh'));
      const text = h('span', 'prompt-choice-text');
      text.append(h('strong', null, p.name), h('span', null, p.desc || '自定义人设'));
      card.append(mark, text, icon('check', 'prompt-choice-check'));
      card.addEventListener('keydown', (e) => {
        const step = ['ArrowRight', 'ArrowDown'].includes(e.key) ? 1 : ['ArrowLeft', 'ArrowUp'].includes(e.key) ? -1 : 0;
        if (!step) return;
        e.preventDefault();
        const buttons = [...grid.children];
        const next = buttons[(buttons.indexOf(card) + step + buttons.length) % buttons.length];
        next.click(); next.focus();
      });
      grid.append(card);
    }
    layout.append(grid, preview); content.append(layout); sync();
  }

  function selectionList(items, selected, choose) {
    const list = h('aside', 'prompt-selector');
    for (const item of items) {
      const row = button('', () => { if (item.id !== selected) return switchTo(() => choose(item.id)); }, 'prompt-selector-item' + (item.id === selected ? ' selected' : ''));
      row.append(h('strong', null, item.name), h('span', null, item.desc || ''));
      row.setAttribute('aria-pressed', String(item.id === selected));
      list.append(row);
    }
    return list;
  }

  function renderServices() {
    const svc = data.services.find((item) => item.id === serviceId) || data.services[0];
    if (!svc) { content.append(heading('暂无渠道', '配置渠道后可以在这里调整提示词。')); draft = null; return; }
    serviceId = svc.id;
    draft = { persona: svc.persona === svc.defaultPersona ? '' : svc.persona, enabled: !!svc.global?.enabled, text: svc.global?.text || '' };
    content.append(heading('渠道默认与系统提示词', '管理员设置 · 影响所选渠道的所有用户。网页用户仍可单独选择自己的人设。'));
    const layout = h('div', 'prompt-editor-layout');
    layout.append(selectionList(data.services, serviceId, (id) => { serviceId = id; }));
    const editor = h('div', 'prompt-editor');
    editor.append(h('span', 'prompt-eyebrow', svc.name + ' · 全局设置'));
    const sel = h('select', 'input'); sel.id = 'prompt-service-persona';
    for (const p of [{ id: '', name: '使用渠道默认人设' }, ...data.personas]) {
      const opt = h('option', null, p.name); opt.value = p.id; sel.append(opt);
    }
    sel.value = draft.persona; sel.onchange = () => { draft.persona = sel.value; changed(); };
    editor.append(field('默认人设', sel));
    const mode = h('select', 'input'); mode.id = 'prompt-capability-mode';
    for (const [value, label] of [['builtin', '使用内置系统提示词'], ['custom', '使用自定义系统提示词']]) {
      const opt = h('option', null, label); opt.value = value; mode.append(opt);
    }
    const ta = h('textarea', 'textarea prompt-textarea'); ta.id = 'prompt-capability'; ta.rows = 12;
    const textField = field('系统提示词', ta, data.limits.capability);
    const syncMode = () => {
      mode.value = draft.enabled ? 'custom' : 'builtin';
      ta.readOnly = !draft.enabled;
      ta.value = draft.enabled ? draft.text : svc.default || '';
      ta.dispatchEvent(new Event('input'));
    };
    ta.addEventListener('input', () => { if (draft.enabled) draft.text = ta.value; changed(); });
    mode.onchange = () => { draft.enabled = mode.value === 'custom'; syncMode(); };
    editor.append(field('提示词来源', mode), textField);
    const tools = h('div', 'prompt-editor-tools');
    tools.append(button('从内置开始编辑', () => { draft.text = svc.default || ''; draft.enabled = true; syncMode(); }, 'btn sm'),
      h('span', null, '自定义会替换整段渠道规则，人设会单独加入。'));
    editor.append(tools); layout.append(editor); content.append(layout); syncMode();
  }

  function renderTemplates() {
    const p = data.personas.find((item) => item.id === templateId) || (templateId === '' ? null : data.personas[0]);
    templateId = p?.id || '';
    draft = { id: templateId, name: p?.name || '', desc: p?.desc || '', text: p?.text || '' };
    const head = heading('共享人设模板库', '管理员维护 · 所有人都可以预览并选择。编辑模板会影响正在使用它的对话。');
    head.append(button('＋ 新建模板', () => switchTo(() => { templateId = ''; }), 'btn sm'));
    content.append(head);
    const layout = h('div', 'prompt-editor-layout');
    layout.append(selectionList(data.personas, templateId, (id) => { templateId = id; }));
    const editor = h('div', 'prompt-editor');
    editor.append(h('span', 'prompt-eyebrow', p ? (p.builtin ? '内置模板 · 可编辑，也可恢复' : '自定义模板') : '新模板'));
    for (const [key, label, limit] of [['name', '模板名称', data.limits.name], ['desc', '一句话简介', data.limits.desc], ['text', '人设正文', data.limits.persona]]) {
      const el = h(key === 'text' ? 'textarea' : 'input', key === 'text' ? 'textarea prompt-textarea' : 'input');
      el.id = 'prompt-template-' + key; el.value = draft[key];
      if (key === 'text') { el.rows = 10; el.placeholder = '描述角色、语气和交流方式，例如：先给结论，再解释依据。'; }
      else el.placeholder = key === 'name' ? '例如：耐心的技术搭档' : '用一句话帮助别人选择';
      el.oninput = () => { draft[key] = el.value; changed(); };
      editor.append(field(label, el, limit));
    }
    if (p) editor.append(button(p.builtin ? '恢复内置模板' : '删除模板', async () => {
      if (saving || !await discard()) return;
      if (!await confirmDialog({ title: p.builtin ? '恢复内置模板？' : '删除这个模板？',
        body: p.builtin ? '将恢复「' + p.name + '」的内置名称、简介和正文。' : '正在使用「' + p.name + '」的用户与渠道将回到默认人设。',
        confirmText: p.builtin ? '恢复' : '删除', danger: true })) return;
      saving = true; syncFooter();
      const controls = [...root.querySelectorAll('button, input, textarea, select')];
      const disabled = controls.map((el) => el.disabled);
      controls.forEach((el) => { el.disabled = true; });
      try {
        await post({ scope: 'persona-delete', id: p.id });
        baseline = JSON.stringify(draft);
        data = await getData();
        toast(p.builtin ? '已恢复内置模板' : '已删除模板'); render(); updateLabel(data);
      } catch (e) { error.textContent = e.message || '操作失败'; }
      finally {
        controls.forEach((el, i) => { if (el.isConnected) el.disabled = disabled[i]; });
        saving = false; syncFooter();
      }
    }, 'btn sm' + (p.builtin ? '' : ' danger')));
    layout.append(editor); content.append(layout);
  }

  function render() {
    tabs.replaceChildren(); content.replaceChildren(); error.textContent = '';
    const views = [['mine', '我的人设', 'user'], ...(data.admin ? [['services', '渠道设置', 'settings'], ['templates', '模板库', 'sparkle']] : [])];
    for (const [id, label, glyph] of views) {
      const tab = button('', () => { if (view !== id) return switchTo(() => { view = id; }); }, 'prompt-tab');
      tab.append(icon(glyph), document.createTextNode(label));
      tab.id = 'prompt-tab-' + id; tab.setAttribute('role', 'tab');
      tab.setAttribute('aria-controls', content.id); tab.setAttribute('aria-selected', String(view === id));
      tab.tabIndex = view === id ? 0 : -1;
      tab.onkeydown = (e) => {
        if (!['ArrowLeft', 'ArrowRight'].includes(e.key)) return;
        e.preventDefault();
        const next = views[(views.findIndex((v) => v[0] === id) + (e.key === 'ArrowRight' ? 1 : views.length - 1)) % views.length][0];
        if (next === view) return;
        switchTo(() => { view = next; }).then(() => document.getElementById('prompt-tab-' + view)?.focus());
      };
      tabs.append(tab);
    }
    content.setAttribute('aria-labelledby', 'prompt-tab-' + view);
    if (view === 'mine') renderMine();
    else if (view === 'services') renderServices();
    else renderTemplates();
    baseline = JSON.stringify(draft); syncFooter();
    content.scrollTop = 0;
  }

  async function saveChanges() {
    if (saving || !dirty() || validation()) return;
    saving = true; syncFooter(); error.textContent = '';
    const controls = [...root.querySelectorAll('button, input, textarea, select')];
    const disabled = controls.map((el) => el.disabled);
    controls.forEach((el) => { el.disabled = true; });
    let saved = false;
    try {
      const payload = view === 'mine' ? { scope: 'user-persona', persona: draft.persona } : view === 'services' ?
        { scope: 'service-settings', id: serviceId, ...draft } : { scope: 'persona-save', data: draft };
      const result = await post(payload);
      saved = true; baseline = JSON.stringify(draft);
      if (view === 'templates') templateId = result.id;
      data = await getData();
      updateLabel(data); render(); toast('已保存，下一条消息起生效');
    } catch (e) { error.textContent = saved ? '已保存，但刷新失败。关闭后重新打开可查看最新设置。' : e.message || '保存失败，请重试'; }
    finally {
      controls.forEach((el, i) => { if (el.isConnected) el.disabled = disabled[i]; });
      saving = false; syncFooter();
    }
  }

  async function load() {
    content.replaceChildren(h('div', 'prompt-loading', '正在加载人设与提示词…')); save.disabled = true;
    try {
      data = await getData();
      if (!root.isConnected) return;
      data.limits = { persona: 8000, capability: 20000, name: 40, desc: 200, ...data.limits };
      updateLabel(data); render();
    } catch (e) {
      content.replaceChildren(heading('暂时无法加载设置', e.message || '请稍后重试'), button('重新加载', load));
      save.disabled = true;
    }
  }
  await load();
}
