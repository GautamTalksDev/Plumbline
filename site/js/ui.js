/* XSS-safe DOM helpers. Never assign untrusted strings to innerHTML. */
(function (global) {
  "use strict";

  function text(el, s) {
    el.textContent = s == null ? "" : String(s);
  }

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        if (k === "className") node.className = attrs[k];
        else if (k === "href") node.setAttribute("href", attrs[k]);
        else if (k === "title") node.setAttribute("title", attrs[k]);
        else if (k.indexOf("on") === 0) {
          /* never attach inline handlers from data */
        } else node.setAttribute(k, attrs[k]);
      });
    }
    (children || []).forEach(function (c) {
      if (c == null) return;
      if (typeof c === "string") node.appendChild(document.createTextNode(c));
      else node.appendChild(c);
    });
    return node;
  }

  function apiBase() {
    if (global.PLUMBLINE_API) return String(global.PLUMBLINE_API).replace(/\/$/, "");
    return "";
  }

  function href(path) {
    var base = global.PLUMBLINE_BASE || "/";
    if (path.charAt(0) === "/") path = path.slice(1);
    return base + path;
  }

  function getJSON(path) {
    return fetch(apiBase() + path, {
      headers: { Accept: "application/json" },
    }).then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status + " for " + path);
      return r.json();
    });
  }

  function badge(sev) {
    var s = String(sev || "NONE");
    return el("span", { className: "badge sev-" + s }, [s]);
  }

  global.PlumblineUI = {
    text: text,
    el: el,
    getJSON: getJSON,
    apiBase: apiBase,
    href: href,
    badge: badge,
  };
})(window);
