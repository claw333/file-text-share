(function () {
  "use strict";

  const page = document.body.dataset.page;
  let csrfToken = "";

  async function api(path, options) {
    const config = { credentials: "same-origin", ...options };
    config.headers = new Headers(config.headers || {});
    if (csrfToken && config.method && config.method !== "GET") {
      config.headers.set("X-CSRF-Token", csrfToken);
    }
    if (config.body && !(config.body instanceof FormData)) {
      config.headers.set("Content-Type", "application/json");
    }

    const response = await fetch(path, config);
    if (!response.ok) {
      let message = "请求失败，请稍后重试";
      try {
        const payload = await response.json();
        if (payload.error) message = payload.error;
      } catch (_) {
        // Keep the generic error when the response is not JSON.
      }
      const error = new Error(message);
      error.status = response.status;
      if (response.status === 401 && page !== "login" && (message === "请先登录" || message === "登录已失效")) {
        window.location.replace("/");
        throw new Error("登录已失效");
      }
      throw error;
    }
    if (response.status === 204) return null;
    return response.json();
  }

  if (page === "login") {
    const passwordInput = document.querySelector("#password");
    const passwordToggle = document.querySelector(".password-toggle");
    const form = document.querySelector("#login-form");
    const error = document.querySelector("#login-error");

    passwordToggle.addEventListener("click", function () {
      const show = passwordInput.type === "password";
      passwordInput.type = show ? "text" : "password";
      passwordToggle.setAttribute("aria-pressed", String(show));
      passwordToggle.setAttribute("aria-label", show ? "隐藏密码" : "显示密码");
      passwordInput.focus();
    });

    passwordInput.addEventListener("input", function () {
      error.hidden = true;
    });

    form.addEventListener("submit", async function (event) {
      event.preventDefault();
      const username = document.querySelector("#username").value.trim();
      if (!username || !passwordInput.value) {
        error.textContent = "用户名或密码错误";
        error.hidden = false;
        return;
      }

      const submit = form.querySelector("button[type='submit']");
      submit.disabled = true;
      const original = submit.innerHTML;
      submit.textContent = "正在验证…";
      try {
        const login = await api("/api/login", {
          method: "POST",
          body: JSON.stringify({ username, password: passwordInput.value }),
        });
        window.location.replace(login.redirectTo || "/share.html");
      } catch (requestError) {
        error.textContent = requestError.message;
        error.hidden = false;
        submit.disabled = false;
        submit.innerHTML = original;
      }
    });
    return;
  }

  if (page === "admin") {
    const usersContainer = document.querySelector("#admin-users");
    const createForm = document.querySelector("#admin-create-user-form");
    const stats = document.querySelector("#admin-stats");
    const toast = document.querySelector("#toast");
    const toastMessage = document.querySelector("#toast-message");
    const deleteModal = document.querySelector("#admin-delete-modal");
    const deleteTitle = document.querySelector("#admin-delete-title");
    const deleteDescription = document.querySelector("#admin-delete-description");
    let toastTimer = null;
    let pendingDeleteUser = null;

    function escapeHtml(value) {
      const div = document.createElement("div");
      div.textContent = value;
      return div.innerHTML;
    }

    function formatTime(value) {
      if (!value) return "尚无";
      return new Intl.DateTimeFormat("zh-CN", {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
      }).format(new Date(value)).replaceAll("/", "-");
    }

    function formatStorage(bytes) {
      const value = Number(bytes) || 0;
      const units = ["B", "KB", "MB", "GB", "TB"];
      let size = value;
      let unitIndex = 0;
      while (size >= 1024 && unitIndex < units.length - 1) {
        size /= 1024;
        unitIndex += 1;
      }
      const rounded = size >= 10 || Number.isInteger(size) ? size.toFixed(0) : size.toFixed(1);
      return `${rounded} ${units[unitIndex]}`;
    }

    function formatQuotaGB(bytes) {
      const gb = (Number(bytes) || 0) / (1024 ** 3);
      if (Number.isInteger(gb)) return String(gb);
      if (gb > 0 && gb < 0.01) return gb.toFixed(8).replace(/0+$/, "").replace(/\.$/, "");
      return gb.toFixed(2).replace(/0+$/, "").replace(/\.$/, "");
    }

    function showToast(message) {
      window.clearTimeout(toastTimer);
      toastMessage.textContent = message;
      toast.hidden = false;
      toastTimer = window.setTimeout(function () {
        toast.hidden = true;
      }, 2800);
    }

    function openDeleteUserModal(id, username) {
      pendingDeleteUser = { id, username };
      deleteTitle.textContent = `删除用户 ${username}？`;
      deleteDescription.textContent = "删除后，该用户的文本、文件和会话都会被删除，无法恢复。";
      deleteModal.hidden = false;
      document.body.style.overflow = "hidden";
      deleteModal.querySelector(".modal-cancel").focus();
    }

    function closeDeleteUserModal() {
      deleteModal.hidden = true;
      document.body.style.overflow = "";
      pendingDeleteUser = null;
    }

    function renderUsers(users) {
      const totalTexts = users.reduce(function (sum, user) { return sum + user.textCount; }, 0);
      const totalFiles = users.reduce(function (sum, user) { return sum + user.fileCount; }, 0);
      stats.innerHTML = `<span><strong>${users.length}</strong><small>用户</small></span><span><strong>${totalTexts}</strong><small>文本</small></span><span><strong>${totalFiles}</strong><small>文件</small></span>`;
      usersContainer.innerHTML = users.map(function (user) {
        const isAdmin = user.role === "admin";
        const roleLabel = isAdmin ? "管理员" : "普通用户";
        const storageText = `${formatStorage(user.storageUsedBytes)} / ${formatStorage(user.storageQuotaBytes)}`;
        const actions = isAdmin
          ? '<span class="admin-muted">保留账号</span>'
          : `<label class="admin-quota-control"><span class="sr-only">空间上限 GB</span><input type="number" min="0.01" step="0.01" inputmode="decimal" value="${formatQuotaGB(user.storageQuotaBytes)}" data-quota-for="${user.id}" /><span>GB</span><button class="button button-soft admin-save-quota" type="button" data-user-id="${user.id}">保存</button></label><label class="admin-password-reset"><span class="sr-only">新密码</span><input type="password" autocomplete="new-password" placeholder="新密码" data-password-for="${user.id}" /><button class="button button-soft admin-reset-password" type="button" data-user-id="${user.id}">改密</button></label><button class="icon-button action-delete admin-delete-user" type="button" data-user-id="${user.id}" aria-label="删除用户" title="删除用户"><svg viewBox="0 0 24 24"><path d="M4 7h16M9 7V4h6v3M7 7l1 14h8l1-14M10 11v6M14 11v6" /></svg></button>`;
        return `<article class="admin-user-card" data-user-id="${user.id}">
          <div class="admin-user-main">
            <span class="avatar">${escapeHtml(user.username.slice(0, 1).toUpperCase())}</span>
            <div>
              <div class="admin-user-title"><strong>${escapeHtml(user.username)}</strong><span class="status-chip ${isAdmin ? "status-used" : "status-new"}"><span></span>${roleLabel}</span></div>
              <dl class="admin-user-meta">
                <div><dt>创建</dt><dd>${formatTime(user.createdAt)}</dd></div>
                <div><dt>最近登录</dt><dd>${formatTime(user.lastLoginAt)}</dd></div>
                <div><dt>最近上传</dt><dd>${formatTime(user.lastUploadAt)}</dd></div>
                <div><dt>内容</dt><dd>${user.textCount} 文本 / ${user.fileCount} 文件</dd></div>
                <div><dt>空间</dt><dd>${storageText}</dd></div>
                <div><dt>登录次数</dt><dd>${user.loginCount}</dd></div>
              </dl>
            </div>
          </div>
          <div class="admin-user-actions">${actions}</div>
        </article>`;
      }).join("");
    }

    async function loadUsers() {
      const payload = await api("/api/admin/users", { method: "GET" });
      renderUsers(payload.users || []);
    }

    async function initializeAdmin() {
      try {
        const session = await api("/api/session", { method: "GET" });
        if (session.role !== "admin") {
          window.location.replace(session.redirectTo || "/share.html");
          return;
        }
        csrfToken = session.csrfToken;
        document.querySelector("#account-name").textContent = session.username;
        document.querySelector("#account-avatar").textContent = session.username.slice(0, 1).toUpperCase();
        await loadUsers();
      } catch (error) {
        if (error.message !== "登录已失效") {
          usersContainer.innerHTML = `<div class="list-error">${escapeHtml(error.message)} <button type="button" id="retry-load">重新加载</button></div>`;
          document.querySelector("#retry-load").addEventListener("click", initializeAdmin);
        }
      }
    }

    createForm.addEventListener("submit", async function (event) {
      event.preventDefault();
      const username = document.querySelector("#admin-new-username").value.trim();
      const password = document.querySelector("#admin-new-password").value;
      if (!username || !password) {
        showToast("请填写用户名和初始密码");
        return;
      }
      try {
        await api("/api/admin/users", { method: "POST", body: JSON.stringify({ username, password }) });
        createForm.reset();
        await loadUsers();
        showToast("用户已创建");
      } catch (error) {
        showToast(error.message);
      }
    });

    usersContainer.addEventListener("click", async function (event) {
      const resetButton = event.target.closest(".admin-reset-password");
      const quotaButton = event.target.closest(".admin-save-quota");
      const deleteButton = event.target.closest(".admin-delete-user");
      try {
        if (quotaButton) {
          const id = quotaButton.dataset.userId;
          const input = usersContainer.querySelector(`[data-quota-for="${id}"]`);
          const quotaGB = Number(input.value);
          if (!Number.isFinite(quotaGB) || quotaGB <= 0) {
            showToast("请输入大于 0 的空间上限");
            return;
          }
          const storageQuotaBytes = Math.round(quotaGB * (1024 ** 3));
          await api(`/api/admin/users/${id}/quota`, { method: "POST", body: JSON.stringify({ storageQuotaBytes }) });
          await loadUsers();
          showToast("空间上限已更新");
        } else if (resetButton) {
          const id = resetButton.dataset.userId;
          const input = usersContainer.querySelector(`[data-password-for="${id}"]`);
          if (!input.value) {
            showToast("请输入新密码");
            return;
          }
          await api(`/api/admin/users/${id}/password`, { method: "POST", body: JSON.stringify({ password: input.value }) });
          input.value = "";
          await loadUsers();
          showToast("密码已修改，该用户需重新登录");
        } else if (deleteButton) {
          const card = deleteButton.closest(".admin-user-card");
          const username = card.querySelector(".admin-user-title strong").textContent;
          openDeleteUserModal(deleteButton.dataset.userId, username);
        }
      } catch (error) {
        showToast(error.message);
      }
    });

    deleteModal.querySelector(".modal-close").addEventListener("click", closeDeleteUserModal);
    deleteModal.querySelector(".modal-cancel").addEventListener("click", closeDeleteUserModal);
    deleteModal.addEventListener("click", function (event) { if (event.target === deleteModal) closeDeleteUserModal(); });
    document.addEventListener("keydown", function (event) { if (event.key === "Escape" && !deleteModal.hidden) closeDeleteUserModal(); });
    deleteModal.querySelector(".modal-confirm").addEventListener("click", async function () {
      if (!pendingDeleteUser) return;
      const user = pendingDeleteUser;
      const confirmButton = deleteModal.querySelector(".modal-confirm");
      confirmButton.disabled = true;
      try {
        await api(`/api/admin/users/${user.id}`, { method: "DELETE" });
        closeDeleteUserModal();
        await loadUsers();
        showToast("用户已删除");
      } catch (error) {
        showToast(error.message);
      } finally {
        confirmButton.disabled = false;
      }
    });

    document.querySelector("#admin-refresh-users").addEventListener("click", loadUsers);
    document.querySelector("#logout-button").addEventListener("click", async function () {
      try {
        await api("/api/logout", { method: "POST" });
      } finally {
        window.location.replace("/");
      }
    });

    initializeAdmin();
    return;
  }

  if (page === "profile") {
    const form = document.querySelector("#profile-password-form");
    const toast = document.querySelector("#toast");
    const toastMessage = document.querySelector("#toast-message");
    let toastTimer = null;

    function showToast(message) {
      window.clearTimeout(toastTimer);
      toastMessage.textContent = message;
      toast.hidden = false;
      toastTimer = window.setTimeout(function () {
        toast.hidden = true;
      }, 2800);
    }

    async function initializeProfile() {
      const session = await api("/api/session", { method: "GET" });
      csrfToken = session.csrfToken;
      document.querySelector("#profile-username").textContent = session.username;
      document.querySelector("#profile-avatar").textContent = session.username.slice(0, 1).toUpperCase();
      document.querySelector("#profile-role").textContent = session.role === "admin" ? "管理员" : "普通用户";
      document.querySelector("#profile-home-link").setAttribute("href", session.redirectTo || "/share.html");
    }

    form.addEventListener("submit", async function (event) {
      event.preventDefault();
      const currentPassword = document.querySelector("#current-password").value;
      const newPassword = document.querySelector("#new-password").value;
      const confirmPassword = document.querySelector("#confirm-password").value;
      if (!currentPassword || !newPassword || !confirmPassword) {
        showToast("请填写完整密码信息");
        return;
      }
      if (newPassword !== confirmPassword) {
        showToast("两次输入的新密码不一致");
        return;
      }
      if (newPassword === currentPassword) {
        showToast("新密码不能与当前密码相同");
        return;
      }
      try {
        await api("/api/me/password", { method: "POST", body: JSON.stringify({ currentPassword, newPassword }) });
        showToast("密码已修改，请用新密码重新登录");
        window.setTimeout(function () {
          window.location.replace("/");
        }, 900);
      } catch (error) {
        showToast(error.message);
      }
    });

    document.querySelector("#logout-button").addEventListener("click", async function () {
      try {
        await api("/api/logout", { method: "POST" });
      } finally {
        window.location.replace("/");
      }
    });

    initializeProfile().catch(function (error) {
      if (error.message !== "登录已失效") showToast(error.message);
    });
    return;
  }

  if (page !== "share") return;

  const textArea = document.querySelector("#shared-text");
  const characterCount = document.querySelector("#character-count");
  const publishButton = document.querySelector("#publish-text");
  const itemList = document.querySelector("#item-list");
  const emptyState = document.querySelector("#empty-state");
  const fileInput = document.querySelector("#file-input");
  const dropZone = document.querySelector("#drop-zone");
  const uploadProgress = document.querySelector("#upload-progress");
  const uploadName = document.querySelector("#upload-file-name");
  const uploadPercent = document.querySelector("#upload-percent");
  const uploadBar = document.querySelector("#upload-bar");
  const storageUsed = document.querySelector("#storage-used");
  const storageQuota = document.querySelector("#storage-quota");
  const storagePercent = document.querySelector("#storage-percent");
  const storageBar = document.querySelector("#storage-bar");
  const modal = document.querySelector("#delete-modal");
  const imagePreviewModal = document.querySelector("#image-preview-modal");
  const imagePreviewTitle = document.querySelector("#image-preview-title");
  const imagePreviewImage = document.querySelector("#image-preview-image");
  const toast = document.querySelector("#toast");
  const toastMessage = document.querySelector("#toast-message");
  const textPreviewLimit = 50;
  const mobileTextPreviewLimit = 32;
  const textDetailLimit = 1000;
  let items = [];
  let currentFilter = "all";
  let pendingDelete = null;
  const expandedDetails = new Set();
  let toastTimer = null;

  itemList.innerHTML = '<div class="list-loading">正在读取共享内容…</div>';

  function escapeHtml(value) {
    const div = document.createElement("div");
    div.textContent = value;
    return div.innerHTML;
  }

  function escapeAttribute(value) {
    return escapeHtml(value).replaceAll('"', "&quot;");
  }

  function textKey(item) {
    return `${item.kind}:${item.id}`;
  }

  function truncateText(value, limit) {
    const characters = Array.from(value || "");
    if (characters.length <= limit) return { text: value || "", truncated: false };
    return { text: characters.slice(0, limit).join(""), truncated: true };
  }

  function compactPreviewText(value) {
    return String(value || "").replace(/\s+/g, " ").trim();
  }

  function currentTextPreviewLimit() {
    if (typeof window.matchMedia === "function" && window.matchMedia("(max-width: 760px)").matches) {
      return mobileTextPreviewLimit;
    }
    return textPreviewLimit;
  }

  function textWithBreaks(value) {
    return escapeHtml(value).replaceAll("\n", "<br>");
  }

  function formatTime(value) {
    return new Intl.DateTimeFormat("zh-CN", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    }).format(new Date(value)).replaceAll("/", "-");
  }

  function formatSize(bytes) {
    const value = Number(bytes) || 0;
    if (value >= 1024 ** 3) return `${formatNumber(value / 1024 ** 3, 2)} GB`;
    if (value >= 1024 ** 2) return `${formatNumber(value / 1024 ** 2, 1)} MB`;
    if (value >= 1024) return `${formatNumber(value / 1024, 1)} KB`;
    return `${value} B`;
  }

  function formatTextLength(value) {
    return `${Array.from(String(value || "")).length} 字`;
  }

  function sourceLabel(value) {
    const parts = String(value || "").split("·").map(function (part) { return part.trim(); }).filter(Boolean);
    if (parts.length >= 3) return `${parts[parts.length - 1]} · ${parts[parts.length - 2]}`;
    if (parts.length === 2) {
      const systemByDevice = {
        "iPhone": "iOS",
        "iPad": "iOS",
        "Android": "Android",
        "Mac": "macOS",
        "Windows PC": "Windows",
        "Linux PC": "Linux",
      };
      return `${systemByDevice[parts[0]] || parts[0]} · ${parts[1]}`;
    }
    return parts[0] || "未知系统 · 浏览器";
  }

  function formatNumber(value, digits) {
    return value.toFixed(digits).replace(/\.0+$/, "").replace(/(\.\d*[1-9])0+$/, "$1");
  }

  function formatStoragePercent(usedBytes, quotaBytes) {
    const used = Number(usedBytes) || 0;
    const quota = Number(quotaBytes) || 0;
    if (quota <= 0 || used <= 0) return "0%";
    const percent = Math.min(100, (used / quota) * 100);
    const displayed = percent < 0.01 ? 0.01 : percent;
    if (displayed >= 10) return `${formatNumber(displayed, 0)}%`;
    return `${formatNumber(displayed, 2)}%`;
  }

  function renderStorage(usedBytes, quotaBytes) {
    const percent = formatStoragePercent(usedBytes, quotaBytes);
    storageUsed.textContent = formatSize(usedBytes);
    storageQuota.textContent = formatSize(quotaBytes);
    storagePercent.textContent = percent;
    storageBar.style.width = percent;
  }

  function remainingLabel(item) {
    const expiresAt = new Date(item.expiresAt).getTime();
    const createdAt = new Date(item.createdAt).getTime();
    let milliseconds = expiresAt - Date.now();
    if (Number.isFinite(createdAt) && Number.isFinite(expiresAt) && expiresAt > createdAt) {
      milliseconds = Math.min(milliseconds, expiresAt - createdAt);
    }
    milliseconds = Math.max(0, milliseconds);
    const hours = Math.ceil(milliseconds / 3_600_000);
    if (hours < 24) return { text: `${hours} 小时后清理`, soon: true };
    return { text: `${Math.ceil(milliseconds / 86_400_000)} 天后清理`, soon: hours <= 48 };
  }

  function fileType(item) {
    const parts = item.fileName.split(".");
    return parts.length > 1 ? parts.pop().slice(0, 6).toUpperCase() : "FILE";
  }

  function fileDetailType(item) {
    const mimeType = String(item.mimeType || "").trim().toLowerCase();
    const subtype = mimeType.includes("/") ? mimeType.split("/").pop() : mimeType;
    const cleaned = subtype.replace(/^x-/, "").replace(/[^a-z0-9.+-]/g, "");
    return cleaned || fileType(item).toLowerCase();
  }

  function greeting() {
    const hour = new Date().getHours();
    if (hour < 6) return "夜深了";
    if (hour < 12) return "早上好";
    if (hour < 18) return "下午好";
    return "晚上好";
  }

  function showToast(message) {
    window.clearTimeout(toastTimer);
    toastMessage.textContent = message;
    toast.hidden = false;
    toastTimer = window.setTimeout(function () {
      toast.hidden = true;
    }, 2800);
  }

  function historyMarkup(item) {
    const isText = item.kind === "text";
    if (!item.events.length) {
      return `<div class="history-panel empty-history" hidden>尚无${isText ? "使用" : "下载"}记录</div>`;
    }
    const rows = item.events.map(function (event) {
      const action = event.eventType === "copy" ? "复制成功" : "下载完成";
      return `<li><span class="history-device"><i></i><span><strong>${escapeHtml(sourceLabel(event.deviceLabel))}</strong><small>${formatTime(event.createdAt)} · ${action}</small></span></span></li>`;
    }).join("");
    return `<div class="history-panel" hidden><div class="history-title"><strong>${isText ? "使用" : "下载"}记录</strong><span>共 ${item.events.length} 次</span></div><ol>${rows}</ol></div>`;
  }

  function isImageFile(item) {
    return typeof item.mimeType === "string" && item.mimeType.toLowerCase().startsWith("image/");
  }

  function textDetailMarkup(item, expanded) {
    const preview = truncateText(compactPreviewText(item.text), currentTextPreviewLimit());
    const detail = truncateText(item.text, textDetailLimit);
    return `<p class="text-preview">${textWithBreaks(preview.text)}${preview.truncated ? "…" : ""}</p>
        <div class="item-detail text-detail"${expanded ? "" : " hidden"}>
          <div class="detail-title"><strong>文本详情</strong><span>预览最多 1000 字</span></div>
          <p class="text-detail-body">${textWithBreaks(detail.text)}${detail.truncated ? "…" : ""}</p>
        </div>`;
  }

  function fileDetailMarkup(item, expanded) {
    const type = escapeHtml(fileType(item));
    const fileName = escapeHtml(item.fileName);
    const safeName = escapeAttribute(item.fileName);
    const detailType = escapeHtml(fileDetailType(item));
    const preview = isImageFile(item)
      ? `<button class="file-thumbnail image-thumbnail is-loading" type="button" data-loading="true" aria-disabled="true" aria-label="放大图片 ${safeName}">
          <img src="/api/files/${item.id}/preview" alt="${safeName} 缩略图" loading="lazy" />
          <span class="thumbnail-spinner" aria-hidden="true"></span>
        </button>`
      : `<div class="file-thumbnail generic-thumbnail" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M6 2h8l4 4v16H6zM14 2v5h5M9 13h6M9 17h6" /></svg>
          <span>${type}</span>
        </div>`;
    return `<div class="item-detail file-detail"${expanded ? "" : " hidden"}>
          ${preview}
          <div class="file-detail-copy">
            <strong>${fileName}</strong>
            <span>${formatSize(item.fileSize)} · ${detailType}</span>
          </div>
        </div>`;
  }

  function renderItem(item) {
    const isText = item.kind === "text";
    const retention = remainingLabel(item);
    const kind = isText ? "文本" : `文件 · ${escapeHtml(fileType(item))}`;
    const expanded = expandedDetails.has(textKey(item));
    const icon = isText
      ? '<div class="item-type-badge text-icon">T</div>'
      : '<div class="item-type-badge file-icon"><svg viewBox="0 0 24 24"><path d="M6 2h8l4 4v16H6zM14 2v5h5" /></svg></div>';
    const content = isText
      ? textDetailMarkup(item, expanded)
      : `<p class="file-name">${escapeHtml(item.fileName)}</p>${fileDetailMarkup(item, expanded)}`;
    const size = `<span>${isText ? formatTextLength(item.text) : formatSize(item.fileSize)}</span>`;
    const primary = isText
      ? '<button class="button button-soft action-copy" type="button"><svg viewBox="0 0 24 24"><rect x="8" y="8" width="12" height="12" rx="2" /><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2" /></svg>复制</button>'
      : '<button class="button button-soft action-download" type="button"><svg viewBox="0 0 24 24"><path d="M12 3v12M7 10l5 5 5-5M5 21h14" /></svg>下载</button>';

    return `<article class="share-item${retention.soon ? " expiring-item" : ""}${expanded ? " is-expanded" : ""}" data-kind="${item.kind}" data-item-id="${item.id}" aria-expanded="${expanded}">
      <div class="item-main">${icon}<div class="item-content">
        <div class="item-topline"><span class="kind-label">${kind}</span></div>
        ${content}
        <div class="item-meta"><span class="meta-line">${size}
          <span><svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></svg>${formatTime(item.createdAt)}</span></span>
          <span class="meta-line"><span><svg viewBox="0 0 24 24"><rect x="3" y="5" width="18" height="13" rx="2" /><path d="M8 21h8M12 18v3" /></svg>${escapeHtml(sourceLabel(item.uploaderDevice))}</span>
          <span class="expiry${retention.soon ? " expiry-soon" : ""}"><svg viewBox="0 0 24 24"><path d="M6 8h12M9 3v3M15 3v3M5 5h14v16H5z" /></svg>${retention.text}</span></span>
        </div>${historyMarkup(item)}
      </div></div>
      <div class="item-actions">${primary}
        <button class="icon-button action-history" type="button" aria-label="展开${isText ? "使用" : "下载"}记录" aria-expanded="false" title="历史记录"><svg viewBox="0 0 24 24"><path d="M3 12a9 9 0 1 0 3-6.7L3 8M3 3v5h5M12 7v5l3 2" /></svg></button>
        <button class="icon-button action-delete" type="button" aria-label="删除${isText ? "文本" : "文件"}" title="删除"><svg viewBox="0 0 24 24"><path d="M4 7h16M9 7V4h6v3M7 7l1 14h8l1-14M10 11v6M14 11v6" /></svg></button>
      </div></article>`;
  }

  function renderItems() {
    const visible = currentFilter === "all" ? items : items.filter(function (item) { return item.kind === currentFilter; });
    itemList.innerHTML = visible.map(renderItem).join("");
    document.querySelector("#all-count").textContent = items.length;
    emptyState.hidden = visible.length !== 0;
    refreshImageThumbnailStates();
  }

  async function loadItems() {
    const payload = await api("/api/items", { method: "GET" });
    items = payload.items || [];
    renderStorage(payload.storageUsedBytes || 0, payload.storageQuotaBytes || 0);
    renderItems();
  }

  async function initialize() {
    try {
      const session = await api("/api/session", { method: "GET" });
      csrfToken = session.csrfToken;
      document.querySelector("#account-name").textContent = session.username;
      document.querySelector("#welcome-name").textContent = session.username;
      document.querySelector("#account-avatar").textContent = session.username.slice(0, 1).toUpperCase();
      document.querySelector("#greeting").textContent = greeting();
      await loadItems();
    } catch (error) {
      if (error.message !== "登录已失效") {
        itemList.innerHTML = `<div class="list-error">${escapeHtml(error.message)} <button type="button" id="retry-load">重新加载</button></div>`;
        document.querySelector("#retry-load").addEventListener("click", initialize);
      }
    }
  }

  textArea.addEventListener("input", function () {
    const length = Array.from(textArea.value).length;
    characterCount.textContent = length.toLocaleString("zh-CN");
    publishButton.disabled = textArea.value.trim().length === 0 || length > 100000;
  });

  publishButton.addEventListener("click", async function () {
    const value = textArea.value.trim();
    if (!value) return;
    publishButton.disabled = true;
    try {
      await api("/api/texts", { method: "POST", body: JSON.stringify({ text: value }) });
      textArea.value = "";
      textArea.dispatchEvent(new Event("input"));
      await loadItems();
      showToast("文本已发送到共享空间");
    } catch (error) {
      showToast(error.message);
      publishButton.disabled = false;
    }
  });

  async function copyItem(item) {
    try {
      await navigator.clipboard.writeText(item.text);
    } catch (_) {
      const helper = document.createElement("textarea");
      helper.value = item.text;
      document.body.appendChild(helper);
      helper.select();
      document.execCommand("copy");
      helper.remove();
    }
    await api(`/api/texts/${item.id}/copy`, { method: "POST" });
    await loadItems();
    showToast("已复制，并记录本次使用");
  }

  async function downloadItem(item) {
    const payload = await api(`/api/files/${item.id}/download-ticket`, { method: "POST" });
    const link = document.createElement("a");
    link.href = payload.url;
    link.download = item.fileName;
    document.body.appendChild(link);
    link.click();
    link.remove();
    showToast("文件下载已开始");
    window.setTimeout(loadItems, 1000);
  }

  function toggleHistory(card) {
    const panel = card.querySelector(".history-panel");
    const historyButton = card.querySelector(".action-history");
    if (!panel || !historyButton) return;
    panel.hidden = !panel.hidden;
    historyButton.classList.toggle("active", !panel.hidden);
    historyButton.setAttribute("aria-expanded", String(!panel.hidden));
  }

  function toggleDetail(card) {
    const panel = card.querySelector(".item-detail");
    if (!panel) return;
    panel.hidden = !panel.hidden;
    const expanded = !panel.hidden;
    const key = `${card.dataset.kind}:${card.dataset.itemId}`;
    if (expanded) expandedDetails.add(key);
    else expandedDetails.delete(key);
    card.classList.toggle("is-expanded", expanded);
    card.setAttribute("aria-expanded", String(expanded));
  }

  function openImagePreview(item) {
    imagePreviewTitle.textContent = item.fileName;
    imagePreviewImage.src = `/api/files/${item.id}/preview`;
    imagePreviewImage.alt = item.fileName;
    imagePreviewModal.hidden = false;
    document.body.style.overflow = "hidden";
    imagePreviewModal.querySelector(".image-preview-close").focus();
  }

  function closeImagePreview() {
    imagePreviewModal.hidden = true;
    imagePreviewImage.removeAttribute("src");
    imagePreviewImage.alt = "";
    if (modal.hidden) document.body.style.overflow = "";
  }

  function setImageThumbnailLoading(button, loading) {
    if (!button) return;
    if (button.dataset) button.dataset.loading = loading ? "true" : "false";
    if (button.classList) button.classList.toggle("is-loading", loading);
    if (typeof button.setAttribute === "function") button.setAttribute("aria-disabled", String(loading));
  }

  function refreshImageThumbnailStates() {
    if (typeof itemList.querySelectorAll !== "function") return;
    itemList.querySelectorAll(".image-thumbnail img").forEach(function (image) {
      const button = image.closest(".image-thumbnail");
      const loaded = image.complete && image.naturalWidth > 0;
      setImageThumbnailLoading(button, !loaded);
    });
  }

  function thumbnailIsLoading(button) {
    const image = typeof button.querySelector === "function" ? button.querySelector("img") : null;
    if (image && image.complete && image.naturalWidth > 0) {
      setImageThumbnailLoading(button, false);
      return false;
    }
    return Boolean(button.dataset && button.dataset.loading === "true");
  }

  itemList.addEventListener("click", async function (event) {
    const card = event.target.closest(".share-item");
    if (!card) return;
    const item = items.find(function (candidate) { return String(candidate.id) === card.dataset.itemId && candidate.kind === card.dataset.kind; });
    if (!item) return;
    const copyButton = event.target.closest(".action-copy");
    const downloadButton = event.target.closest(".action-download");
    const historyButton = event.target.closest(".action-history");
    const deleteButton = event.target.closest(".action-delete");
    const imageButton = event.target.closest(".image-thumbnail");

    try {
      if (imageButton) {
        if (thumbnailIsLoading(imageButton)) {
          showToast("正在下载，请稍后");
          return;
        }
        openImagePreview(item);
        return;
      }
      if (copyButton) {
        await copyItem(item);
        return;
      }
      if (downloadButton) {
        await downloadItem(item);
        return;
      }
      if (deleteButton) {
        pendingDelete = item;
        document.querySelector("#delete-title").textContent = `删除这条${item.kind === "text" ? "文本" : "文件"}？`;
        modal.hidden = false;
        document.body.style.overflow = "hidden";
        modal.querySelector(".modal-cancel").focus();
        return;
      }
      if (historyButton) {
        toggleHistory(card);
        return;
      }
      if (!event.target.closest(".item-actions")) {
        toggleDetail(card);
      }
    } catch (error) {
      showToast(error.message);
    }
  });

  function closeModal() {
    modal.hidden = true;
    document.body.style.overflow = "";
    pendingDelete = null;
  }

  modal.querySelector(".modal-close").addEventListener("click", closeModal);
  modal.querySelector(".modal-cancel").addEventListener("click", closeModal);
  modal.addEventListener("click", function (event) { if (event.target === modal) closeModal(); });
  imagePreviewModal.querySelector(".image-preview-close").addEventListener("click", closeImagePreview);
  imagePreviewModal.addEventListener("click", closeImagePreview);
  itemList.addEventListener("load", function (event) {
    if (event.target.matches(".image-thumbnail img")) {
      setImageThumbnailLoading(event.target.closest(".image-thumbnail"), false);
    }
  }, true);
  itemList.addEventListener("error", function (event) {
    if (event.target.matches(".image-thumbnail img")) {
      setImageThumbnailLoading(event.target.closest(".image-thumbnail"), true);
    }
  }, true);
  document.addEventListener("keydown", function (event) {
    if (event.key !== "Escape") return;
    if (!imagePreviewModal.hidden) closeImagePreview();
    else if (!modal.hidden) closeModal();
  });
  modal.querySelector(".modal-confirm").addEventListener("click", async function () {
    if (!pendingDelete) return;
    const item = pendingDelete;
    try {
      await api(`/api/${item.kind === "text" ? "texts" : "files"}/${item.id}`, { method: "DELETE" });
      closeModal();
      await loadItems();
      showToast(`${item.kind === "text" ? "文本" : "文件"}已删除`);
    } catch (error) {
      showToast(error.message);
    }
  });

  document.querySelectorAll(".filter-tab").forEach(function (tab) {
    tab.addEventListener("click", function () {
      currentFilter = tab.dataset.filter;
      document.querySelectorAll(".filter-tab").forEach(function (candidate) { candidate.classList.remove("active"); });
      tab.classList.add("active");
      renderItems();
    });
  });

  function uploadFile(file) {
    if (!file) return;
    if (file.size > 1024 ** 3) {
      showToast("文件超过 1 GB，无法上传");
      return;
    }
    dropZone.hidden = true;
    uploadProgress.hidden = false;
    uploadName.textContent = file.name;
    uploadBar.style.width = "0";
    uploadPercent.textContent = "0%";

    const form = new FormData();
    form.append("file", file);
    const request = new XMLHttpRequest();
    request.open("POST", "/api/files");
    request.setRequestHeader("X-CSRF-Token", csrfToken);
    request.upload.addEventListener("progress", function (event) {
      if (!event.lengthComputable) return;
      const percent = Math.round((event.loaded / event.total) * 100);
      uploadBar.style.width = `${percent}%`;
      uploadPercent.textContent = `${percent}%`;
    });
    request.addEventListener("load", async function () {
      dropZone.hidden = false;
      uploadProgress.hidden = true;
      fileInput.value = "";
      if (request.status === 201) {
        await loadItems();
        showToast("文件已上传到共享空间");
        return;
      }
      try {
        showToast(JSON.parse(request.responseText).error || "上传失败");
      } catch (_) {
        showToast("上传失败，请稍后重试");
      }
    });
    request.addEventListener("error", function () {
      dropZone.hidden = false;
      uploadProgress.hidden = true;
      showToast("上传中断，请检查网络后重试");
    });
    request.send(form);
  }

  fileInput.addEventListener("change", function () { uploadFile(fileInput.files[0]); });
  ["dragenter", "dragover"].forEach(function (name) {
    dropZone.addEventListener(name, function (event) {
      event.preventDefault();
      dropZone.classList.add("dragging");
    });
  });
  ["dragleave", "drop"].forEach(function (name) {
    dropZone.addEventListener(name, function (event) {
      event.preventDefault();
      dropZone.classList.remove("dragging");
    });
  });
  dropZone.addEventListener("drop", function (event) { uploadFile(event.dataTransfer.files[0]); });

  document.querySelector("#logout-button").addEventListener("click", async function () {
    try {
      await api("/api/logout", { method: "POST" });
    } finally {
      window.location.replace("/");
    }
  });

  initialize();
})();
