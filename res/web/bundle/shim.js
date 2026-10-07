(function () {
  "use strict";
  var base = "/__ext/%TOKEN%/";
  function post(name, body) {
    try {
      return fetch(base + name, {
        method: "POST", body: JSON.stringify(body || {}), keepalive: true,
        headers: { "Content-Type": "text/plain" }
      }).catch(function () {});
    } catch (e) {
      return Promise.resolve();
    }
  }
  function text(args) {
    return Array.prototype.map.call(args, function (a) {
      if (typeof a === "string") return a;
      try { return JSON.stringify(a); } catch (e) { return String(a); }
    }).join(" ");
  }
  ["log", "info", "warn", "error", "debug"].forEach(function (level) {
    var original = console[level];
    console[level] = function () {
      post("log", { level: level, text: text(arguments) });
      return original.apply(console, arguments);
    };
  });
  window.addEventListener("error", function (e) {
    post("log", { level: "exception", text: (e.message || "error") + (e.filename ? " (" + e.filename + ":" + e.lineno + ")" : "") });
  });
  window.addEventListener("unhandledrejection", function (e) {
    post("log", { level: "exception", text: "unhandled rejection: " + ((e.reason && e.reason.stack) || e.reason) });
  });
  window.ext = Object.freeze({
    log: function () { return post("log", { level: "log", text: text(arguments) }); },
    exit: function (code) {
      return post("exit", { code: code | 0 }).then(function () {
        try { window.close(); } catch (e) {}
      });
    }
  });
  post("alive", {});
  setInterval(function () { post("alive", {}); }, 1000);
  window.addEventListener("pagehide", function () { post("closed", {}); });
})();
