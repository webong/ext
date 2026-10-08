(function () {
  "use strict";
  // The page side of the web engine. One file serves every way a host can show
  // a page:
  //  - served over loopback by Run, which replaces the placeholder below with a
  //    per-run token, so reports go over HTTP;
  //  - injected unchanged into an embedded WKWebView or Android WebView at
  //    document start, which leaves the placeholder alone, so reports go to the
  //    app through its native message handler.
  var token = "%TOKEN%";
  var served = token !== "%" + "TOKEN%" && token !== "";
  var base = served ? "/__ext/" + token + "/" : "";
  var native = null, kind = "none";
  if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.ext) {
    native = function (s) { window.webkit.messageHandlers.ext.postMessage(s); };
    kind = "wkwebview";
  } else if (window.ExtHost && typeof window.ExtHost.postMessage === "function") {
    native = function (s) { window.ExtHost.postMessage(s); };
    kind = "android";
  } else if (served) {
    kind = "http";
  }

  // post reports one event to the host: kind is log, exit, alive, closed or host.
  function post(name, body) {
    try {
      if (native) {
        var frame = body || {};
        frame.kind = name;
        native(JSON.stringify(frame));
        return Promise.resolve();
      }
      if (served) {
        return fetch(base + name, {
          method: "POST", body: JSON.stringify(body || {}), keepalive: true,
          headers: { "Content-Type": "text/plain" }
        }).catch(function () {});
      }
    } catch (e) {}
    return Promise.resolve();
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

  // host is the two-way channel between the page and the program showing it.
  // Messages are strings; ext.plugin/v1 frames pass through unchanged.
  var handler = null, polling = false;
  var host = {
    transport: kind,
    send: function (message) { return post("host", { data: String(message) }); },
    // The host calls _deliver with a string; the page receives it in onmessage.
    _deliver: function (message) {
      if (typeof handler !== "function") return;
      try { handler(String(message)); } catch (e) { console.error("ext.host.onmessage: " + (e && e.stack || e)); }
    }
  };
  // Over HTTP the page has to ask for what the host sends, so delivery waits for
  // the first handler. A native host pushes into _deliver instead.
  // After an empty answer it pauses briefly, so a server that answers at once
  // cannot make the page spin.
  function poll() {
    fetch(base + "next").then(function (response) {
      if (response.status !== 200) return false;
      return response.text().then(function (t) { host._deliver(t); return true; });
    }).then(function (delivered) {
      if (delivered) poll(); else setTimeout(poll, 100);
    }, function () { setTimeout(poll, 500); });
  }
  Object.defineProperty(host, "onmessage", {
    enumerable: true,
    get: function () { return handler; },
    set: function (fn) {
      handler = fn;
      if (kind === "http" && !polling) { polling = true; poll(); }
    }
  });

  window.ext = Object.freeze({
    host: host,
    log: function () { return post("log", { level: "log", text: text(arguments) }); },
    exit: function (code) {
      return post("exit", { code: code | 0 }).then(function () {
        try { window.close(); } catch (e) {}
      });
    }
  });
  // Over HTTP the engine learns that a tab was closed from these. A native host
  // owns its webview and knows, so on those transports they would only be wasted
  // work across the bridge once a second.
  if (kind === "http") {
    post("alive", {});
    setInterval(function () { post("alive", {}); }, 1000);
    window.addEventListener("pagehide", function () { post("closed", {}); });
  }
})();
