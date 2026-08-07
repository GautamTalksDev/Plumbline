/* Resolve asset base for root deploy and GitHub project pages. Zero network beyond self. */
(function (global) {
  "use strict";
  var path = global.location.pathname;
  var base = "/";
  if (path.indexOf("/Plumbline/") === 0 || path === "/Plumbline") {
    base = "/Plumbline/";
  }
  global.PLUMBLINE_BASE = base;
})(window);
