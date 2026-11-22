export const playerName = member => member?.game_id || member?.name || "未登录";
export const submitterName = task => task?.owner_name || "旧账号";
const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&":"&amp;", "<":"&lt;", ">":"&gt;", '"':"&quot;", "'":"&#39;" }[c]));

export function loginPayload(mode, fields) {
  if (mode === "token") {
    const token = fields.token.trim();
    if (!token) throw new Error("请输入访问令牌。");
    return { path:"/api/v1/session", body:{token} };
  }
  const game_id = fields.gameID.trim();
  if (!/^[A-Za-z0-9_]{3,16}$/.test(game_id)) throw new Error("游戏 ID 需为 3–16 位字母、数字或下划线。");
  const password = fields.password;
  if (!password || new TextEncoder().encode(password).length > 256) throw new Error("请输入有效密码。");
  if (mode === "register") {
    if ([...password].length < 10) throw new Error("密码至少需要 10 个字符。");
    if (password !== fields.confirmPassword) throw new Error("两次输入的密码不一致。");
    if (!fields.invitation.trim()) throw new Error("请输入管理员提供的邀请码。");
    return {path:"/api/v1/players/register",body:{game_id,password,invitation:fields.invitation.trim()}};
  }
  return {path:"/api/v1/session",body:{game_id,password}};
}

export function playerAccountHTML(member, networks, members = [], invitations = []) {
  const admin = member?.admin;
  return `<section class="player-account"><div class="player-current"><i data-lucide="user-round"></i><div><strong>${esc(playerName(member))}</strong><small>${admin ? "管理员" : "团队玩家"} · 下单自动记录此身份</small></div></div><form id="player-profile-form" class="player-form"><label>游戏 ID<input id="profile-game-id" required pattern="[A-Za-z0-9_]{3,16}" minlength="3" maxlength="16" value="${esc(member?.game_id || "")}" autocomplete="username" placeholder="例如 CaeliaEve"></label>${member?.password_login ? '<label>当前密码<input id="profile-current-password" type="password" required autocomplete="current-password" maxlength="256"></label>' : ""}<label>${member?.password_login ? "新密码（留空保留）" : "设置玩家登录密码（可选）"}<input id="profile-password" type="password" autocomplete="new-password" minlength="10" maxlength="256"></label><p class="muted">游戏 ID 大小写不区分。改名不改变订单归属，旧订单保留当时的名字。</p><p id="player-profile-error" class="error" role="alert"></p><button class="secondary" type="submit">保存身份</button></form>${admin ? `<section class="player-team"><h3>邀请团队玩家</h3><p class="muted">给每位玩家独立账号。邀请码只授予所选网络的权限，有效期 7 天，代码仅生成时显示。</p><form id="player-invite-form" class="player-form"><label>网络<select id="invite-network" required>${networks.map(n=>`<option value="${esc(n.id)}">${esc(n.name)}</option>`).join("")}</select></label><label>权限<select id="invite-permission"><option value="craft">查看库存并下单</option><option value="view">仅查看库存</option></select></label><label>可注册人数<input id="invite-uses" type="number" min="1" max="100" value="20" required></label><button type="submit" class="secondary">生成邀请码</button><p id="player-invite-error" class="error" role="alert"></p><div id="player-invite-result" class="player-invite-result" hidden><strong>请保存邀请码</strong><code id="player-invite-code"></code><button type="button" class="secondary" id="copy-player-invite">复制邀请码</button></div></form><div class="player-invitations">${invitations.map(i=>`<div class="player-team-row"><span>${esc(networks.find(n=>n.id===i.network_id)?.name || i.network_id)}<small>${i.can_craft?"允许下单":"仅查看"} · ${i.uses}/${i.max_uses} 人 · ${esc(new Date(i.expires_at).toLocaleDateString())}</small></span>${i.revoked?'<span class="muted">已撤销</span>':`<button class="text-button" data-revoke-invite="${esc(i.id)}">撤销</button>`}</div>`).join("")}</div><h3>成员与账号恢复</h3><p class="muted">忘记密码或身份冲突时，可由管理员重设；会退出该成员其他设备的登录。</p><div class="player-members">${members.map(m=>`<div class="player-team-row"><span>${esc(playerName(m))}<small>${m.admin?"管理员":m.password_login?"玩家账号":"令牌成员"}</small></span><button class="text-button" data-edit-player="${esc(m.id)}">重设身份 / 密码</button>${m.admin?"":`<button class="text-button" data-revoke-player="${esc(m.id)}">停用</button>`}</div>`).join("")}</div><form id="player-reset-form" class="player-form" hidden><strong id="player-reset-name"></strong><input id="player-reset-member" type="hidden"><label>游戏 ID<input id="player-reset-game-id" required pattern="[A-Za-z0-9_]{3,16}" maxlength="16"></label><label>新密码<input id="player-reset-password" type="password" required minlength="10" maxlength="256" autocomplete="new-password"></label><p id="player-reset-error" class="error" role="alert"></p><button class="secondary" type="submit">保存新凭据</button></form></section>` : ""}</section>`;
}
