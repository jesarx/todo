(function () {
  "use strict";

  // ---- confirmaciones ----
  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-confirm]");
    if (btn && !window.confirm(btn.dataset.confirm)) {
      e.preventDefault();
      e.stopPropagation();
    }
  });

  // ---- sin conexión: cola local con sincronización ----
  // Los formularios con data-offline se mandan por fetch. Si no hay red, la
  // acción se guarda en una cola local (localStorage) y se reenvía sola al
  // volver la conexión, en el mismo orden en que se hizo. Cada alta lleva un
  // client_id único: el servidor deduplica, así que un reintento no duplica,
  // y las acciones sobre una tarea que aún no se sincroniza viajan a
  // /t/c/<client_id>/… porque todavía no existe su id de la base.
  var QKEY = "todo-queue";
  var authFail = false;

  function readQueue() {
    try { return JSON.parse(localStorage.getItem(QKEY)) || []; } catch (e) { return []; }
  }
  function writeQueue(q) {
    try { localStorage.setItem(QKEY, JSON.stringify(q)); } catch (e) {}
    paintSyncbar();
  }
  function uuid() {
    if (window.crypto && crypto.randomUUID) return crypto.randomUUID();
    return "q" + Date.now() + "-" + Math.random().toString(36).slice(2, 10);
  }
  function paintSyncbar() {
    var bar = document.getElementById("syncbar");
    if (!bar) return;
    var n = readQueue().length;
    bar.hidden = n === 0;
    if (authFail) {
      bar.textContent = n + " cambio(s) sin subir · vuelve a entrar para sincronizar";
      return;
    }
    bar.textContent = n === 1
      ? "1 cambio guardado en este dispositivo · se sube al volver la conexión"
      : n + " cambios guardados en este dispositivo · se suben al volver la conexión";
  }

  var flushing = false;
  function flush() {
    if (flushing || authFail) return;
    var q = readQueue();
    if (!q.length) return;
    flushing = true;
    var sent = 0;
    (function next() {
      if (!q.length) {
        flushing = false;
        writeQueue(q);
        if (sent) location.reload(); // ya sincronizado: refresca con lo real
        return;
      }
      fetch(q[0].url, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: q[0].body,
        credentials: "same-origin"
      }).then(function (res) {
        if (res.status === 401) {        // sesión caducada: nada se pierde
          authFail = true;
          flushing = false;
          writeQueue(q);
          return;
        }
        if (res.status >= 500) {         // problema del servidor: reintenta luego
          flushing = false;
          writeQueue(q);
          return;
        }
        // 2xx/3xx = aplicado; 4xx = no va a funcionar nunca, se descarta
        q.shift();
        sent++;
        writeQueue(q);
        next();
      }).catch(function () {
        flushing = false;                // sigue sin red: la cola espera
        writeQueue(q);
      });
    })();
  }

  // ---- pintado optimista de lo que aún no se sincroniza ----

  function queuedItem(cid, title) {
    var li = document.createElement("li");
    li.className = "task queued";
    li.dataset.cid = cid;

    var f = document.createElement("form");
    f.method = "post";
    f.action = "/t/c/" + cid + "/toggle";
    f.className = "inline";
    f.setAttribute("data-offline", "");
    f.dataset.optimistic = "done";
    var set = document.createElement("input");
    set.type = "hidden"; set.name = "set"; set.value = "done";
    var tick = document.createElement("button");
    tick.className = "tick";
    tick.setAttribute("aria-label", "Marcar como hecha");
    tick.textContent = "○";
    f.appendChild(set); f.appendChild(tick);

    var body = document.createElement("div");
    body.className = "body";
    var t = document.createElement("span");
    t.className = "title";
    t.textContent = title;
    var meta = document.createElement("p");
    meta.className = "meta";
    var tag = document.createElement("span");
    tag.textContent = "⏳ por sincronizar";
    var del = document.createElement("button");
    del.type = "button";
    del.className = "iconbtn";
    del.dataset.unqueue = cid;
    del.textContent = "quitar";
    meta.appendChild(tag); meta.appendChild(del);
    body.appendChild(t); body.appendChild(meta);

    li.appendChild(f); li.appendChild(body);
    return li;
  }

  function paintQueued() {
    var list = document.getElementById("tasklist");
    if (!list) return;
    var done = {};
    readQueue().forEach(function (it) {
      var p = new URLSearchParams(it.body);
      var m = it.url.match(/^\/t\/c\/([^/]+)\/toggle$/);
      if (m) { done[m[1]] = p.get("set") === "done"; return; }
      var m2 = it.url.match(/^\/t\/(\d+)\/toggle$/);
      if (m2) {
        var li = list.querySelector('li[data-id="' + m2[1] + '"]');
        if (li) li.classList.toggle("offdone", p.get("set") === "done");
        return;
      }
      if (it.url !== "/t") return;
      // alta pendiente: solo en la pestaña a la que pertenece
      if (list.dataset.list && p.get("list_id") !== list.dataset.list) return;
      var cid = p.get("client_id");
      if (!cid || list.querySelector('li[data-cid="' + cid + '"]')) return;
      var empty = document.getElementById("empty");
      if (empty) empty.remove();
      // en orden de la cola: el más nuevo acaba hasta arriba, como en el servidor
      list.insertBefore(queuedItem(cid, p.get("title") || ""), list.firstChild);
    });
    Object.keys(done).forEach(function (cid) {
      var li = list.querySelector('li[data-cid="' + cid + '"]');
      if (li) li.classList.toggle("offdone", done[cid]);
    });
  }

  // quitar: si el alta todavía no sale del teléfono, se borra de la cola y ya
  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-unqueue]");
    if (!btn) return;
    var cid = btn.dataset.unqueue;
    writeQueue(readQueue().filter(function (it) {
      return it.body.indexOf(encodeURIComponent(cid)) === -1 && it.url.indexOf(cid) === -1;
    }));
    var li = btn.closest("li");
    if (li) li.remove();
  });

  // offlineAfter deja la pantalla como si la acción ya hubiera pasado
  function offlineAfter(f) {
    if (f.classList.contains("add")) {
      var input = f.querySelector('input[name="title"]');
      var cid = f.querySelector('input[name="client_id"]');
      var list = document.getElementById("tasklist");
      if (list && input && cid) {
        var empty = document.getElementById("empty");
        if (empty) empty.remove();
        list.insertBefore(queuedItem(cid.value, input.value), list.firstChild);
      }
      if (input) input.value = "";
      var notes = f.querySelector('textarea[name="notes"]');
      if (notes) notes.value = "";
      return;
    }
    if (f.dataset.optimistic) {
      var li = f.closest("li");
      if (li) li.classList.toggle("offdone", f.dataset.optimistic === "done");
      var set = f.querySelector('input[name="set"]');
      var tick = f.querySelector(".tick");
      if (set && tick) {           // deja el botón listo para deshacerlo
        var nowDone = set.value === "done";
        set.value = nowDone ? "open" : "done";
        tick.textContent = nowDone ? "✓" : "○";
        tick.classList.toggle("on", nowDone);
        f.dataset.optimistic = nowDone ? "open" : "done";
      }
      return;
    }
    if (f.classList.contains("detail")) {
      // la edición no se puede repintar sin recargar: se avisa y ya
      var msg = f.querySelector(".pending");
      if (!msg) {
        msg = document.createElement("p");
        msg.className = "pending muted small";
        f.appendChild(msg);
      }
      msg.textContent = "⏳ cambios guardados en este dispositivo; se suben al volver la conexión";
      return;
    }
    if (f.action.indexOf("/chk") !== -1) {
      var item = f.closest("li");
      var box = f.querySelector(".box");
      var s = f.querySelector('input[name="set"]');
      if (item && box && s) {
        var mark = s.value === "1";
        item.classList.toggle("done", mark);
        box.textContent = mark ? "✓" : "○";
        s.value = mark ? "0" : "1";
      }
    }
  }

  document.addEventListener("submit", function (e) {
    if (e.defaultPrevented) return;
    var f = e.target;
    if (!f.hasAttribute || !f.hasAttribute("data-offline")) return;
    e.preventDefault();
    if (f.dataset.busy) return;
    f.dataset.busy = "1";
    var cid = f.querySelector('input[name="client_id"]');
    if (cid) cid.value = uuid();
    var body = new URLSearchParams(new FormData(f)).toString();
    var url = f.getAttribute("action");
    var buttons = f.querySelectorAll("button");
    buttons.forEach(function (b) { b.disabled = true; });
    var rearm = function () {
      delete f.dataset.busy;
      buttons.forEach(function (b) { b.disabled = false; });
    };
    fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body,
      credentials: "same-origin"
    }).then(function (res) {
      if (res.status === 401) { location.href = "/login"; return; }
      if (res.status >= 400 && res.status < 500) {
        rearm();
        res.text().then(function (t) { window.alert(t || "No se pudo guardar."); });
        return;
      }
      location.reload();
    }).catch(function () {
      // sin conexión: a la cola y la pantalla sigue funcionando
      var q = readQueue();
      q.push({ url: url, body: body, ts: Date.now() });
      writeQueue(q);
      offlineAfter(f);
      rearm();
    });
  });

  paintQueued();
  paintSyncbar();
  flush();
  window.addEventListener("online", flush);

  // ---- anti doble envío en los formularios normales ----
  document.addEventListener("submit", function (e) {
    if (e.defaultPrevented) return;
    var f = e.target;
    if (f.hasAttribute && f.hasAttribute("data-offline")) return;
    if (f.dataset.sent) { e.preventDefault(); return; }
    f.dataset.sent = "1";
    setTimeout(function () {
      f.querySelectorAll("button:not([type=button])").forEach(function (b) {
        b.disabled = true;
        b.dataset.lock = "1";
      });
    }, 0);
  });
  window.addEventListener("pageshow", function () {
    document.querySelectorAll("form[data-sent]").forEach(function (f) { delete f.dataset.sent; });
    document.querySelectorAll("button[data-lock]").forEach(function (b) {
      b.disabled = false;
      delete b.dataset.lock;
    });
  });

  // ---- listas que se siguen solas en las notas ----
  // Al dar Enter dentro de una lista, el renglón nuevo nace con la misma
  // marca (*, -, 1., [ ]), como en las apps de mensajería. Si el renglón
  // quedó vacío, ese segundo Enter quita la marca y sale de la lista.
  // Shift+Enter siempre da un salto de renglón normal.

  function listPrefix(line) {
    var m = /^(\s*)((?:[-*+]\s+)?\[[ xX]\]\s+)(.*)$/.exec(line); // casilla
    if (m) return { indent: m[1], next: m[2].replace(/\[[xX]\]/, "[ ]"), rest: m[3] };
    m = /^(\s*)([-*+]\s+)(.*)$/.exec(line);                        // viñeta
    if (m) return { indent: m[1], next: m[2], rest: m[3] };
    m = /^(\s*)(\d{1,3})([.)]\s+)(.*)$/.exec(line);                 // numerada
    if (m) return { indent: m[1], next: (parseInt(m[2], 10) + 1) + m[3], rest: m[4] };
    return null;
  }

  // replaceRange escribe con execCommand cuando se puede: así el navegador
  // conserva el deshacer (Ctrl+Z) del propio campo.
  function replaceRange(el, start, end, text) {
    el.focus();
    el.setSelectionRange(start, end);
    var ok = false;
    try {
      ok = document.execCommand(text ? "insertText" : "delete", false, text);
    } catch (err) { ok = false; }
    if (!ok) {
      el.value = el.value.slice(0, start) + text + el.value.slice(end);
      var at = start + text.length;
      el.setSelectionRange(at, at);
    }
    el.dispatchEvent(new Event("input", { bubbles: true }));
  }

  document.addEventListener("keydown", function (e) {
    if (e.isComposing || e.keyCode === 229) return;   // el teclado está componiendo
    if (e.key !== "Enter" && e.keyCode !== 13) return;
    if (e.shiftKey || e.ctrlKey || e.metaKey || e.altKey) return;
    var el = e.target;
    if (!el || el.tagName !== "TEXTAREA" || el.name !== "notes") return;
    if (el.selectionStart !== el.selectionEnd) return; // con texto seleccionado, normal
    var pos = el.selectionStart;
    var lineStart = el.value.lastIndexOf("\n", pos - 1) + 1;
    var p = listPrefix(el.value.slice(lineStart, pos));
    if (!p) return;
    e.preventDefault();
    if (p.rest.trim() === "") {
      replaceRange(el, lineStart, pos, "");            // renglón vacío: se sale
    } else {
      replaceRange(el, pos, pos, "\n" + p.indent + p.next);
    }
  });

  // ---- teclado (en la computadora) ----
  document.addEventListener("keydown", function (e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    var el = document.activeElement;
    var typing = el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT");
    if (e.key === "Escape" && typing) { el.blur(); return; }
    if (typing) return;
    var add = document.getElementById("addtitle");
    if ((e.key === "/" || e.key === "n") && add) { add.focus(); e.preventDefault(); }
  });

  // ---- service worker ----
  if ("serviceWorker" in navigator) {
    navigator.serviceWorker.register("/sw.js").catch(function () {});
    // en el login (saliste o caducó la sesión) se borra lo que quedó en caché
    if (location.pathname === "/login" && navigator.serviceWorker.controller) {
      navigator.serviceWorker.controller.postMessage("clear");
    }
  }
  // almacenamiento persistente: evita que el sistema (sobre todo Android)
  // tire la cola offline o la sesión por falta de espacio
  if (navigator.storage && navigator.storage.persist) {
    navigator.storage.persist().catch(function () {});
  }
})();
