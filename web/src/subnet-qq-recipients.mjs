import {pluginEscape as esc} from './plugin-ui.mjs';

const maxRecipients = 10;
const qqNumber = /^[1-9][0-9]{4,14}$/;

export function subnetQQRecipients(values) {
  const recipients = [...new Set(values)];
  if (recipients.some(value => !qqNumber.test(value))) throw Error('请输入 5–15 位有效 QQ 号。');
  if (recipients.length > maxRecipients) throw Error(`每条预警最多 @ ${maxRecipients} 位群友。`);
  return recipients;
}

export function subnetQQRecipientFields() {
  return `<fieldset class="subnet-qq-recipients" data-subnet-qq hidden>
    <legend>群内接收人</legend>
    <div data-selected-qq class="subnet-qq-selected"></div>
    <div class="subnet-qq-manual"><label>手动添加 QQ 号<input type="text" inputmode="numeric" data-manual-qq autocomplete="off" placeholder="输入 QQ 号，可用逗号分隔"></label><button type="button" class="secondary" data-add-qq>添加</button></div>
    <details data-qq-picker><summary>从群成员选择</summary>
      <div class="subnet-qq-manual"><label>搜索群成员<input type="search" data-qq-search placeholder="群名片、昵称或 QQ 号"></label><button type="button" class="text-button" data-refresh-qq>刷新群成员</button></div>
      <p data-qq-status class="muted" role="status"></p><div class="qq-members" data-qq-members></div>
    </details>
    <p class="error" data-qq-error role="alert"></p>
  </fieldset>`;
}

export function bindSubnetQQRecipients(dialog, request, groupSelect) {
  const field = dialog.querySelector('[data-subnet-qq]');
  const input = field.querySelector('[data-manual-qq]');
  const picker = field.querySelector('[data-qq-picker]');
  const search = field.querySelector('[data-qq-search]');
  const status = field.querySelector('[data-qq-status]');
  const list = field.querySelector('[data-qq-members]');
  const selected = field.querySelector('[data-selected-qq]');
  const error = field.querySelector('[data-qq-error]');
  let recipients = [], members = [], version = 0, timer;

  function renderMembers() {
    const query = search.value.trim().toLowerCase();
    const matches = members.filter(m => `${m.card} ${m.nickname} ${m.user_id}`.toLowerCase().includes(query));
    list.innerHTML = matches.slice(0, 100).map(m => `<button type="button" data-choose-qq="${esc(m.user_id)}" aria-pressed="${recipients.includes(m.user_id)}"><strong>${esc(m.card || m.nickname || m.user_id)}</strong><small>${esc(m.user_id)}</small></button>`).join('') || (query && members.length ? '<p class="muted">没有匹配的群成员。</p>' : '');
  }

  function renderSelected() {
    selected.innerHTML = recipients.length ? recipients.map(qq => {
      const member = members.find(m => m.user_id === qq);
      const label = member?.card || member?.nickname;
      return `<button type="button" class="subnet-qq-chip" data-remove-qq="${qq}" aria-label="移除接收人 ${qq}">${label ? `${esc(label)} · ` : ''}${qq} <span aria-hidden="true">×</span></button>`;
    }).join('') : '<span class="muted">留空时发送普通群消息。</span>';
    renderMembers();
  }

  async function loadMembers(refresh = false, attempts = 0) {
    clearTimeout(timer);
    const group = groupSelect.value;
    const current = ++version;
    if (!group || !picker.open) return;
    status.textContent = refresh ? '正在请求机器人刷新…' : '正在读取群成员…';
    try {
      const data = await request(group, refresh);
      if (current !== version || !dialog.open || !picker.open) return;
      members = data.members;
      renderSelected();
      const pending = data.group.refresh_version !== data.group.refreshed_version;
      if (pending && attempts < 12) {
        status.textContent = '正在等待机器人同步群成员…';
        timer = setTimeout(() => void loadMembers(false, attempts + 1), 2500);
      } else if (data.group.error) {
        status.textContent = `群成员同步失败，仍可手动添加 QQ 号。${data.group.error}`;
      } else if (pending || !data.group.members_at) {
        status.textContent = '群成员暂未同步，可点击刷新或手动添加 QQ 号。';
      } else if (members.length) {
        status.textContent = `${members.length} 位群成员${members.length > 100 ? '，可搜索更多成员' : ''}`;
      } else {
        status.textContent = '暂无群成员，可手动添加 QQ 号。';
      }
    } catch (e) {
      if (current === version && dialog.open) status.textContent = `无法读取群成员。${e.message}`;
    }
  }

  function reset(values = []) {
    clearTimeout(timer);
    version++;
    recipients = subnetQQRecipients(values);
    members = [];
    input.value = '';
    search.value = '';
    status.textContent = '';
    error.textContent = '';
    field.hidden = !groupSelect.value;
    renderSelected();
    if (!field.hidden && picker.open) void loadMembers();
  }

  function collect() {
    if (!groupSelect.value) return null;
    const manual = input.value.trim().split(/[\s,，、]+/).filter(Boolean);
    const combined = subnetQQRecipients([...recipients, ...manual]);
    return {
      group_id: groupSelect.value,
      recipient: combined.length === 1 ? combined[0] : '',
      ...(combined.length > 1 ? {recipients: combined} : {}),
      private_copy: false,
    };
  }

  function addManual() {
    try {
      const target = collect();
      recipients = target?.recipients || (target?.recipient ? [target.recipient] : []);
      input.value = '';
      error.textContent = '';
      renderSelected();
      input.focus();
    } catch (e) { error.textContent = e.message; }
  }

  groupSelect.addEventListener('change', () => reset());
  search.addEventListener('input', renderMembers);
  picker.addEventListener('toggle', () => {
    if (picker.open) void loadMembers();
    else { clearTimeout(timer); version++; }
  });
  field.querySelector('[data-refresh-qq]').onclick = () => void loadMembers(true);
  field.querySelector('[data-add-qq]').onclick = addManual;
  input.addEventListener('keydown', e => {
    if (e.key === 'Enter' && !e.isComposing) { e.preventDefault(); addManual(); }
  });
  field.addEventListener('click', e => {
    const button = e.target.closest('[data-choose-qq], [data-remove-qq]');
    if (!button) return;
    const qq = button.dataset.chooseQq || button.dataset.removeQq;
    try {
      recipients = subnetQQRecipients(recipients.includes(qq) ? recipients.filter(v => v !== qq) : [...recipients, qq]);
      error.textContent = '';
      renderSelected();
      [...field.querySelectorAll('[data-choose-qq]')].find(b => b.dataset.chooseQq === qq)?.focus();
    } catch (e) { error.textContent = e.message; }
  });
  dialog.addEventListener('close', () => { clearTimeout(timer); version++; }, {once: true});
  return {collect, reset};
}
