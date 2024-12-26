/* wxread Web UI 前端逻辑:账号列表、扫码登录、备注编辑。无框架,原生 fetch。 */

const $ = (sel) => document.querySelector(sel);

const statusText = {
  pending: "等待扫码…",
  scanned: "已扫码,请在微信中确认登录",
  success: "登录成功!",
  expired: "二维码已过期,请重新生成",
  declined: "你在微信中拒绝了授权",
  canceled: "登录已取消",
  error: "登录失败",
};

async function api(path, opts = {}) {
  const resp = await fetch(path, {
    headers: { "content-type": "application/json" },
    ...opts,
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
  return data;
}

function fmtTime(unix) {
  if (!unix || unix < 0) return "尚未刷新";
  return new Date(unix * 1000).toLocaleString("zh-CN", { hour12: false });
}

let toastTimer = null;
function toast(msg) {
  const el = $("#toast");
  el.textContent = msg;
  el.classList.remove("hidden");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.add("hidden"), 2600);
}

/* ---------- 账号列表 ---------- */

let allAccounts = [];
let listRequest = 0;
const profileAttempts = new Set();
async function loadAccounts() {
  const request = ++listRequest;
  $("#reload-btn").disabled = true;
  try {
    const accounts = await api("/api/accounts");
    if (request !== listRequest) return;
    allAccounts = accounts;
    $("#account-count").textContent = accounts.length;
    renderAccounts();
    for (const account of accounts) {
      if (!account.profile_updated_at && !profileAttempts.has(account.alias)) {
        profileAttempts.add(account.alias);
        api(`/api/accounts/${encodeURIComponent(account.alias)}/profile`, { method: "POST" })
          .then(updated => {
            if (request !== listRequest) return;
            const index = allAccounts.findIndex(a => a.alias === updated.alias);
            if (index >= 0) { allAccounts[index] = updated; renderAccounts(); }
          })
          .catch(() => { /* Keep the cached ID visible; details or re-login can retry. */ });
      }
    }
  } catch (err) {
    if (request !== listRequest) return;
    $("#list-message").textContent = `无法加载账号：${err.message}。请点击「更新列表」重试。`;
    $("#list-message").classList.remove("hidden");
    $("#empty").classList.add("hidden");
    $("#list-summary").textContent = "加载失败";
  } finally {
    if (request === listRequest) $("#reload-btn").disabled = false;
  }
}

function renderAccounts() {
  const query = $("#account-search").value.trim().toLocaleLowerCase();
  const accounts = allAccounts.filter(a => `${a.name || ""} ${a.user_vid || a.vid} ${a.alias}`.toLocaleLowerCase().includes(query));
  $("#list-message").classList.toggle("hidden", !query || accounts.length > 0);
  $("#list-message").textContent = "没有找到匹配的账号，试试其他昵称或用户 ID。";
  $("#list-summary").textContent = query ? `找到 ${accounts.length} 个账号，共 ${allAccounts.length} 个` : `共 ${allAccounts.length} 个账号`;
  $("#account-table").classList.toggle("hidden", accounts.length === 0);
  const tbody = $("#rows");
  tbody.textContent = "";
  $("#empty").classList.toggle("hidden", allAccounts.length > 0 || !!query);

  for (const a of accounts) {
    const tr = document.createElement("tr");

    const tdName = document.createElement("td");
    const nameWrap = document.createElement("div");
    nameWrap.className = "name-cell";
    const name = document.createElement("span");
    name.className = "account-name";
    name.textContent = a.name || "微信读书用户";
    name.title = name.textContent;
    const userID = document.createElement("span");
    userID.className = "alias-ref";
    userID.textContent = `用户 ID ${a.user_vid || a.vid}`;
    const avatar = document.createElement("span");
    avatar.className = "avatar";
    avatar.setAttribute("aria-hidden", "true");
    const initial = Array.from(a.name || "读")[0];
    avatar.textContent = initial;
    if (a.avatar && /^https?:\/\//i.test(a.avatar)) {
      const image = document.createElement("img");
      image.alt = "";
      image.loading = "lazy";
      image.referrerPolicy = "no-referrer";
      image.src = a.avatar;
      image.addEventListener("error", () => { avatar.textContent = initial; }, { once: true });
      avatar.replaceChildren(image);
    }
    const details = document.createElement("div");
    details.className = "account-details";
    details.append(name, userID);
    nameWrap.append(avatar, details);
    tdName.append(nameWrap);

    const tdTime = document.createElement("td");
    tdTime.className = "time-cell";
    tdTime.textContent = fmtTime(a.rotated_at);
    const tdCredential = document.createElement("td");
    const credential = document.createElement("span");
    credential.className = "credential" + (a.has_access_token ? "" : " missing");
    credential.textContent = a.has_access_token ? "✓ 已保存" : "— 待刷新";
    credential.title = "表示本地是否保存凭据，不代表实时登录状态";
    tdCredential.append(credential);

    const tdOps = document.createElement("td");
    tdOps.className = "ops";

    const detailBtn = button("详情", "btn small", () => showDetails(a));

    const refreshBtn = button("刷新", "btn small", async () => {
      refreshBtn.disabled = true;
      refreshBtn.textContent = "刷新中…";
      try {
        await api(`/api/accounts/${encodeURIComponent(a.alias)}/refresh`, { method: "POST" });
        toast(`已刷新 ${a.name || a.user_vid || a.vid}`);
        await loadAccounts();
      } catch (err) {
        toast(`刷新失败:${err.message}`);
        refreshBtn.disabled = false;
        refreshBtn.textContent = "刷新";
      }
    });

    const delBtn = button("删除", "btn small danger", async () => {
      const displayName = a.name || a.user_vid || a.vid;
      if (!confirm(`确定删除账号「${displayName}」?仅删除本地凭据,不影响微信读书账号。`)) return;
      try {
        await api(`/api/accounts/${encodeURIComponent(a.alias)}`, { method: "DELETE" });
        toast(`已删除 ${displayName}`);
        await loadAccounts();
      } catch (err) {
        toast(`删除失败:${err.message}`);
      }
    });

    tdOps.append(detailBtn, " ", refreshBtn, " ", delBtn);
    tr.append(tdName, tdCredential, tdTime, tdOps);
    tbody.append(tr);
  }
}

function button(text, cls, onClick) {
  const b = document.createElement("button");
  b.textContent = text;
  b.className = cls;
  b.addEventListener("click", onClick);
  return b;
}

/* ---------- 扫码登录 ---------- */

let loginId = null;
let pollTimer = null;
let loginGeneration = 0;
let successTimer = null;

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

function resetLoginUI() {
  stopPolling();
  loginGeneration++;
  clearTimeout(successTimer);
  loginId = null;
  $("#qr-area").classList.add("hidden");
  $("#login-status").textContent = "";
  $("#login-status").className = "status";
  $("#login-start").disabled = false;
  $("#login-start").textContent = "生成登录二维码";
}

async function startLogin() {
  const generation = ++loginGeneration;
  stopPolling();
  $("#login-start").disabled = true;
  $("#login-start").textContent = "正在生成…";
  try {
    const data = await api("/api/login", {
      method: "POST",
      body: JSON.stringify({}),
    });
    if (generation !== loginGeneration) return;
    loginId = data.id;
    $("#login-start").textContent = "等待扫码确认";
    $("#qr-area").classList.remove("hidden");
    $("#qr-img").src = `${data.qr_url}?t=${Date.now()}`;
    setStatus("pending");
    pollTimer = setInterval(pollLogin, 1500);
  } catch (err) {
    if (generation !== loginGeneration) return;
    $("#login-start").textContent = "重新生成二维码";
    toast(`发起登录失败:${err.message}`);
    $("#login-start").disabled = false;
  }
}

async function pollLogin() {
  if (!loginId) return;
  const generation = loginGeneration;
  let data;
  try {
    data = await api(`/api/login/${loginId}`);
  } catch {
    return; // 网络抖动,下一轮再试
  }
  if (generation !== loginGeneration) return;
  setStatus(data.status, data.error);

  if (data.status === "success") {
    stopPolling();
    const name = data.account.name || data.account.vid;
    toast(`账号「${name}」登录成功`);
    successTimer = setTimeout(() => {
      $("#login-dialog").close();
      loadAccounts();
    }, 900);
  } else if (["expired", "declined", "canceled", "error"].includes(data.status)) {
    stopPolling();
    $("#login-start").textContent = "重新生成二维码";
    $("#login-start").disabled = false; // 允许重新生成二维码
  }
}

function setStatus(status, errMsg) {
  const el = $("#login-status");
  const label = statusText[status] || status;
  // 错误详情与状态文案相同(如"拒绝授权")时只展示一次,避免重复。
  const duplicate = !errMsg || errMsg === label || label.includes(errMsg) || errMsg.includes(label);
  el.textContent = duplicate ? label : `${label}:${errMsg}`;
  el.className = "status" + (status === "success" ? " ok" : ["expired", "declined", "error", "canceled"].includes(status) ? " err" : "");
}

/* ---------- 账号详情 ---------- */

let detailAccount = null;
let detailRequest = 0;

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

async function showDetails(account, force = false) {
  if (!account) return;
  const generation = ++detailRequest;
  detailAccount = account;
  const name = account.name || "账号详情";
  $("#detail-title").textContent = name;
  $("#detail-content").innerHTML = `<p class="empty">${force ? "正在从微信读书更新数据…" : `正在加载 ${escapeHtml(name)} 的书架与账号信息…`}</p>`;
  if (!$("#detail-dialog").open) $("#detail-dialog").showModal();
  $("#detail-reload").disabled = true;
  try {
    const data = await api(`/api/accounts/${encodeURIComponent(account.alias)}/details`, {
      method: force ? "POST" : "GET",
    });
    if (generation !== detailRequest) return;
    renderDetails(data);
    loadAccounts();
  } catch (err) {
    if (generation !== detailRequest) return;
    $("#detail-content").innerHTML = `<p class="empty">加载失败:${escapeHtml(err.message)}</p>`;
  } finally {
    if (generation === detailRequest) $("#detail-reload").disabled = false;
  }
}

const detailSectionNames = { shelf: "书架", user: "用户信息", card: "会员卡", session: "网页会话", profile: "资料缓存" };

function renderDetails(d) {
  const root = $("#detail-content");
  root.textContent = "";

  if (d.cached_at) {
    const updated = document.createElement("p");
    updated.className = "kv-raw";
    updated.style.padding = "8px 18px 0";
    updated.textContent = `数据更新于 ${fmtTime(d.cached_at)}(点「刷新数据」强制回源)`;
    root.append(updated);
  }

  const failed = Object.entries(d.errors || {});
  if (failed.length) {
    const warn = document.createElement("p");
    warn.className = "detail-warn";
    warn.textContent = `部分数据未能获取(${failed.map(([k]) => detailSectionNames[k] || k).join("、")}),可点击「刷新数据」重试。`;
    root.append(warn);
  }

  const user = nested(d.user, "user");
  if (user && (user.name || user.nickname)) $("#detail-title").textContent = user.name || user.nickname;
  if (user) root.append(detailSection("用户信息", userCard(user)));
  if (d.card) root.append(detailSection("会员卡", memberCardBlock(d.card)));

  const books = Array.isArray(d.books) ? d.books : [];
  root.append(detailSection(`书架(${books.length} 本)`, shelfGrid(books)));
}


function detailSection(title, el) {
  const sec = document.createElement("section");
  sec.className = "detail-section";
  const h = document.createElement("h3");
  h.textContent = title;
  sec.append(h, el);
  return sec;
}

// nested: 微信读书接口的返回有的把对象包在子字段里,有的直接平铺,这里做兼容。
function nested(obj, ...keys) {
  if (!obj || typeof obj !== "object") return null;
  for (const k of keys) {
    if (obj[k] && typeof obj[k] === "object" && !Array.isArray(obj[k])) return obj[k];
  }
  return obj;
}

function kvListEl(entries) {
  const wrap = document.createElement("div");
  wrap.className = "kv-list";
  for (const [k, v, cls] of entries) {
    const row = document.createElement("div");
    row.className = "kv-row";
    const key = document.createElement("span");
    key.className = "kv-key";
    key.textContent = k;
    const val = document.createElement("span");
    val.className = "kv-val" + (cls ? " " + cls : "");
    val.textContent = v;
    row.append(key, val);
    wrap.append(row);
  }
  return wrap;
}

// 只展示年月日:会员卡的起止都卡在 23:59:59,日期足够,时刻是噪音。
function fmtDay(unix) {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleDateString("zh-CN");
}

function fmtRemain(seconds) {
  if (!seconds || seconds <= 0) return "—";
  const days = Math.floor(seconds / 86400);
  return days >= 1 ? `约 ${days} 天` : "不足 1 天";
}

function memberCardBlock(c) {
  return kvListEl([
    ["起始日期", fmtDay(c.startTime)],
    ["到期时间", fmtDay(c.expiredTime)],
    ["当前状态", c.expired ? "已过期" : "有效中", c.expired ? "off" : "on"],
    ["剩余时长", c.expired ? "—" : fmtRemain(c.remainTime)],
  ]);
}

function userCard(u) {
  const wrap = document.createElement("div");
  wrap.className = "user-card";
  const head = document.createElement("div");
  head.className = "user-head";
  if (u.avatar) {
    const img = document.createElement("img");
    img.className = "user-avatar";
    img.src = u.avatar;
    img.alt = "头像";
    head.append(img);
  }
  const info = document.createElement("div");
  info.className = "user-info";
  const name = document.createElement("div");
  name.className = "user-name";
  name.textContent = (u.name || u.nickname || "微信读书用户").trim();
  info.append(name);
  const vid = document.createElement("div");
  vid.className = "user-vid";
  vid.textContent = `用户 ID:${u.userVid || u.vid || "—"}`;
  info.append(vid);
  head.append(info);
  wrap.append(head);
  return wrap;
}

function shelfGrid(books) {
  const wrap = document.createElement("div");
  wrap.className = "shelf-grid";
  if (!books.length) {
    wrap.innerHTML = '<p class="empty">书架暂无书籍</p>';
    return wrap;
  }
  for (const b of books) {
    const item = document.createElement("div");
    item.className = "shelf-item";
    if (b.cover) {
      const img = document.createElement("img");
      img.loading = "lazy";
      img.src = b.cover;
      img.alt = "";
      item.append(img);
    }
    const title = document.createElement("div");
    title.className = "shelf-title";
    title.textContent = b.title || b.bookId;
    title.title = b.title || b.bookId || "";
    const author = document.createElement("div");
    author.className = "shelf-author";
    author.textContent = b.author || "";
    item.append(title, author);
    wrap.append(item);
  }
  return wrap;
}

/* ---------- 挑战赛 · 自动阅读 ---------- */

let challengeAlias = null;
let challengeBooks = [];
const challengePicked = new Set();
let challengeRunTimer = null;

let currentView = "accounts";

function setView(view) {
  currentView = view;
  $("#accounts-view").classList.toggle("hidden", view !== "accounts");
  $("#challenge-view").classList.toggle("hidden", view !== "challenge");
  $("#logs-view").classList.toggle("hidden", view !== "logs");
  for (const [id, name] of [["nav-accounts", "accounts"], ["nav-challenge", "challenge"], ["nav-logs", "logs"]]) {
    $(`#${id}`).classList.toggle("active", view === name);
    if (view === name) $(`#${id}`).setAttribute("aria-current", "page");
    else $(`#${id}`).removeAttribute("aria-current");
  }
  $("#crumb").textContent = { accounts: "我的账号", challenge: "挑战赛", logs: "日志" }[view] || "我的账号";
}

async function openChallenge() {
  setView("challenge");
  const accounts = allAccounts.length ? allAccounts : await api("/api/accounts");
  const sel = $("#challenge-account");
  sel.textContent = "";
  if (!accounts.length) {
    challengeAlias = null;
    $("#challenge-books").innerHTML = '<p class="empty">还没有账号,先到「我的账号」扫码添加。</p>';
    $("#challenge-banner").classList.add("hidden");
    $("#challenge-laststatus").textContent = "尚未执行过";
    return;
  }
  $("#challenge-banner").classList.remove("hidden");
  const stillThere = accounts.some((a) => a.alias === challengeAlias);
  if (!stillThere) challengeAlias = accounts[0].alias;
  for (const a of accounts) {
    const opt = document.createElement("option");
    opt.value = a.alias;
    opt.textContent = a.name || a.remark || a.alias;
    sel.append(opt);
  }
  sel.value = challengeAlias;
  await loadChallenge();
}

async function loadChallenge() {
  if (!challengeAlias) return;
  $("#challenge-books").innerHTML = '<p class="empty">正在加载书架…</p>';
  try {
    // GET details 走本地缓存,瞬时返回;书架取自缓存封面墙。
    const d = await api(`/api/accounts/${encodeURIComponent(challengeAlias)}/details`);
    renderChallenge(d);
  } catch (err) {
    $("#challenge-books").innerHTML = `<p class="empty">加载失败:${escapeHtml(err.message)}</p>`;
  }
}

function renderChallenge(d) {
  challengeBooks = Array.isArray(d.books) ? d.books : [];
  const cfg = d.reading || {};
  challengePicked.clear();
  for (const id of cfg.book_ids || []) challengePicked.add(id);

  $("#challenge-state").textContent = cfg.enabled ? "已开启" : "未开启";
  $("#challenge-banner").classList.toggle("off", !cfg.enabled);
  $("#challenge-banner").classList.toggle("on", !!cfg.enabled);
  $("#challenge-runat").textContent = cfg.run_at || "—";
  $("#challenge-minutes").textContent = `${cfg.minutes || 30} 分钟`;
  $("#challenge-enabled").checked = !!cfg.enabled;
  $("#challenge-runat-input").value = cfg.run_at || "03:00";
  $("#challenge-minutes-input").value = cfg.minutes || 30;
  // 断点续跑提示:当日(或上次)会话没刷完,引导一键继续
  const incomplete = !cfg.running && cfg.run_total > 0 && cfg.run_done < cfg.run_total;
  if (incomplete) {
    const done = (cfg.run_done * 0.5).toFixed(1);
    const total = (cfg.run_total * 0.5).toFixed(1);
    $("#challenge-laststatus").textContent = `上次会话未完成(已记 ${done}/${total} 分钟),点「立即刷一次」接着跑`;
  } else {
    $("#challenge-laststatus").textContent = cfg.last_status
      ? `上次执行(${cfg.last_run_date || "—"}):${cfg.last_status}`
      : "尚未执行过";
  }
  updateChallengeButtons(!!cfg.running, !!cfg.paused);

  const grid = $("#challenge-books");
  grid.textContent = "";
  if (!challengeBooks.length) {
    grid.innerHTML = '<p class="empty">书架为空,先在详情里刷新数据。</p>';
    return;
  }
  for (const b of challengeBooks) {
    const card = document.createElement("div");
    card.className = "challenge-book" + (challengePicked.has(b.bookId) ? " selected" : "");
    card.title = b.title || b.bookId || "";
    if (b.cover) {
      const img = document.createElement("img");
      img.loading = "lazy";
      img.src = b.cover;
      img.alt = "";
      card.append(img);
    } else {
      const placeholder = document.createElement("div");
      placeholder.className = "challenge-book-placeholder";
      placeholder.textContent = "▤";
      card.append(placeholder);
    }
    const check = document.createElement("span");
    check.className = "challenge-check";
    check.textContent = "✓";
    card.append(check);
    const title = document.createElement("div");
    title.className = "challenge-book-title";
    title.textContent = b.title || b.bookId;
    card.append(title);
    card.addEventListener("click", () => {
      if (challengePicked.has(b.bookId)) {
        challengePicked.delete(b.bookId);
        card.classList.remove("selected");
      } else {
        challengePicked.add(b.bookId);
        card.classList.add("selected");
      }
    });
    grid.append(card);
  }
}

function challengeBannerFromInputs() {
  const enabled = $("#challenge-enabled").checked;
  $("#challenge-state").textContent = enabled ? "已开启" : "未开启";
  $("#challenge-banner").classList.toggle("off", !enabled);
  $("#challenge-banner").classList.toggle("on", enabled);
  $("#challenge-runat").textContent = $("#challenge-runat-input").value || "—";
  $("#challenge-minutes").textContent = `${$("#challenge-minutes-input").value || 30} 分钟`;
}

function updateChallengeButtons(running, paused) {
  $("#challenge-run").disabled = running;
  const pauseBtn = $("#challenge-pause");
  const stopBtn = $("#challenge-stop");
  pauseBtn.classList.toggle("hidden", !running);
  stopBtn.classList.toggle("hidden", !running);
  pauseBtn.textContent = paused ? "继续阅读" : "暂停";
  pauseBtn.disabled = false;
}

// 暂停/继续:暂停只停心跳上报,进度保留;继续后刷完剩余时长。
async function toggleChallengePause() {
  if (!challengeAlias) return;
  const btn = $("#challenge-pause");
  btn.disabled = true;
  const action = btn.textContent === "暂停" ? "pause" : "resume";
  try {
    const data = await api(`/api/accounts/${encodeURIComponent(challengeAlias)}/reading/pause`, {
      method: "POST",
      body: JSON.stringify({ action }),
    });
    updateChallengeButtons(true, data.paused);
    $("#challenge-laststatus").textContent = data.paused ? "已暂停(剩余时长保留,点「继续阅读」恢复)" : "阅读中…";
  } catch (err) {
    toast(`操作失败:${err.message}`);
  }
  btn.disabled = false;
}

// 停止:终止当前会话;已上报的时长服务端已记账不会回滚,当日调度视为已完成。
async function stopChallenge() {
  if (!challengeAlias) return;
  if (!confirm("确定停止本次阅读会话?已上报的时长会保留,当日不再重跑。")) return;
  const btn = $("#challenge-stop");
  btn.disabled = true;
  try {
    await api(`/api/accounts/${encodeURIComponent(challengeAlias)}/reading/stop`, { method: "POST" });
    $("#challenge-laststatus").textContent = "正在停止…";
  } catch (err) {
    toast(`停止失败:${err.message}`);
    btn.disabled = false;
  }
}

async function saveChallenge() {
  if (!challengeAlias) return;
  const saveBtn = $("#challenge-save");
  saveBtn.disabled = true;
  try {
    await api(`/api/accounts/${encodeURIComponent(challengeAlias)}/reading`, {
      method: "POST",
      body: JSON.stringify({
        enabled: $("#challenge-enabled").checked,
        book_ids: Array.from(challengePicked),
        minutes: Number($("#challenge-minutes-input").value) || 30,
        run_at: $("#challenge-runat-input").value || "03:00",
      }),
    });
    challengeBannerFromInputs();
    toast($("#challenge-enabled").checked ? "已开启,到点自动阅读" : "配置已保存(未开启)");
  } catch (err) {
    toast(`保存失败:${err.message}`);
  }
  saveBtn.disabled = false;
}

async function runChallengeNow() {
  if (!challengeAlias) return;
  const runBtn = $("#challenge-run");
  runBtn.disabled = true;
  try {
    await api(`/api/accounts/${encodeURIComponent(challengeAlias)}/reading/run`, { method: "POST" });
    toast("阅读会话已启动,每 30 秒记 0.5 分钟");
    $("#challenge-laststatus").textContent = "阅读中…";
  } catch (err) {
    toast(`启动失败:${err.message}`);
    runBtn.disabled = false;
    return;
  }
  let polls = 0;
  if (challengeRunTimer) clearInterval(challengeRunTimer);
  challengeRunTimer = setInterval(async () => {
    polls++;
    try {
      const cfg = await api(`/api/accounts/${encodeURIComponent(challengeAlias)}/reading`);
      updateChallengeButtons(!!cfg.running, !!cfg.paused);
      // 会话是否结束以 running 为准:暂停中会话仍在,轮询不能停。
      if (!cfg.running) {
        $("#challenge-laststatus").textContent = cfg.last_status || "已结束";
        clearInterval(challengeRunTimer);
        runBtn.disabled = false;
        return;
      }
      $("#challenge-laststatus").textContent = cfg.last_status || "阅读中…";
    } catch { /* 单次轮询失败忽略 */ }
    if (polls > 360) { clearInterval(challengeRunTimer); runBtn.disabled = false; }
  }, 5000);
}

/* ---------- 日志 ---------- */

let logsTimer = null;

async function openLogs() {
  setView("logs");
  const sel = $("#log-alias");
  const current = sel.value;
  sel.textContent = "";
  const defaultOpt = document.createElement("option");
  defaultOpt.value = "";
  defaultOpt.textContent = "全部账号";
  sel.append(defaultOpt);
  const accounts = allAccounts.length ? allAccounts : await api("/api/accounts").catch(() => []);
  for (const a of accounts) {
    const opt = document.createElement("option");
    opt.value = a.alias;
    opt.textContent = a.name || a.remark || a.alias;
    sel.append(opt);
  }
  sel.value = current;
  await loadLogs();
  if (!logsTimer) logsTimer = setInterval(() => { if (currentView === "logs") loadLogs(true); }, 10000);
}

async function loadLogs(silent = false) {
  const params = new URLSearchParams({ limit: "200" });
  if ($("#log-alias").value) params.set("alias", $("#log-alias").value);
  if ($("#log-level").value) params.set("level", $("#log-level").value);
  try {
    const logs = await api(`/api/logs?${params}`);
    $("#log-count").textContent = logs.length;
    $("#logs-updated").textContent = `更新于 ${new Date().toLocaleTimeString("zh-CN", { hour12: false })}`;
    renderLogs(logs, silent);
  } catch (err) {
    if (!silent) toast(`加载日志失败:${err.message}`);
  }
}

function renderLogs(logs, silent) {
  const list = $("#logs-list");
  list.textContent = "";
  if (!logs.length) {
    list.innerHTML = '<p class="empty">暂无日志</p>';
    return;
  }
  for (const e of logs) {
    const row = document.createElement("div");
    row.className = `log-row log-${e.level}`;
    const time = document.createElement("span");
    time.className = "log-time";
    time.textContent = fmtTime(e.ts);
    const level = document.createElement("span");
    level.className = "log-level";
    level.textContent = e.level;
    const source = document.createElement("span");
    source.className = "log-source";
    source.textContent = e.source || "-";
    const alias = document.createElement("span");
    alias.className = "log-alias";
    alias.textContent = e.name || e.alias || "-";
    alias.title = `别名 ${e.alias}`;
    const msg = document.createElement("span");
    msg.className = "log-msg";
    msg.textContent = e.message;
    msg.title = e.message;
    row.append(time, level, source, alias, msg);
    list.append(row);
  }
  list.scrollTop = 0; // 最新在最上
}

/* ---------- 事件绑定 ---------- */

function openLogin() {
  resetLoginUI();
  $("#login-dialog").showModal();
  startLogin();
}
$("#add-btn").addEventListener("click", openLogin);
$("#empty-add").addEventListener("click", openLogin);
$("#reload-btn").addEventListener("click", loadAccounts);
$("#account-search").addEventListener("input", renderAccounts);
$("#login-start").addEventListener("click", startLogin);
$("#login-close").addEventListener("click", () => $("#login-dialog").close());
$("#login-dialog").addEventListener("close", () => {
  resetLoginUI();
  loadAccounts();
});
$("#detail-close").addEventListener("click", () => $("#detail-dialog").close());
$("#detail-reload").addEventListener("click", () => showDetails(detailAccount, true));
$("#nav-accounts").addEventListener("click", (e) => { e.preventDefault(); setView("accounts"); });
$("#nav-challenge").addEventListener("click", (e) => { e.preventDefault(); openChallenge(); });
$("#nav-logs").addEventListener("click", (e) => { e.preventDefault(); openLogs(); });
$("#challenge-account").addEventListener("change", (e) => { challengeAlias = e.target.value; loadChallenge(); });
$("#challenge-save").addEventListener("click", saveChallenge);
$("#challenge-run").addEventListener("click", runChallengeNow);
$("#challenge-pause").addEventListener("click", toggleChallengePause);
$("#challenge-stop").addEventListener("click", stopChallenge);
$("#log-alias").addEventListener("change", () => loadLogs());
$("#log-level").addEventListener("change", () => loadLogs());
$("#logs-reload").addEventListener("click", () => loadLogs());
$("#logs-clear").addEventListener("click", async () => {
  if (!confirm("确定清空全部日志?")) return;
  try {
    await api("/api/logs", { method: "DELETE" });
    toast("日志已清空");
    loadLogs();
  } catch (err) {
    toast(`清空失败:${err.message}`);
  }
});

loadAccounts();
