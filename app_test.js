const assert = require("node:assert/strict");
const fs = require("node:fs");
const test = require("node:test");
const vm = require("node:vm");

const source = fs.readFileSync("app.js", "utf8");
const indexHtml = fs.readFileSync("index.html", "utf8");
const shareHtml = fs.readFileSync("share.html", "utf8");
const adminHtml = fs.readFileSync("admin.html", "utf8");
const profileHtml = fs.readFileSync("profile.html", "utf8");
const styles = fs.readFileSync("styles.css", "utf8");
const dockerfile = fs.readFileSync("Dockerfile", "utf8");
const favicon = fs.readFileSync("favicon.png");

function relativeLuminance(hex) {
  const normalized = hex.replace("#", "");
  const channels = [0, 2, 4].map((offset) => parseInt(normalized.slice(offset, offset + 2), 16) / 255);
  const linear = channels.map((channel) => (
    channel <= 0.03928 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4
  ));
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
}

function contrastRatio(foreground, background) {
  const foregroundLuminance = relativeLuminance(foreground);
  const backgroundLuminance = relativeLuminance(background);
  return (Math.max(foregroundLuminance, backgroundLuminance) + 0.05) / (Math.min(foregroundLuminance, backgroundLuminance) + 0.05);
}

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

function loginHarness(fetchImpl) {
  const listeners = {};
  function element(extra = {}) {
    return {
      hidden: false,
      textContent: "",
      value: "",
      type: "",
      disabled: false,
      innerHTML: "",
      attrs: {},
      classNames: new Set(),
      classList: {
        add(name) {
          this.owner.classNames.add(name);
        },
        remove(name) {
          this.owner.classNames.delete(name);
        },
        toggle(name, value) {
          if (value) this.owner.classNames.add(name);
          else this.owner.classNames.delete(name);
        },
        contains(name) {
          return this.owner.classNames.has(name);
        },
      },
      setAttribute(name, value) {
        this.attrs[name] = value;
      },
      removeAttribute(name) {
        delete this.attrs[name];
      },
      addEventListener(type, handler) {
        listeners[type] = handler;
      },
      closest() {
        return null;
      },
      focus() {},
      ...extra,
    };
  }
  const usernameField = element();
  const passwordField = element();
  const usernameInput = element({
    closest(selector) {
      return selector === ".field" ? usernameField : null;
    },
    addEventListener(type, handler) {
      listeners[`username:${type}`] = handler;
    },
  });
  const passwordInput = element({
    type: "password",
    closest(selector) {
      return selector === ".field" ? passwordField : null;
    },
    addEventListener(type, handler) {
      listeners[`password:${type}`] = handler;
    },
  });
  const submitButton = element({ innerHTML: "安全登录" });
  const form = element({
    querySelector(selector) {
      return selector === "button[type='submit']" ? submitButton : null;
    },
    addEventListener(type, handler) {
      listeners[`form:${type}`] = handler;
    },
  });
  for (const candidate of [usernameField, passwordField, usernameInput, passwordInput, submitButton, form]) {
    candidate.classList.owner = candidate;
  }
  const elements = {
    "#username": usernameInput,
    "#password": passwordInput,
    "#username-error": element({ hidden: true }),
    "#password-error": element({ hidden: true }),
    ".password-toggle": element({
      addEventListener(type, handler) {
        listeners[`password-toggle:${type}`] = handler;
      },
    }),
    "#login-form": form,
    "#login-error": element({ hidden: true }),
  };
  elements[".password-toggle"].classList.owner = elements[".password-toggle"];
  elements["#login-error"].classList.owner = elements["#login-error"];
  elements["#username-error"].classList.owner = elements["#username-error"];
  elements["#password-error"].classList.owner = elements["#password-error"];
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
      body: { dataset: { page: "login" } },
      querySelector(selector) {
        return elements[selector] || null;
      },
    },
  };
  vm.runInNewContext(source, context);
  return { elements, fields: { usernameField, passwordField }, listeners, redirects };
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

