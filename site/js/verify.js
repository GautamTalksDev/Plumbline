/* /verify/: fill the copyable command for the current origin. */
(function () {
  "use strict";
  var UI = window.PlumblineUI;
  var cmd = document.getElementById("verify-cmd");
  var btn = document.getElementById("verify-cmd-copy");
  var note = document.getElementById("verify-cmd-note");
  if (!cmd || !UI) return;

  var localFallback = "plumb verify-log --bundle ./site/log --pubkey ./site/log/pubkey.txt";
  var text = localFallback;
  try {
    var origin = window.location.origin;
    var base = (window.PLUMBLINE_BASE || "/").replace(/\/?$/, "/");
    if (origin && origin.indexOf("http") === 0) {
      var bundle = origin + base + "log/";
      var key = origin + base + "log/pubkey.txt";
      text = "plumb verify-log --bundle " + bundle + " --pubkey-hex $(curl -fsS " + key + ")";
      if (note) {
        UI.text(note, "Command uses this site’s origin. Compare the public key file with docs/log-public-key.ed25519.");
      }
    }
  } catch (e) {
    text = localFallback;
  }
  UI.text(cmd, text);

  if (btn) {
    btn.addEventListener("click", function () {
      UI.copyText(cmd.textContent, btn);
    });
  }
})();
