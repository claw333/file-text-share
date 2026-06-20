const assert = require("node:assert/strict");
const fs = require("node:fs");
const test = require("node:test");
const vm = require("node:vm");

const source = fs.readFileSync("app.js", "utf8");
const shareHtml = fs.readFileSync("share.html", "utf8");
const styles = fs.readFileSync("styles.css", "utf8");

function response(status, payload) {
  return {
    ok: status >= 200 && status < 300,
    status,
    async json() {
      return payload;
    },
  };
}

function flushAsync() {
  return new Promise((resolve) => setImmediate(resolve));
}

function profileHarness(fetchImpl) {
  const listeners = {};
  const elements = {
    "#profile-password-form": {
      addEventListener(type, handler) {
        listeners[type] = handler;
      },
    },
    "#toast": { hidden: true },
    "#toast-message": { textContent: "" },
    "#profile-username": { textContent: "" },
    "#profile-avatar": { textContent: "" },
    "#profile-role": { textContent: "" },
    "#profile-home-link": {
      attrs: {},
      setAttribute(name, value) {
        this.attrs[name] = value;
      },
    },
    "#current-password": { value: "" },
    "#new-password": { value: "" },
    "#confirm-password": { value: "" },
    "#logout-button": { addEventListener() {} },
  };
  const redirects = [];
  const context = {
    Headers,
    FormData: class FormData {},
    fetch: fetchImpl,
    window: {
      clearTimeout() {},
      setTimeout() {
        return 1;
      },
      location: {
        replace(target) {
          redirects.push(target);
        },
      },
    },
    document: {
      body: { dataset: { page: "profile" } },
      querySelector(selector) {
        return elements[selector] || null;
      },
    },
  };
  vm.runInNewContext(source, context);
  return { elements, listeners, redirects };
}

function shareHarness(fetchImpl) {
  const listeners = {};
  function element(extra = {}) {
    return {
      hidden: false,
      textContent: "",
      value: "",
      disabled: false,
      files: [],
      dataset: {},
      style: {},
      classList: {
        add() {},
        remove() {},
        toggle() {},
      },
      addEventListener(type, handler) {
        listeners[type] = handler;
      },
      dispatchEvent() {},
      querySelector(selector) {
        return elements[selector] || null;
      },
      focus() {},
      ...extra,
    };
  }
  const itemList = element({
    _html: "",
    set innerHTML(value) {
      this._html = value;
    },
    get innerHTML() {
      return this._html;
    },
  });
  const modal = element({
    hidden: true,
    querySelector(selector) {
      return elements[selector] || null;
    },
  });
  const filterTabs = [
    element({ dataset: { filter: "all" } }),
    element({ dataset: { filter: "text" } }),
    element({ dataset: { filter: "file" } }),
  ];
  const elements = {
    "#shared-text": element(),
    "#character-count": element(),
    "#publish-text": element(),
    "#item-list": itemList,
    "#empty-state": element({ hidden: true }),
    "#file-input": element(),
    "#drop-zone": element(),
    "#upload-progress": element({ hidden: true }),
    "#upload-file-name": element(),
    "#upload-percent": element(),
    "#upload-bar": element(),
    "#delete-modal": modal,
    ".modal-close": element(),
    ".modal-cancel": element(),
    ".modal-confirm": element(),
    "#delete-title": element(),
    "#toast": element({ hidden: true }),
    "#toast-message": element(),
    "#account-name": element(),
    "#welcome-name": element(),
    "#account-avatar": element(),
    "#greeting": element(),
    "#storage-used": element(),
    "#storage-quota": element(),
    "#storage-percent": element(),
    "#storage-bar": element(),
    "#all-count": element(),
    "#logout-button": element(),
  };
  const redirects = [];
  const context = {
    Headers,
    FormData: class FormData {},
    XMLHttpRequest: class XMLHttpRequest {},
    fetch: fetchImpl,
    navigator: { clipboard: { writeText() {} } },
    window: {
      clearTimeout() {},
      setTimeout() {
        return 1;
      },
      location: {
        replace(target) {
          redirects.push(target);
        },
      },
    },
    document: {
      body: {
        dataset: { page: "share" },
        style: {},
        appendChild() {},
      },
      createElement() {
        return {
          set textContent(value) {
            this._text = value;
          },
          get innerHTML() {
            return String(this._text)
              .replace(/&/g, "&amp;")
              .replace(/</g, "&lt;")
              .replace(/>/g, "&gt;");
          },
          addEventListener() {},
          click() {},
          remove() {},
          select() {},
        };
      },
      querySelector(selector) {
        return elements[selector] || null;
      },
      querySelectorAll(selector) {
        if (selector === ".filter-tab") return filterTabs;
        return [];
      },
      addEventListener() {},
      execCommand() {},
    },
  };
  vm.runInNewContext(source, context);
  return { elements, redirects };
}

