const assert = require("node:assert/strict");
const fs = require("node:fs");
const test = require("node:test");
const vm = require("node:vm");

const source = fs.readFileSync("app.js", "utf8");

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
