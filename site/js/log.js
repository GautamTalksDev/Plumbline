/* /log/ — live tree head from static bundle JSON. */
(function () {
  "use strict";
  var UI = window.PlumblineUI;

  function setStat(id, value) {
    var n = document.getElementById(id);
    if (n) UI.text(n, value);
  }

  function showHash(id, value) {
    var n = document.getElementById(id);
    if (!n) return;
    UI.text(n, value);
    var btn = document.getElementById(id + "-copy");
    if (btn) {
      btn.onclick = function () { UI.copyText(value, btn); };
    }
  }

  Promise.all([
    UI.getJSON("log/meta.json"),
    UI.getJSON("log/checkpoint.json"),
    UI.getText("log/pubkey.txt"),
  ])
    .then(function (parts) {
      var meta = parts[0];
      var cp = parts[1];
      var pub = String(parts[2] || "").trim();

      setStat("stat-tree", String(meta.tree_size));
      setStat("stat-entries", String(meta.entry_count));
      setStat("stat-lastrun", meta.last_run_date || "—");
      setStat("stat-checkpoints", String(meta.checkpoint_count));
      setStat("stat-canon", meta.canon_version || "—");
      setStat("stat-taxonomy", meta.taxonomy_version || "—");

      showHash("root-hash", cp.root_hash || "");
      showHash("pubkey", pub);
      setStat("cp-size", String(cp.tree_size));
      setStat("cp-ts", cp.timestamp
        ? new Date(cp.timestamp * 1000).toISOString()
        : "—");
      showHash("cp-sig", cp.signature || "");
    })
    .catch(function (err) {
      var e = document.getElementById("log-error");
      if (e) {
        e.hidden = false;
        UI.text(e, String(err.message || err));
      }
    });
})();
