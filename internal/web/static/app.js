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
  el.textContent = errMsg ? `${statusText[status] || status}:${errMsg}` : statusText[status] || status;
  el.className = "status" + (status === "success" ? " ok" : ["expired", "declined", "error", "canceled"].includes(status) ? " err" : "");
}

/* ---------- 账号详情 ---------- */

let detailAccount = null;
let detailRequest = 0;

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

async function showDetails(account) {
  if (!account) return;
  const generation = ++detailRequest;
  detailAccount = account;
  const name = account.name || "账号详情";
  $("#detail-title").textContent = name;
  $("#detail-content").innerHTML = `<p class="empty">正在加载 ${escapeHtml(name)} 的书架与账号信息…</p>`;
  if (!$("#detail-dialog").open) $("#detail-dialog").showModal();
  $("#detail-reload").disabled = true;
  try {
    const data = await api(`/api/accounts/${encodeURIComponent(account.alias)}/details`);
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
  if (d.card) root.append(detailSection("会员卡", kvBlock(d.card)));

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

function kvEntries(o, skip = new Set()) {
  const NOISE = new Set(["errCode", "errMsg", "synckey", "succ"]);
  return Object.entries(o || {})
    .filter(([k]) => !NOISE.has(k) && !skip.has(k))
    .map(([k, v]) => {
      let val = v === null || v === undefined ? "" : typeof v === "object" ? JSON.stringify(v) : String(v);
      if (val.length > 90) val = val.slice(0, 90) + "…";
      return [k, val];
    });
}

function kvListEl(entries) {
  const wrap = document.createElement("div");
  wrap.className = "kv-list";
  for (const [k, v] of entries) {
    const row = document.createElement("div");
    row.className = "kv-row";
    const key = document.createElement("span");
    key.className = "kv-key";
    key.textContent = k;
    const val = document.createElement("span");
    val.className = "kv-val";
    val.textContent = v;
    row.append(key, val);
    wrap.append(row);
  }
  return wrap;
}

function kvBlock(o) {
  if (!o || typeof o !== "object") {
    const p = document.createElement("p");
    p.className = "kv-raw";
    p.textContent = o === undefined || o === null ? "(空)" : JSON.stringify(o);
    return p;
  }
  const entries = kvEntries(o);
  return kvListEl(entries.length ? entries : [["原始返回", JSON.stringify(o).slice(0, 200)]]);
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
  const name = document.createElement("div");
  name.className = "user-name";
  name.textContent = u.name || u.nickname || u.vid || u.userVid || "微信读书用户";
  head.append(name);
  wrap.append(head);
  const kv = kvEntries(u, new Set(["name", "nickname", "avatar"]));
  if (kv.length) wrap.append(kvListEl(kv));
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
$("#detail-reload").addEventListener("click", () => showDetails(detailAccount));

loadAccounts();
