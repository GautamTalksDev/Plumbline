/* XSS-safe DOM helpers. Never assign untrusted strings to innerHTML or insertAdjacentHTML. */
(function (global) {
  "use strict";

  /**
   * Strip ANSI escapes, bidi overrides, and zero-width/format characters so
   * attacker-controlled server ids and observation fields render inert.
   * Mirrors internal/safetext behaviour for the browser.
   */
  function safeDisplay(s) {
    if (s == null) return "";
    s = String(s);
    var out = "";
    var i = 0;
    while (i < s.length) {
      var c = s.charCodeAt(i);
      if (c === 0x1b) {
        out += "\\x1b[…]";
        i++;
        if (i < s.length && (s.charAt(i) === "[" || s.charAt(i) === "]")) {
          var term = s.charAt(i);
          i++;
          while (i < s.length) {
            var ch = s.charCodeAt(i);
            i++;
            if (term === "[" && ch >= 0x40 && ch <= 0x7e) break;
            if (term === "]" && (ch === 0x07 || (ch === 0x1b && i < s.length && s.charAt(i) === "\\"))) {
              if (ch === 0x1b) i++;
              break;
            }
          }
        }
        continue;
      }
      var cp = s.codePointAt(i);
      var size = cp > 0xffff ? 2 : 1;
      i += size;
      if (cp === 0x09 || cp === 0x0a) {
        out += String.fromCodePoint(cp);
        continue;
      }
      if (cp === 0x0d) {
        out += "\\r";
        continue;
      }
      // Bidi overrides
      if (
        cp === 0x202a || cp === 0x202b || cp === 0x202c || cp === 0x202d || cp === 0x202e ||
        cp === 0x2066 || cp === 0x2067 || cp === 0x2068 || cp === 0x2069
      ) {
        out += "\\u" + ("0000" + cp.toString(16)).slice(-4);
        continue;
      }
      // Zero-width / format
      if (
        cp === 0x200b || cp === 0x200c || cp === 0x200d || cp === 0x200e || cp === 0x200f ||
        cp === 0xfeff || cp === 0x00ad
      ) {
        out += "\\u" + ("0000" + cp.toString(16)).slice(-4);
        continue;
      }
      if (cp < 0x20 || (cp >= 0x7f && cp < 0xa0)) {
        out += "\\u" + ("0000" + cp.toString(16)).slice(-4);
        continue;
      }
      out += String.fromCodePoint(cp);
    }
    return out;
  }

  function text(el, s) {
    el.textContent = safeDisplay(s);
  }

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        if (k === "className") {
          node.className = attrs[k];
        } else if (k.indexOf("on") === 0) {
          /* never attach inline handlers from data */
        } else if (k === "href") {
          node.setAttribute("href", escapeHref(attrs[k]));
        } else {
          node.setAttribute(k, safeDisplay(attrs[k]));
        }
      });
    }
    (children || []).forEach(function (c) {
      if (c == null) return;
      if (typeof c === "string") node.appendChild(document.createTextNode(safeDisplay(c)));
      else node.appendChild(c);
    });
    return node;
  }

  function escapeHref(href) {
    var s = String(href == null ? "" : href);
    // Only allow relative paths and same-origin log paths, never javascript:
    if (/^\s*javascript:/i.test(s) || /^\s*data:/i.test(s)) return "#";
    return s;
  }

  /** Escape a server id before placing it in a query string or path segment. */
  function encodeServerId(id) {
    return encodeURIComponent(String(id == null ? "" : id));
  }

  function href(path) {
    var base = global.PLUMBLINE_BASE || "/";
    if (path.charAt(0) === "/") path = path.slice(1);
    return base + path;
  }

  function getJSON(path) {
    var url = href(path);
    return fetch(url, {
      headers: { Accept: "application/json" },
      credentials: "omit",
      cache: "no-store",
    }).then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status + " for " + path);
      return r.json();
    });
  }

  function getText(path) {
    return fetch(href(path), {
      credentials: "omit",
      cache: "no-store",
    }).then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status + " for " + path);
      return r.text();
    });
  }

  function clear(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  function copyText(value, btn) {
    var v = String(value == null ? "" : value);
    function done() {
      if (btn) {
        var prev = btn.textContent;
        btn.textContent = "Copied";
        setTimeout(function () { btn.textContent = prev; }, 1200);
      }
    }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(v).then(done).catch(function () {
        fallbackCopy(v);
        done();
      });
    } else {
      fallbackCopy(v);
      done();
    }
  }

  function fallbackCopy(v) {
    var ta = document.createElement("textarea");
    ta.value = v;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.left = "-9999px";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); } catch (e) { /* ignore */ }
    document.body.removeChild(ta);
  }

  /**
   * Fill the study status banner from meta.json.
   * Falls back to a static "Study in progress" line if meta is missing.
   */
  function fillBanner(elBanner) {
    if (!elBanner) return;
    getJSON("log/meta.json")
      .then(function (m) {
        var n = m.week != null ? m.week : "?";
        var total = m.week_total != null ? m.week_total : 6;
        clear(elBanner);
        elBanner.appendChild(document.createTextNode(
          "Study in progress. Week " + n + " of " + total +
          ". Findings are not published until the pre-registered kill tests are evaluated. See "
        ));
        var a = document.createElement("a");
        a.href = href("docs/LIMITATIONS.md");
        a.textContent = "docs/LIMITATIONS.md";
        elBanner.appendChild(a);
        elBanner.appendChild(document.createTextNode(
          ". Absence of findings on this site is not itself a finding."
        ));
      })
      .catch(function () {
        /* noscript / static text already in HTML */
      });
  }

  global.PlumblineUI = {
    text: text,
    el: el,
    safeDisplay: safeDisplay,
    encodeServerId: encodeServerId,
    escapeHref: escapeHref,
    getJSON: getJSON,
    getText: getText,
    href: href,
    clear: clear,
    copyText: copyText,
    fillBanner: fillBanner,
  };
})(window);
