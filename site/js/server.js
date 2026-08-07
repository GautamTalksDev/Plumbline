/* /server/ — search by server id; render observation history (hashes only). */
(function () {
  "use strict";
  var UI = window.PlumblineUI;
  var status = document.getElementById("status");
  var list = document.getElementById("obs");
  var sidInput = document.getElementById("sid");
  var loadBtn = document.getElementById("load");

  function load(id) {
    UI.clear(list);
    UI.text(status, "Loading…");
    var encoded = UI.encodeServerId(id);
    history.replaceState(null, "", UI.href("server/") + "?id=" + encoded);

    UI.getJSON("log/servers/index.json")
      .then(function (idx) {
        var servers = idx.servers || [];
        var entry = null;
        for (var i = 0; i < servers.length; i++) {
          if (servers[i].id === id) {
            entry = servers[i];
            break;
          }
        }
        if (!entry) {
          UI.text(status, "No observations for that server id.");
          return null;
        }
        // Filename is sha256 hex — never interpolate the raw id into a path.
        var file = entry.file;
        if (!/^[0-9a-f]{64}\.json$/i.test(file)) {
          throw new Error("invalid server filename in index");
        }
        return UI.getJSON("log/servers/" + file).then(function (hist) {
          var obs = hist.observations || [];
          UI.text(status, obs.length + " observation(s)");
          var title = document.getElementById("server-id");
          if (title) UI.text(title, hist.id || id);

          obs.forEach(function (o) {
            var tr = UI.el("tr", {});
            var cells = [
              ["Date", o.date],
              ["Hash", o.hash],
              ["Protocol", o.protocol_version],
              ["Event", o.event_type],
            ];
            cells.forEach(function (pair) {
              var td = UI.el("td", { "data-label": pair[0], className: pair[0] === "Hash" ? "mono" : "" });
              UI.text(td, pair[1] == null ? "" : String(pair[1]));
              tr.appendChild(td);
            });
            list.appendChild(tr);
          });
        });
      })
      .catch(function (err) {
        UI.text(status, "");
        var p = UI.el("p", { className: "error" });
        UI.text(p, String(err.message || err));
        list.parentNode.insertBefore(p, list);
      });
  }

  if (loadBtn) {
    loadBtn.addEventListener("click", function () {
      var id = sidInput.value.trim();
      if (id) load(id);
    });
  }
  if (sidInput) {
    sidInput.addEventListener("keydown", function (ev) {
      if (ev.key === "Enter") {
        ev.preventDefault();
        var id = sidInput.value.trim();
        if (id) load(id);
      }
    });
  }

  var params = new URLSearchParams(location.search);
  var initial = params.get("id");
  if (initial) {
    sidInput.value = initial;
    load(initial);
  } else {
    UI.text(status, "Enter a server id to load observation history.");
  }
})();