function adminHarness(fetchImpl) {
  const listeners = {};
  function element(extra = {}) {
    return {
      hidden: false,
      textContent: "",
      value: "",
      disabled: false,
      attrs: {},
      dataset: {},
      style: {},
      classList: { add() {}, remove() {}, toggle() {} },
      addEventListener(type, handler) {
        listeners[type] = handler;
      },
      setAttribute(name, value) {
        this.attrs[name] = value;
      },
      querySelector(selector) {
        return elements[selector] || null;
      },
      focus() {},
      reset() {},
      ...extra,
    };
  }
  const usersContainer = element({
    _html: "",
    set innerHTML(value) {
      this._html = value;
    },
    get innerHTML() {
      return this._html;
    },
    querySelector(selector) {
      if (selector === '[data-quota-for="2"]') return elements["quota-input-2"];
      return elements[selector] || null;
    },
    addEventListener(type, handler) {
      listeners[`users:${type}`] = handler;
    },
  });
  const deleteModal = element({
    hidden: true,
    querySelector(selector) {
      return elements[selector] || null;
    },
  });
  const elements = {
    "#admin-users": usersContainer,
    "#admin-create-user-form": element(),
    "#admin-stats": element({
      _html: "",
      set innerHTML(value) {
        this._html = value;
      },
      get innerHTML() {
        return this._html;
      },
    }),
    "#toast": element({ hidden: true }),
    "#toast-message": element(),
    "#admin-delete-modal": deleteModal,
    "#admin-delete-title": element(),
    "#admin-delete-description": element(),
    ".modal-close": element(),
    ".modal-cancel": element(),
    ".modal-confirm": element(),
    "#admin-new-username": element(),
    "#admin-new-password": element(),
    "#account-name": element(),
    "#account-avatar": element(),
    "#admin-refresh-users": element(),
    "#logout-button": element(),
    "quota-input-2": element({ value: "10" }),
  };
  const redirects = [];
  const context = {
    Headers,
    FormData: class FormData {},
    fetch: fetchImpl,
    Intl,
    window: {
      clearTimeout() {},
      setTimeout() {
        return 1;
      },
      location: {
        replace(target) {
          redirects.push(target);
        },
      },
    },
    document: {
      body: {
        dataset: { page: "admin" },
        style: {},
      },
      createElement() {
        return {
          set textContent(value) {
            this._text = value;
          },
          get innerHTML() {
            return String(this._text)
              .replace(/&/g, "&amp;")
              .replace(/</g, "&lt;")
              .replace(/>/g, "&gt;");
          },
        };
      },
      querySelector(selector) {
        return elements[selector] || null;
      },
      addEventListener() {},
    },
  };
  vm.runInNewContext(source, context);
  return { elements, listeners, redirects };
}

test("profile password error stays on page and shows backend message", async () => {
  const requests = [];
  const harness = profileHarness(async (path, config) => {
    requests.push({ path, config });
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    return response(401, { error: "当前密码错误" });
  });

  await flushAsync();
  harness.elements["#current-password"].value = "WrongPassword#2026";
  harness.elements["#new-password"].value = "UpdatedPassword#2026";
  harness.elements["#confirm-password"].value = "UpdatedPassword#2026";

  await harness.listeners.submit({ preventDefault() {} });

  assert.deepEqual(harness.redirects, []);
  assert.equal(harness.elements["#toast"].hidden, false);
  assert.equal(harness.elements["#toast-message"].textContent, "当前密码错误");
  assert.equal(requests[1].config.headers.get("X-CSRF-Token"), "csrf-token");
});

test("profile session expiration still redirects to login", async () => {
  const harness = profileHarness(async () => response(401, { error: "请先登录" }));

  await flushAsync();

  assert.deepEqual(harness.redirects, ["/"]);
  assert.equal(harness.elements["#toast"].hidden, true);
});

