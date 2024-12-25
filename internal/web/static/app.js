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
async function loadAccounts() {
  const request = ++listRequest;
  $("#reload-btn").disabled = true;
  try {
    const accounts = await api("/api/accounts");
    if (request !== listRequest) return;
    allAccounts = accounts;
    $("#account-count").textContent = accounts.length;
    renderAccounts();
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
  const accounts = allAccounts.filter(a => `${a.remark || ""} ${a.alias}`.toLocaleLowerCase().includes(query));
  $("#list-message").classList.toggle("hidden", !query || accounts.length > 0);
  $("#list-message").textContent = "没有找到匹配的账号，试试其他备注或账号名称。";
  $("#list-summary").textContent = query ? `找到 ${accounts.length} 个账号，共 ${allAccounts.length} 个` : `共 ${allAccounts.length} 个账号`;
  $("#account-table").classList.toggle("hidden", accounts.length === 0);
  const tbody = $("#rows");
  tbody.textContent = "";
  $("#empty").classList.toggle("hidden", allAccounts.length > 0 || !!query);

  for (const a of accounts) {
    const tr = document.createElement("tr");

    // 账号列:备注即显示名(可直接编辑),括号里是别名,供 CLI --alias 使用。
    const tdName = document.createElement("td");
    const nameWrap = document.createElement("div");
    nameWrap.className = "name-cell";

    const remark = document.createElement("input");
    remark.className = "remark";
    remark.value = a.remark || "";
    remark.placeholder = "添加备注";
    remark.maxLength = 100;
    remark.setAttribute("aria-label", `编辑 ${a.remark || a.alias} 的备注`);
    remark.title = "点击编辑备注";
    remark.addEventListener("keydown", (event) => {
      if (event.key === "Enter") remark.blur();
      if (event.key === "Escape") { remark.value = a.remark || ""; remark.blur(); }
    });
    remark.addEventListener("change", async () => {
      try {
        await api(`/api/accounts/${encodeURIComponent(a.alias)}/remark`, {
          method: "PUT",
          body: JSON.stringify({ remark: remark.value.trim() }),
        });
        toast("备注已保存");
        await loadAccounts();
      } catch (err) {
        remark.value = a.remark || "";
        toast(`保存失败:${err.message}`);
      }
    });

    const aliasRef = document.createElement("span");
    aliasRef.className = "alias-ref";
    aliasRef.title = `别名 ${a.alias}(CLI --alias 用)`;
    aliasRef.textContent = a.alias;

    const avatar = document.createElement("span");
    avatar.className = "avatar";
    avatar.setAttribute("aria-hidden", "true");
    avatar.textContent = Array.from(a.remark || a.alias)[0] || "读";
    const details = document.createElement("div");
    details.className = "account-details";
    details.append(remark, aliasRef);
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

    const refreshBtn = button("刷新", "btn small", async () => {
      refreshBtn.disabled = true;
      refreshBtn.textContent = "刷新中…";
      try {
        await api(`/api/accounts/${encodeURIComponent(a.alias)}/refresh`, { method: "POST" });
        toast(`已刷新 ${a.remark || a.alias}`);
        await loadAccounts();
      } catch (err) {
        toast(`刷新失败:${err.message}`);
        refreshBtn.disabled = false;
        refreshBtn.textContent = "刷新";
      }
    });

    const delBtn = button("删除", "btn small danger", async () => {
      const displayName = a.remark || a.alias;
      if (!confirm(`确定删除账号「${displayName}」?仅删除本地凭据,不影响微信读书账号。`)) return;
      try {
        await api(`/api/accounts/${encodeURIComponent(a.alias)}`, { method: "DELETE" });
        toast(`已删除 ${displayName}`);
        await loadAccounts();
      } catch (err) {
        toast(`删除失败:${err.message}`);
      }
    });

    tdOps.append(refreshBtn, " ", delBtn);
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
  $("#login-remark").disabled = false;
}

async function startLogin() {
  const generation = ++loginGeneration;
  stopPolling();
  const remark = $("#login-remark").value.trim();
  $("#login-start").disabled = true;
  $("#login-start").textContent = "正在生成…";
  try {
    const data = await api("/api/login", {
      method: "POST",
      body: JSON.stringify({ remark }),
    });
    if (generation !== loginGeneration) return;
    loginId = data.id;
    $("#login-start").textContent = "等待扫码确认";
    $("#login-remark").disabled = true;
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
    const name = data.account.remark || data.account.alias;
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

/* ---------- 事件绑定 ---------- */

function openLogin() {
  resetLoginUI();
  $("#login-remark").value = "";
  $("#login-dialog").showModal();
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

loadAccounts();
