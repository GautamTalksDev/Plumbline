/* Resolve asset/API bases for root deploy (plumbd) and GitHub project pages. */
(function (global) {
  "use strict";
  var path = global.location.pathname;
  var base = "/";
  if (path.indexOf("/Plumbline/") === 0 || path === "/Plumbline") {
    base = "/Plumbline/";
  }
  global.PLUMBLINE_BASE = base;
  if (!global.PLUMBLINE_API) {
    // Same origin when served by plumbd; override via api-origin.js on Pages.
    global.PLUMBLINE_API = "";
  }
})(window);