test("share item file type escapes file extension before rendering", async () => {
  const harness = shareHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    if (path === "/api/items") {
      return response(200, {
        items: [{
          id: 42,
          kind: "file",
          fileName: "evil.</span>",
          fileSize: 12,
          createdAt: "2026-06-13T08:30:00Z",
          expiresAt: "2026-06-14T08:30:00Z",
          uploaderDevice: "browser",
          events: [],
        }],
      });
    }
    return response(404, { error: "not found" });
  });

  await flushAsync();
  await flushAsync();

  const html = harness.elements["#item-list"].innerHTML;
  assert.equal(html.includes("</SPAN"), false);
  assert.equal(html.includes("&lt;/SPAN"), true);
  assert.deepEqual(harness.redirects, []);
});

test("share page renders current user storage usage", async () => {
  const harness = shareHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    if (path === "/api/items") {
      return response(200, {
        storageUsedBytes: 1536,
        storageQuotaBytes: 5368709120,
        items: [],
      });
    }
    return response(404, { error: "not found" });
  });

  await flushAsync();
  await flushAsync();

  assert.equal(harness.elements["#storage-used"].textContent, "1.5 KB");
  assert.equal(harness.elements["#storage-quota"].textContent, "5 GB");
  assert.equal(harness.elements["#storage-percent"].textContent, "0.01%");
  assert.equal(harness.elements["#storage-bar"].style.width, "0.01%");
});

test("share storage summary keeps desktop labels unwrapped and spaced", () => {
  assert.match(shareHtml, /class="storage-copy"/);
  assert.match(shareHtml, /<\/strong>\s+<small><span id="storage-percent"/);
  assert.match(styles, /\.retention-summary small \{[^}]*white-space:\s*nowrap/);
  assert.match(styles, /\.retention-summary > div:not\(\.storage-summary\) \{[^}]*flex:\s*0 0 auto/);
  assert.match(styles, /\.storage-copy \{[^}]*gap:\s*0 8px/);
});

test("admin users render storage quota and used space", async () => {
  const harness = adminHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "admin", role: "admin", csrfToken: "csrf-token", redirectTo: "/admin.html" });
    }
    if (path === "/api/admin/users") {
      return response(200, {
        users: [{
          id: 2,
          username: "quotauser",
          role: "user",
          createdAt: "2026-06-13T08:30:00Z",
          updatedAt: "2026-06-13T08:30:00Z",
          lastLoginAt: null,
          lastUploadAt: null,
          textCount: 1,
          fileCount: 1,
          loginCount: 0,
          storageUsedBytes: 1536,
          storageQuotaBytes: 5368709120,
        }, {
          id: 3,
          username: "tinyquota",
          role: "user",
          createdAt: "2026-06-13T08:30:00Z",
          updatedAt: "2026-06-13T08:30:00Z",
          lastLoginAt: null,
          lastUploadAt: null,
          textCount: 0,
          fileCount: 0,
          loginCount: 0,
          storageUsedBytes: 0,
          storageQuotaBytes: 107374,
        }],
      });
    }
    return response(404, { error: "not found" });
  });

  await flushAsync();
  await flushAsync();

  const html = harness.elements["#admin-users"].innerHTML;
  assert.match(html, /1\.5 KB/);
  assert.match(html, /5 GB/);
  assert.match(html, /value="0\.0001"/);
});

test("admin quota save posts bytes with csrf token", async () => {
  const requests = [];
  const harness = adminHarness(async (path, config = {}) => {
    requests.push({ path, config });
    if (path === "/api/session") {
      return response(200, { username: "admin", role: "admin", csrfToken: "csrf-token", redirectTo: "/admin.html" });
    }
    if (path === "/api/admin/users") {
      return response(200, { users: [] });
    }
    if (path === "/api/admin/users/2/quota") {
      return response(204, {});
    }
    return response(404, { error: "not found" });
  });

  await flushAsync();
  await flushAsync();
  await harness.listeners["users:click"]({
    target: {
      closest(selector) {
        if (selector === ".admin-save-quota") {
          return { dataset: { userId: "2" }, disabled: false };
        }
        return null;
      },
    },
  });

  const quotaRequest = requests.find((request) => request.path === "/api/admin/users/2/quota");
  assert.ok(quotaRequest);
  assert.equal(quotaRequest.config.headers.get("X-CSRF-Token"), "csrf-token");
  assert.equal(quotaRequest.config.body, JSON.stringify({ storageQuotaBytes: 10737418240 }));
});
