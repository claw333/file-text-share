(function () {
  "use strict";

  const page = document.body.dataset.page;
  let csrfToken = "";

  function passwordIsValid(value) {
    return /^(?=.*[a-z])(?=.*[A-Z])(?=.*\d)(?=.*[^A-Za-z0-9]).{12,128}$/.test(value);
  }

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
    if (response.status === 401 && page === "share") {
      window.location.replace("/");
      throw new Error("登录已失效");
    }
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
      throw error;
    }
    if (response.status === 204) return null;
    return response.json();
  }

  if (page === "login") {
    const passwordInput = document.querySelector("#password");
    const passwordToggle = document.querySelector(".password-toggle");
    const passwordRule = document.querySelector("#password-rule");
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
      passwordRule.classList.toggle("valid", passwordIsValid(passwordInput.value));
      error.hidden = true;
    });

    form.addEventListener("submit", async function (event) {
      event.preventDefault();
      const username = document.querySelector("#username").value.trim();
      if (!username || !passwordIsValid(passwordInput.value)) {
        error.textContent = "用户名或密码不符合要求，请检查后重试。";
        error.hidden = false;
        return;
      }

      const submit = form.querySelector("button[type='submit']");
      submit.disabled = true;
      const original = submit.innerHTML;
      submit.textContent = "正在验证…";
      try {
        await api("/api/login", {
          method: "POST",
          body: JSON.stringify({ username, password: passwordInput.value }),
        });
        window.location.replace("/share.html");
      } catch (requestError) {
        error.textContent = requestError.message;
        error.hidden = false;
        submit.disabled = false;
        submit.innerHTML = original;
      }
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
  const modal = document.querySelector("#delete-modal");
  const toast = document.querySelector("#toast");
  const toastMessage = document.querySelector("#toast-message");
  let items = [];
  let currentFilter = "all";
  let pendingDelete = null;
  let toastTimer = null;

  itemList.innerHTML = '<div class="list-loading">正在读取共享内容…</div>';

  function escapeHtml(value) {
    const div = document.createElement("div");
    div.textContent = value;
    return div.innerHTML;
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
    if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
    if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
    if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${bytes} B`;
  }

  function remainingLabel(expiresAt) {
    const milliseconds = new Date(expiresAt).getTime() - Date.now();
    const hours = Math.max(0, Math.ceil(milliseconds / 3_600_000));
    if (hours < 24) return { text: `${hours} 小时后清理`, soon: true };
    return { text: `${Math.ceil(hours / 24)} 天后清理`, soon: hours <= 48 };
  }

  function fileType(item) {
    const parts = item.fileName.split(".");
    return parts.length > 1 ? parts.pop().slice(0, 6).toUpperCase() : "FILE";
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
      return `<li><span class="history-device"><i></i><span><strong>${escapeHtml(event.deviceLabel)}</strong><small>${formatTime(event.createdAt)} · ${action}</small></span></span></li>`;
    }).join("");
    return `<div class="history-panel" hidden><div class="history-title"><strong>${isText ? "使用" : "下载"}记录</strong><span>共 ${item.events.length} 次</span></div><ol>${rows}</ol></div>`;
  }

  function renderItem(item) {
    const isText = item.kind === "text";
    const count = item.events.length;
    const statusText = count ? `已${isText ? "复制" : "下载"} ${count} 次` : `尚未${isText ? "使用" : "下载"}`;
    const retention = remainingLabel(item.expiresAt);
    const kind = isText ? "文本" : `文件 · ${fileType(item)}`;
    const icon = isText
      ? '<div class="item-type-badge text-icon">T</div>'
      : '<div class="item-type-badge file-icon"><svg viewBox="0 0 24 24"><path d="M6 2h8l4 4v16H6zM14 2v5h5" /></svg></div>';
    const content = isText
      ? `<p class="text-preview">${escapeHtml(item.text).replaceAll("\n", "<br>")}</p>`
      : `<p class="file-name">${escapeHtml(item.fileName)}</p>`;
    const size = isText ? "" : `<span>${formatSize(item.fileSize)}</span>`;
    const primary = isText
      ? '<button class="button button-soft action-copy" type="button"><svg viewBox="0 0 24 24"><rect x="8" y="8" width="12" height="12" rx="2" /><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2" /></svg>复制</button>'
      : '<button class="button button-soft action-download" type="button"><svg viewBox="0 0 24 24"><path d="M12 3v12M7 10l5 5 5-5M5 21h14" /></svg>下载</button>';

    return `<article class="share-item${retention.soon ? " expiring-item" : ""}" data-kind="${item.kind}" data-item-id="${item.id}">
      <div class="item-main">${icon}<div class="item-content">
        <div class="item-topline"><span class="kind-label">${kind}</span><span class="status-chip ${count ? "status-used" : "status-new"}"><span></span>${statusText}</span></div>
        ${content}
        <div class="item-meta">${size}
          <span><svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></svg>${formatTime(item.createdAt)}</span>
          <span><svg viewBox="0 0 24 24"><rect x="3" y="5" width="18" height="13" rx="2" /><path d="M8 21h8M12 18v3" /></svg>${escapeHtml(item.uploaderDevice)}</span>
          <span class="expiry${retention.soon ? " expiry-soon" : ""}"><svg viewBox="0 0 24 24"><path d="M6 8h12M9 3v3M15 3v3M5 5h14v16H5z" /></svg>${retention.text}</span>
        </div>${historyMarkup(item)}
      </div></div>
      <div class="item-actions">${primary}
        <button class="icon-button action-history" type="button" aria-label="展开${isText ? "使用" : "下载"}记录" title="历史记录"><svg viewBox="0 0 24 24"><path d="M3 12a9 9 0 1 0 3-6.7L3 8M3 3v5h5M12 7v5l3 2" /></svg></button>
        <button class="icon-button action-delete" type="button" aria-label="删除${isText ? "文本" : "文件"}" title="删除"><svg viewBox="0 0 24 24"><path d="M4 7h16M9 7V4h6v3M7 7l1 14h8l1-14M10 11v6M14 11v6" /></svg></button>
      </div></article>`;
  }

  function renderItems() {
    const visible = currentFilter === "all" ? items : items.filter(function (item) { return item.kind === currentFilter; });
    itemList.innerHTML = visible.map(renderItem).join("");
    document.querySelector("#all-count").textContent = items.length;
    emptyState.hidden = visible.length !== 0;
  }

  async function loadItems() {
    const payload = await api("/api/items", { method: "GET" });
    items = payload.items || [];
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

  itemList.addEventListener("click", async function (event) {
    const card = event.target.closest(".share-item");
    if (!card) return;
    const item = items.find(function (candidate) { return String(candidate.id) === card.dataset.itemId && candidate.kind === card.dataset.kind; });
    if (!item) return;
    const copyButton = event.target.closest(".action-copy");
    const downloadButton = event.target.closest(".action-download");
    const historyButton = event.target.closest(".action-history");
    const deleteButton = event.target.closest(".action-delete");

    try {
      if (copyButton) await copyItem(item);
      if (downloadButton) await downloadItem(item);
      if (historyButton) {
        const panel = card.querySelector(".history-panel");
        panel.hidden = !panel.hidden;
        historyButton.classList.toggle("active", !panel.hidden);
        historyButton.setAttribute("aria-expanded", String(!panel.hidden));
      }
      if (deleteButton) {
        pendingDelete = item;
        document.querySelector("#delete-title").textContent = `删除这条${item.kind === "text" ? "文本" : "文件"}？`;
        modal.hidden = false;
        document.body.style.overflow = "hidden";
        modal.querySelector(".modal-cancel").focus();
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
  document.addEventListener("keydown", function (event) { if (event.key === "Escape" && !modal.hidden) closeModal(); });
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