function shareHarness(fetchImpl, options = {}) {
  const listeners = {};
  const TestDate = options.now ? class extends Date {
    constructor(...args) {
      super(...(args.length ? args : [options.now]));
    }

    static now() {
      return new Date(options.now).getTime();
    }
  } : Date;
  function element(extra = {}) {
    return {
      hidden: false,
      textContent: "",
      value: "",
      disabled: false,
      files: [],
      attrs: {},
      dataset: {},
      style: {},
      classList: {
        add() {},
        remove() {},
        toggle() {},
      },
      setAttribute(name, value) {
        this.attrs[name] = value;
      },
      removeAttribute(name) {
        delete this.attrs[name];
        delete this[name];
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
    addEventListener(type, handler) {
      listeners[`item-list:${type}`] = handler;
    },
  });
  const modal = element({
    hidden: true,
    querySelector(selector) {
      return elements[selector] || null;
    },
  });
  const imagePreviewModal = element({
    hidden: true,
    addEventListener(type, handler) {
      listeners[`image-preview-modal:${type}`] = handler;
    },
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
    "#image-preview-modal": imagePreviewModal,
    "#image-preview-title": element(),
    "#image-preview-image": element({ src: "", alt: "" }),
    ".image-preview-close": element(),
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
    Date: TestDate,
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
      matchMedia(query) {
        return {
          matches: Boolean(options.mobile && query === "(max-width: 760px)"),
          media: query,
          addEventListener() {},
          removeEventListener() {},
        };
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
  return { elements, listeners, redirects };
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

test("login required fields block empty submit and mark both inputs", async () => {
  const requests = [];
  const harness = loginHarness(async (path) => {
    requests.push(path);
    return response(401, { error: "用户名或密码错误" });
  });

  await harness.listeners["form:submit"]({ preventDefault() {} });

  assert.deepEqual(requests, []);
  assert.equal(harness.elements["#login-error"].hidden, true);
  assert.equal(harness.elements["#username-error"].hidden, false);
  assert.equal(harness.elements["#password-error"].hidden, false);
  assert.equal(harness.elements["#username-error"].textContent, "请输入用户名");
  assert.equal(harness.elements["#password-error"].textContent, "请输入密码");
  assert.equal(harness.elements["#username"].attrs["aria-invalid"], "true");
  assert.equal(harness.elements["#password"].attrs["aria-invalid"], "true");
  assert.equal(harness.elements["#username"].attrs["aria-describedby"], "username-error");
  assert.equal(harness.elements["#password"].attrs["aria-describedby"], "password-error");
  assert.equal(harness.fields.usernameField.classList.contains("is-invalid"), true);
  assert.equal(harness.fields.passwordField.classList.contains("is-invalid"), true);
});

test("login required validation marks only the empty password field", async () => {
  const requests = [];
  const harness = loginHarness(async (path) => {
    requests.push(path);
    return response(401, { error: "用户名或密码错误" });
  });
  harness.elements["#username"].value = "demo";

  await harness.listeners["form:submit"]({ preventDefault() {} });

  assert.deepEqual(requests, []);
  assert.equal(harness.elements["#login-error"].hidden, true);
  assert.equal(harness.elements["#username-error"].hidden, true);
  assert.equal(harness.elements["#password-error"].hidden, false);
  assert.equal(harness.elements["#password-error"].textContent, "请输入密码");
  assert.equal(harness.elements["#username"].attrs["aria-invalid"], undefined);
  assert.equal(harness.elements["#password"].attrs["aria-invalid"], "true");
  assert.equal(harness.elements["#username"].attrs["aria-describedby"], undefined);
  assert.equal(harness.elements["#password"].attrs["aria-describedby"], "password-error");
  assert.equal(harness.fields.usernameField.classList.contains("is-invalid"), false);
  assert.equal(harness.fields.passwordField.classList.contains("is-invalid"), true);
});

test("login backend errors still show after required fields pass", async () => {
  const requests = [];
  const harness = loginHarness(async (path, config) => {
    requests.push({ path, config });
    return response(401, { error: "用户名或密码错误" });
  });
  harness.elements["#username"].value = "demo";
  harness.elements["#password"].value = "WrongPassword#2026";

  await harness.listeners["form:submit"]({ preventDefault() {} });

  assert.equal(requests.length, 1);
  assert.equal(requests[0].path, "/api/login");
  assert.equal(harness.elements["#login-error"].textContent, "用户名或密码错误");
  assert.equal(harness.elements["#username"].attrs["aria-invalid"], undefined);
  assert.equal(harness.elements["#password"].attrs["aria-invalid"], undefined);
  assert.equal(harness.elements["#username-error"].hidden, true);
  assert.equal(harness.elements["#password-error"].hidden, true);
});

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

test("mobile share items cap initial expiry labels at configured retention days", async () => {
  const harness = shareHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    if (path === "/api/items") {
      return response(200, {
        items: [{
          id: 42,
          kind: "text",
          text: "fresh note",
          createdAt: "2026-06-21T10:00:30Z",
          expiresAt: "2026-07-21T10:00:30Z",
          uploaderDevice: "iPhone · Safari",
          events: [],
        }, {
          id: 43,
          kind: "file",
          fileName: "fresh.png",
          fileSize: 1024,
          mimeType: "image/png",
          createdAt: "2026-06-21T10:00:30Z",
          expiresAt: "2026-06-28T10:00:30Z",
          uploaderDevice: "iPhone · Safari",
          events: [],
        }],
      });
    }
    return response(404, { error: "not found" });
  }, { mobile: true, now: "2026-06-21T10:00:00Z" });

  await flushAsync();
  await flushAsync();

  const html = harness.elements["#item-list"].innerHTML;
  assert.match(html, /30 天后清理/);
  assert.match(html, /7 天后清理/);
  assert.doesNotMatch(html, /31 天后清理|8 天后清理/);
});

test("share item blank area toggles details while history button toggles history", async () => {
  const harness = shareHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    if (path === "/api/items") {
      return response(200, {
        items: [{
          id: 42,
          kind: "text",
          text: "review note",
          createdAt: "2026-06-13T08:30:00Z",
          expiresAt: "2026-06-14T08:30:00Z",
          uploaderDevice: "browser",
          events: [{ eventType: "copy", deviceLabel: "Mac · Chrome", createdAt: "2026-06-13T08:31:00Z" }],
        }],
      });
    }
    return response(404, { error: "not found" });
  });

  await flushAsync();
  await flushAsync();

  const historyPanel = { hidden: true };
  const detailPanel = { hidden: true };
  const historyButton = {
    attrs: {},
    active: false,
    classList: {
      toggle(name, value) {
        if (name === "active") historyButton.active = value;
      },
    },
    setAttribute(name, value) {
      this.attrs[name] = value;
    },
  };
  const cardClasses = new Set();
  const card = {
    dataset: { kind: "text", itemId: "42" },
    attrs: {},
    classList: {
      toggle(name, value) {
        if (value) cardClasses.add(name);
        else cardClasses.delete(name);
      },
    },
    setAttribute(name, value) {
      this.attrs[name] = value;
    },
    querySelector(selector) {
      if (selector === ".history-panel") return historyPanel;
      if (selector === ".item-detail") return detailPanel;
      if (selector === ".action-history") return historyButton;
      return null;
    },
  };
  const blankTarget = {
    closest(selector) {
      if (selector === ".share-item") return card;
      if (selector === ".item-actions") return null;
      if (selector === ".image-thumbnail") return null;
      if (selector === ".action-history") return null;
      return null;
    },
  };
  const historyTarget = {
    closest(selector) {
      if (selector === ".share-item") return card;
      if (selector === ".action-history") return historyButton;
      return null;
    },
  };

  await harness.listeners["item-list:click"]({ target: blankTarget });
  assert.equal(detailPanel.hidden, false);
  assert.equal(cardClasses.has("is-expanded"), true);
  assert.equal(card.attrs["aria-expanded"], "true");
  assert.equal(historyPanel.hidden, true);

  await harness.listeners["item-list:click"]({ target: blankTarget });
  assert.equal(detailPanel.hidden, true);
  assert.equal(cardClasses.has("is-expanded"), false);
  assert.equal(card.attrs["aria-expanded"], "false");

  await harness.listeners["item-list:click"]({ target: historyTarget });
  assert.equal(detailPanel.hidden, true);
  assert.equal(historyPanel.hidden, false);
  assert.equal(historyButton.active, true);
  assert.equal(historyButton.attrs["aria-expanded"], "true");

  await harness.listeners["item-list:click"]({ target: historyTarget });
  assert.equal(detailPanel.hidden, true);
  assert.equal(historyPanel.hidden, true);
  assert.equal(historyButton.active, false);
  assert.equal(historyButton.attrs["aria-expanded"], "false");
});

test("share text detail truncates long text to 1000 characters", async () => {
  const visible = "x".repeat(1000);
  const hiddenTail = "TAIL_SHOULD_NOT_RENDER";
  const harness = shareHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    if (path === "/api/items") {
      return response(200, {
        items: [{
          id: 42,
          kind: "text",
          text: visible + hiddenTail,
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
  assert.match(html, /class="text-preview"/);
  assert.match(html, /class="item-detail text-detail" hidden/);
  assert.match(html, /预览最多 1000 字/);
  assert.match(html, new RegExp(`${visible}…`));
  assert.equal(html.includes(hiddenTail), false);
});

test("share text preview is one line and limited to 50 characters on desktop", async () => {
  const visible = "x".repeat(50);
  const harness = shareHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    if (path === "/api/items") {
      return response(200, {
        items: [{
          id: 42,
          kind: "text",
          text: `${visible}\nSECOND_LINE_SHOULD_COLLAPSE`,
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

  const preview = harness.elements["#item-list"].innerHTML.match(/<p class="text-preview">([^<]*)<\/p>/)[1];
  assert.equal(preview, `${visible}…`);
  assert.equal(preview.includes("<br>"), false);
  assert.equal(preview.includes("SECOND_LINE_SHOULD_COLLAPSE"), false);
});

test("share text preview uses a shorter mobile limit", async () => {
  const visible = "m".repeat(32);
  const harness = shareHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    if (path === "/api/items") {
      return response(200, {
        items: [{
          id: 42,
          kind: "text",
          text: `${visible}MOBILE_TAIL`,
          createdAt: "2026-06-13T08:30:00Z",
          expiresAt: "2026-06-14T08:30:00Z",
          uploaderDevice: "browser",
          events: [],
        }],
      });
    }
    return response(404, { error: "not found" });
  }, { mobile: true });

  await flushAsync();
  await flushAsync();

  const preview = harness.elements["#item-list"].innerHTML.match(/<p class="text-preview">([^<]*)<\/p>/)[1];
  assert.equal(preview, `${visible}…`);
  assert.equal(preview.includes("MOBILE_TAIL"), false);
});

test("share file details render generic and image thumbnails", async () => {
  const harness = shareHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    if (path === "/api/items") {
      return response(200, {
        items: [{
          id: 7,
          kind: "file",
          fileName: "script.py",
          fileSize: 42,
          mimeType: "text/x-python",
          createdAt: "2026-06-13T08:30:00Z",
          expiresAt: "2026-06-14T08:30:00Z",
          uploaderDevice: "browser",
          events: [],
        }, {
          id: 8,
          kind: "file",
          fileName: "photo.png",
          fileSize: 128,
          mimeType: "image/png",
          createdAt: "2026-06-13T08:30:00Z",
          expiresAt: "2026-06-14T08:30:00Z",
          uploaderDevice: "browser",
          events: [],
        }, {
          id: 9,
          kind: "file",
          fileName: "notes.txt",
          fileSize: 41,
          mimeType: "text/plain; charset=utf-8",
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
  assert.match(html, /class="file-thumbnail generic-thumbnail"/);
  assert.match(html, />PY<\/span>/);
  assert.match(html, /class="file-thumbnail image-thumbnail is-loading"/);
  assert.match(html, /data-loading="true"/);
  assert.match(html, /class="thumbnail-spinner"/);
  assert.match(html, /src="\/api\/files\/8\/preview"/);
  assert.match(html, /aria-label="放大图片 photo\.png"/);
  assert.match(html, />128 B · png<\/span>/);
  assert.match(html, />41 B · plain<\/span>/);
  assert.doesNotMatch(html, /image\/png/);
  assert.doesNotMatch(html, /plaincharset/);
});

test("share records omit usage status chips and normalize source labels", async () => {
  const harness = shareHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    if (path === "/api/items") {
      return response(200, {
        items: [{
          id: 11,
          kind: "text",
          text: "轻递文本",
          createdAt: "2026-06-13T08:30:00Z",
          expiresAt: "2026-06-14T08:30:00Z",
          uploaderDevice: "iPhone · Safari · iOS",
          events: [{ eventType: "copy", deviceLabel: "Windows PC · Chrome · Windows", createdAt: "2026-06-13T09:00:00Z" }],
        }, {
          id: 12,
          kind: "file",
          fileName: "report.pdf",
          fileSize: 42,
          mimeType: "application/pdf",
          createdAt: "2026-06-13T08:30:00Z",
          expiresAt: "2026-06-14T08:30:00Z",
          uploaderDevice: "Windows PC · Chrome · Windows",
          events: [{ eventType: "download", deviceLabel: "iPhone · Safari · iOS", createdAt: "2026-06-13T09:00:00Z" }],
        }],
      });
    }
    return response(404, { error: "not found" });
  }, { mobile: true });

  await flushAsync();
  await flushAsync();

  const html = harness.elements["#item-list"].innerHTML;
  assert.doesNotMatch(html, /尚未使用|尚未下载|已复制\s+\d+\s+次|已下载\s+\d+\s+次/);
  assert.doesNotMatch(html, /class="status-chip/);
  assert.match(html, /<div class="item-meta"><span class="meta-line"><span>4 字<\/span>/);
  assert.match(html, /<div class="item-meta"><span class="meta-line"><span>42 B<\/span>/);
  assert.match(html, /<div class="item-meta"><span class="meta-line"><span>4 字<\/span>\s*<span><svg[\s\S]*?2026-06-13[\s\S]*?<\/span><\/span>\s*<span class="meta-line"><span><svg[\s\S]*?iOS · Safari<\/span>/);
  assert.match(html, />iOS · Safari<\/span>/);
  assert.match(html, />Windows · Chrome<\/span>/);
  assert.doesNotMatch(html, /iPhone · Safari · iOS|Windows PC · Chrome · Windows/);
});

test("share image thumbnail opens preview modal", async () => {
  const harness = shareHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    if (path === "/api/items") {
      return response(200, {
        items: [{
          id: 8,
          kind: "file",
          fileName: "photo.png",
          fileSize: 128,
          mimeType: "image/png",
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

  const card = {
    dataset: { kind: "file", itemId: "8" },
    querySelector() {
      return null;
    },
  };
  const imageButton = {};
  const imageTarget = {
    closest(selector) {
      if (selector === ".share-item") return card;
      if (selector === ".image-thumbnail") return imageButton;
      return null;
    },
  };

  await harness.listeners["item-list:click"]({ target: imageTarget });

  assert.equal(harness.elements["#image-preview-modal"].hidden, false);
  assert.equal(harness.elements["#image-preview-title"].textContent, "photo.png");
  assert.equal(harness.elements["#image-preview-image"].src, "/api/files/8/preview");
  assert.equal(harness.elements["#image-preview-image"].alt, "photo.png");
});

test("share loading image thumbnail blocks preview and shows toast", async () => {
  const harness = shareHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    if (path === "/api/items") {
      return response(200, {
        items: [{
          id: 8,
          kind: "file",
          fileName: "photo.png",
          fileSize: 128,
          mimeType: "image/png",
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

  const card = {
    dataset: { kind: "file", itemId: "8" },
    querySelector() {
      return null;
    },
  };
  const imageButton = { dataset: { loading: "true" } };
  const imageTarget = {
    closest(selector) {
      if (selector === ".share-item") return card;
      if (selector === ".image-thumbnail") return imageButton;
      return null;
    },
  };

  await harness.listeners["item-list:click"]({ target: imageTarget });

  assert.equal(harness.elements["#image-preview-modal"].hidden, true);
  assert.equal(harness.elements["#toast"].hidden, false);
  assert.equal(harness.elements["#toast-message"].textContent, "正在下载，请稍后");
});

test("share image preview closes when any preview area is clicked", async () => {
  const harness = shareHarness(async (path) => {
    if (path === "/api/session") {
      return response(200, { username: "demo", role: "user", csrfToken: "csrf-token", redirectTo: "/share.html" });
    }
    if (path === "/api/items") {
      return response(200, {
        items: [{
          id: 8,
          kind: "file",
          fileName: "photo.png",
          fileSize: 128,
          mimeType: "image/png",
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

  const card = {
    dataset: { kind: "file", itemId: "8" },
    querySelector() {
      return null;
    },
  };
  const imageButton = { dataset: { loading: "false" } };
  const imageTarget = {
    closest(selector) {
      if (selector === ".share-item") return card;
      if (selector === ".image-thumbnail") return imageButton;
      return null;
    },
  };

  await harness.listeners["item-list:click"]({ target: imageTarget });
  assert.equal(harness.elements["#image-preview-modal"].hidden, false);

  harness.listeners["image-preview-modal:click"]({ target: harness.elements["#image-preview-image"] });

  assert.equal(harness.elements["#image-preview-modal"].hidden, true);
  assert.equal(harness.elements["#image-preview-image"].alt, "");
});

test("share storage summary keeps desktop labels unwrapped and spaced", () => {
  assert.match(shareHtml, /class="storage-copy"/);
  assert.match(shareHtml, /<\/strong>\s+<small><span id="storage-percent"/);
  assert.match(styles, /\.retention-summary small \{[^}]*white-space:\s*nowrap/);
  assert.match(styles, /\.retention-summary > div:not\(\.storage-summary\) \{[^}]*flex:\s*0 0 auto/);
  assert.match(styles, /\.storage-copy \{[^}]*gap:\s*0 8px/);
});

test("share collapsed records use one-line previews and uniform height", () => {
  assert.match(styles, /\.share-item \{[^}]*min-height:\s*116px/);
  assert.match(styles, /\.share-item:not\(\.is-expanded\) \{[^}]*align-items:\s*center/);
  assert.match(styles, /\.text-preview,\s*\.file-name \{[^}]*height:\s*22px[^}]*white-space:\s*nowrap[^}]*text-overflow:\s*ellipsis/);
  assert.match(styles, /\.text-preview \{[^}]*-webkit-line-clamp:\s*1/);
});

test("desktop share metadata uses one compact row with equal gaps", () => {
  assert.match(styles, /\.item-meta \{[^}]*display:\s*grid[^}]*grid-template-columns:\s*repeat\(4,\s*max-content\)[^}]*column-gap:\s*18px/);
  assert.match(styles, /\.meta-line \{[^}]*display:\s*contents/);
  assert.match(styles, /\.meta-line > span \{[^}]*white-space:\s*nowrap/);
});

test("mobile collapsed records show metadata without vertical clipping", () => {
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.item-meta \{[^}]*display:\s*grid[^}]*height:\s*auto[^}]*overflow:\s*visible/);
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.meta-line \{[^}]*display:\s*flex[^}]*gap:\s*12px/);
  assert.doesNotMatch(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.share-item:not\(\.is-expanded\) \.item-meta/);
});

test("mobile text details sit below the type badge like file details", () => {
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.share-item\.is-expanded \.text-detail \{[^}]*margin-top:\s*40px/);
});

test("image preview and thumbnail loading states stay bounded and visible", () => {
  assert.match(styles, /\.image-preview-dialog \{[^}]*width:\s*min\(960px,\s*calc\(100vw - 48px\)\)[^}]*overflow:\s*hidden/);
  assert.match(styles, /\.image-preview-dialog img \{[^}]*max-width:\s*100%[^}]*max-height:\s*calc\(100svh - 128px\)/);
  assert.match(styles, /\.image-thumbnail\.is-loading img \{[^}]*filter:\s*brightness\(0\.55\)/);
  assert.match(styles, /\.thumbnail-spinner \{[^}]*animation:\s*spin 800ms linear infinite/);
});

test("composer text input matches the file drop zone height", () => {
  assert.match(styles, /\.text-composer textarea \{[^}]*height:\s*166px/);
  assert.match(styles, /\.drop-zone \{[^}]*height:\s*166px/);
  assert.doesNotMatch(styles, /@media \(max-width: 960px\) \{[\s\S]*?\.text-composer textarea \{[^}]*height:\s*140px/);
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.text-composer textarea,\s*\.drop-zone \{[^}]*height:\s*140px/);
});

test("app footers omit the device timezone note", () => {
  for (const html of [shareHtml, adminHtml, profileHtml]) {
    assert.equal(html.includes("所有时间按当前设备时区显示"), false);
    assert.equal(html.includes("当前设备时区"), false);
    assert.equal(html.includes("精确到秒"), false);
    assert.equal(html.includes("<p><span></span>"), false);
  }
  assert.match(styles, /\.app-footer \{[^}]*justify-content:\s*flex-start/);
  assert.match(styles, /\.app-footer \{[^}]*text-align:\s*left/);
  assert.doesNotMatch(styles, /\.app-footer p:first-child/);
  assert.doesNotMatch(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.app-footer \{[^}]*text-align:\s*center/);
});

test("mobile login uses the light login background only", () => {
  assert.match(indexHtml, /<meta name="theme-color" content="#eef3fb" \/>/);
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.login-body \{[^}]*background:\s*#eef3fb/);
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.login-shell \{[^}]*min-height:\s*100svh;[^}]*background:\s*#eef3fb/);
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.login-panel \{[^}]*background:\s*#eef3fb/);
});

test("all pages reference the project favicon", () => {
  assert.ok(favicon.length > 0);
  for (const html of [indexHtml, shareHtml, adminHtml, profileHtml]) {
    assert.match(html, /<link rel="icon" type="image\/png" href="favicon\.png" \/>/);
  }
});

test("docker build context includes embedded favicon asset", () => {
  assert.match(dockerfile, /COPY .*favicon\.png/);
});

test("landing login page omits footer security text and feature badges", () => {
  assert.equal(indexHtml.includes("登录后在你的设备之间共享内容，过期自动清理。"), true);
  assert.equal(indexHtml.includes("登录后在你的设备之间共享内容。"), false);
  assert.equal(indexHtml.includes("登录后在你的设备之间短暂停靠"), false);
  assert.equal(indexHtml.includes("连接已受保护"), false);
  assert.equal(indexHtml.includes("服务端会话"), false);
  assert.doesNotMatch(indexHtml, /<span>HTTPS<\/span>|<span>自动清理<\/span>/);
  assert.doesNotMatch(indexHtml, /class="login-badges"/);
  assert.doesNotMatch(styles, /\.login-badges/);
  assert.doesNotMatch(styles, /\.login-footer/);
});

test("mobile login secondary text keeps accessible contrast", () => {
  const mobileLoginTextColor = "#536077";
  assert.ok(contrastRatio(mobileLoginTextColor, "#eef3fb") >= 4.5);
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.login-card-heading > p:last-child \{[^}]*color:\s*#536077/);
});

test("login required validation uses Ant Design style field errors", () => {
  assert.match(indexHtml, /id="username-error"[^>]*class="field-error"[^>]*role="alert"[^>]*hidden/);
  assert.match(indexHtml, /id="password-error"[^>]*class="field-error"[^>]*role="alert"[^>]*hidden/);
  assert.match(styles, /\.field-error \{[^}]*margin-top:\s*6px[^}]*color:\s*var\(--red\)[^}]*font-size:\s*12px/);
  assert.match(styles, /\.field\.is-invalid \.input-wrap input \{[^}]*border-color:\s*var\(--red\)[^}]*background:\s*white/);
  assert.match(styles, /\.field\.is-invalid \.input-wrap input:focus \{[^}]*box-shadow:\s*0 0 0 3px rgba\(193,\s*72,\s*72,\s*0\.12\)/);
  assert.match(styles, /\.form-error \{[^}]*background:\s*transparent/);
  assert.doesNotMatch(styles, /\.field\.is-invalid \.input-wrap input \{[^}]*background:\s*#fffafa/);
  assert.doesNotMatch(styles, /\.form-error \{[^}]*padding:\s*10px/);
});

test("mobile admin quota and password controls use separate aligned rows", () => {
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.admin-user-actions \{[^}]*display:\s*grid[^}]*grid-template-columns:\s*1fr auto/);
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.admin-quota-control,\s*\.admin-password-reset \{[^}]*grid-column:\s*1 \/ -1[^}]*display:\s*grid/);
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.admin-quota-control \{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\) auto auto/);
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.admin-password-reset \{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\) auto/);
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.admin-save-quota,\s*\.admin-reset-password \{[^}]*white-space:\s*nowrap/);
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
