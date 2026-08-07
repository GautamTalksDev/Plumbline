/* Shared: fill study banner on pages that include #study-banner. */
(function () {
  "use strict";
  if (window.PlumblineUI) {
    window.PlumblineUI.fillBanner(document.getElementById("study-banner"));
  }
})();
