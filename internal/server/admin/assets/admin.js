/* Skimdit admin: autosave, preview, uploads, passkeys, dialogs. */
(function () {
  "use strict";
  var doc = document, body = doc.body;
  var csrf = body.getAttribute("data-csrf") || "";
  var base = body.getAttribute("data-base") || "/admin/";

  function post(url, data, isJSON) {
    var opts = { method: "POST", headers: { "X-CSRF-Token": csrf }, credentials: "same-origin" };
    if (data instanceof FormData) opts.body = data;
    else if (isJSON) { opts.body = JSON.stringify(data); opts.headers["Content-Type"] = "application/json"; }
    return fetch(url, opts);
  }

  // Dialogs: destructive actions confirm in a dialog.
  doc.querySelectorAll("[data-open]").forEach(function (b) {
    b.addEventListener("click", function () {
      var d = doc.getElementById(b.getAttribute("data-open"));
      if (d) { d.showModal(); d.dispatchEvent(new Event("open")); }
    });
  });
  doc.querySelectorAll("dialog [data-close]").forEach(function (b) {
    b.addEventListener("click", function () { b.closest("dialog").close(); });
  });

  // ---- editor ----
  var form = doc.getElementById("editor");
  if (form) {
    var state = doc.querySelector(".save-state");
    var problems = doc.querySelector(".problems");
    var frame = doc.querySelector(".preview iframe");
    var textarea = doc.getElementById("body");
    var timer = null, saving = null, dirty = false;
    var save = function (force) {
      clearTimeout(timer);
      var data = new FormData(form);
      if (force) data.set("force", "1");
      state.textContent = "Saving…";
      saving = post(form.action, data).then(function (r) { return r.json(); }).then(function (j) {
        dirty = false;
        if (j.error) { state.textContent = "Not saved: " + j.error; return; }
        state.textContent = "Saved " + j.saved;
        problems.innerHTML = "";
        (j.problems || []).forEach(function (p) { var li = doc.createElement("li"); li.textContent = p; problems.appendChild(li); });
        if (frame) frame.src = form.getAttribute("data-preview") + "?t=" + Date.now();
      }).catch(function () { state.textContent = "Not saved: connection lost. Your text is still here."; });
      return saving;
    };
    form.addEventListener("input", function () {
      dirty = true;
      state.textContent = "Editing…";
      clearTimeout(timer);
      timer = setTimeout(save, 1200);
    });
    form.addEventListener("submit", function (e) { e.preventDefault(); save(true); });
    doc.addEventListener("keydown", function (e) {
      if ((e.ctrlKey || e.metaKey) && e.key === "s") { e.preventDefault(); save(true); }
    });
    window.addEventListener("beforeunload", function (e) { if (dirty) { e.preventDefault(); e.returnValue = ""; } });
    // Save before publishing or scheduling.
    doc.querySelectorAll("form[data-flush]").forEach(function (f) {
      f.addEventListener("submit", function (e) {
        if (!dirty && !timer) return;
        e.preventDefault();
        save(true).then(function () { f.submit(); });
      });
    });
    // Tabs on narrow screens.
    doc.querySelectorAll("[data-tab]").forEach(function (t) {
      t.addEventListener("click", function () {
        var name = t.getAttribute("data-tab");
        doc.querySelectorAll("[data-tab]").forEach(function (o) { o.setAttribute("aria-selected", o === t ? "true" : "false"); });
        doc.querySelectorAll("[data-pane]").forEach(function (p) { p.hidden = p.getAttribute("data-pane") !== name; });
        if (name === "preview" && dirty) save(true);
      });
    });
    // Uploads: file picker and drag and drop into the text.
    var upState = doc.querySelector(".upload-state");
    var insertAt = function (text) {
      var s = textarea.selectionStart, e = textarea.selectionEnd, v = textarea.value;
      textarea.value = v.slice(0, s) + text + v.slice(e);
      textarea.selectionStart = textarea.selectionEnd = s + text.length;
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      textarea.focus();
    };
    var snippet = function (name) {
      if (/\.(jpe?g|png|gif|webp|svg)$/i.test(name)) return "\n![Describe the image](" + name + ")\n";
      return "[" + name + "](" + name + ")";
    };
    var upload = function (file) {
      var fd = new FormData();
      fd.append("file", file);
      upState.textContent = "Uploading " + file.name + "…";
      return post(form.getAttribute("data-upload"), fd).then(function (r) { return r.json(); }).then(function (j) {
        if (j.error) { upState.textContent = j.error; return; }
        upState.textContent = "Uploaded " + j.name + ". Replace “Describe the image” with what it shows.";
        var li = doc.createElement("li");
        li.innerHTML = "<code></code> ";
        li.firstChild.textContent = j.name;
        doc.querySelector(".file-list").appendChild(li);
        insertAt(snippet(j.name));
      }).catch(function () { upState.textContent = "Upload failed."; });
    };
    var picker = doc.querySelector("[data-upload-input]");
    if (picker) picker.addEventListener("change", function () { Array.from(picker.files).forEach(upload); picker.value = ""; });
    textarea.addEventListener("dragover", function (e) { e.preventDefault(); textarea.classList.add("dragging"); });
    textarea.addEventListener("dragleave", function () { textarea.classList.remove("dragging"); });
    textarea.addEventListener("drop", function (e) {
      textarea.classList.remove("dragging");
      if (!e.dataTransfer.files.length) return;
      e.preventDefault();
      Array.from(e.dataTransfer.files).forEach(upload);
    });
    doc.querySelectorAll("[data-insert]").forEach(function (b) {
      b.addEventListener("click", function () { insertAt(snippet(b.getAttribute("data-insert"))); });
    });
    // Versions.
    var revDialog = doc.getElementById("revisions");
    if (revDialog) revDialog.addEventListener("open", function () {
      var list = revDialog.querySelector(".revision-list");
      list.textContent = "Loading…";
      fetch(form.getAttribute("data-revisions"), { credentials: "same-origin" }).then(function (r) { return r.json(); }).then(function (revs) {
        list.innerHTML = "";
        (revs || []).forEach(function (rv) {
          var li = doc.createElement("li");
          var b = doc.createElement("button");
          b.type = "button"; b.className = "link"; b.textContent = new Date(rv.At * 1000).toLocaleString() + " · " + rv.Words + " words";
          b.addEventListener("click", function () {
            var f = revDialog.querySelector(".revision-form");
            f.querySelector("[name=revision]").value = rv.ID;
            f.submit();
          });
          var p = doc.createElement("span"); p.className = "muted"; p.textContent = rv.Preview;
          li.append(b, p);
          list.appendChild(li);
        });
      });
    });
  }

  // ---- passkeys ----
  var b64 = {
    dec: function (s) { s = s.replace(/-/g, "+").replace(/_/g, "/"); while (s.length % 4) s += "="; return Uint8Array.from(atob(s), function (c) { return c.charCodeAt(0); }); },
    enc: function (buf) { return btoa(String.fromCharCode.apply(null, new Uint8Array(buf))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, ""); }
  };
  var status = doc.querySelector(".passkey-status");
  var say = function (t) { if (status) status.textContent = t; };
  var credJSON = function (c) {
    var r = c.response, out = { id: c.id, rawId: b64.enc(c.rawId), type: c.type, response: { clientDataJSON: b64.enc(r.clientDataJSON) } };
    if (r.attestationObject) { out.response.attestationObject = b64.enc(r.attestationObject); if (r.getTransports) out.response.transports = r.getTransports(); }
    if (r.authenticatorData) { out.response.authenticatorData = b64.enc(r.authenticatorData); out.response.signature = b64.enc(r.signature); if (r.userHandle) out.response.userHandle = b64.enc(r.userHandle); }
    return out;
  };
  var add = doc.querySelector("[data-passkey-add]");
  if (add) add.addEventListener("click", function () {
    if (!window.PublicKeyCredential) { say("This browser does not support passkeys."); return; }
    add.disabled = true; say("Follow your browser's prompt…");
    post(base + "settings/passkeys/begin", null).then(function (r) { return r.json(); }).then(function (o) {
      var pk = o.publicKey;
      pk.challenge = b64.dec(pk.challenge);
      pk.user.id = b64.dec(pk.user.id);
      (pk.excludeCredentials || []).forEach(function (c) { c.id = b64.dec(c.id); });
      return navigator.credentials.create({ publicKey: pk });
    }).then(function (cred) {
      var name = encodeURIComponent(doc.getElementById("pkname").value || "");
      return post(base + "settings/passkeys/finish?name=" + name, credJSON(cred), true);
    }).then(function (r) { return r.json(); }).then(function (j) {
      if (j.error) { say(j.error); add.disabled = false; return; }
      location.reload();
    }).catch(function (e) { say("No passkey was added: " + e.message); add.disabled = false; });
  });
  var login = doc.querySelector("[data-passkey-login]");
  if (login) login.addEventListener("click", function () {
    if (!window.PublicKeyCredential) { say("This browser does not support passkeys."); return; }
    login.disabled = true; say("Follow your browser's prompt…");
    fetch(base + "login/passkey/begin", { method: "POST", credentials: "same-origin" }).then(function (r) { return r.json(); }).then(function (o) {
      if (o.error) throw new Error(o.error);
      var pk = o.publicKey;
      pk.challenge = b64.dec(pk.challenge);
      (pk.allowCredentials || []).forEach(function (c) { c.id = b64.dec(c.id); });
      return navigator.credentials.get({ publicKey: pk });
    }).then(function (cred) {
      return fetch(base + "login/passkey/finish", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(credJSON(cred)) });
    }).then(function (r) { return r.json(); }).then(function (j) {
      if (j.error) throw new Error(j.error);
      location.href = login.getAttribute("data-next");
    }).catch(function (e) { say("Not signed in: " + e.message); login.disabled = false; });
  });
})();
